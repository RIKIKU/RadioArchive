package main

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/RIKIKU/RadioArchive/internal/oidcclient/oidctest"
	"github.com/RIKIKU/RadioArchive/internal/server"
)

// accountsToken is the made-up accounts service account's Authentik token
// (AUTHENTIK_API_TOKEN), which must be in no log line, answer or error.
const accountsToken = "made-up-authentik-api-token-0123456789abcdef"

// signedInAs is the account GET /api/v1/account says the browser is
// signed in as, "" for none.
func signedInAs(t *testing.T, c *http.Client, base string) string {
	t.Helper()
	code, body := call(t, c, base, "GET", "/api/v1/account", "")
	var st struct {
		Account *struct {
			ID string `json:"id"`
		} `json:"account"`
		Profile json.RawMessage `json:"profile"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(body), &st) != nil {
		t.Fatalf("account = %d %s", code, body)
	}
	if st.Account == nil {
		return ""
	}
	return st.Account.ID
}

// accountOf is the account as GET /api/v1/admin/accounts lists it.
func accountOf(t *testing.T, admin *http.Client, base, id string) (server.AccountInfo, bool) {
	t.Helper()
	code, body := call(t, admin, base, "GET", "/api/v1/admin/accounts", "")
	var all []server.AccountInfo
	if code != 200 || json.Unmarshal([]byte(body), &all) != nil {
		t.Fatalf("accounts = %d %s", code, body)
	}
	for _, a := range all {
		if a.ID == id {
			return a, true
		}
	}
	return server.AccountInfo{}, false
}

// TestIntegrationDisableAccount (SPEC.md P3, R55, R111; ADR 0033; issue
// #105's acceptance, against the made-up Authentik): disabling a signed-in
// listener signs out every one of their browsers at its next request,
// refuses their next sign-in, and switches them off in Authentik with the
// accounts token. With Authentik down, disabling still locks them out of
// the radio, the admin is told it didn't reach Authentik, the Accounts list
// says so, and repeating it once Authentik is back switches them off there.
// Enabling lets them sign in again with their saved profile. Their live
// feed never depended on it (P3: the A13 tests). An admin can't disable,
// enable or delete their own account, a listener can't do any of it, and
// deleting removes the account, its profile and sessions and its Authentik
// user. The token is in no log line or answer.
func TestIntegrationDisableAccount(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := oidctest.New(t, p4ClientSecret, "1", initialAdmin, "Made Up")
	p.ServeUsers(accountsToken)
	st, logs, srv, _ := binaryServer(t, ctx, p.URL, environ{"INITIAL_ADMIN_EMAIL": initialAdmin, "AUTHENTIK_API_TOKEN": accountsToken})
	var answers []string
	do := func(c *http.Client, method, path, body string) (int, string) {
		t.Helper()
		code, b := call(t, c, srv.URL, method, path, body)
		answers = append(answers, b)
		return code, b
	}

	owner, _ := signInAs(t, p, srv.URL, "1", initialAdmin, true)
	if !isAdmin(t, owner, srv.URL) {
		t.Fatal("not an admin")
	}
	phone, _ := signInAs(t, p, srv.URL, "2", "listener@example.test", true)
	laptop, _ := signInAs(t, p, srv.URL, "2", "listener@example.test", true)
	if code, b := do(laptop, "PUT", "/api/v1/account/profile", `{"v":1,"talkgroups":[],"look":"console"}`); code != http.StatusNoContent {
		t.Fatalf("save the profile = %d %s", code, b)
	}

	// A listener can do none of it.
	for _, r := range [][3]string{{"PATCH", "/api/v1/admin/accounts/1", `{"status":"disabled"}`}, {"DELETE", "/api/v1/admin/accounts/1", ""}} {
		if code, b := do(phone, r[0], r[1], r[2]); code != 403 {
			t.Errorf("a listener's %s %s = %d %s, want 403", r[0], r[1], code, b)
		}
	}
	// Nor the admin on their own account.
	for _, r := range [][3]string{{"PATCH", "/api/v1/admin/accounts/1", `{"status":"disabled"}`}, {"PATCH", "/api/v1/admin/accounts/1", `{"status":"active"}`},
		{"DELETE", "/api/v1/admin/accounts/1", ""}} {
		if code, b := do(owner, r[0], r[1], r[2]); code != 409 || !strings.Contains(b, `"own_account"`) {
			t.Errorf("own account: %s %s = %d %s, want 409 own_account", r[0], r[1], code, b)
		}
	}
	for _, body := range []string{`{}`, `{"role":"admin","status":"disabled"}`, `{"status":"gone"}`} {
		if code, b := do(owner, "PATCH", "/api/v1/admin/accounts/2", body); code != 400 {
			t.Errorf("PATCH %s = %d %s, want 400", body, code, b)
		}
	}
	if u, _ := p.UserOf("1"); !u.Active || slices.ContainsFunc(p.UserCalls(), func(c string) bool { return !strings.HasPrefix(c, "GET") }) {
		t.Fatalf("a refusal reached Authentik: %v", p.UserCalls())
	}

	// Disable: both browsers signed out at their next request, Authentik
	// switched off, the sign-in refused.
	code, b := do(owner, "PATCH", "/api/v1/admin/accounts/2", `{"status":"disabled"}`)
	if code != 200 || !strings.Contains(b, `"status":"disabled"`) || !strings.Contains(b, `"authentik_pending":false`) {
		t.Fatalf("disable = %d %s", code, b)
	}
	for name, c := range map[string]*http.Client{"phone": phone, "laptop": laptop} {
		if who := signedInAs(t, c, srv.URL); who != "" {
			t.Errorf("the %s is still signed in as %q", name, who)
		}
	}
	if u, _ := p.UserOf("2"); u.Active {
		t.Error("still active in Authentik")
	}
	// Authentik refuses them now; and were it not to, the radio would.
	p.SetUser("2", oidctest.User{Active: true, Type: "internal"})
	if _, to := signInAs(t, p, srv.URL, "2", "listener@example.test", true); to != "/?signin=disabled" {
		t.Errorf("a disabled account's sign-in went to %q", to)
	}
	p.SetUser("2", oidctest.User{Active: false, Type: "internal"})

	// Enable, with Authentik down: the radio's change stands, and the
	// admin is told; the list says Authentik is still to be told.
	p.SetUsersDown(true)
	code, b = do(owner, "PATCH", "/api/v1/admin/accounts/2", `{"status":"active"}`)
	if code != 502 || !strings.Contains(b, `"authentik_unreachable"`) || !strings.Contains(b, `"status":"active"`) || !strings.Contains(b, `"authentik_pending":true`) {
		t.Fatalf("enable with Authentik down = %d %s", code, b)
	}
	if a, _ := accountOf(t, owner, srv.URL, "2"); a.Status != "active" || !a.AuthentikPending {
		t.Errorf("listed: %+v", a)
	}
	if u, _ := p.UserOf("2"); u.Active {
		t.Fatal("Authentik changed while it was down")
	}
	// Repeating it once Authentik is back switches them on there, with no
	// new entry: the radio had nothing more to change.
	p.SetUsersDown(false)
	if code, b = do(owner, "PATCH", "/api/v1/admin/accounts/2", `{"status":"active"}`); code != 200 || !strings.Contains(b, `"authentik_pending":false`) {
		t.Fatalf("enable again = %d %s", code, b)
	}
	if u, _ := p.UserOf("2"); !u.Active {
		t.Fatal("repeating it didn't reach Authentik")
	}

	// Disable with Authentik down: locked out of the radio at once.
	again, _ := signInAs(t, p, srv.URL, "2", "listener@example.test", true)
	if who := signedInAs(t, again, srv.URL); who != "2" {
		t.Fatalf("not signed in again: %q", who)
	}
	p.SetUsersDown(true)
	code, b = do(owner, "PATCH", "/api/v1/admin/accounts/2", `{"status":"disabled"}`)
	if code != 502 || !strings.Contains(b, `"authentik_unreachable"`) || !strings.Contains(b, "Disabled in the radio") {
		t.Fatalf("disable with Authentik down = %d %s", code, b)
	}
	if who := signedInAs(t, again, srv.URL); who != "" {
		t.Errorf("with Authentik down, still signed in to the radio as %q", who)
	}
	if _, to := signInAs(t, p, srv.URL, "2", "listener@example.test", true); to != "/?signin=disabled" {
		t.Errorf("with Authentik down, the sign-in went to %q", to)
	}
	if u, _ := p.UserOf("2"); !u.Active {
		t.Fatal("Authentik changed while it was down")
	}
	// Repeating it once Authentik is back switches them off there.
	p.SetUsersDown(false)
	if code, b = do(owner, "PATCH", "/api/v1/admin/accounts/2", `{"status":"disabled"}`); code != 200 || !strings.Contains(b, `"authentik_pending":false`) {
		t.Fatalf("disable again = %d %s", code, b)
	}
	if u, _ := p.UserOf("2"); u.Active {
		t.Error("repeating it didn't reach Authentik")
	}

	// Enable: they sign in again, with their profile as they left it.
	if code, b = do(owner, "PATCH", "/api/v1/admin/accounts/2", `{"status":"active"}`); code != 200 || !strings.Contains(b, `"status":"active"`) {
		t.Fatalf("enable = %d %s", code, b)
	}
	back, _ := signInAs(t, p, srv.URL, "2", "listener@example.test", true)
	if who := signedInAs(t, back, srv.URL); who != "2" {
		t.Fatalf("enabled, but not signed in: %q", who)
	}
	if _, body := do(back, "GET", "/api/v1/account", ""); !strings.Contains(body, `"look":"console"`) {
		t.Errorf("the profile after enabling: %s", body)
	}

	// Who and when are in the audit log, for each change; no entry for the
	// repeat that only reached Authentik.
	_, body := do(owner, "GET", "/api/v1/admin/audit?kind=account&thing=2", "")
	var page server.AuditPage
	if err := json.Unmarshal([]byte(body), &page); err != nil {
		t.Fatal(err)
	}
	var statuses []string
	for _, e := range page.Entries {
		for _, c := range e.Changes {
			if c.Field == "status" {
				statuses = append(statuses, string(c.Before)+">"+string(c.After))
			}
		}
		if e.Who.ID != "1" || e.Via != "browser" || e.At.IsZero() {
			t.Errorf("entry %+v", e)
		}
	}
	if want := []string{`"disabled">"active"`, `"active">"disabled"`, `"disabled">"active"`, `"active">"disabled"`}; !slices.Equal(statuses, want) {
		t.Errorf("the audit log's status changes, newest first: %q, want %q", statuses, want)
	}

	// Delete: the account, its profile and sessions, and its Authentik user.
	code, b = do(owner, "DELETE", "/api/v1/admin/accounts/2", "")
	if code != 200 || !strings.Contains(b, `"id":"2"`) {
		t.Fatalf("delete = %d %s", code, b)
	}
	if who := signedInAs(t, back, srv.URL); who != "" {
		t.Errorf("deleted, still signed in as %q", who)
	}
	if _, ok := accountOf(t, owner, srv.URL, "2"); ok {
		t.Error("still listed")
	}
	if _, there := p.UserOf("2"); there {
		t.Error("the Authentik user is still there")
	}
	var profiles int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM listener_profiles WHERE subject = '2'`).Scan(&profiles); err != nil || profiles != 0 {
		t.Errorf("%d profiles left, %v", profiles, err)
	}
	if code, b := do(owner, "DELETE", "/api/v1/admin/accounts/2", ""); code != 404 {
		t.Errorf("delete again = %d %s", code, b)
	}

	// The token is in no log line or answer; the listener's name and email
	// are in no log line.
	if slices.ContainsFunc(answers, func(a string) bool { return strings.Contains(a, accountsToken) }) {
		t.Error("an answer holds the Authentik token")
	}
	for _, s := range []string{accountsToken, "listener@example.test", "Made Up 2"} {
		if strings.Contains(logs.String(), s) {
			t.Errorf("a log line holds %q", s)
		}
	}
	p4NoAddresses(t, logs)
}
