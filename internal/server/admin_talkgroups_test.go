package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
	"github.com/RIKIKU/RadioArchive/internal/public"
	"github.com/RIKIKU/RadioArchive/internal/server"
)

// The admin table's data (SPEC.md R34, R50, A13, A23; ADR 0026).

// table reads GET /api/v1/admin/talkgroups, by system and talkgroup.
func (e *env) table(t *testing.T) (server.Table, map[[2]int64]server.TableRow) {
	t.Helper()
	tb := decodeJSON[server.Table](t, e.mustAdmin(t, "GET", "/talkgroups", nil, 200))
	rows := map[[2]int64]server.TableRow{}
	for _, r := range tb.Talkgroups {
		rows[[2]int64{r.SystemID, int64(r.ID)}] = r
	}
	return tb, rows
}

// refusedAt uploads a call the server refuses as ignored.
func (e *env) refusedAt(t *testing.T, key string, n int64, tg int32, patched []int32, start time.Time) {
	t.Helper()
	audio := oggBytes(byte(n))
	m := recording(cp.CallID{CallNum: n, StartTime: start.Unix()}, audio)
	m.Talkgroup, m.PatchedTalkgroups = tg, patched
	m.StartTimeMS, m.StopTimeMS = start.UnixMilli(), start.Add(10*time.Second).UnixMilli()
	if code, resp := e.upload(t, key, m, audio); code != 200 || resp.Reason != "ignored_talkgroup" {
		t.Fatalf("upload on %d+%v: %d %+v, want refused as ignored", tg, patched, code, resp)
	}
}

func heardAt(r server.TableRow) string {
	if r.LastHeardAt == nil {
		return "never"
	}
	return r.LastHeardAt.UTC().Format(time.RFC3339)
}

