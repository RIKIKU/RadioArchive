package account

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RIKIKU/RadioArchive/internal/store"
)

// Admins (ADR 0025): every finished sign-in records its account; the first
// proven INITIAL_ADMIN_EMAIL, in any case, is an admin, which the account
// says, and Admin finds at every request, from the row as it is then: a
// demotion or a disabling takes effect at the next request. A disabled
// account's sign-in is refused with its own notice, and its session isn't
// live.
func TestAdmins(t *testing.T) {
	w := newWorld(t)
	admin := func() (Admin, error) {
		r := httptest.NewRequest("GET", "/api/v1/admin/systems", nil)
		for _, c := range w.c.Jar.Cookies(mustURL(t, w.app.URL)) {
			r.AddCookie(c)
		}
		return w.svc.Admin(httptest.NewRecorder(), r)
	}
	if _, err := admin(); !errors.Is(err, ErrSignInRequired) {
		t.Errorf("signed out: %v", err)
	}
	w.signIn(t, "/")
	if a, ok := w.acc.get(w.p.Issuer(), subject); !ok || a.Role != store.RoleListener || a.Email != email {
		t.Fatalf("the listener's account: %+v, %v", a, ok)
	}
	if st := w.state(t); st.Account == nil || st.Account.Admin {
		t.Fatalf("a listener: %+v", st.Account)
	}
	if _, err := admin(); !errors.Is(err, ErrNotAdmin) {
		t.Errorf("a listener: %v", err)
	}

	// INITIAL_ADMIN_EMAIL, in another case, but not proven: still a listener.
	w.p.SetIdentity(subject, "admin@example.TEST", "Made Up", false)
	w.signIn(t, "/")
	if _, err := admin(); !errors.Is(err, ErrNotAdmin) {
		t.Errorf("an unproven INITIAL_ADMIN_EMAIL: %v", err)
	}
	w.p.SetIdentity(subject, "admin@example.TEST", "Made Up", true)
	w.signIn(t, "/")
	if st := w.state(t); st.Account == nil || !st.Account.Admin {
		t.Fatalf("the first admin: %+v", st.Account)
	}
	a, err := admin()
	if err != nil || a.Subject != subject || a.Issuer != w.p.Issuer() || a.Email != "admin@example.TEST" || a.Via != ViaBrowser {
		t.Fatalf("Admin = %+v, %v", a, err)
	}
	if !strings.Contains(w.logs.String(), "the first admin signed in") {
		t.Error("the first admin's promotion isn't logged")
	}
	if strings.Contains(w.logs.String(), "admin@example.TEST") {
		t.Error("the log holds the admin's email")
	}

	// Demoted: not an admin at the next request. Disabled: signed out.
	w.acc.set(w.p.Issuer(), subject, store.RoleListener, store.StatusActive)
	if _, err := admin(); !errors.Is(err, ErrNotAdmin) {
		t.Errorf("demoted: %v", err)
	}
	w.acc.set(w.p.Issuer(), subject, store.RoleAdmin, store.StatusDisabled)
	if _, err := admin(); !errors.Is(err, ErrSignInRequired) {
		t.Errorf("disabled: %v", err)
	}
	if st := w.state(t); st.Account != nil {
		t.Errorf("disabled, the account is still signed in: %+v", st.Account)
	}
	n := w.ss.count()
	if resp := w.signIn(t, "/recent"); resp.Header.Get("Location") != signInDisabled {
		t.Errorf("a disabled account's sign-in went to %q", resp.Header.Get("Location"))
	}
	if w.ss.count() != n {
		t.Error("a disabled account's sign-in kept a session")
	}
}

// TestModuleAdmin (ADR 0028): a module access token signs in as the
// account its subject names under the browser client's issuer, read at
// every request, and says it came from the module.
func TestModuleAdmin(t *testing.T) {
	w := newWorld(t)
	admin := func(token string) (Admin, error) {
		r := httptest.NewRequest("GET", "/api/v1/admin/systems", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		return w.svc.Admin(httptest.NewRecorder(), r)
	}
	tok := w.p.ModuleAccessToken(t, subject, nil, false)
	if _, err := admin(tok); !errors.Is(err, ErrNotAdmin) {
		t.Errorf("no account yet: %v", err)
	}
	w.signIn(t, "/")
	if _, err := admin(tok); !errors.Is(err, ErrNotAdmin) {
		t.Errorf("a listener: %v", err)
	}
	w.acc.set(w.p.Issuer(), subject, store.RoleAdmin, store.StatusActive)
	a, err := admin(tok)
	if err != nil || a.Via != ViaModule || a.Issuer != w.p.Issuer() || a.Subject != subject || a.Email != email {
		t.Fatalf("an admin's token: %+v, %v", a, err)
	}
	w.acc.set(w.p.Issuer(), subject, store.RoleAdmin, store.StatusDisabled)
	if _, err := admin(tok); !errors.Is(err, ErrDisabled) {
		t.Errorf("disabled: %v", err)
	}
	if _, err := admin(w.p.ModuleAccessToken(t, subject, map[string]any{"aud": "x"}, false)); !errors.Is(err, ErrBadToken) {
		t.Errorf("another audience: %v", err)
	}
	if strings.Contains(w.logs.String(), strings.Split(tok, ".")[2]) {
		t.Error("the log holds a token")
	}
}
