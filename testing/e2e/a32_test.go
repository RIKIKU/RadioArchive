//go:build cgo

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/RIKIKU/RadioArchive/internal/limits"
	"github.com/RIKIKU/RadioArchive/internal/public"
	"github.com/RIKIKU/RadioArchive/internal/server"
	"github.com/RIKIKU/RadioArchive/internal/version"
)

// lockedBuffer is a log both the server and the client write to.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// a32Listener is a made-up listener's address, which the rig's proxy
// (127.0.0.1, TRUSTED_PROXY) forwards.
const a32Listener = "203.0.113.77"

// TestA32NoNamesInTelemetry is SPEC.md A32 (R90, P1; ADR 0040) end to end:
// a day's kinds of fixture traffic, compressed, through the real recorder
// client and the server: calls on every fixture talkgroup (public, patched,
// hidden, ignored, unknown and unattached) from every fixture unit,
// heartbeats with a lost control channel, listeners on the live feed, the
// public API (catalogue, search, recent calls, unit search, call details,
// downloads and a zip) from a listener's address behind the proxy, admin
// edits, and requests over a limit. Then no log line (the server's and the
// client's) and no metric holds a talkgroup or unit name or the listener's
// address. RadioArchive sends no traces. The audit harness
// (cmd/radioarchive) checks the admin routes' logs for every name an audit
// entry holds.
func TestA32NoNamesInTelemetry(t *testing.T) {
	r := newRig(t, version.Version)
	logs := &lockedBuffer{}
	r.log = slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	r.metricsToken = "a32-made-up-metrics-token-0123456789"
	lc := limits.Defaults()
	lc.TrustedProxy = netip.MustParseAddr("127.0.0.1")
	lc.Requests = 40
	lim, err := limits.New(lc)
	if err != nil {
		t.Fatal(err)
	}
	r.lim = lim
	r.serverUp(t)
	for unit, alias := range map[string]string{"5001": "Engine 1", "5100-5199": "Portables"} {
		r.admin(t, "PUT", fmt.Sprintf("/systems/%d/units/%s", r.sys, unit), map[string]any{"alias": alias}, 201)
	}
	r.clientUp(t, "")
	waitFor(t, "the client's live connection", 10*time.Second, r.client.LiveConnected)

	// Listeners: each set, the hidden talkgroup's included, from the
	// listener's address.
	for _, set := range [][]int{{1001}, {1002}, {1001, 1003}} {
		a32Listen(t, r.api, set)
	}
	r.replay(t, "acceptance.json")
	r.replay(t, "a10-hidden.json")
	r.replay(t, "a3-ignored.json")
	stored := r.waitRecordings(t, 5)

	// An admin's edits: a rename, hiding and making public again.
	tg := fmt.Sprintf("/systems/%d/talkgroups/1002", r.sys)
	for _, state := range []string{"hidden", "public"} {
		r.admin(t, "PUT", tg, map[string]any{"label": "FG 41", "name": "Fireground 41", "tag": "fireground", "state": state}, 200)
	}

	// The public API, as a listener uses it, then over the request limit.
	var ids []string
	for _, c := range stored {
		ids = append(ids, c.id)
	}
	list, _ := json.Marshal(map[string]any{"ids": ids})
	zip, _ := json.Marshal(map[string]any{"calls": ids, "conversations": []any{}})
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/v1/catalogue", ""},
		{"GET", "/api/v1/catalogue/search?q=north", ""},
		{"GET", "/api/v1/recent-calls", ""},
		{"GET", "/api/v1/units/search?q=engine", ""},
		{"QUERY", "/api/v1/calls", string(list)},
		{"POST", "/api/v1/calls", string(list)},
		{"GET", "/api/v1/calls/" + ids[0] + "/recording", ""},
		{"GET", "/api/v1/calls/" + ids[0] + "/download", ""},
		{"POST", "/api/v1/zip", string(zip)},
	} {
		a32Do(t, r.api, c.method, c.path, c.body)
	}
	refused := 0
	for range lc.Requests + 5 {
		if a32Do(t, r.api, "GET", "/api/v1/catalogue", "") == http.StatusTooManyRequests {
			refused++
		}
	}
	if refused == 0 {
		t.Fatal("no request was refused over the limit")
	}

	// The metrics: only with the token.
	if code, _ := a32Metrics(t, r.api, ""); code != http.StatusUnauthorized {
		t.Errorf("/metrics with no token = %d, want 401", code)
	}
	code, metrics := a32Metrics(t, r.api, r.metricsToken)
	if code != http.StatusOK {
		t.Fatalf("/metrics = %d %s", code, metrics)
	}
	for _, want := range []string{
		`radioarchive_calls_rejected_total{reason="not_attached"} 1`,
		`radioarchive_calls_rejected_total{reason="unknown_talkgroup"} 1`,
		`radioarchive_calls_rejected_total{reason="ignored_talkgroup"} 1`,
		fmt.Sprintf(`radioarchive_recorder_connected{recorder=%q} 1`, r.name),
		fmt.Sprintf(`radioarchive_rejected_unattached_calls{recorder=%q,short_name="other"} 1`, r.name),
		fmt.Sprintf(`radioarchive_rejected_talkgroup_calls{system="%d",talkgroup="4711",reason="unknown_talkgroup"} 1`, r.sys),
		fmt.Sprintf(`radioarchive_limit_hits_total{limit="requests"} %d`, refused),
		`radioarchive_live_delay_seconds_count`,
		`radioarchive_telemetry_dropped_total 0`,
	} {
		if !strings.Contains(metrics, want) {
			t.Errorf("/metrics has no %s", want)
		}
	}

	// Names and the address: in nothing.
	names := regexp.MustCompile(`Dispatch North|DISP N|Fireground 41|FG 41|\bAdmin\b|\bADMIN\b|Engine 1|Portables`)
	for what, text := range map[string]string{"a log line": logs.String(), "a metric": metrics} {
		for _, line := range strings.Split(text, "\n") {
			if m := names.FindString(line); m != "" {
				t.Errorf("%s holds the name %q: %s", what, m, line)
			}
			if strings.Contains(line, a32Listener) {
				t.Errorf("%s holds the listener's address: %s", what, line)
			}
		}
	}
	if !strings.Contains(logs.String(), `"msg"`) {
		t.Fatal("nothing was logged: the check checked nothing")
	}
}

// a32Listen opens a listener on the live feed from the listener's address,
// and keeps it open, reading, until the test ends.
func a32Listen(t *testing.T, api string, set []int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h := http.Header{"X-Forwarded-For": {a32Listener}}
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(api, "http")+public.LivePath, &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	b, _ := json.Marshal(map[string]any{"type": "set", "talkgroups": set})
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			if _, _, err := c.Read(context.Background()); err != nil {
				return
			}
		}
	}()
}

func a32Do(t *testing.T, api, method, path, body string) int {
	t.Helper()
	req, _ := http.NewRequest(method, api+path, strings.NewReader(body))
	req.Header.Set("X-Forwarded-For", a32Listener)
	req.Header.Set("Origin", api)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

func a32Metrics(t *testing.T, api, token string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", api+server.MetricsPath, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}
