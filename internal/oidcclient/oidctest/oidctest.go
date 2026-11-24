// Package oidctest is a made-up OIDC provider for tests, standing in for
// Authentik as the radio's sign-in sees it (ADR 0009): discovery, keys, and
// a token endpoint that checks the client's credentials and the PKCE
// verifier against the challenge the authorization request carried. Only
// tests import it.
package oidctest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
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

	mu        sync.Mutex
	codes     map[string]grant
	verifiers []string
	nonce     string // when set, the ID token carries this nonce instead
}

type grant struct{ challenge, nonce string }

// New starts a provider for ClientID with clientSecret, closed when the
// test ends.
func New(t testing.TB, clientSecret, subject, email, name string) *Provider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &Provider{Subject: subject, Email: email, Name: name, key: key, clientSecret: clientSecret, codes: map[string]grant{}}
	mux := http.NewServeMux()
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	p.URL = p.srv.URL
	iss := p.Issuer()
	mux.HandleFunc("GET /application/o/"+ClientID+"/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": iss, "authorization_endpoint": p.URL + "/authorize", "token_endpoint": p.URL + "/token",
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
		p.mu.Unlock()
		sum := sha256.Sum256([]byte(v))
		id, secret, _ := r.BasicAuth()
		if id == "" { // the client's credentials in the form instead
			id, secret = r.Form.Get("client_id"), r.Form.Get("client_secret")
		}
		if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge || id != ClientID || secret != p.clientSecret {
			t.Logf("token request refused: code known %v, challenge %v, client %v", ok,
				base64.RawURLEncoding.EncodeToString(sum[:]) == g.challenge, id == ClientID && secret == p.clientSecret)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "k"))
		if err != nil {
			t.Errorf("signer: %v", err)
			return
		}
		raw, err := jwt.Signed(signer).Claims(map[string]any{
			"iss": iss, "aud": ClientID, "sub": p.Subject, "nonce": nonce, "email": p.Email, "name": p.Name,
			"iat": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix(),
		}).Serialize()
		if err != nil {
			t.Errorf("sign: %v", err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 300, "id_token": raw})
	})
	return p
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
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("nonce") == "" || q.Get("state") == "" {
		t.Fatalf("the authorization request has no PKCE S256 challenge, nonce or state: %s", authURL)
	}
	code = "code-" + q.Get("state")[:8]
	p.mu.Lock()
	p.codes[code] = grant{challenge: q.Get("code_challenge"), nonce: q.Get("nonce")}
	p.mu.Unlock()
	return code, q.Get("state")
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
