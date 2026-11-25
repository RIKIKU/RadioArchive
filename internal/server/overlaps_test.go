package server_test

import (
	"context"
	"fmt"
	"net/url"
	"testing"
	"time"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
	"github.com/RIKIKU/RadioArchive/internal/server"
)

// uploadFor stores a call on tg patched with patched, starting at start and
// lasting d.
func (e *env) uploadFor(t *testing.T, key string, n int64, tg int32, patched []int32, start time.Time, d time.Duration) string {
	t.Helper()
	audio := oggBytes(byte(n))
	m := recording(cp.CallID{CallNum: n, StartTime: start.Unix()}, audio)
	m.Talkgroup, m.PatchedTalkgroups = tg, patched
	m.StartTimeMS, m.StopTimeMS = start.UnixMilli(), start.Add(d).UnixMilli()
	m.Transmissions = []cp.RecordingTransmission{{Unit: 5001, OffsetMS: 0, DurationMS: d.Milliseconds()}}
	code, resp := e.upload(t, key, m, audio)
	if code != 201 {
		t.Fatalf("upload on %d: %d %+v", tg, code, resp)
	}
	return resp.CallID
}

// TestIntegrationOverlaps is R13 and ADR 0027's rule: two recorders' calls
// on the same system and talkgroup whose times overlap, ends included,
// over the last 24 hours, each pair once, hidden talkgroups' too. Calls on
// another talkgroup (a patch included), another system, from the same
// recorder, still in progress, or from before the 24 hours don't count.
func TestIntegrationOverlaps(t *testing.T) {
	e := newEnv(t)
	now := time.Date(2025, 12, 1, 12, 0, 0, 0, time.UTC)
	e.clock.set(now)
	ctx := context.Background()

	if code, _ := e.adminReq(t, "", "GET", "/overlaps", nil); code != 401 {
		t.Errorf("overlaps without an admin: %d, want 401", code)
	}
	empty := decodeJSON[server.Overlaps](t, e.mustAdmin(t, "GET", "/overlaps", nil, 200))
	if len(empty.Overlaps) != 0 || !empty.Since.Equal(now.Add(-24*time.Hour)) {
		t.Fatalf("no calls: %+v", empty)
	}

	// South ridge and North hill on one system; West on another.
	south, ks := e.recorder(t)
	sys := e.sys
	north := "North hill " + south
	kn, err := e.st.AddRecorder(ctx, north)
	if err != nil {
		t.Fatal(err)
	}
	e.mustAdmin(t, "PUT", "/recorders/"+url.PathEscape(north)+"/attachments/ex", map[string]any{"system_id": sys}, 200)
	_, kw := e.recorder(t)

	s := 10 * time.Second
	// Overlapping on 1001: by 5 s; touching at the end; inside a 90-minute call.
	e.uploadFor(t, ks, 1, 1001, nil, now.Add(-time.Hour), s)
	e.uploadFor(t, kn, 1, 1001, nil, now.Add(-time.Hour+5*time.Second), s)
	e.uploadFor(t, ks, 2, 1001, nil, now.Add(-30*time.Minute), s)
	e.uploadFor(t, kn, 2, 1001, nil, now.Add(-30*time.Minute+s), s)
	// (The long call is the later recorder's, North hill's, and starts an
	// hour before the other: the longest call bounds how far back to look.)
	e.uploadFor(t, kn, 3, 1001, nil, now.Add(-3*time.Hour), 90*time.Minute)
	e.uploadFor(t, ks, 3, 1001, nil, now.Add(-2*time.Hour), s)
	// Hidden 1003 counts: the page is an admin's.
	e.uploadFor(t, ks, 4, 1003, nil, now.Add(-5*time.Minute), s)
	e.uploadFor(t, kn, 4, 1003, nil, now.Add(-5*time.Minute), s)

	// Not overlapping: a second apart; another talkgroup, even patched in;
	// the same recorder twice; another system; before the 24 hours.
	e.uploadFor(t, ks, 5, 1001, nil, now.Add(-20*time.Minute), s)
	e.uploadFor(t, kn, 5, 1001, nil, now.Add(-20*time.Minute+11*time.Second), s)
	e.uploadFor(t, ks, 6, 1001, []int32{1002}, now.Add(-15*time.Minute), s)
	e.uploadFor(t, kn, 6, 1002, nil, now.Add(-15*time.Minute), s)
	e.uploadFor(t, ks, 7, 1001, nil, now.Add(-12*time.Minute), s)
	e.uploadFor(t, ks, 8, 1001, nil, now.Add(-12*time.Minute), s)
	e.uploadFor(t, kw, 1, 1001, nil, now.Add(-time.Hour), s)
	e.uploadFor(t, ks, 9, 1001, nil, now.Add(-25*time.Hour), s)
	e.uploadFor(t, kn, 9, 1001, nil, now.Add(-25*time.Hour), s)
	// Still in progress (no end yet): counted once it ends.
	e.uploadFor(t, ks, 10, 1002, nil, now.Add(-2*time.Minute), s)
	live := e.uploadFor(t, kn, 10, 1002, nil, now.Add(-2*time.Minute), s)
	if _, err := e.st.Pool.Exec(ctx, `UPDATE calls SET ended_at = NULL WHERE id = $1`, live); err != nil {
		t.Fatal(err)
	}

	got := decodeJSON[server.Overlaps](t, e.mustAdmin(t, "GET", "/overlaps", nil, 200))
	want := []server.Overlap{
		{SystemID: sys, SystemLabel: "EX", Talkgroup: 1001, Recorders: [2]string{north, south}, Calls: 3,
			Message: fmt.Sprintf("%s and %s both recorded TG 1001: 3 overlapping calls in the last 24 h", north, south)},
		{SystemID: sys, SystemLabel: "EX", Talkgroup: 1003, Recorders: [2]string{north, south}, Calls: 1,
			Message: fmt.Sprintf("%s and %s both recorded TG 1003: 1 overlapping call in the last 24 h", north, south)},
	}
	if fmt.Sprint(got.Overlaps) != fmt.Sprint(want) {
		t.Errorf("overlaps:\n got %+v\nwant %+v", got.Overlaps, want)
	}

	// A day later, all of them are older than 24 hours.
	e.clock.set(now.Add(25 * time.Hour))
	if later := decodeJSON[server.Overlaps](t, e.mustAdmin(t, "GET", "/overlaps", nil, 200)); len(later.Overlaps) != 0 {
		t.Errorf("a day later: %+v", later.Overlaps)
	}
}
