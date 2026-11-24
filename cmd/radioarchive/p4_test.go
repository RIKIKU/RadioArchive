package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/RIKIKU/RadioArchive/internal/gate"
	"github.com/RIKIKU/RadioArchive/internal/oidcclient/oidctest"
	"github.com/RIKIKU/RadioArchive/internal/public"
	"github.com/RIKIKU/RadioArchive/internal/retention"
	"github.com/RIKIKU/RadioArchive/internal/server"
	"github.com/RIKIKU/RadioArchive/internal/store"
	"github.com/RIKIKU/RadioArchive/internal/store/storetest"
	"github.com/RIKIKU/RadioArchive/internal/version"
)

// Made-up listeners' addresses: the connecting one, and the one a proxy
// forwards.
const (
	listenerIP  = "203.0.113.45"
	forwardedIP = "2001:db8:41::7"
)

const p4ClientSecret = "made-up-client-secret-0123456789"

// p4Server is the binary's handler as serve wires it, with listeners'
// sign-in on against authentikURL, on a fresh database, behind a proxy that
// says every request is from a listener's address. It returns the store,
// the log and the server, which is also APP_URL.
func p4Server(t *testing.T, ctx context.Context, authentikURL string) (*store.Store, *bytes.Buffer, *httptest.Server) {
	t.Helper()
	st, err := store.Open(ctx, storetest.FreshURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	logs := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	dir := t.TempDir()
	// As serve wires it: the gate, told when retention deletes a call, and
	// its live feed.
	job, err := retention.New(retention.Config{Store: st, Dir: dir, Log: log})
	if err != nil {
		t.Fatal(err)
	}
	g := gate.New(st, dir, log, gate.WithRetention(job))
	go g.Live().Run(ctx)
	rs, err := server.NewRecorders(st, log, server.RecordersConfig{RecordingsDir: dir, ServerVersion: version.Version,
		Live: g.Live(), Notice: g.Live()})
	if err != nil {
		t.Fatal(err)
	}
	var api http.Handler
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = listenerIP + ":40001"
		r.Header.Set("X-Forwarded-For", forwardedIP)
		api.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	sc, err := signInFromEnv(environ{
		"APP_URL": srv.URL, "AUTHENTIK_URL": authentikURL, "OIDC_CLIENT_SECRET": p4ClientSecret,
	}.get)
	if err != nil {
		t.Fatal(err)
	}
	if api, err = apiHandler(st, log, rs, g, sc); err != nil {
		t.Fatal(err)
	}
	return st, logs, srv
}

// TestIntegrationP4NothingKept is SPEC.md P4 for the binary's handler, with
// listeners' sign-in on: a listener who hasn't signed in uses every public
// page and route, and opens the account menu and the sign-in, and the
// server keeps nothing of them: no row is added or changed in any table,
// and no log line holds their address.
func TestIntegrationP4NothingKept(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Authentik that isn't answering: sign-in says so, and nothing waits.
	down := httptest.NewServer(http.NotFoundHandler())
	defer down.Close()
	st, logs, srv := p4Server(t, ctx, down.URL)

	before := rows(t, st)
	// The live feed: a listener connects, sends a set, is answered, leaves.
	wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
	defer wcancel()
	ws, _, err := websocket.Dial(wctx, "ws"+strings.TrimPrefix(srv.URL, "http")+public.LivePath, nil)
	if err != nil {
		t.Fatalf("live feed: %v", err)
	}
	if err := ws.Write(wctx, websocket.MessageText, []byte(`{"type":"set","talkgroups":[{"id":1001}]}`)); err != nil {
		t.Fatal(err)
	}
	for {
		_, b, err := ws.Read(wctx)
		if err != nil {
			t.Fatalf("live feed: no answer to the set: %v", err)
		}
		if strings.Contains(string(b), `"listening"`) {
			break
		}
	}
	ws.Close(websocket.StatusNormalClosure, "")
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/", ""},
		{"GET", "/?fireground=41", ""},
		{"GET", "/robots.txt", ""},
		{"GET", "/healthz", ""},
		{"GET", "/api/v1/catalogue", ""},
		{"GET", "/api/v1/catalogue/search?q=north", ""},
		{"GET", "/api/v1/fireground/41", ""},
		{"QUERY", "/api/v1/calls", `{"ids":["6f1d2c3b-4a59-4e8f-9d7c-0b1a2c3d4e5f"]}`},
		{"POST", "/api/v1/calls", `{"ids":["6f1d2c3b-4a59-4e8f-9d7c-0b1a2c3d4e5f"]}`},
		{"GET", "/api/v1/calls/6f1d2c3b-4a59-4e8f-9d7c-0b1a2c3d4e5f/recording", ""},
		// The basket (R64, ADR 0018) lives in the browser: its page, its
		// availability check (the call list, above), its zips and the call
		// link keep nothing either.
		{"GET", "/basket", ""},
		// A shared conversation's page (R66, ADR 0020): its list is in the
		// fragment, so all the server sees is the path.
		{"GET", "/conversation", ""},
		{"GET", "/api/v1/recent-calls", ""},
		{"GET", public.ZipURL(public.ZipRequest{Calls: []string{"6f1d2c3b-4a59-4e8f-9d7c-0b1a2c3d4e5f"},
			Conversations: []public.ZipConversation{{Name: "Structure fire", Note: "A note", Calls: []string{"0f8e2c1a-6b3d-4e5f-9a7b-1c2d3e4f5a6b"}}}}), ""},
		{"POST", "/api/v1/zip", `{"calls":["6f1d2c3b-4a59-4e8f-9d7c-0b1a2c3d4e5f"],"conversations":[]}`},
		{"GET", "/calls/6f1d2c3b-4a59-4e8f-9d7c-0b1a2c3d4e5f", ""},
		{"GET", "/api/v1/account", ""},
		{"GET", "/auth/signin?return=/", ""},
		{"GET", "/auth/callback?code=x&state=y", ""},
		{"PUT", "/api/v1/account/profile", `{"v":1,"talkgroups":[{"id":1001}]}`},
		{"PUT", "/api/v1/account/profile", `{"v":1,"talkgroups":[],"basket":{"calls":[{"id":"6f1d2c3b-4a59-4e8f-9d7c-0b1a2c3d4e5f","added":1764000000000}],"conversations":[]}}`},
		{"DELETE", "/api/v1/account/profile", ""},
		{"POST", "/auth/signout", ""},
		{"GET", "/auth/end-session", ""},
	} {
		resp := p4Do(t, noRedirects, srv.URL, c.method, c.path, c.body)
		// (The app's pages answer 503 in a Go-only build, with no app built.)
		if resp.StatusCode >= 500 && (strings.HasPrefix(c.path, "/api/") || strings.HasPrefix(c.path, "/auth/")) {
			t.Errorf("%s %s = %d", c.method, c.path, resp.StatusCode)
		}
	}
	if after := rows(t, st); after.String() != before.String() {
		t.Errorf("the database changed:\nbefore %s\nafter  %s", before, after)
	}
	p4NoAddresses(t, logs)
}