// TestIntegrationAdminTalkgroupTable is A23's server half and ADR 0026's
// rule: every talkgroup of every system in one answer, hidden and ignored
// ones too, with last heard and calls in the last 30 days for a plain
// call, a patched one, an ignored talkgroup's refused calls, a live call
// with no recording yet, and after retention has deleted the calls.
func TestIntegrationAdminTalkgroupTable(t *testing.T) {
	e := newEnv(t)
	now := time.Date(2025, 12, 1, 12, 0, 0, 0, time.UTC)
	e.clock.set(now)
	_, key := e.recorder(t)
	sys := e.sys
	e.mustAdmin(t, "PUT", fmt.Sprintf("/systems/%d/talkgroups/1005", sys), map[string]any{"tag": "other"}, 201)
	other := e.fixture(t)

	if code, _ := e.adminReq(t, "", "GET", "/talkgroups", nil); code != 401 {
		t.Errorf("the table without an admin: %d, want 401", code)
	}
	e.wantRefusal(t, "GET", "/talkgroups?system=1", nil, 400, "unknown_parameter")

	e.uploadAt(t, key, 1, 1001, nil, now.Add(-2*time.Hour), 5001)
	e.uploadAt(t, key, 2, 1001, []int32{1002}, now.Add(-time.Hour), 5001)
	e.refusedAt(t, key, 3, 1004, nil, now.Add(-50*time.Minute))
	// Refused because 1004 is in its patch: it counts for 1004, as its
	// rejection does, and not for 1002 (ADR 0026).
	e.refusedAt(t, key, 4, 1002, []int32{1004}, now.Add(-40*time.Minute))
	e.uploadAt(t, key, 5, 1003, nil, now.Add(-30*time.Minute), 5001)
	// A patch naming a talkgroup the system doesn't list moves nothing.
	e.uploadAt(t, key, 6, 1001, []int32{9999}, now.Add(-25*time.Hour), 5001)
	// Older than 30 days, and uploaded last: not counted, and last heard
	// doesn't move back.
	e.uploadAt(t, key, 7, 1001, nil, now.Add(-31*24*time.Hour), 5001)

	// A live call, its recording not arrived: heard at its start.
	c := e.open(t, key)
	live := cp.CallID{CallNum: 8, StartTime: now.Add(-20 * time.Minute).Unix()}
	cs := callStart(live)
	cs.Talkgroup = 1002
	cs.StartTimeMS, cs.FirstPCMMS = now.Add(-20*time.Minute).UnixMilli(), now.Add(-20*time.Minute).UnixMilli()+300
	send(t, c, cs)
	waitUntil(t, "1002 heard live", func() bool {
		_, rows := e.table(t)
		return heardAt(rows[[2]int64{sys, 1002}]) == now.Add(-20*time.Minute).Format(time.RFC3339)
	})

	tb, rows := e.table(t)
	if want := now.Add(-server.TableWindow); !tb.CallsSince.Equal(want) {
		t.Errorf("calls_since %s, want %s", tb.CallsSince, want)
	}
	if len(tb.Talkgroups) != 9 {
		t.Errorf("%d rows, want the 5 of the first system and the 4 of the second", len(tb.Talkgroups))
	}
	for i, r := range tb.Talkgroups[1:] {
		if p := tb.Talkgroups[i]; p.SystemID > r.SystemID || (p.SystemID == r.SystemID && p.ID >= r.ID) {
			t.Errorf("rows out of order: %d.%d before %d.%d", p.SystemID, p.ID, r.SystemID, r.ID)
		}
	}
	at := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }
	for _, w := range []struct {
		sys   int64
		tg    int32
		state string
		heard string
		calls int64
	}{
		{sys, 1001, "public", at(time.Hour), 3},
		{sys, 1002, "public", at(20 * time.Minute), 2},
		{sys, 1003, "hidden", at(30 * time.Minute), 1},
		{sys, 1004, "ignored", at(40 * time.Minute), 2},
		{sys, 1005, "public", "never", 0},
		{other, 1001, "public", "never", 0},
		{other, 1003, "hidden", "never", 0},
	} {
		r, ok := rows[[2]int64{w.sys, int64(w.tg)}]
		if !ok {
			t.Errorf("%d.%d missing from the table", w.sys, w.tg)
			continue
		}
		if r.State != w.state || heardAt(r) != w.heard || r.Calls30Days != w.calls {
			t.Errorf("%d.%d: %s, last heard %s, %d calls; want %s, %s, %d",
				w.sys, w.tg, r.State, heardAt(r), r.Calls30Days, w.state, w.heard, w.calls)
		}
	}
	// Every R22 field as the talkgroup holds it, as the talkgroup's own
	// GET gives it.
	one := decodeJSON[server.Talkgroup](t, e.mustAdmin(t, "GET", fmt.Sprintf("/systems/%d/talkgroups/1001", sys), nil, 200))
	got, _ := json.Marshal(rows[[2]int64{sys, 1001}].Talkgroup)
	if want, _ := json.Marshal(one); !bytes.Equal(got, want) {
		t.Errorf("table row %s, want the talkgroup's own %s", got, want)
	}

	// Retention deletes every call and rejection: last heard stays, the
	// counts go to nought.
	e.clock.set(now.Add(40 * 24 * time.Hour))
	if res, err := e.job.RunNightly(context.Background()); err != nil || res.Calls != 6 {
		t.Fatalf("the nightly run: %+v %v, want 6 calls deleted", res, err)
	}
	_, after := e.table(t)
	for _, k := range [][2]int64{{sys, 1001}, {sys, 1002}, {sys, 1003}, {sys, 1004}} {
		if b, a := rows[k], after[k]; heardAt(a) != heardAt(b) || a.Calls30Days != 0 {
			t.Errorf("%d.%d after retention: last heard %s, %d calls; want %s and 0", k[0], k[1], heardAt(a), a.Calls30Days, heardAt(b))
		}
	}
}

