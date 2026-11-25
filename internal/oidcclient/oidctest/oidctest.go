// Package oidctest is a made-up OIDC provider for tests, standing in for
// Authentik as the radio's sign-in sees it (ADR 0009): discovery, keys, and
// a token endpoint that checks the client's credentials and the PKCE
// verifier against the challenge the authorization request carried. Only
// tests import it, and testing/fakeauthentik, which serves it on its own
// for the browser tests (ADR 0025).
package oidctest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// ClientID is the client the provider knows, as the blueprint names it.
const ClientID = "radioarchive"

// Provider is the made-up provider. Its ID tokens name Subject, Email and
// Name.
type Provider struct {
	// URL is where it answers, as AUTHENTIK_URL would be.
	URL                  string
	Subject, Email, Name string

	srv          *httptest.Server
	key          *rsa.PrivateKey
	clientSecret string
	logf         func(format string, args ...any)

	mu        sync.Mutex
	codes     map[string]grant
	verifiers []string
	nonce     string // when set, the ID token carries this nonce instead
	// unverified makes the ID tokens say the email isn't proven
	// (email_verified false); they say it is unless SetIdentity says not.
	unverified bool
	// people are the others a standalone provider signs in, by the value
	// of the browser's PersonCookie (AddPerson).
	people map[string]Person
}

// Person is someone a standalone provider can sign in besides its own
// Subject, with their address proven.
type Person struct{ Subject, Email, Name string }

// PersonCookie names, on the provider's own /authorize, which of its
// AddPerson people a browser signs in as. A browser without it signs in as
// the provider's Subject.
const PersonCookie = "fakeauthentik_person"

// AddPerson lets a standalone provider sign in p too: a browser whose
// PersonCookie is key signs in as p. Each browser chooses its own, so
// tests running side by side don't change each other's sign-ins.
func (p *Provider) AddPerson(key string, person Person) {
	p.mu.Lock()
	if p.people == nil {
		p.people = map[string]Person{}
	}
	p.people[key] = person
	p.mu.Unlock()
}

// SetIdentity makes the next sign-ins someone else's, as another person
// signing in to Authentik would be, with their email proven or not.
func (p *Provider) SetIdentity(subject, email, name string, verified bool) {
	p.mu.Lock()
	p.Subject, p.Email, p.Name, p.unverified = subject, email, name, !verified
	p.mu.Unlock()
}

type grant struct {
	challenge, nonce string
	person           *Person // someone else than Subject (AddPerson)
}

