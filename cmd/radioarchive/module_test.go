package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/oidcclient/oidctest"
	"github.com/RIKIKU/RadioArchive/internal/server"
)

// bearerCall sends a request as the PowerShell module does: a bearer
// token, no cookie, no Origin or Sec-Fetch-Site (it isn't a browser).
func bearerCall(t *testing.T, base, token, method, path, body string, cookie ...string) (int, string) {
	t.Helper()
	r, _ := http.NewRequest(method, base+path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	for _, c := range cookie {
		r.Header.Add("Cookie", c)
	}
	resp, err := noRedirects.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// TestIntegrationModuleTokens (SPEC.md P3, R51, R55, R111; ADR 0028): the
// admin API takes the PowerShell module's access tokens as
// Authorization: Bearer, checking signature, issuer, audience, expiry and
// that it's an access token, then the account its subject maps to, role
// admin and status active, at every request. Every other kind of token is
// refused, as is a token with a session cookie beside it, and a token
// opens nothing on a public route.
func TestIntegrationModuleTokens(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := oidctest.New(t, p4ClientSecret, "1", initialAdmin, "Made Up")
	st, logs, srv, api := binaryServer(t, ctx, p.URL, environ{"INITIAL_ADMIN_EMAIL": initialAdmin})

	// What the module reads from the radio's URL alone.
	code, b := call(t, noRedirects, srv.URL, "GET", "/auth/module", "")
	var settings struct {
		Issuer, Scope string
		ClientID      string `json:"client_id"`
	}
	if code != 200 || json.Unmarshal([]byte(b), &settings) != nil || settings.Issuer != p.ModuleIssuer() ||
		settings.ClientID != oidctest.ModuleClientID || settings.Scope != "openid email profile offline_access" {
		t.Fatalf("GET /auth/module = %d %s", code, b)
	}

	// The owner and a listener, each signed in once with the browser, so
	// each has an account row; the module's tokens map onto them by subject.
	owner, _ := signInAs(t, p, srv.URL, "1", initialAdmin, true)
	if !isAdmin(t, owner, srv.URL) {
		t.Fatal("the owner isn't an admin")
	}
	signInAs(t, p, srv.URL, "2", "listener@example.test", true)
	signInAs(t, p, srv.URL, "3", "second@example.test", true)

	tok := p.ModuleAccessToken(t, "1", nil, false)
	// Who the module signed in as, and how: the audit log's "how" (#112).
	if code, b := bearerCall(t, srv.URL, tok, "GET", "/api/v1/admin/me", ""); code != 200 ||
		!strings.Contains(b, `"id":"1"`) || !strings.Contains(b, `"via":"module"`) || !strings.Contains(b, `"role":"admin"`) {
		t.Errorf("GET /api/v1/admin/me as the module = %d %s", code, b)
	}
	if code, b := call(t, owner, srv.URL, "GET", "/api/v1/admin/me", ""); code != 200 || !strings.Contains(b, `"via":"browser"`) {
		t.Errorf("GET /api/v1/admin/me in the browser = %d %s", code, b)
	}
	// A change from the module: no Origin, no Sec-Fetch-Site, no cookie.
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/api/v1/admin/systems", `{"id":1,"label":"EX","name":"Example network"}`},
		{"PUT", "/api/v1/admin/systems/1/talkgroups/1001", `{"label":"DISP N","name":"Dispatch North","tag":"dispatch","state":"public"}`},
		{"PUT", "/api/v1/admin/systems/1/talkgroups/1003", `{"label":"ADMIN","name":"Admin","tag":"admin","state":"hidden"}`},
	} {
		if code, b := bearerCall(t, srv.URL, tok, c.method, c.path, c.body); code >= 300 {
			t.Fatalf("as the module: %s %s = %d %s", c.method, c.path, code, b)
		}
	}
	// A26, module part: the module's changes are in the audit log as from
	// the module, with who and when (ADR 0029).
	if code, b := bearerCall(t, srv.URL, tok, "GET", "/api/v1/admin/audit?kind=talkgroup&thing=1/1003", ""); code != 200 ||
		!strings.Contains(b, `"via":"module"`) || !strings.Contains(b, `"id":"1"`) || !strings.Contains(b, `"at":"`) || strings.Contains(b, `"via":"browser"`) {
		t.Errorf("the module's change in the audit log = %d %s", code, b)
	}
	// Every admin route answers the module's admin.
	for _, r := range api.Routes() {
		if r.Kind != server.AdminRoute {
			continue
		}
		method, path := adminPath(r.Pattern)
		body := ""
		if method != "GET" && method != "DELETE" {
			body = "{}"
		}
		if code, b := bearerCall(t, srv.URL, tok, method, path, body); code == 401 || code == 403 || code >= 500 {
			t.Errorf("as the module: %s %s = %d %s", method, path, code, b)
		}
	}

	refused := func(what, token string, wantCode int, wantError string, cookie ...string) {
		t.Helper()
		code, b := bearerCall(t, srv.URL, token, "GET", "/api/v1/admin/systems", "", cookie...)
		if code != wantCode || !strings.Contains(b, `"`+wantError+`"`) {
			t.Errorf("%s: %d %s, want %d %s", what, code, b, wantCode, wantError)
		}
	}
	past := time.Now().Add(-time.Minute).Unix()
	refused("expired", p.ModuleAccessToken(t, "1", map[string]any{"exp": past, "iat": past - 300}, false), 401, "invalid_token")
	refused("signed with another key", p.ModuleAccessToken(t, "1", nil, true), 401, "invalid_token")
	refused("the browser client's audience", p.ModuleAccessToken(t, "1", map[string]any{"aud": oidctest.ClientID}, false), 401, "invalid_token")
	refused("another audience alone", p.ModuleAccessToken(t, "1", map[string]any{"aud": "someone-else"}, false), 401, "invalid_token")
	refused("the browser client's issuer alone", p.ModuleAccessToken(t, "1", map[string]any{"iss": p.Issuer()}, false), 401, "invalid_token")
	refused("another issuer", p.ModuleAccessToken(t, "1", map[string]any{"iss": "https://elsewhere.example/application/o/x/"}, false), 401, "invalid_token")
	refused("another client's azp", p.ModuleAccessToken(t, "1", map[string]any{"azp": oidctest.ClientID}, false), 401, "invalid_token")
	refused("no scope (an ID token's shape)", p.ModuleAccessToken(t, "1", map[string]any{"scope": nil}, false), 401, "invalid_token")
	refused("an ID token for the module", p.IDToken(t, "1", oidctest.ModuleClientID, p.ModuleIssuer()), 401, "invalid_token")
	refused("the browser's ID token", p.IDToken(t, "1", oidctest.ClientID, p.Issuer()), 401, "invalid_token")
	parts := strings.Split(tok, ".")
	none := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`)) + "." + parts[1] + "."
	refused("unsigned (alg none)", none, 401, "invalid_token")
	refused("malformed", "not-a-token", 401, "invalid_token")
	refused("a listener's", p.ModuleAccessToken(t, "2", nil, false), 403, "admin_required")
	refused("someone who never signed in with the browser", p.ModuleAccessToken(t, "77", nil, false), 403, "admin_required")
	sessionCookie := ""
	for _, c := range owner.Jar.Cookies(mustParse(t, srv.URL)) {
		if c.Name == "ra_session" {
			sessionCookie = c.Name + "=" + c.Value
		}
	}
	if sessionCookie == "" {
		t.Fatal("the owner's browser has no session cookie")
	}
	refused("an admin's token with an admin's session cookie beside it", tok, 400, "two_credentials", sessionCookie)
	r, _ := http.NewRequest("GET", srv.URL+"/api/v1/admin/systems", nil)
	r.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("a:b")))
	if resp, err := noRedirects.Do(r); err != nil || resp.StatusCode != 401 {
		t.Errorf("Basic credentials: %v %v", resp, err)
	} else {
		resp.Body.Close()
	}

	// Promoted, the second account's token works; demoted, it's refused at
	// its next request; disabled, too.
	second := p.ModuleAccessToken(t, "3", nil, false)
	refused("before promotion", second, 403, "admin_required")
	if code, b := bearerCall(t, srv.URL, tok, "PATCH", "/api/v1/admin/accounts/3", `{"role":"admin"}`); code != 200 {
		t.Fatalf("promote as the module = %d %s", code, b)
	}
	if code, b := bearerCall(t, srv.URL, second, "GET", "/api/v1/admin/systems", ""); code != 200 {
		t.Fatalf("promoted: %d %s", code, b)
	}
	if code, b := bearerCall(t, srv.URL, tok, "PATCH", "/api/v1/admin/accounts/3", `{"role":"listener"}`); code != 200 {
		t.Fatalf("demote = %d %s", code, b)
	}
	refused("demoted", second, 403, "admin_required")
	if code, _ := bearerCall(t, srv.URL, tok, "PATCH", "/api/v1/admin/accounts/3", `{"role":"admin"}`); code != 200 {
		t.Fatal("promote again")
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE accounts SET status = 'disabled' WHERE subject = '3'`); err != nil {
		t.Fatal(err)
	}
	refused("disabled", second, 401, "account_disabled")
	// The module's own role is the same rule as the browser's.
	if code, b := bearerCall(t, srv.URL, tok, "PATCH", "/api/v1/admin/accounts/1", `{"role":"listener"}`); code != 409 || !strings.Contains(b, "own_role") {
		t.Errorf("own role as the module = %d %s", code, b)
	}

	// P3: a public route answers a token exactly as it answers no token,
	// whoever's it is.
	for _, path := range []string{"/api/v1/catalogue", "/api/v1/catalogue/search?q=admin", "/api/v1/catalogue/search?q=1003",
		"/api/v1/recent-calls", "/api/v1/recent-calls?talkgroups=1:1003", "/api/v1/fireground/43", "/api/v1/units/search?q=1"} {
		anonCode, anonBody := call(t, noRedirects, srv.URL, "GET", path, "")
		for _, token := range []string{tok, "not-a-token"} {
			code, body := bearerCall(t, srv.URL, token, "GET", path, "")
			if code != anonCode || body != anonBody {
				t.Errorf("GET %s with a bearer token: %d %s; with none %d %s", path, code, body, anonCode, anonBody)
			}
		}
	}
	// And the account's routes don't take it: the account is the browser's.
	if code, b := bearerCall(t, srv.URL, tok, "GET", "/api/v1/account", ""); code != 200 || !strings.Contains(b, `"account":null`) {
		t.Errorf("GET /api/v1/account with a token = %d %s, want signed out", code, b)
	}

	// No token, and no address, in the logs.
	if strings.Contains(logs.String(), parts[2]) || strings.Contains(logs.String(), parts[1]) {
		t.Error("a log line holds the token")
	}
	for _, s := range []string{initialAdmin, "listener@example.test", "second@example.test"} {
		if strings.Contains(logs.String(), s) {
			t.Errorf("a log line holds %q", s)
		}
	}
	if !strings.Contains(logs.String(), "a module token was refused") {
		t.Error("refused tokens aren't logged")
	}
}

// TestIntegrationModuleSignInOff: with sign-in off, the module is told so,
// and a bearer token opens nothing.
func TestIntegrationModuleSignInOff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, _, srv, _ := binaryServer(t, ctx, "", nil)
	if code, b := call(t, noRedirects, srv.URL, "GET", "/auth/module", ""); code != 404 || !strings.Contains(b, "module_sign_in_off") {
		t.Errorf("GET /auth/module with sign-in off = %d %s", code, b)
	}
	if code, b := bearerCall(t, srv.URL, "made-up", "GET", "/api/v1/admin/systems", ""); code != 401 {
		t.Errorf("a token with sign-in off = %d %s", code, b)
	}
}
