package account

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RIKIKU/RadioArchive/internal/authentik"
	"github.com/RIKIKU/RadioArchive/internal/oidcclient/oidctest"
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

// usersToken is the made-up accounts service account's token, which must
// never reach a log.
const usersToken = "made-up-authentik-api-token-fedcba9876543210"

// TestSetStatusReachesAuthentik (ADR 0033): disabling is the radio's
// change, then Authentik's user switched off through its API with the
// service account's token; if Authentik is down the radio's change
// stands, the account says Authentik is still to be told, and repeating
// it repairs Authentik. Enabling mirrors it. An Authentik superuser or
// service account is never switched off. Without a token, Authentik isn't
// told and the answer says so. Deleting deletes the Authentik user inside
// the radio's transaction, so a deletion Authentik missed deletes nothing;
// the combined deployment's leaves Authentik alone.
func TestSetStatusReachesAuthentik(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	w.p.ServeUsers(usersToken)
	w.svc.cfg.Directory = &authentik.Users{URL: mustURL(t, w.p.URL), Token: usersToken, HTTP: http.DefaultClient}
	w.svc.cfg.DeleteAuthentikUsers = true
	w.signIn(t, "/")
	iss := w.p.Issuer()
	w.acc.rows[[2]string{iss, "1"}] = store.Account{Issuer: iss, Subject: "1", Role: store.RoleAdmin, Status: store.StatusActive}
	by := Admin{Issuer: iss, Subject: "1", Name: "Made Up Admin", Via: ViaBrowser}

	a, err := w.svc.SetStatus(ctx, by, subject, store.StatusDisabled)
	if err != nil || a.Status != store.StatusDisabled || a.AuthentikPending {
		t.Fatalf("disable: %+v, %v", a, err)
	}
	if u, _ := w.p.UserOf(subject); u.Active {
		t.Error("still active in Authentik")
	}
	if got, _ := w.acc.get(iss, subject); got.AuthentikPending {
		t.Error("the account says Authentik is still to be told")
	}

	// Authentik down: the radio's change stands, and says so.
	w.p.SetUsersDown(true)
	a, err = w.svc.SetStatus(ctx, by, subject, store.StatusActive)
	if !errors.Is(err, ErrAuthentikUnreachable) || a.Status != store.StatusActive || !a.AuthentikPending {
		t.Fatalf("enable with Authentik down: %+v, %v", a, err)
	}
	if got, _ := w.acc.get(iss, subject); got.Status != store.StatusActive || !got.AuthentikPending {
		t.Errorf("the account: %+v", got)
	}
	if u, _ := w.p.UserOf(subject); u.Active {
		t.Error("Authentik changed while it was down")
	}
	// Repeating it once Authentik is back repairs it.
	w.p.SetUsersDown(false)
	if a, err = w.svc.SetStatus(ctx, by, subject, store.StatusActive); err != nil || a.AuthentikPending {
		t.Fatalf("enable again: %+v, %v", a, err)
	}
	if u, _ := w.p.UserOf(subject); !u.Active {
		t.Error("Authentik wasn't repaired")
	}

	// An Authentik superuser or service account is left alone.
	for _, u := range []oidctest.User{{Active: true, Superuser: true, Type: "internal"}, {Active: true, Type: "service_account"}, {Active: true, Type: "internal_service_account"}} {
		w.p.SetUser("7", u)
		w.acc.rows[[2]string{iss, "7"}] = store.Account{Issuer: iss, Subject: "7", Role: store.RoleListener, Status: store.StatusActive}
		a, err := w.svc.SetStatus(ctx, by, "7", store.StatusDisabled)
		if !errors.Is(err, ErrAuthentikProtected) || a.Status != store.StatusDisabled || !a.AuthentikPending {
			t.Errorf("%+v: %+v, %v", u, a, err)
		}
		if got, _ := w.p.UserOf("7"); !got.Active {
			t.Errorf("%+v was switched off", u)
		}
		if _, err := w.svc.Delete(ctx, by, "7"); !errors.Is(err, ErrAuthentikProtected) {
			t.Errorf("deleting %+v: %v", u, err)
		}
		if _, there := w.p.UserOf("7"); !there {
			t.Errorf("%+v was deleted", u)
		}
		if _, ok := w.acc.get(iss, "7"); !ok {
			t.Errorf("a refused deletion deleted the radio's account")
		}
	}

	// Deleting: Authentik down, nothing is deleted anywhere.
	w.p.SetUsersDown(true)
	if _, err := w.svc.Delete(ctx, by, subject); !errors.Is(err, ErrAuthentikUnreachable) {
		t.Errorf("delete with Authentik down: %v", err)
	}
	if _, ok := w.acc.get(iss, subject); !ok {
		t.Error("the account was deleted while Authentik was down")
	}
	w.p.SetUsersDown(false)
	if was, err := w.svc.Delete(ctx, by, subject); err != nil || was.Subject != subject {
		t.Fatalf("delete: %+v, %v", was, err)
	}
	if _, there := w.p.UserOf(subject); there {
		t.Error("the Authentik user wasn't deleted")
	}
	if _, ok := w.acc.get(iss, subject); ok {
		t.Error("the account wasn't deleted")
	}

	// The combined deployment's: the radio's rows only.
	w.svc.cfg.DeleteAuthentikUsers = false
	w.acc.rows[[2]string{iss, "8"}] = store.Account{Issuer: iss, Subject: "8", Role: store.RoleListener, Status: store.StatusActive}
	calls := len(w.p.UserCalls())
	if _, err := w.svc.Delete(ctx, by, "8"); err != nil || len(w.p.UserCalls()) != calls {
		t.Errorf("a deletion without Authentik's users: %v, calls %v", err, w.p.UserCalls()[calls:])
	}

	// No token: the radio's change, and Authentik not told.
	w.svc.cfg.Directory = nil
	w.svc.cfg.DeleteAuthentikUsers = true
	w.acc.rows[[2]string{iss, "9"}] = store.Account{Issuer: iss, Subject: "9", Role: store.RoleListener, Status: store.StatusActive}
	if a, err := w.svc.SetStatus(ctx, by, "9", store.StatusDisabled); !errors.Is(err, ErrAuthentikOff) || a.Status != store.StatusDisabled || !a.AuthentikPending {
		t.Errorf("no token: %+v, %v", a, err)
	}
	if _, err := w.svc.Delete(ctx, by, "9"); !errors.Is(err, ErrAuthentikOff) {
		t.Errorf("deleting with no token: %v", err)
	}

	// The radio's refusals come first, and Authentik isn't asked.
	calls = len(w.p.UserCalls())
	if _, err := w.svc.SetStatus(ctx, by, "1", store.StatusDisabled); !errors.Is(err, store.ErrOwnAccount) || len(w.p.UserCalls()) != calls {
		t.Errorf("own account: %v", err)
	}
	if strings.Contains(w.logs.String(), usersToken) {
		t.Error("the log holds the Authentik token")
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
