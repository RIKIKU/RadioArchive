package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/RIKIKU/RadioArchive/internal/oidcclient/oidctest"
	"github.com/RIKIKU/RadioArchive/internal/public"
	"github.com/RIKIKU/RadioArchive/internal/server"
)

// The first admin's address, as the operator sets INITIAL_ADMIN_EMAIL.
const initialAdmin = "owner@example.test"

// signInAs signs a new browser in to srv through the made-up Authentik p as
// the person with this subject and email, and returns the browser and
// where the sign-in sent it.
func signInAs(t *testing.T, p *oidctest.Provider, srv, subject, email string, verified bool) (*http.Client, string) {
	t.Helper()
	p.SetIdentity(subject, email, "Made Up "+subject, verified)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, CheckRedirect: noRedirects.CheckRedirect}
	resp := p4Do(t, c, srv, "GET", "/auth/signin?return=/", "")
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("sign-in = %d", resp.StatusCode)
	}
	code, state := p.Grant(t, resp.Header.Get("Location"))
	resp = p4Do(t, c, srv, "GET", "/auth/callback?code="+code+"&state="+state, "")
	return c, resp.Header.Get("Location")
}

// call sends a request as the app's page would (same site), returning the
// status and body.
func call(t *testing.T, c *http.Client, base, method, path, body string) (int, string) {
	t.Helper()
	r, _ := http.NewRequest(method, base+path, strings.NewReader(body))
	r.Header.Set("Origin", base)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// isAdmin is what GET /api/v1/account says of the browser's account.
func isAdmin(t *testing.T, c *http.Client, base string) bool {
	t.Helper()
	code, body := call(t, c, base, "GET", "/api/v1/account", "")
	var st struct {
		Account *struct {
			Admin bool `json:"admin"`
		} `json:"account"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(body), &st) != nil || st.Account == nil {
		t.Fatalf("account = %d %s", code, body)
	}
	return st.Account.Admin
}

// adminPath fills a route's pattern with made-up values: a request that
// reaches the handler, whatever it then answers. The subject is never the
// admin's own, so ending its sessions signs nobody out who matters here.
func adminPath(pattern string) (method, path string) {
	method, path, _ = strings.Cut(pattern, " ")
	return method, strings.NewReplacer("{system}", "1", "{talkgroup}", "1001", "{unit}", "5001", "{recorder}", "Bench",
		"{short_name}", "ex", "{group}", "1", "{tag}", "1", "{call}", "00000000-0000-4000-8000-000000000000",
		"{subject}", "999").Replace(path)
}

// TestIntegrationAdminRoutes (SPEC.md R51, R55, R111; ADR 0025): every
// admin route the binary serves answers 401 signed out and 403 for a
// listener, and is answered for an admin; an admin demoted, or disabled, is
// refused at their next request; an admin can't change their own role; a
// state-changing admin request from another site is refused. The first
// admin is INITIAL_ADMIN_EMAIL, signing in in another case.
func TestIntegrationAdminRoutes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := oidctest.New(t, p4ClientSecret, "1", initialAdmin, "Made Up")
	st, logs, srv, api := binaryServer(t, ctx, p.URL, environ{"INITIAL_ADMIN_EMAIL": initialAdmin})

	var routes []string
	for _, r := range api.Routes() {
		if r.Kind == server.AdminRoute {
			routes = append(routes, r.Pattern)
		}
	}
	if len(routes) < 20 {
		t.Fatalf("only %d admin routes: %q", len(routes), routes)
	}
	every := func(who string, c *http.Client, want func(code int) bool, wants string) {
		t.Helper()
		for _, pat := range routes {
			method, path := adminPath(pat)
			body := ""
			if method != "GET" && method != "DELETE" {
				body = "{}"
			}
			if code, b := call(t, c, srv.URL, method, path, body); !want(code) {
				t.Errorf("%s: %s %s = %d %s, want %s", who, method, path, code, b, wants)
			}
		}
	}
	answered := func(code int) bool { return code != 401 && code != 403 && code < 500 }

	every("signed out", noRedirects, func(c int) bool { return c == 401 }, "401")

	owner, to := signInAs(t, p, srv.URL, "1", "Owner@Example.TEST", true)
	if to != "/" || !isAdmin(t, owner, srv.URL) {
		t.Fatalf("INITIAL_ADMIN_EMAIL in another case, proven: sent to %q, admin %v", to, isAdmin(t, owner, srv.URL))
	}
	listener, _ := signInAs(t, p, srv.URL, "2", "listener@example.test", true)
	if isAdmin(t, listener, srv.URL) {
		t.Fatal("another address is an admin")
	}
	// Once an admin exists, the address makes nobody else one.
	if again, _ := signInAs(t, p, srv.URL, "4", initialAdmin, true); isAdmin(t, again, srv.URL) {
		t.Error("a second account with INITIAL_ADMIN_EMAIL is an admin")
	}
	every("a listener", listener, func(c int) bool { return c == 403 }, "403")
	every("the admin", owner, answered, "an answer")

	// A second admin, then demoted: refused at their next request.
	second, _ := signInAs(t, p, srv.URL, "3", "second@example.test", true)
	if code, b := call(t, owner, srv.URL, "PATCH", "/api/v1/admin/accounts/3", `{"role":"admin"}`); code != 200 || !strings.Contains(b, `"role":"admin"`) {
		t.Fatalf("promote = %d %s", code, b)
	}
	if !isAdmin(t, second, srv.URL) {
		t.Fatal("promoted, but not an admin")
	}
	every("the second admin", second, answered, "an answer")
	if code, b := call(t, owner, srv.URL, "PATCH", "/api/v1/admin/accounts/3", `{"role":"listener"}`); code != 200 {
		t.Fatalf("demote = %d %s", code, b)
	}
	every("a demoted admin", second, func(c int) bool { return c == 403 }, "403")

	// An admin never changes their own role.
	if code, b := call(t, owner, srv.URL, "PATCH", "/api/v1/admin/accounts/1", `{"role":"listener"}`); code != 409 || !strings.Contains(b, `"own_role"`) {
		t.Errorf("own role = %d %s, want 409 own_role", code, b)
	}
	if code, b := call(t, owner, srv.URL, "PATCH", "/api/v1/admin/accounts/3", `{"role":"owner"}`); code != 400 {
		t.Errorf("no such role = %d %s, want 400", code, b)
	}
	if code, b := call(t, owner, srv.URL, "PATCH", "/api/v1/admin/accounts/77", `{"role":"admin"}`); code != 404 {
		t.Errorf("no such account = %d %s, want 404", code, b)
	}

	// Disabled (issue #105 sets it; by hand here): their sessions aren't
	// live at the next request, and their sign-in is refused.
	if code, _ := call(t, owner, srv.URL, "PATCH", "/api/v1/admin/accounts/3", `{"role":"admin"}`); code != 200 {
		t.Fatal("promote again")
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE accounts SET status = 'disabled' WHERE subject = '3'`); err != nil {
		t.Fatal(err)
	}
	every("a disabled admin", second, func(c int) bool { return c == 401 }, "401")
	var sessions int
	_ = st.Pool.QueryRow(ctx, `SELECT count(*) FROM listener_sessions WHERE subject = '3'`).Scan(&sessions)
	if _, to := signInAs(t, p, srv.URL, "3", "second@example.test", true); to != "/?signin=disabled" {
		t.Errorf("a disabled account's sign-in went to %q", to)
	}
	var after int
	_ = st.Pool.QueryRow(ctx, `SELECT count(*) FROM listener_sessions WHERE subject = '3'`).Scan(&after)
	if after != sessions {
		t.Errorf("a disabled account's sign-in kept a session: %d, was %d", after, sessions)
	}

	// From another site: refused, and nothing is made.
	for _, h := range []map[string]string{
		{"Origin": "https://elsewhere.example", "Sec-Fetch-Site": "cross-site"},
		{"Origin": "https://elsewhere.example"},
		{"Sec-Fetch-Site": "same-site"},
	} {
		r, _ := http.NewRequest("POST", srv.URL+"/api/v1/admin/systems", strings.NewReader(`{"id":1,"label":"EX","name":"Example network"}`))
		r.Header.Set("Content-Type", "application/json")
		for k, v := range h {
			r.Header.Set(k, v)
		}
		resp, err := owner.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 403 || !strings.Contains(string(b), `"other_site"`) {
			t.Errorf("cross-site POST with %v = %d %s, want 403 other_site", h, resp.StatusCode, b)
		}
	}
	var systems int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM systems`).Scan(&systems); err != nil || systems != 0 {
		t.Errorf("%d systems after cross-site requests, %v", systems, err)
	}

	// Logs name accounts by ID, never by email.
	for _, s := range []string{"owner@example.test", "Owner@Example.TEST", "listener@example.test", "second@example.test"} {
		if strings.Contains(logs.String(), s) {
			t.Errorf("a log line holds %q", s)
		}
	}
	p4NoAddresses(t, logs)
}

// TestIntegrationFirstAdminUnproven: an address Authentik hasn't proven
// (email_verified false, as Authentik's own mapping always says) makes
// nobody admin, INITIAL_ADMIN_EMAIL or not.
func TestIntegrationFirstAdminUnproven(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := oidctest.New(t, p4ClientSecret, "1", initialAdmin, "Made Up")
	_, _, srv, _ := binaryServer(t, ctx, p.URL, environ{"INITIAL_ADMIN_EMAIL": initialAdmin})
	if c, _ := signInAs(t, p, srv.URL, "1", initialAdmin, false); isAdmin(t, c, srv.URL) {
		t.Error("an unproven INITIAL_ADMIN_EMAIL is an admin")
	}
	if c, _ := signInAs(t, p, srv.URL, "1", initialAdmin, true); !isAdmin(t, c, srv.URL) {
		t.Error("proven at the next sign-in, it isn't an admin")
	}
}

// TestIntegrationA13AdminHearsWhatAnyoneHears is A13's listener half for
// the binary (P3, R34, R111): signed in as an admin, for real, the public
// routes and the live feed answer byte for byte as they do an anonymous
// listener, so hidden 1003, which the admin set up through the admin API,
// is absent. internal/server's TestIntegrationSignedInSameAnswers and
// TestIntegrationSignedInSameLiveFeed cover every public route, with
// calls.
func TestIntegrationA13AdminHearsWhatAnyoneHears(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := oidctest.New(t, p4ClientSecret, "1", initialAdmin, "Made Up")
	_, _, srv, _ := binaryServer(t, ctx, p.URL, environ{"INITIAL_ADMIN_EMAIL": initialAdmin})
	admin, _ := signInAs(t, p, srv.URL, "1", initialAdmin, true)
	if !isAdmin(t, admin, srv.URL) {
		t.Fatal("not an admin")
	}
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/api/v1/admin/systems", `{"id":1,"label":"EX","name":"Example network"}`},
		{"PUT", "/api/v1/admin/systems/1/talkgroups/1001", `{"label":"DISP N","name":"Dispatch North","tag":"dispatch","state":"public"}`},
		{"PUT", "/api/v1/admin/systems/1/talkgroups/1003", `{"label":"ADMIN","name":"Admin","tag":"admin","state":"hidden"}`},
	} {
		if code, b := call(t, admin, srv.URL, c.method, c.path, c.body); code >= 300 {
			t.Fatalf("%s %s = %d %s", c.method, c.path, code, b)
		}
	}
	// The admin sees 1003 in the admin API.
	if code, b := call(t, admin, srv.URL, "GET", "/api/v1/admin/systems/1/talkgroups/1003", ""); code != 200 || !strings.Contains(b, "Admin") {
		t.Fatalf("admin talkgroup 1003 = %d %s", code, b)
	}
	for _, path := range []string{"/api/v1/catalogue", "/api/v1/catalogue/search?q=admin", "/api/v1/catalogue/search?q=1003",
		"/api/v1/recent-calls", "/api/v1/recent-calls?talkgroups=1:1003"} {
		anonCode, anonBody := call(t, noRedirects, srv.URL, "GET", path, "")
		code, body := call(t, admin, srv.URL, "GET", path, "")
		if code != anonCode || body != anonBody {
			t.Errorf("GET %s: as an admin %d %s; anonymous %d %s", path, code, body, anonCode, anonBody)
		}
		if strings.Contains(body, "1003") || strings.Contains(body, `"Admin"`) {
			t.Errorf("GET %s shows hidden 1003: %s", path, body)
		}
	}

	// The live feed, with 1003 in the set: the same frames, and 1003 isn't
	// being listened to.
	feed := func(h http.Header) []string {
		wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
		defer wcancel()
		ws, _, err := websocket.Dial(wctx, "ws"+strings.TrimPrefix(srv.URL, "http")+public.LivePath, &websocket.DialOptions{HTTPHeader: h})
		if err != nil {
			t.Fatalf("live feed: %v", err)
		}
		defer ws.CloseNow()
		if err := ws.Write(wctx, websocket.MessageText, []byte(`{"type":"set","talkgroups":[{"id":1001},{"id":1003}]}`)); err != nil {
			t.Fatal(err)
		}
		var got []string
		for {
			_, b, err := ws.Read(wctx)
			if err != nil {
				t.Fatalf("live feed: %v", err)
			}
			got = append(got, string(b))
			if strings.Contains(string(b), `"listening"`) {
				return got
			}
		}
	}
	h := http.Header{}
	for _, c := range admin.Jar.Cookies(mustParse(t, srv.URL)) {
		h.Add("Cookie", c.Name+"="+c.Value)
	}
	if !strings.Contains(h.Get("Cookie"), "ra_session=") {
		t.Fatal("the admin's browser has no session cookie")
	}
	anon, adm := feed(nil), feed(h)
	if strings.Join(anon, "\n") != strings.Join(adm, "\n") {
		t.Errorf("the live feed differs:\nanonymous %q\nadmin     %q", anon, adm)
	}
	if last := adm[len(adm)-1]; strings.Contains(last, "1003") {
		t.Errorf("the admin listens to hidden 1003: %s", last)
	}
}