// TestIntegrationAdminTalkgroupCalls is A13's server half: an admin lists
// hidden 1003's calls, newest first and paged, with the call patched with
// it, and plays one byte for byte, while the public recording route
// answers as for a call that never existed.
func TestIntegrationAdminTalkgroupCalls(t *testing.T) {
	e := newEnv(t)
	now := time.Date(2025, 12, 1, 12, 0, 0, 0, time.UTC)
	e.clock.set(now)
	_, key := e.recorder(t)
	path := fmt.Sprintf("/systems/%d/talkgroups/1003/calls", e.sys)

	c11 := e.uploadAt(t, key, 11, 1003, nil, now.Add(-3*time.Hour), 5001)
	c12 := e.uploadAt(t, key, 12, 1003, nil, now.Add(-2*time.Hour), 5001)
	c13 := e.uploadAt(t, key, 13, 1001, []int32{1003}, now.Add(-90*time.Minute), 5001)
	e.uploadAt(t, key, 14, 1001, nil, now.Add(-80*time.Minute), 5001)
	c15 := e.uploadAt(t, key, 15, 1003, nil, now.Add(-time.Hour), 5001)

	var got []string
	page := decodeJSON[server.TalkgroupCalls](t, e.mustAdmin(t, "GET", path+"?limit=2", nil, 200))
	for page.Next != nil {
		for _, c := range page.Calls {
			got = append(got, c.ID)
		}
		page = decodeJSON[server.TalkgroupCalls](t, e.mustAdmin(t, "GET", path+"?limit=2&cursor="+url.QueryEscape(*page.Next), nil, 200))
	}
	for _, c := range page.Calls {
		got = append(got, c.ID)
	}
	if want := []string{c15, c13, c12, c11}; !slices.Equal(got, want) {
		t.Errorf("1003's calls %q, want %q", got, want)
	}

	all := decodeJSON[server.TalkgroupCalls](t, e.mustAdmin(t, "GET", path, nil, 200))
	if all.Next != nil || len(all.Calls) != 4 {
		t.Fatalf("one page: %d calls, next %v", len(all.Calls), all.Next)
	}
	for _, c := range all.Calls {
		if c.State != "hidden" || c.InProgress || c.Recording == nil || c.EndedAt == nil {
			t.Errorf("call %s: %+v, want hidden with a recording", c.ID, c)
		}
	}
	if p := all.Calls[1]; p.Talkgroup != 1001 || !slices.Equal(p.PatchedTalkgroups, []int32{1003}) {
		t.Errorf("the patched call: %+v", p)
	}

	// Played byte for byte through the admin API; the public route answers
	// as for a call that never existed.
	rec := all.Calls[0].Recording
	if rec.URL != server.AdminPath+"/calls/"+c15+"/recording" || rec.Size != int64(len(oggBytes(15))) {
		t.Errorf("recording %+v", rec)
	}
	code, body := e.adminReq(t, "test admin", "GET", strings.TrimPrefix(rec.URL, server.AdminPath), nil)
	if code != 200 || !bytes.Equal(body, oggBytes(15)) {
		t.Errorf("admin playback: %d, %d bytes", code, len(body))
	}
	if a := do(t, e.publicReq("GET", public.RecordingURL(c15), "")); a.status != 404 {
		t.Errorf("public playback of a hidden call: %s", a)
	}

	if code, _ := e.adminReq(t, "", "GET", path, nil); code != 401 {
		t.Errorf("without an admin: %d, want 401", code)
	}
	e.wantRefusal(t, "GET", path+"?limit=0", nil, 400, "invalid_limit")
	e.wantRefusal(t, "GET", path+"?limit=501", nil, 400, "invalid_limit")
	e.wantRefusal(t, "GET", path+"?cursor=nope", nil, 400, "invalid_cursor")
	e.wantRefusal(t, "GET", path+"?order=oldest", nil, 400, "unknown_parameter")
	e.wantRefusal(t, "GET", fmt.Sprintf("/systems/%d/talkgroups/9999/calls", e.sys), nil, 404, "talkgroup_not_found")
}
