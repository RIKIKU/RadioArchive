package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/RIKIKU/RadioArchive/internal/limits"
	"github.com/RIKIKU/RadioArchive/internal/public"
)

// remote is the headers of a request from addr, with more.
func remote(addr string, more ...string) http.Header {
	h := http.Header{"X-Test-Remote": {addr}}
	for i := 0; i+1 < len(more); i += 2 {
		h.Add(more[i], more[i+1])
	}
	return h
}

// liveRefusal dials the live socket expecting a refusal, and returns the
// handshake's answer.
func (e *env) liveRefusal(t *testing.T, h http.Header) answer {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, resp, err := websocket.Dial(ctx, e.liveURL(), &websocket.DialOptions{HTTPHeader: h})
	if err == nil {
		c.CloseNow()
		t.Fatal("the socket was let in")
	}
	if resp == nil {
		t.Fatalf("no answer: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	hd := resp.Header.Clone()
	hd.Del("Date")
	return answer{status: resp.StatusCode, header: hd, body: b}
}

// liveCheck is the browser's question after a refused socket (ADR 0037): a
// GET of the live path that isn't an upgrade.
func (e *env) liveCheck(t *testing.T, h http.Header) answer {
	t.Helper()
	f := &a10World{e: e}
	r := f.req("GET", public.LivePath, "")
	for k, v := range h {
		r.Header[k] = v
	}
	return do(t, r)
}

// liveRefused checks a is R71's refusal: status and code, Retry-After 30
// and an R78 body saying so.
func liveRefused(t *testing.T, what string, a answer, status int, code, msg string) {
	t.Helper()
	var body struct {
		Error      string `json:"error"`
		Message    string `json:"message"`
		RetryAfter int    `json:"retry_after"`
	}
	if a.status != status || a.header.Get("Retry-After") != "30" || json.Unmarshal(a.body, &body) != nil ||
		body.Error != code || body.Message != msg || body.RetryAfter != 30 {
		t.Errorf("%s: want %d %s %q:\n%s", what, status, code, msg, a)
	}
}

// stillListening checks each socket is still open and served, by sending
// it a set and getting its acknowledgement.
func stillListening(t *testing.T, ls []*listener) {
	t.Helper()
	for i, l := range ls {
		if err := l.closedWith(); err != nil {
			t.Fatalf("socket %d closed: %v", i+1, err)
		}
		l.set([]any{1001}, nil)
	}
}

// TestIntegrationA20Live is A20's socket half on the server (R71): at the
// default limits one client's 5th listener socket is refused with 429 and
// Retry-After while its 4 carry on; with 50 open from 13 clients, a new
// listener is refused as the radio being full, with Retry-After: 30, and
// none of the 50 drops; once one closes, a new listener gets in. A
// signed-in admin is let in over either cap. The browser's check answers
// as the handshake does, and 426 when there's room.
func TestIntegrationA20Live(t *testing.T) {
	e := newLimitedEnv(t, limits.Defaults())
	tooMany := "Too many live connections from here: at most 4 at once. Try again in 30 seconds."
	full := "The radio is full right now. Try again in 30 seconds."

	var open []*listener
	for i := range 4 {
		open = append(open, e.listenWith(t, []any{1001}, nil, remote(fmt.Sprintf("192.0.2.20:%d", 6000+i))))
	}
	liveRefused(t, "the 5th socket", e.liveRefusal(t, remote("192.0.2.20:6100")), 429, "too_many_live", tooMany)
	liveRefused(t, "the browser's check for the 5th", e.liveCheck(t, remote("192.0.2.20:6101")), 429, "too_many_live", tooMany)
	stillListening(t, open)
	// Another client isn't affected; its check says there's room.
	if a := e.liveCheck(t, remote("192.0.2.21:6000")); a.status != http.StatusUpgradeRequired {
		t.Errorf("a check with room: %s", a)
	}
	// An admin at the same address is let in, and isn't counted.
	admin := e.listenWith(t, []any{1001}, nil, remote("192.0.2.20:6102", "Cookie", "radioarchive_session=made-up", "X-Test-Admin", "admin-1"))
	if a := e.liveCheck(t, remote("192.0.2.20:6103", "Cookie", "radioarchive_session=made-up", "X-Test-Admin", "admin-1")); a.status != http.StatusUpgradeRequired {
		t.Errorf("an admin's check: %s", a)
	}
	// A signed-in listener isn't.
	liveRefused(t, "a signed-in listener's 5th", e.liveRefusal(t, remote("192.0.2.20:6104", "Cookie", "radioarchive_session=made-up")),
		429, "too_many_live", tooMany)

	// 46 more from 12 other clients: 50 in all.
	for i := range 46 {
		open = append(open, e.listenWith(t, []any{1001}, nil, remote(fmt.Sprintf("198.51.100.%d:7000", i/4))))
	}
	liveRefused(t, "the 51st, from a new client", e.liveRefusal(t, remote("203.0.113.30:8000")), 503, "radio_full", full)
	liveRefused(t, "the browser's check when full", e.liveCheck(t, remote("203.0.113.30:8001")), 503, "radio_full", full)
	// The module's token is an admin's too.
	module := e.listenWith(t, []any{1001}, nil, remote("203.0.113.31:8000", "Authorization", "Bearer admin-2"))
	stillListening(t, append(open, admin, module))

	// One closes: a new listener gets in, and the radio is full again.
	_ = open[10].conn.Close(websocket.StatusNormalClosure, "")
	deadline := time.Now().Add(5 * time.Second)
	for e.liveCheck(t, remote("203.0.113.30:8002")).status != http.StatusUpgradeRequired {
		if time.Now().After(deadline) {
			t.Fatal("no room after a socket closed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	open[10] = e.listenWith(t, []any{1001}, nil, remote("203.0.113.30:8003"))
	liveRefused(t, "the 51st again", e.liveRefusal(t, remote("203.0.113.32:8000")), 503, "radio_full", full)
	stillListening(t, open)
	// A recorder client is never counted: its own live socket connects
	// while the radio is full, and its call reaches the listeners.
	rec := e.liveRecorder(t)
	_, callID := rec.start(1001, nil, 5001, time.Now().UnixMilli())
	for _, l := range []*listener{open[0], open[49], admin} {
		l.wait("its call_start", isEvent("call_start", callID))
	}
}