// New starts a provider for ClientID with clientSecret, closed when the
// test ends.
func New(t testing.TB, clientSecret, subject, email, name string) *Provider {
	t.Helper()
	p, mux, err := build(clientSecret, subject, email, name, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	p.URL = p.srv.URL
	return p
}

// Standalone is the provider answering at url, outside a test: its
// GET /authorize grants every request at once, as Authentik does for
// someone already signed in, and sends the browser back to the client. It
// is for testing/fakeauthentik, which the browser tests sign in through.
func Standalone(url, clientSecret, subject, email, name string, logf func(format string, args ...any)) (*Provider, http.Handler, error) {
	p, mux, err := build(clientSecret, subject, email, name, logf)
	if err != nil {
		return nil, nil, err
	}
	p.URL = url
	mux.HandleFunc("GET /authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		back, err := urlParse(q.Get("redirect_uri"))
		if err != nil || q.Get("client_id") != ClientID || q.Get("response_type") != "code" {
			http.Error(w, "not this client's authorization request", http.StatusBadRequest)
			return
		}
		code, state, err := p.grant(q)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if c, err := r.Cookie(PersonCookie); err == nil {
			p.mu.Lock()
			who, ok := p.people[c.Value]
			if ok {
				g := p.codes[code]
				g.person = &who
				p.codes[code] = g
			}
			p.mu.Unlock()
			if !ok {
				http.Error(w, "no such person", http.StatusBadRequest)
				return
			}
		}
		bq := back.Query()
		bq.Set("code", code)
		bq.Set("state", state)
		back.RawQuery = bq.Encode()
		http.Redirect(w, r, back.String(), http.StatusFound)
	})
	mux.HandleFunc("GET /end-session", func(w http.ResponseWriter, r *http.Request) {
		if to := r.URL.Query().Get("post_logout_redirect_uri"); to != "" {
			http.Redirect(w, r, to, http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return p, mux, nil
}

func urlParse(s string) (*url.URL, error) {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("not an absolute URL")
	}
	return u, nil
}

// build is the provider and its endpoints, at p.URL once it's set.
func build(clientSecret, subject, email, name string, logf func(format string, args ...any)) (*Provider, *http.ServeMux, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	p := &Provider{Subject: subject, Email: email, Name: name, key: key, clientSecret: clientSecret, codes: map[string]grant{}, logf: logf}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /application/o/"+ClientID+"/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": p.Issuer(), "authorization_endpoint": p.URL + "/authorize", "token_endpoint": p.URL + "/token",
			"jwks_uri": p.URL + "/jwks", "end_session_endpoint": p.URL + "/end-session",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		p.mu.Lock()
		g, ok := p.codes[r.Form.Get("code")]
		delete(p.codes, r.Form.Get("code"))
		v := r.Form.Get("code_verifier")
		p.verifiers = append(p.verifiers, v)
		nonce := g.nonce
		if p.nonce != "" {
			nonce = p.nonce
		}
		sub, email, name, verified := p.Subject, p.Email, p.Name, !p.unverified
		if g.person != nil {
			sub, email, name, verified = g.person.Subject, g.person.Email, g.person.Name, true
		}
		p.mu.Unlock()
		sum := sha256.Sum256([]byte(v))
		id, secret, _ := r.BasicAuth()
		if id == "" { // the client's credentials in the form instead
			id, secret = r.Form.Get("client_id"), r.Form.Get("client_secret")
		}
		if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge || id != ClientID || secret != p.clientSecret {
			p.logf("token request refused: code known %v, challenge %v, client %v", ok,
				base64.RawURLEncoding.EncodeToString(sum[:]) == g.challenge, id == ClientID && secret == p.clientSecret)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "k"))
		if err != nil {
			p.logf("signer: %v", err)
			http.Error(w, "signer", http.StatusInternalServerError)
			return
		}
		raw, err := jwt.Signed(signer).Claims(map[string]any{
			"iss": p.Issuer(), "aud": ClientID, "sub": sub, "nonce": nonce, "email": email, "name": name, "email_verified": verified,
			"iat": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix(),
		}).Serialize()
		if err != nil {
			p.logf("sign: %v", err)
			http.Error(w, "sign", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 300, "id_token": raw})
	})
	return p, mux, nil
}

// Issuer is the provider's issuer, as Authentik names an application's.
func (p *Provider) Issuer() string { return p.URL + "/application/o/" + ClientID + "/" }

// Grant answers an authorization request as Authentik would after the
// person signed in: a code for its challenge and nonce, and the state to
// send back with it.
func (p *Provider) Grant(t testing.TB, authURL string) (code, state string) {
	t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	code, state, err = p.grant(u.Query())
	if err != nil {
		t.Fatalf("%v: %s", err, authURL)
	}
	return code, state
}

func (p *Provider) grant(q url.Values) (code, state string, err error) {
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("nonce") == "" || len(q.Get("state")) < 8 {
		return "", "", errors.New("the authorization request has no PKCE S256 challenge, nonce or state")
	}
	code = "code-" + q.Get("state")[:8]
	p.mu.Lock()
	p.codes[code] = grant{challenge: q.Get("code_challenge"), nonce: q.Get("nonce")}
	p.mu.Unlock()
	return code, q.Get("state"), nil
}

// Verifiers are the PKCE verifiers token requests have sent.
func (p *Provider) Verifiers() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.verifiers...)
}

// SetNonce makes the ID tokens carry nonce instead of the one asked for.
func (p *Provider) SetNonce(nonce string) {
	p.mu.Lock()
	p.nonce = nonce
	p.mu.Unlock()
}
