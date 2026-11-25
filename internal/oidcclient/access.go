package oidcclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
)

// AccessVerifier checks the PowerShell module's access tokens (ADR 0028):
// tokens Authentik issued to the module's own public client, which the
// module sends as Authorization: Bearer on admin requests. The keys come
// from the issuer's JWKS, found by discovery on first use, so the API
// starts while Authentik is still starting, and are fetched again when an
// unknown key ID appears.
type AccessVerifier struct {
	Issuer   string // e.g. https://auth.example.org/application/o/radioarchive-powershell/
	ClientID string // the module's client, which the token's audience must be
	HTTP     *http.Client

	mu       sync.Mutex
	verifier *oidc.IDTokenVerifier
}

// ErrBadToken is a bearer token that isn't one of the module's access
// tokens: a bad signature, another issuer or audience, expired, an ID
// token, or not a JWT at all.
var ErrBadToken = errors.New("oidc: not the module's access token")

func (v *AccessVerifier) setup() (*oidc.IDTokenVerifier, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.verifier != nil {
		return v.verifier, nil
	}
	// The provider keeps this context to fetch keys later, so it mustn't be
	// a request's.
	p, err := oidc.NewProvider(oidc.ClientContext(context.Background(), v.HTTP), v.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery for the module's issuer: %w", err)
	}
	v.verifier = p.Verifier(&oidc.Config{ClientID: v.ClientID, SupportedSigningAlgs: []string{oidc.RS256}})
	return v.verifier, nil
}

// Verify checks raw and returns its subject, the Authentik user ID
// (sub_mode user_id). In order: the signature (RS256, a key in the issuer's
// JWKS), the issuer, the audience (the module's client) and the expiry;
// then that it's an access token, not the ID token issued with it: an
// access token names the module's client as its azp and carries the
// granted scope, which Authentik's ID tokens don't. Errors wrap ErrBadToken
// and never hold the token; any other error is discovery failing.
func (v *AccessVerifier) Verify(ctx context.Context, raw string) (string, error) {
	vf, err := v.setup()
	if err != nil {
		return "", err
	}
	tok, err := vf.Verify(oidc.ClientContext(ctx, v.HTTP), raw)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrBadToken, reason(err))
	}
	var claims struct {
		AZP   string `json:"azp"`
		Scope any    `json:"scope"`
	}
	if err := tok.Claims(&claims); err != nil {
		return "", fmt.Errorf("%w: claims", ErrBadToken)
	}
	if s, ok := claims.Scope.(string); !ok || s == "" || claims.AZP != v.ClientID {
		return "", fmt.Errorf("%w: not an access token", ErrBadToken)
	}
	if tok.Subject == "" {
		return "", fmt.Errorf("%w: no subject", ErrBadToken)
	}
	return tok.Subject, nil
}

// reason is why go-oidc refused a token, in a word, for the log: never the
// token, its claims' values, or the issuer it named.
func reason(err error) string {
	var exp *oidc.TokenExpiredError
	if errors.As(err, &exp) {
		return "expired"
	}
	msg := err.Error()
	for _, r := range []struct{ has, says string }{
		{"audience", "audience"},
		{"issued by a different provider", "issuer"},
		{"signature", "signature"},
		{"malformed", "malformed"},
		{"signing algorithm", "algorithm"},
		{"fetching keys", "keys"},
	} {
		if strings.Contains(msg, r.has) {
			return r.says
		}
	}
	return "refused"
}