// TestIntegrationP4SignedInProfile is the other side of P4 (R110, ADR
// 0021): a signed-in listener's saved settings are exactly one row of
// listener_profiles, keyed by the account and holding nothing of the
// listener's address; saving again changes that row and adds none; Clear
// saved settings deletes it, leaving the database as it was. Anonymous
// saves alongside keep nothing.
func TestIntegrationP4SignedInProfile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := oidctest.New(t, p4ClientSecret, "42", "listener@example.test", "Made Up")
	st, logs, srv := p4Server(t, ctx, p.URL)
	anon := noRedirects
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar, CheckRedirect: noRedirects.CheckRedirect}
	const profile = `{"v":1,"talkgroups":[{"id":1001,"priority":true}],"look":"console","alerts":false,` +
		`"basket":{"calls":[{"id":"6f1d2c3b-4a59-4e8f-9d7c-0b1a2c3d4e5f","added":1764000000000}],"conversations":[]}}`

	before := rows(t, st)
	resp := p4Do(t, browser, srv.URL, "GET", "/auth/signin?return=/", "")
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("sign-in = %d", resp.StatusCode)
	}
	code, state := p.Grant(t, resp.Header.Get("Location"))
	if resp := p4Do(t, browser, srv.URL, "GET", "/auth/callback?code="+code+"&state="+state, ""); resp.Header.Get("Location") != "/" {
		t.Fatalf("callback went to %q", resp.Header.Get("Location"))
	}
	// Signing in, and reading an account with nothing saved, keep nothing.
	if resp := p4Do(t, browser, srv.URL, "GET", "/api/v1/account", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("account = %d", resp.StatusCode)
	}
	if after := rows(t, st); after.String() != before.String() {
		t.Fatalf("signing in changed the database:\nbefore %s\nafter  %s", before, after)
	}

	for i, body := range []string{profile, strings.Replace(profile, `"look":"console"`, `"look":"harbour"`, 1)} {
		if resp := p4Do(t, browser, srv.URL, "PUT", "/api/v1/account/profile", body); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("save %d = %d", i, resp.StatusCode)
		}
		// An anonymous listener saving at the same time keeps nothing.
		if resp := p4Do(t, anon, srv.URL, "PUT", "/api/v1/account/profile", body); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("anonymous save = %d", resp.StatusCode)
		}
		after := rows(t, st)
		for tb, v := range after {
			if tb != "listener_profiles" && v != before[tb] {
				t.Errorf("save %d changed %s: %s to %s", i, tb, before[tb], v)
			}
		}
		if !strings.HasPrefix(after["listener_profiles"], "1:") {
			t.Fatalf("save %d: listener_profiles %s, want one row", i, after["listener_profiles"])
		}
	}
	var issuer, subject, saved string
	if err := st.Pool.QueryRow(ctx, `SELECT issuer, subject, profile::text FROM listener_profiles`).Scan(&issuer, &subject, &saved); err != nil {
		t.Fatal(err)
	}
	if issuer != p.Issuer() || subject != "42" || !strings.Contains(saved, `"harbour"`) {
		t.Errorf("the row is %q %q %s", issuer, subject, saved)
	}
	for _, s := range []string{listenerIP, forwardedIP, "listener@example.test", "Made Up"} {
		if strings.Contains(saved, s) {
			t.Errorf("the profile holds %q", s)
		}
	}

	if resp := p4Do(t, browser, srv.URL, "DELETE", "/api/v1/account/profile", ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("clear = %d", resp.StatusCode)
	}
	if after := rows(t, st); after.String() != before.String() {
		t.Errorf("after Clear saved settings the database isn't as it was:\nbefore %s\nafter  %s", before, after)
	}
	p4NoAddresses(t, logs)
}

