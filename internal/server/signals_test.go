package server_test

import (
	"bytes"
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
	"github.com/RIKIKU/RadioArchive/internal/server"
)

// Recorder signals and the recorder notice (SPEC.md R14, R15, R90; A27's
// server and feed half; ADR 0010), on the test's clock.

// The recorder notice's bytes, whichever recorder or system caused it.
var (
	noticeOn  = []byte(`{"type":"notice","notice":"recorders","active":true}`)
	noticeOff = []byte(`{"type":"notice","notice":"recorders","active":false}`)
)

func heartbeat(systems ...cp.SystemRate) cp.Heartbeat {
	if systems == nil {
		systems = []cp.SystemRate{}
	}
	return cp.Heartbeat{Type: cp.TypeHeartbeat, ClientVersion: "0.1.0-test", TimeMS: time.Now().UnixMilli(),
		Systems: systems}
}

func rate(short, typ string, r float64) cp.SystemRate {
	return cp.SystemRate{ShortName: short, SystemType: typ, DecodeRate: r}
}

func (e *env) recorderStatus(t *testing.T, name string) server.RecorderStatus {
	t.Helper()
	return decodeJSON[server.RecorderStatus](t, e.mustAdmin(t, "GET", "/recorders/"+url.PathEscape(name), nil, 200))
}

