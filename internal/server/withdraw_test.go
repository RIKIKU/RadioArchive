package server_test

import (
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
	"github.com/RIKIKU/RadioArchive/internal/gate"
)

// withdraw sends the call_end the recorder client sends when trunk-recorder
// concluded the call without a recording (ADR 0053).
func (r *liveRec) withdraw(id cp.CallID, stopMS int64) {
	r.t.Helper()
	send(r.t, r.c, cp.CallEnd{Type: "call_end", CallID: id, StopTimeMS: stopMS, PatchedTalkgroups: []int32{}, Withdrawn: true})
}

func (e *env) waitGone(t *testing.T, recorder string, id cp.CallID) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, found := e.call(t, recorder, id); !found {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("call %+v is still stored", id)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestIntegrationWithdrawnCall (issue #185, ADR 0053): a call trunk-recorder
// concluded without a recording is withdrawn. Live, listeners get
// call_withdrawn; ended and waiting for its recording, it stops counting as
// a missing recording. A call whose recording genuinely never came still
// counts, and one whose recording did arrive is kept.
func TestIntegrationWithdrawnCall(t *testing.T) {
	e := newEnv(t)
	rec := e.liveRecorder(t)
	l := e.listen(t, []any{1001}, nil)
	now := time.Now()

	// Withdrawn while live: the listener is told, and nothing is stored.
	live, liveID := rec.start(1001, nil, 5001, now.UnixMilli())
	rec.frames(live, 0, 3, 'w')
	l.wait("the live call's start", isEvent(gate.TypeCallStart, liveID))
	rec.withdraw(live, now.Add(2*time.Second).UnixMilli())
	l.wait("the withdrawal", isEvent(gate.TypeCallWithdrawn, liveID))
	e.waitGone(t, rec.name, live)
	if ev, _ := l.of(liveID); !slices.Equal(ev, []string{"call_start", "call_withdrawn"}) {
		t.Errorf("the withdrawn call's events: %v", ev)
	}

	// Two calls that ended 11 minutes ago with no recording: both missing.
	// The recorder then says one of them will never have one.
	startMS := now.Add(-12 * time.Minute).UnixMilli()
	stopMS := now.Add(-11 * time.Minute).UnixMilli()
	dropped, _ := rec.start(1001, nil, 5002, startMS)
	failed, _ := rec.start(1001, nil, 5003, startMS+1000)
	rec.end(dropped, stopMS)
	rec.end(failed, stopMS)
	waitUntil(t, "two missing recordings", func() bool {
		return strings.Contains(e.scrape(t), "radioarchive_recordings_missing 2\n")
	})
	rec.withdraw(dropped, stopMS)
	e.waitGone(t, rec.name, dropped)
	waitUntil(t, "one missing recording", func() bool {
		return strings.Contains(e.scrape(t), "radioarchive_recordings_missing 1\n")
	})
	if _, found := e.call(t, rec.name, failed); !found {
		t.Fatal("the call whose upload failed was deleted")
	}

	// A call whose recording arrived is kept, whatever the recorder says.
	kept, _ := rec.start(1001, nil, 5004, startMS+2000)
	audio := oggBytes(7)
	meta := recording(kept, audio)
	meta.StartTimeMS, meta.StopTimeMS = startMS+2000, stopMS
	resp, err := http.DefaultClient.Do(uploadRequest(t, e.srv.URL, hdr(rec.key), meta, audio))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		t.Fatalf("upload: %d %s", resp.StatusCode, b)
	}
	rec.withdraw(kept, stopMS)
	// A barrier: the socket handles messages in order.
	bar, _ := rec.start(1001, nil, 5005, now.UnixMilli())
	rec.end(bar, now.Add(time.Second).UnixMilli())
	e.waitCall(t, rec.name, bar, func(callRow) bool { return true })
	if r, found := e.call(t, rec.name, kept); !found || r.path == nil {
		t.Errorf("a recorded call was withdrawn: %+v (found %v)", r, found)
	}

	// A withdrawal for a call the server never heard of changes nothing.
	rec.withdraw(cp.CallID{SysNum: 0, CallNum: 999999, StartTime: now.Unix()}, now.UnixMilli())

	wantMetrics(t, e.scrape(t),
		"radioarchive_recordings_missing 1",
		`radioarchive_recorder_protocol_events_total{kind="calls_withdrawn"} 2`)
}