// noRedirects is a client that follows no redirect, keeping no cookie.
var noRedirects = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// p4Do sends a request as the app's own page would, from the site itself.
func p4Do(t *testing.T, c *http.Client, base, method, path, body string) *http.Response {
	t.Helper()
	r, _ := http.NewRequest(method, base+path, strings.NewReader(body))
	r.Header.Set("Origin", base)
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp
}

// p4NoAddresses fails when a log line holds a listener's address.
func p4NoAddresses(t *testing.T, logs *bytes.Buffer) {
	t.Helper()
	for _, ip := range []string{listenerIP, forwardedIP} {
		if strings.Contains(logs.String(), ip) {
			t.Errorf("a log line holds the listener's address %s:\n%s", ip, logs.String())
		}
	}
}

// tables is every table's row count and content hash, by name.
type tables map[string]string

func (tb tables) String() string {
	var b strings.Builder
	for _, n := range slices.Sorted(maps.Keys(tb)) {
		fmt.Fprintf(&b, "%s=%s ", n, tb[n])
	}
	return b.String()
}

// rows is every table's row count and content hash: what a request could
// have added or changed.
func rows(t *testing.T, st *store.Store) tables {
	t.Helper()
	ctx := context.Background()
	q, err := st.Pool.Query(ctx, `SELECT quote_ident(table_name) FROM information_schema.tables
		WHERE table_schema = 'public' AND table_type = 'BASE TABLE' ORDER BY table_name`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for q.Next() {
		var n string
		if err := q.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	q.Close()
	if len(names) < 5 {
		t.Fatalf("only tables %q", names)
	}
	out := tables{}
	for _, tb := range names {
		var n int64
		var sum string
		if err := st.Pool.QueryRow(ctx, fmt.Sprintf(`SELECT count(*), coalesce(md5(string_agg(t::text, ',' ORDER BY t::text)), '') FROM %s t`, tb)).Scan(&n, &sum); err != nil {
			t.Fatal(err)
		}
		out[tb] = fmt.Sprintf("%d:%s", n, sum[:min(8, len(sum))])
	}
	return out
}