// waitStatus polls the admin API until ok accepts the recorder's state.
func (e *env) waitStatus(t *testing.T, name, what string, ok func(server.RecorderStatus) bool) server.RecorderStatus {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		s := e.recorderStatus(t, name)
		if ok(s) {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("recorder never %s: %+v", what, s)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// notices is the recorder notices the listener has had, in order.
func (l *listener) notices() [][]byte {
	var out [][]byte
	for _, m := range l.all() {
		if !m.Binary && m.Type == "notice" {
			out = append(out, m.Raw)
		}
	}
	return out
}

// waitNotices waits until the listener has had n notices, the last of them
// last.
func (l *listener) waitNotices(n int, last []byte) {
	l.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		got := l.notices()
		if len(got) == n && bytes.Equal(got[n-1], last) {
			return
		}
		if len(got) > n || time.Now().After(deadline) {
			l.t.Fatalf("listener's notices %q, want %d ending %s", got, n, last)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// settle waits for a few signal checks, for a test that wants nothing to
// happen.
func settle() { time.Sleep(2500 * time.Millisecond) }

// later moves the test's clock to d from now.
func (e *env) later(d time.Duration) { e.clock.set(time.Now().Add(d)) }

// A27, disconnected: a recorder whose connection drops shows disconnected
// at once, and after 2 minutes every listener has the notice, a new one at
// once; it clears when the recorder reconnects.
func TestIntegrationA27Disconnected(t *testing.T) {
	e := newEnv(t)
	name, key := e.recorder(t)
	c := e.open(t, key)
	send(t, c, heartbeat(rate("ex", "p25", 38.5)))
	s := e.waitStatus(t, name, "connected and decoding", func(s server.RecorderStatus) bool {
		return s.Connected && len(s.Systems) == 1 && s.Systems[0].DecodeRate != nil
	})
	if s.Warning || s.ConnectedSince == nil || s.LastConnectedAt == nil || s.ClientVersion == nil ||
		*s.ClientVersion != "0.1.0-test" || s.Systems[0].SystemID == nil || *s.Systems[0].SystemID != e.sys {
		t.Errorf("connected recorder = %+v", s)
	}
	l := e.listen(t, []any{1001}, nil)

	c.Close(websocket.StatusNormalClosure, "")
	s = e.waitStatus(t, name, "disconnected", func(s server.RecorderStatus) bool { return s.Signals.Disconnected })
	if s.Connected || !s.Warning || len(s.Warnings) != 1 || !strings.HasPrefix(s.Warnings[0], "Disconnected for") ||
		s.DisconnectedSince == nil || s.Signals.Deaf {
		t.Errorf("disconnected recorder = %+v", s)
	}
	// Not yet 2 minutes: no notice.
	e.later(time.Minute + 50*time.Second)
	settle()
	if n := l.notices(); len(n) != 0 {
		t.Fatalf("notice before 2 minutes: %q", n)
	}
	e.later(2*time.Minute + time.Second)
	l.waitNotices(1, noticeOn)
	if got := e.rs.Sig.TrippedCount(server.SignalDisconnected); got != 1 {
		t.Errorf("disconnected tripped %d times, want 1", got)
	}
	if !e.rs.Sig.NoticeActive.Load() || e.rs.Sig.NoticeRaised.Load() != 1 {
		t.Error("notice metrics don't show it on")
	}
	// A listener who connects now gets it at once, before naming a set.
	late := e.listen(t, nil, nil)
	late.waitNotices(1, noticeOn)

	// Reconnecting clears it, for both.
	e.open(t, key)
	l.waitNotices(2, noticeOff)
	late.waitNotices(2, noticeOff)
	if s := e.waitStatus(t, name, "connected", func(s server.RecorderStatus) bool { return s.Connected }); s.Signals.Disconnected {
		t.Errorf("reconnected recorder = %+v", s)
	}
	// One who connects now gets nothing.
	fresh := e.listen(t, nil, nil)
	settle()
	if n := fresh.notices(); len(n) != 0 {
		t.Errorf("a listener connecting with the notice off got %q", n)
	}
}

// A27, deaf: a decode rate of 0 on an attached trunked system for 2
// minutes puts the notice on, and a healthy one clears it. So does the
// plugin going silent (no systems relayed). A conventional system's rate,
// and an unattached system's, are never watched.
func TestIntegrationA27Deaf(t *testing.T) {
	e := newEnv(t)
	name, key := e.recorder(t)
	c := e.open(t, key)
	l := e.listen(t, []any{1001}, nil)

	send(t, c, heartbeat(rate("ex", "p25", 0), rate("conv", "conventional", 0), rate("other", "p25", 0)))
	s := e.waitStatus(t, name, "deaf", func(s server.RecorderStatus) bool { return s.Signals.Deaf })
	if s.DeafReason == nil || *s.DeafReason != server.DeafDecodeRate || !s.Warning || len(s.Systems) != 3 ||
		!strings.Contains(s.Warnings[0], "Control channel lost on ex") {
		t.Errorf("deaf recorder = %+v", s)
	}
	for _, sys := range s.Systems {
		if want := sys.ShortName == "ex"; sys.Deaf != want || (sys.SystemID != nil) != want || sys.DecodeRate == nil {
			t.Errorf("system %+v: deaf %v, want %v", sys, sys.Deaf, want)
		}
	}
	e.later(2*time.Minute + time.Second)
	l.waitNotices(1, noticeOn)
	send(t, c, heartbeat(rate("ex", "p25", 38.5)))
	l.waitNotices(2, noticeOff)
	if s := e.recorderStatus(t, name); s.Signals.Deaf || s.Warning {
		t.Errorf("recovered recorder = %+v", s)
	}

	// The plugin goes silent: trunk-recorder is down.
	e.clock.set(time.Time{})
	send(t, c, heartbeat())
	s = e.waitStatus(t, name, "down", func(s server.RecorderStatus) bool { return s.Signals.Deaf })
	if *s.DeafReason != server.DeafNoHeartbeat || !strings.HasPrefix(s.Warnings[0], "No heartbeat from trunk-recorder") {
		t.Errorf("down recorder = %+v", s)
	}
	e.later(2*time.Minute + time.Second)
	l.waitNotices(3, noticeOn)
	send(t, c, heartbeat(rate("ex", "p25", 38.5)))
	l.waitNotices(4, noticeOff)

	// Only ex's rate is watched.
	e.clock.set(time.Time{})
	send(t, c, heartbeat(rate("ex", "p25", 38.5), rate("conv", "conventional", 0), rate("other", "p25", 0)))
	e.later(10 * time.Minute)
	settle()
	if n := l.notices(); len(n) != 4 {
		t.Errorf("notices %q after a healthy ex", n)
	}
	if got := e.rs.Sig.TrippedCount(server.SignalDeaf); got != 2 {
		t.Errorf("deaf tripped %d times, want 2", got)
	}
}

// A27, quiet: a recorder with no calls past its quiet threshold shows
// quiet to admins, and puts nothing on the feed. The threshold is off by
// default.
func TestIntegrationA27Quiet(t *testing.T) {
	e := newEnv(t)
	name, key := e.recorder(t)
	c := e.open(t, key)
	send(t, c, heartbeat(rate("ex", "p25", 38.5)))
	l := e.listen(t, []any{1001}, nil)

	e.later(24 * time.Hour)
	settle()
	if s := e.recorderStatus(t, name); s.Signals.Quiet || s.QuietAfterMinutes != nil || s.Warning {
		t.Errorf("quiet with no threshold: %+v", s)
	}

	e.clock.set(time.Time{})
	start := callStart(ident)
	start.StartTimeMS = time.Now().UnixMilli()
	send(t, c, start)
	e.waitStatus(t, name, "with a last call", func(s server.RecorderStatus) bool { return s.LastCallAt != nil })
	s := decodeJSON[server.RecorderStatus](t, e.mustAdmin(t, "PATCH", "/recorders/"+url.PathEscape(name),
		map[string]any{"quiet_after_minutes": 30}, 200))
	if s.QuietAfterMinutes == nil || *s.QuietAfterMinutes != 30 || s.Signals.Quiet {
		t.Errorf("after setting 30 min: %+v", s)
	}
	e.later(31 * time.Minute)
	s = e.waitStatus(t, name, "quiet", func(s server.RecorderStatus) bool { return s.Signals.Quiet })
	if !s.Warning || len(s.Warnings) != 1 || !strings.HasPrefix(s.Warnings[0], "Quiet: no calls for 3") {
		t.Errorf("quiet recorder = %+v", s)
	}
	settle()
	if n := l.notices(); len(n) != 0 {
		t.Errorf("quiet put %q on the feed", n)
	}
	if got := e.rs.Sig.TrippedCount(server.SignalQuiet); got != 1 {
		t.Errorf("quiet tripped %d times, want 1", got)
	}

	// Off again, and bad values.
	s = decodeJSON[server.RecorderStatus](t, e.mustAdmin(t, "PATCH", "/recorders/"+url.PathEscape(name),
		map[string]any{"quiet_after_minutes": nil}, 200))
	if s.QuietAfterMinutes != nil || s.Signals.Quiet {
		t.Errorf("after turning it off: %+v", s)
	}
	for _, v := range []any{0, -1, 10081, 1.5, "30"} {
		e.wantRefusal(t, "PATCH", "/recorders/"+url.PathEscape(name), map[string]any{"quiet_after_minutes": v},
			400, "invalid_quiet_after_minutes")
	}
	e.wantRefusal(t, "PATCH", "/recorders/"+url.PathEscape(name), map[string]any{}, 400, "invalid_body")
	e.wantRefusal(t, "PATCH", "/recorders/nobody", map[string]any{"quiet_after_minutes": 5}, 404, "recorder_not_found")
	e.wantRefusal(t, "GET", "/recorders/nobody", nil, 404, "recorder_not_found")
}

// The notice is the same bytes whichever recorder, signal or system raised
// it, and names none of them; and it stays on while any recorder still
// raises it.
func TestIntegrationRecorderNoticeNamesNothing(t *testing.T) {
	e := newEnv(t)
	nameA, keyA := e.recorder(t)
	nameB, keyB := e.recorder(t)
	a, b := e.open(t, keyA), e.open(t, keyB)
	send(t, a, heartbeat(rate("ex", "p25", 38.5)))
	send(t, b, heartbeat(rate("ex", "p25", 38.5)))
	l := e.listen(t, []any{1001, 1002, 1003}, nil)

	// A drops.
	a.Close(websocket.StatusNormalClosure, "")
	e.waitStatus(t, nameA, "disconnected", func(s server.RecorderStatus) bool { return s.Signals.Disconnected })
	e.later(3 * time.Minute)
	l.waitNotices(1, noticeOn)
	byDisconnect := l.notices()[0]

	// B goes deaf too; A comes back, and the notice stays on for B. The
	// clock only moves on, so A stays disconnected long enough meanwhile.
	send(t, b, heartbeat(rate("ex", "p25", 0)))
	e.waitStatus(t, nameB, "deaf", func(s server.RecorderStatus) bool { return s.Signals.Deaf })
	e.later(6 * time.Minute)
	settle()
	a = e.open(t, keyA)
	send(t, a, heartbeat(rate("ex", "p25", 38.5)))
	settle()
	if n := l.notices(); len(n) != 1 {
		t.Fatalf("notices %q while B is still deaf, want the first only", n)
	}
	send(t, b, heartbeat(rate("ex", "p25", 38.5)))
	l.waitNotices(2, noticeOff)

	// Now only B's decode rate.
	send(t, b, heartbeat(rate("ex", "p25", 0.2)))
	e.waitStatus(t, nameB, "deaf", func(s server.RecorderStatus) bool { return s.Signals.Deaf })
	e.later(9 * time.Minute)
	l.waitNotices(3, noticeOn)
	if byDeaf := l.notices()[2]; !bytes.Equal(byDisconnect, byDeaf) || !bytes.Equal(byDeaf, noticeOn) {
		t.Errorf("notice by a disconnection %s, by a decode rate %s", byDisconnect, byDeaf)
	}
	for _, n := range l.notices() {
		for _, s := range []string{nameA, nameB, "ex", "1001", "Dispatch", "EX-"} {
			if strings.Contains(string(n), s) {
				t.Errorf("notice %s names %q", n, s)
			}
		}
	}
}

// Every active recorder is listed; a retired one isn't, and one that has
// never connected isn't counted disconnected.
func TestIntegrationRecordersList(t *testing.T) {
	e := newEnv(t)
	connected, key := e.recorder(t)
	e.open(t, key)
	never, _ := e.recorder(t)
	retired, _ := e.recorder(t)
	if err := e.st.RetireRecorderByName(context.Background(), retired); err != nil {
		t.Fatal(err)
	}
	e.waitStatus(t, connected, "connected", func(s server.RecorderStatus) bool { return s.Connected })
	all := decodeJSON[[]server.RecorderStatus](t, e.mustAdmin(t, "GET", "/recorders", nil, 200))
	got := map[string]server.RecorderStatus{}
	for _, s := range all {
		got[s.Name] = s
	}
	if _, ok := got[retired]; ok || len(all) != 2 {
		t.Errorf("recorders %+v, want %s and %s", all, connected, never)
	}
	if s := got[never]; s.Connected || s.Signals != (server.RecorderSignals{}) || s.Warning ||
		s.DisconnectedSince != nil || s.LastConnectedAt != nil || len(s.Systems) != 1 || s.Systems[0].DecodeRate != nil {
		t.Errorf("never connected = %+v", s)
	}
	// Until its first heartbeat, a connected recorder is neither deaf nor
	// well.
	e.later(time.Hour)
	settle()
	if s := e.recorderStatus(t, connected); s.Warning {
		t.Errorf("connected, no heartbeat yet = %+v", s)
	}
}
