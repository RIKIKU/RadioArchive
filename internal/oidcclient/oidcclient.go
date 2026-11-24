// Package oidcclient signs people in with Authentik over OIDC (ADR 0009):
// the authorization code flow with PKCE (S256), a state and a nonce, and
// the ID token checked by coreos/go-oidc, as PagerArchive does.
//
// It knows nothing of listeners or admins. Listener sign-in (internal/account)
// uses it now; admin sign-in (M4, R51) is meant to use the same client and
// the same Authentik provider, and decide for itself who is an admin (R55).
package oidcclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Client is one OIDC client of Authentik. The provider is discovered on
// first use, so the API starts, and listeners listen, while Authentik is
// still starting (P3).
type Client struct {
	Issuer       string // e.g. https://auth.example.org/application/o/radioarchive/
	ClientID     string
	ClientSecret string
	RedirectURL  string       // APP_URL/auth/callback
	HTTP         *http.Client // reaches Authentik (see authentik.Transport)

	mu         sync.Mutex
	config     *oauth2.Config
	verifier   *oidc.IDTokenVerifier
	endSession string
}

func (c *Client) ctx(ctx context.Context) context.Context { return oidc.ClientContext(ctx, c.HTTP) }

func (c *Client) setup() (*oauth2.Config, *oidc.IDTokenVerifier, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.config != nil {
		return c.config, c.verifier, nil
	}
	// The provider keeps this context to fetch signing keys later, so it
	// mustn't be a request's.
	p, err := oidc.NewProvider(c.ctx(context.Background()), c.Issuer)
	if err != nil {
		return nil, nil, fmt.Errorf("oidc discovery: %w", err)
	}
	var extra struct {
		EndSession string `json:"end_session_endpoint"`
	}
	_ = p.Claims(&extra)
	c.endSession = extra.EndSession
	c.verifier = p.Verifier(&oidc.Config{ClientID: c.ClientID})
	c.config = &oauth2.Config{
		ClientID: c.ClientID, ClientSecret: c.ClientSecret, RedirectURL: c.RedirectURL,
		Endpoint: p.Endpoint(), Scopes: []string{oidc.ScopeOpenID, "email", "profile"},
	}
	return c.config, c.verifier, nil
}

// Attempt is one sign-in under way: what the browser keeps (in a cookie the
// caller seals) until Authentik sends it back.
type Attempt struct {
	State    string
	Nonce    string
	Verifier string // PKCE
}

// NewAttempt is a fresh sign-in.
func NewAttempt() Attempt {
	return Attempt{State: oauth2.GenerateVerifier(), Nonce: oauth2.GenerateVerifier(), Verifier: oauth2.GenerateVerifier()}
}

// AuthURL is where to send the browser to sign in. extra adds parameters,
// such as prompt=none for a silent check (R103, M7).
func (c *Client) AuthURL(a Attempt, extra ...oauth2.AuthCodeOption) (string, error) {
	cfg, _, err := c.setup()
	if err != nil {
		return "", err
	}
	opts := append([]oauth2.AuthCodeOption{oidc.Nonce(a.Nonce), oauth2.S256ChallengeOption(a.Verifier)}, extra...)
	return cfg.AuthCodeURL(a.State, opts...), nil
}

// Identity is who signed in.
type Identity struct {
	Subject string // Authentik's user ID
	Email   string
	Name    string
	// IDToken is the raw ID token, kept only to end the Authentik session
	// on sign-out (id_token_hint). Never sent to a page or logged.
	IDToken string
}

// ErrRefused is a sign-in that didn't check out: a wrong or missing code,
// a token that doesn't verify, or the wrong nonce.
var ErrRefused = errors.New("oidc: sign-in refused")

// Finish trades Authentik's code for the ID token, with the attempt's PKCE
// verifier, and checks the token: its signature, issuer, audience, expiry
// and nonce.
func (c *Client) Finish(ctx context.Context, a Attempt, code string) (Identity, error) {
	cfg, verifier, err := c.setup()
	if err != nil {
		return Identity{}, err
	}
	ctx = c.ctx(ctx)
	tok, err := cfg.Exchange(ctx, code, oauth2.VerifierOption(a.Verifier))
	if err != nil {
		return Identity{}, fmt.Errorf("%w: code exchange: %s", ErrRefused, oauthReason(err))
	}
	raw, _ := tok.Extra("id_token").(string)
	idt, err := verifier.Verify(ctx, raw)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: id token: %v", ErrRefused, err)
	}
	if idt.Nonce != a.Nonce {
		return Identity{}, fmt.Errorf("%w: id token: wrong nonce", ErrRefused)
	}
	var claims struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := idt.Claims(&claims); err != nil {
		return Identity{}, fmt.Errorf("%w: id token claims: %v", ErrRefused, err)
	}
	return Identity{Subject: idt.Subject, Email: claims.Email, Name: claims.Name, IDToken: raw}, nil
}

// oauthReason is an exchange error's OAuth error code, never its body or
// the URL.
func oauthReason(err error) string {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		if re.ErrorCode != "" {
			return re.ErrorCode
		}
		if re.Response != nil {
			return fmt.Sprintf("HTTP %d", re.Response.StatusCode)
		}
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return "failed"
}

// EndSessionURL is Authentik's end-session endpoint (OIDC RP-initiated
// logout), which ends the browser's Authentik session and sends it to
// returnTo, which must be registered on the provider. Without an ID token,
// Authentik won't return the browser, so neither is sent.
func (c *Client) EndSessionURL(idToken, returnTo string) (string, error) {
	if _, _, err := c.setup(); err != nil {
		return "", err
	}
	c.mu.Lock()
	end := c.endSession
	c.mu.Unlock()
	if end == "" {
		return "", errors.New("oidc: Authentik named no end-session endpoint")
	}
	u, err := url.Parse(end)
	if err != nil {
		return "", err
	}
	if idToken != "" {
		q := u.Query()
		q.Set("id_token_hint", idToken)
		q.Set("post_logout_redirect_uri", returnTo)
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}
