package account

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/store"
)

// The Android app's bearer token stands in for the browser's session on the
// account's routes (ADR 0052).

func (w *world) appState(t *testing.T, token string) (int, State, http.Header) {
	t.Helper()
	resp := w.do(t, "GET", AccountPath, "", "Authorization", "Bearer "+token)
	var st State
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode, st, resp.Header
}

func (w *world) appPut(t *testing.T, token, body string) *http.Response {
	t.Helper()
	// No Origin: an app sends none, and needs none.
	return w.do(t, "PUT", ProfilePath, body, "Authorization", "Bearer "+token, "Content-Type", "application/json")
}

func TestAppSignsInWithItsToken(t *testing.T) {
	w := newWorld(t)
	tok := w.p.AppAccessToken(t, subject, nil, false)
	code, st, _ := w.appState(t, tok)
	if code != http.StatusOK || st.Account == nil || st.Account.ID != subject || !st.Saves {
		t.Fatalf("GET with the app's token = %d %+v, want the account", code, st)
	}
	// Its first sign-in made the account, under the browser client's issuer.
	if a, _ := w.acc.Account(t.Context(), w.p.Issuer(), subject); a == nil || a.Email != email || a.Role != store.RoleListener {
		t.Fatalf("account row %+v", a)
	}
	// It saves and reads the profile without a cookie or an Origin.
	if resp := w.appPut(t, tok, `{"v":1,"talkgroups":[{"id":1001},{"id":1002,"priority":true}],"alerts":true}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT with the app's token = %d", resp.StatusCode)
	}
	_, st, _ = w.appState(t, tok)
	if st.Profile == nil || len(st.Profile.Talkgroups) != 2 || !st.Profile.Talkgroups[1].Priority || !st.Profile.Alerts {
		t.Fatalf("read back %+v", st.Profile)
	}
	// The same account as the browser's: signing in there shows the same profile.
	w.signIn(t, "/")
	if b := w.state(t); b.Profile == nil || len(b.Profile.Talkgroups) != 2 {
		t.Fatalf("the browser sees %+v", b.Profile)
	}
}

func TestAppClearsItsProfile(t *testing.T) {
	w := newWorld(t)
	tok := w.p.AppAccessToken(t, subject, nil, false)
	w.appPut(t, tok, `{"v":1,"talkgroups":[{"id":1001}]}`)
	if resp := w.do(t, "DELETE", ProfilePath, "", "Authorization", "Bearer "+tok); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE = %d", resp.StatusCode)
	}
	if _, ok := w.db.rows[w.key()]; ok {
		t.Fatal("the profile is still there")
	}
}

func TestAppTokenRefused(t *testing.T) {
	w := newWorld(t)
	cases := map[string]string{
		"another key":      w.p.AppAccessToken(t, subject, nil, true),
		"expired":          w.p.AppAccessToken(t, subject, map[string]any{"exp": time.Now().Add(-time.Minute).Unix()}, false),
		"no scope":         w.p.AppAccessToken(t, subject, map[string]any{"scope": nil}, false),
		"another azp":      w.p.AppAccessToken(t, subject, map[string]any{"azp": "radioarchive"}, false),
		"the module's":     w.p.ModuleAccessToken(t, subject, nil, false),
		"the browser's ID": w.p.IDToken(t, subject, "radioarchive", w.p.Issuer()),
		"not a JWT":        "junk",
	}
	for name, tok := range cases {
		code, _, h := w.appState(t, tok)
		if code != http.StatusUnauthorized || !strings.Contains(h.Get("WWW-Authenticate"), "invalid_token") {
			t.Errorf("%s: GET = %d %q, want 401 invalid_token", name, code, h.Get("WWW-Authenticate"))
		}
		if resp := w.appPut(t, tok, `{"v":1,"talkgroups":[]}`); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: PUT = %d, want 401", name, resp.StatusCode)
		}
	}
	if len(w.acc.rows) != 0 {
		t.Errorf("a refused token made an account: %v", w.acc.rows)
	}
}

func TestAppNeverMakesTheFirstAdmin(t *testing.T) {
	w := newWorld(t)
	// The world's INITIAL_ADMIN_EMAIL, proven: a browser sign-in would promote it.
	tok := w.p.AppAccessToken(t, subject, map[string]any{"email": "admin@example.test"}, false)
	if _, st, _ := w.appState(t, tok); st.Account == nil || st.Account.Admin {
		t.Fatalf("the app's first sign-in: %+v", st.Account)
	}
}

func TestAppDisabledAccountIsSignedOut(t *testing.T) {
	w := newWorld(t)
	tok := w.p.AppAccessToken(t, subject, nil, false)
	w.appState(t, tok)
	k := [2]string{w.p.Issuer(), subject}
	a := w.acc.rows[k]
	a.Status = store.StatusDisabled
	w.acc.rows[k] = a
	if code, st, _ := w.appState(t, tok); code != http.StatusOK || st.Account != nil {
		t.Fatalf("disabled: GET = %d %+v, want signed out", code, st.Account)
	}
	if resp := w.appPut(t, tok, `{"v":1,"talkgroups":[]}`); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("disabled: PUT = %d, want 401", resp.StatusCode)
	}
}

func TestAppSettings(t *testing.T) {
	w := newWorld(t)
	resp := w.do(t, "GET", appPath, "")
	var s AppSettings
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d %v", appPath, resp.StatusCode, err)
	}
	if s.Issuer != w.p.AppIssuer() || s.ClientID != "radioarchive-android" || !strings.Contains(s.Scope, "offline_access") {
		t.Fatalf("settings %+v", s)
	}
}

// A browser's cross-site request still can't change the profile: the
// same-site check is waived only for a request with the token and no cookie.
func TestAppTokenDoesNotWaiveSameSiteForCookies(t *testing.T) {
	w := newWorld(t)
	w.signIn(t, "/")
	resp := w.do(t, "PUT", ProfilePath, `{"v":1,"talkgroups":[]}`, "Authorization", "Bearer junk",
		"Content-Type", "application/json", "Origin", "https://evil.example")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cookie, junk token, other site: PUT = %d, want 403", resp.StatusCode)
	}
}
