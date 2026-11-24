//go:build cgo

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/server"
	"github.com/RIKIKU/RadioArchive/internal/store/storetest"
	"github.com/RIKIKU/RadioArchive/internal/version"
	"github.com/RIKIKU/RadioArchive/testing/fakerecorder"
)

// A27 end to end, server and feed half (SPEC.md R14, R15; ADR 0010): the
// fake recorder's heartbeats through the recorder client to the server's
// signals, the admin API and a listener's live feed, on the server's
// controllable clock. The Recorders page itself is M4's.

var (
	noticeOn  = []byte(`{"type":"notice","notice":"recorders","active":true}`)
	noticeOff = []byte(`{"type":"notice","notice":"recorders","active":false}`)
)

// beats replays a heartbeat scenario (system_rates every 3 s) to the client
// over and over, each time as a new plugin boot, until stopped.
func (r *rig) beats(t *testing.T, scenario string) (stop func()) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "fakerecorder", "testdata", "scenarios", scenario))
	if err != nil {
		t.Fatal(err)
	}
	conn, err := fakerecorder.Dial(r.client.UDPAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer conn.Close()
		for i := uint32(0); ctx.Err() == nil; i++ {
			s, err := fakerecorder.LoadScenario(bytes.NewReader(raw))
			if err != nil {
				t.Error(err)
				return
			}
			s.BootID += i
			s.EpochMS = time.Now().UnixMilli()
			plan, err := fakerecorder.Plan(s, nil)
			if err != nil {
				t.Error(err)
				return
			}
			_ = fakerecorder.Replay(ctx, conn, plan, 1)
		}
	}()
	return func() { cancel(); <-done }
}

func (r *rig) status(t *testing.T) server.RecorderStatus {
	t.Helper()
	var s server.RecorderStatus
	if err := json.Unmarshal(r.admin(t, "GET", "/recorders/"+url.PathEscape(r.name), nil, 200), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func (r *rig) waitStatus(t *testing.T, what string, ok func(server.RecorderStatus) bool) server.RecorderStatus {
	t.Helper()
	var s server.RecorderStatus
	waitFor(t, "the recorder "+what, 20*time.Second, func() bool { s = r.status(t); return ok(s) })
	return s
}

func (h *heard) notices() [][]byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.notice)
}

func (h *heard) waitNotices(t *testing.T, want ...[]byte) {
	t.Helper()
	waitFor(t, "the listener's notices", 20*time.Second, func() bool {
		return slices.EqualFunc(h.notices(), want, bytes.Equal)
	})
}

func decoding(rate float64) func(server.RecorderStatus) bool {
	return func(s server.RecorderStatus) bool {
		return s.Connected && len(s.Systems) == 1 && s.Systems[0].DecodeRate != nil && *s.Systems[0].DecodeRate == rate
	}
}

// TestA27RecorderSignals is SPEC.md A27's server and feed half. Its
// database is its own: every recorder that has ever connected counts.
func TestA27RecorderSignals(t *testing.T) {
	r := newRigOn(t, version.Version, storetest.FreshURL(t))
	r.serverUp(t)
	r.clientUp(t, "")
	waitFor(t, "the client's live connection", 10*time.Second, r.client.LiveConnected)
	stop := r.beats(t, "a27-healthy.json")
	l := r.listen(t, 1001)
	if s := r.waitStatus(t, "decoding", decoding(38.5)); s.Warning {
		t.Fatalf("healthy recorder = %+v", s)
	}

	// The connection drops for 2 minutes.
	stop()
	r.clientDown()
	if s := r.waitStatus(t, "disconnected", func(s server.RecorderStatus) bool { return s.Signals.Disconnected }); !s.Warning || s.Connected {
		t.Errorf("disconnected recorder = %+v", s)
	}
	r.srvClock.advance(2*time.Minute + time.Second)
	l.waitNotices(t, noticeOn)
	// And clears on reconnect.
	r.clientUp(t, "")
	waitFor(t, "the client's live connection", 10*time.Second, r.client.LiveConnected)
	stop = r.beats(t, "a27-healthy.json")
	l.waitNotices(t, noticeOn, noticeOff)
	r.waitStatus(t, "decoding again", decoding(38.5))

	// A decode rate of 0 for 2 minutes.
	stop()
	stop = r.beats(t, "a27-decode-zero.json")
	s := r.waitStatus(t, "deaf", func(s server.RecorderStatus) bool { return s.Signals.Deaf })
	if s.DeafReason == nil || *s.DeafReason != server.DeafDecodeRate || !s.Systems[0].Deaf || !s.Warning {
		t.Errorf("deaf recorder = %+v", s)
	}
	r.srvClock.advance(2*time.Minute + time.Second)
	l.waitNotices(t, noticeOn, noticeOff, noticeOn)
	stop()
	stop = r.beats(t, "a27-healthy.json")
	defer stop()
	l.waitNotices(t, noticeOn, noticeOff, noticeOn, noticeOff)

	// Merely quiet: no calls for its threshold. Admins see it; the
	// listener sees nothing.
	r.admin(t, "PATCH", "/recorders/"+url.PathEscape(r.name), map[string]any{"quiet_after_minutes": 5}, 200)
	r.srvClock.advance(10 * time.Minute)
	if s := r.waitStatus(t, "quiet", func(s server.RecorderStatus) bool { return s.Signals.Quiet }); !s.Warning || s.Signals.Deaf {
		t.Errorf("quiet recorder = %+v", s)
	}
	time.Sleep(3 * time.Second)
	if got := l.notices(); len(got) != 4 {
		t.Errorf("notices %q after a quiet recorder, want the 4 before", got)
	}
}
