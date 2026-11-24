package main

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/RIKIKU/RadioArchive/internal/gate"
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

// TestIntegrationP4NothingKept is SPEC.md P4 for the binary's handler, with
// listeners' sign-in on: a listener who hasn't signed in uses every public
// page and route, and opens the account menu and the sign-in, and the
// server keeps nothing of them: no row is added or changed in any table,
// and no log line holds their address.
func TestIntegrationP4NothingKept(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st, err := store.Open(ctx, storetest.FreshURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
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
	// Authentik that isn't answering: sign-in says so, and nothing waits.
	down := httptest.NewServer(http.NotFoundHandler())
	defer down.Close()
	sc, err := signInFromEnv(environ{
		"APP_URL": "http://radio.example.test", "AUTHENTIK_URL": down.URL,
		"OIDC_CLIENT_SECRET": "made-up-client-secret-0123456789", "AUTHENTIK_API_TOKEN": "made-up-token",
	}.get)
	if err != nil {
		t.Fatal(err)
	}
	api, err := apiHandler(st, log, rs, g, sc)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = listenerIP + ":40001"
		r.Header.Set("X-Forwarded-For", forwardedIP)
		api.ServeHTTP(w, r)
	}))
	defer srv.Close()

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
		{"POST", "/auth/signout", ""},
		{"GET", "/auth/end-session", ""},
	} {
		r, _ := http.NewRequest(c.method, srv.URL+c.path, strings.NewReader(c.body))
		r.Header.Set("Origin", "http://radio.example.test")
		if c.body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		resp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(r)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		// (The app's pages answer 503 in a Go-only build, with no app built.)
		if resp.StatusCode >= 500 && (strings.HasPrefix(c.path, "/api/") || strings.HasPrefix(c.path, "/auth/")) {
			t.Errorf("%s %s = %d", c.method, c.path, resp.StatusCode)
		}
	}
	if after := rows(t, st); after != before {
		t.Errorf("the database changed:\nbefore %s\nafter  %s", before, after)
	}
	for _, ip := range []string{listenerIP, forwardedIP} {
		if strings.Contains(logs.String(), ip) {
			t.Errorf("a log line holds the listener's address %s:\n%s", ip, logs.String())
		}
	}
}

// rows is every table's row count and content hash: what a request could
// have added or changed.
func rows(t *testing.T, st *store.Store) string {
	t.Helper()
	ctx := context.Background()
	q, err := st.Pool.Query(ctx, `SELECT quote_ident(table_name) FROM information_schema.tables
		WHERE table_schema = 'public' AND table_type = 'BASE TABLE' ORDER BY table_name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for q.Next() {
		var n string
		if err := q.Scan(&n); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, n)
	}
	q.Close()
	if len(tables) < 5 {
		t.Fatalf("only tables %q", tables)
	}
	var b strings.Builder
	for _, tb := range tables {
		var n int64
		var sum string
		if err := st.Pool.QueryRow(ctx, fmt.Sprintf(`SELECT count(*), coalesce(md5(string_agg(t::text, ',' ORDER BY t::text)), '') FROM %s t`, tb)).Scan(&n, &sum); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "%s=%d:%s ", tb, n, sum[:min(8, len(sum))])
	}
	return b.String()
}
