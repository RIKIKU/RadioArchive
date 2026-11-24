package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
	"github.com/RIKIKU/RadioArchive/internal/gate"
	"github.com/RIKIKU/RadioArchive/internal/public"
	"github.com/RIKIKU/RadioArchive/internal/server"
)

// Recent calls and search (SPEC.md R16, R23, R30, R46, R73; ADR 0013).

// recent asks for a page of recent calls and wants a 200.
func (e *env) recent(t *testing.T, q url.Values) public.RecentCallsResponse {
	t.Helper()
	a := e.pub(t, "GET", public.RecentCallsPath+"?"+q.Encode(), "")
	if a.status != 200 {
		t.Fatalf("recent calls ?%s: %s", q.Encode(), a)
	}
	if a.header.Get("Cache-Control") != "no-store" {
		t.Errorf("recent calls' Cache-Control = %q", a.header.Get("Cache-Control"))
	}
	return decodeJSON[public.RecentCallsResponse](t, a.body)
}

func ids(cs []public.Call) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

// around is a range of an hour either side of t.
func around(t time.Time, kv ...string) url.Values {
	q := url.Values{"from": {t.Add(-time.Hour).Format(time.RFC3339)}, "to": {t.Add(time.Hour).Format(time.RFC3339)}}
	for i := 0; i+1 < len(kv); i += 2 {
		q.Set(kv[i], kv[i+1])
	}
	return q
}

// The fixture's calls (recording()) start around 2025-11-24T16:00Z.
var fixtureTime = time.UnixMilli(1764000000120).UTC()

// uploadAt stores a call on tg patched with patched, starting at start and
// lasting 10 s, with unit heard on it.
func (e *env) uploadAt(t *testing.T, key string, n int64, tg int32, patched []int32, start time.Time, unit int64) string {
	t.Helper()
	audio := oggBytes(byte(n))
	m := recording(cp.CallID{CallNum: n, StartTime: start.Unix()}, audio)
	m.Talkgroup, m.PatchedTalkgroups = tg, patched
	m.StartTimeMS, m.StopTimeMS = start.UnixMilli(), start.Add(10*time.Second).UnixMilli()
	m.Transmissions = []cp.RecordingTransmission{{Unit: uint32(unit), OffsetMS: 0, DurationMS: 10000}}
	code, resp := e.upload(t, key, m, audio)
	if code != 201 {
		t.Fatalf("upload on %d: %d %+v", tg, code, resp)
	}
	return resp.CallID
}

// TestIntegrationRecentCalls: the list, its order and its filters, each row
// exactly as the call details give that call.
func TestIntegrationRecentCalls(t *testing.T) {
	e := newEnv(t)
	_, key := e.recorder(t)
	t0 := fixtureTime
	a := e.uploadAt(t, key, 1, 1001, nil, t0, 5001)
	b := e.uploadAt(t, key, 2, 1002, nil, t0.Add(time.Minute), 5123)
	c := e.uploadAt(t, key, 3, 1001, []int32{1002}, t0.Add(2*time.Minute), 5001)

	got := e.recent(t, around(t0))
	if !slices.Equal(ids(got.Calls), []string{c, b, a}) || got.Next != nil {
		t.Fatalf("newest first: %v, next %v", ids(got.Calls), got.Next)
	}
	// Each row is the call details' call, byte for byte.
	details := e.publicCalls(t, c, b, a)
	for i := range details {
		want, _ := json.Marshal(details[i])
		row, _ := json.Marshal(got.Calls[i])
		if string(row) != string(want) {
			t.Errorf("row %d is\n%s\nbut its call details are\n%s", i, row, want)
		}
	}
	if got := e.recent(t, around(t0, "order", "oldest")); !slices.Equal(ids(got.Calls), []string{a, b, c}) {
		t.Errorf("oldest first: %v", ids(got.Calls))
	}

	for _, f := range []struct {
		q    url.Values
		want []string
	}{
		{around(t0, "talkgroups", "1001"), []string{c, a}},
		{around(t0, "talkgroups", "1002"), []string{c, b}}, // c is patched with 1002 (R16)
		{around(t0, "talkgroups", "1002,1001"), []string{c, b, a}},
		{around(t0, "talkgroups", fmt.Sprintf("%d.1002", e.sys)), []string{c, b}},
		{around(t0, "talkgroups", fmt.Sprintf("%d.1002", e.sys+1)), []string{}},
		{around(t0, "talkgroups", "4711"), []string{}},
		{around(t0, "talkgroups", ""), []string{c, b, a}}, // none named: every public talkgroup
		{around(t0, "unit", "5001"), []string{c, a}},
		{around(t0, "unit", "5123", "talkgroups", "1001"), []string{}},
		{around(t0, "unit", fmt.Sprintf("%d.5123", e.sys)), []string{b}},
		{around(t0, "unit", fmt.Sprintf("%d.5123", e.sys+1)), []string{}},
		{url.Values{"from": {t0.Format(time.RFC3339Nano)}, "to": {t0.Add(time.Minute).Format(time.RFC3339Nano)}}, []string{a}},
		{url.Values{"from": {t0.Add(time.Minute).Format(time.RFC3339Nano)}}, []string{c, b}}, // to defaults to from + 31 days
	} {
		if got := e.recent(t, f.q); !slices.Equal(ids(got.Calls), f.want) {
			t.Errorf("?%s: %v, want %v", f.q.Encode(), ids(got.Calls), f.want)
		}
	}
}

// TestIntegrationRecentCallsBounds is SPEC.md A22 and R73's refusals: a
// range over 31 days is refused naming the limit, and malformed searches
// are refused with a stable code (R78).
func TestIntegrationRecentCallsBounds(t *testing.T) {
	e := newEnv(t)
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		q    url.Values
		code string
	}{
		{url.Values{"from": {from.Format(time.RFC3339)}, "to": {from.AddDate(0, 0, 32).Format(time.RFC3339)}}, "range_too_long"},
		{url.Values{"from": {from.Format(time.RFC3339)}, "to": {from.Add(gate.MaxRange + time.Second).Format(time.RFC3339)}}, "range_too_long"},
		{url.Values{"to": {from.Format(time.RFC3339)}, "from": {from.Format(time.RFC3339)}}, "invalid_range"},
		{url.Values{"from": {"yesterday"}}, "invalid_time"},
		{url.Values{"talkgroups": {"1001,DISP N"}}, "invalid_talkgroups"},
		{url.Values{"talkgroups": {strings.Repeat("1001,", public.MaxTalkgroups) + "1001"}}, "too_many_talkgroups"},
		{url.Values{"unit": {"Engine 1"}}, "invalid_unit"},
		{url.Values{"order": {"random"}}, "invalid_order"},
		{url.Values{"cursor": {"not-a-cursor"}}, "invalid_cursor"},
		{url.Values{"limit": {"101"}}, "invalid_limit"},
		{url.Values{"limit": {"0"}}, "invalid_limit"},
		{url.Values{"q": {"dispatch"}}, "unknown_parameter"},
		{url.Values{"from": {"2026-10-01T00:00:00Z", "2026-10-02T00:00:00Z"}}, "invalid_from"},
	} {
		a := e.pub(t, "GET", public.RecentCallsPath+"?"+c.q.Encode(), "")
		var body struct{ Error, Message string }
		_ = json.Unmarshal(a.body, &body)
		if a.status != 400 || body.Error != c.code || body.Message == "" {
			t.Errorf("?%s: %s, want 400 %s", c.q.Encode(), a, c.code)
		}
		if c.code == "range_too_long" && !strings.Contains(body.Message, "31 days") {
			t.Errorf("the range refusal doesn't name the 31-day limit: %q", body.Message)
		}
	}
	// Exactly 31 days is allowed, as is the default range (the last 31
	// days).
	e.recent(t, url.Values{"from": {from.Format(time.RFC3339)}, "to": {from.Add(gate.MaxRange).Format(time.RFC3339)}})
	e.recent(t, url.Values{})
}

// TestIntegrationRecentCallsPaging is SPEC.md A22's paging: at most 100
// rows a page, and load more over calls that share a start time neither
// repeats nor skips one, either way, at any page size.
func TestIntegrationRecentCallsPaging(t *testing.T) {
	e := newEnv(t)
	name, _ := e.recorder(t)
	t0 := fixtureTime
	// 250 calls, five to each start time.
	var want []string
	rows, err := e.st.Pool.Query(context.Background(), `
		INSERT INTO calls (recorder_id, sys_num, call_num, start_time, system_id, short_name, talkgroup, started_at, ended_at)
		SELECT r.id, 0, n, 0, $2, 'ex', 1001, $3::timestamptz + (n / 5) * interval '1 s', $3::timestamptz + interval '1 h'
		FROM recorders r, generate_series(0, 249) n WHERE r.name = $1
		RETURNING id::text`, name, e.sys, t0)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		want = append(want, id)
	}
	if rows.Err() != nil || len(want) != 250 {
		t.Fatalf("inserted %d: %v", len(want), rows.Err())
	}
	slices.Sort(want)
	for _, order := range []string{"newest", "oldest"} {
		for _, limit := range []string{"", "7", "5", "1"} {
			q := around(t0, "order", order)
			if limit != "" {
				q.Set("limit", limit)
			}
			var got []string
			pages := 0
			var last time.Time
			for {
				p := e.recent(t, q)
				pages++
				most := 100
				if limit != "" {
					most, _ = strconv.Atoi(limit)
				}
				if len(p.Calls) > most || len(p.Calls) == 0 {
					t.Fatalf("%s, limit %q: page %d has %d rows", order, limit, pages, len(p.Calls))
				}
				for _, c := range p.Calls {
					at, _ := time.Parse(time.RFC3339, c.StartedAt)
					if !last.IsZero() && (order == "newest" && at.After(last) || order == "oldest" && at.Before(last)) {
						t.Fatalf("%s: out of order at %s", order, c.StartedAt)
					}
					last = at
					got = append(got, c.ID)
				}
				if p.Next == nil {
					break
				}
				q.Set("cursor", *p.Next)
			}
			if limit == "" && pages != 3 {
				t.Errorf("%s: %d pages of 100, want 3", order, pages)
			}
			slices.Sort(got)
			if !slices.Equal(got, want) {
				t.Errorf("%s, limit %q: %d calls over %d pages, want each of the 250 once", order, limit, len(got), pages)
			}
		}
	}
}

// TestIntegrationRecentCallsPatches is SPEC.md A4's recent-calls half: a
// call on 1001 patched with hidden 1003 is listed under no filter, and a
// call on 1001 patched with 1002 is found by a filter naming 1002, once.
func TestIntegrationRecentCallsPatches(t *testing.T) {
	e := newEnv(t)
	_, key := e.recorder(t)
	t0 := fixtureTime
	hidden := e.uploadAt(t, key, 1, 1001, []int32{1003}, t0, 5001)
	patched := e.uploadAt(t, key, 2, 1001, []int32{1002}, t0.Add(time.Minute), 5001)
	for _, q := range []url.Values{around(t0), around(t0, "talkgroups", "1001"), around(t0, "talkgroups", "1003"),
		around(t0, "unit", "5001"), around(t0, "talkgroups", "1001,1002,1003")} {
		for _, c := range e.recent(t, q).Calls {
			if c.ID == hidden {
				t.Errorf("?%s lists the call patched with 1003", q.Encode())
			}
		}
	}
	if got := e.recent(t, around(t0, "talkgroups", "1002")); !slices.Equal(ids(got.Calls), []string{patched}) {
		t.Errorf("a filter naming 1002 lists %v, want the patched call once", ids(got.Calls))
	}
	if got := e.recent(t, around(t0, "talkgroups", "1001,1002")); !slices.Equal(ids(got.Calls), []string{patched}) {
		t.Errorf("a filter naming 1001 and 1002 lists %v, want the patched call once", ids(got.Calls))
	}
}

// TestIntegrationRecentCallsHiding is SPEC.md A11's recent-calls half:
// hiding 1001 takes its calls out of the next answer, and making it public
// lists them again (R30, R74).
func TestIntegrationRecentCallsHiding(t *testing.T) {
	e := newEnv(t)
	_, key := e.recorder(t)
	t0 := fixtureTime
	a := e.uploadAt(t, key, 1, 1001, nil, t0, 5001)
	b := e.uploadAt(t, key, 2, 1002, []int32{1001}, t0.Add(time.Minute), 5001)
	c := e.uploadAt(t, key, 3, 1002, nil, t0.Add(2*time.Minute), 5001)
	path := fmt.Sprintf("/systems/%d/talkgroups/1001", e.sys)
	if got := ids(e.recent(t, around(t0)).Calls); !slices.Equal(got, []string{c, b, a}) {
		t.Fatalf("before: %v", got)
	}
	e.mustAdmin(t, "PATCH", path, map[string]any{"state": "hidden"}, 200)
	if got := ids(e.recent(t, around(t0)).Calls); !slices.Equal(got, []string{c}) {
		t.Errorf("1001 hidden: %v, want only 1002's own call", got)
	}
	if got := ids(e.recent(t, around(t0, "talkgroups", "1001")).Calls); len(got) != 0 {
		t.Errorf("1001 hidden, a filter naming it: %v", got)
	}
	e.mustAdmin(t, "PATCH", path, map[string]any{"state": "public"}, 200)
	if got := ids(e.recent(t, around(t0)).Calls); !slices.Equal(got, []string{c, b, a}) {
		t.Errorf("public again: %v", got)
	}
}

// TestIntegrationRecentCallsDelay is SPEC.md A14's recent-calls half: with
// 1001's delay at 5 min, a call ending at 12:00 isn't listed at 12:04:59
// and is at 12:05 (R23). A call in progress on a talkgroup with no delay
// is listed, as the call details give it.
func TestIntegrationRecentCallsDelay(t *testing.T) {
	e := newEnv(t)
	rec := e.liveRecorder(t)
	noon := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	e.clock.set(noon)
	e.mustAdmin(t, "PATCH", fmt.Sprintf("/systems/%d/talkgroups/1001", e.sys), map[string]any{"delay_minutes": 5}, 200)
	start := noon.Add(-20 * time.Second)
	id := e.uploadAt(t, rec.key, 1, 1001, nil, start, 5001)
	_, err := e.st.Pool.Exec(context.Background(), `UPDATE calls SET ended_at = $2 WHERE id = $1::uuid`, id, noon)
	if err != nil {
		t.Fatal(err)
	}
	// A patched call takes its patch's longest delay: on 1002, patched
	// with delayed 1001.
	patched := e.uploadAt(t, rec.key, 2, 1002, []int32{1001}, start.Add(time.Second), 5001)
	_, err = e.st.Pool.Exec(context.Background(), `UPDATE calls SET ended_at = $2 WHERE id = $1::uuid`, patched, noon)
	if err != nil {
		t.Fatal(err)
	}
	live, liveID := rec.start(1002, nil, 5001, noon.Add(-time.Second).UnixMilli())
	_ = live

	for _, at := range []time.Time{noon, noon.Add(4*time.Minute + 59*time.Second)} {
		e.clock.set(at)
		got := e.recent(t, url.Values{}) // the default range: the 31 days to now
		if !slices.Equal(ids(got.Calls), []string{liveID}) {
			t.Errorf("at %s: %v, want only the call in progress", at.Format("15:04:05"), ids(got.Calls))
		}
		if len(got.Calls) == 1 && (!got.Calls[0].InProgress || got.Calls[0].Recording != nil) {
			t.Errorf("the call in progress: %+v", got.Calls[0])
		}
		if got := e.recent(t, url.Values{"talkgroups": {"1001"}}); len(got.Calls) != 0 {
			t.Errorf("at %s, a filter naming 1001 lists %v", at.Format("15:04:05"), ids(got.Calls))
		}
	}
	e.clock.set(noon.Add(5 * time.Minute))
	if got := e.recent(t, url.Values{}); !slices.Equal(ids(got.Calls), []string{liveID, patched, id}) {
		t.Errorf("at 12:05: %v, want the call in progress and both delayed calls", ids(got.Calls))
	}
}

// TestIntegrationRecentCallsDelayPaging: calls a delay holds back don't
// shorten a page or end the list early; the page is filled from the calls
// after them.
func TestIntegrationRecentCallsDelayPaging(t *testing.T) {
	e := newEnv(t)
	name, _ := e.recorder(t)
	noon := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	e.clock.set(noon)
	e.mustAdmin(t, "PATCH", fmt.Sprintf("/systems/%d/talkgroups/1001", e.sys), map[string]any{"delay_minutes": 5}, 200)
	// 150 calls on delayed 1001 that ended a minute ago, newer than 150 on
	// 1002 that ended an hour ago.
	_, err := e.st.Pool.Exec(context.Background(), `
		INSERT INTO calls (recorder_id, sys_num, call_num, start_time, system_id, short_name, talkgroup, started_at, ended_at)
		SELECT r.id, 0, n, 0, $2, 'ex', CASE WHEN n >= 150 THEN 1001 ELSE 1002 END,
		       $3::timestamptz - interval '2 h' + n * interval '1 s',
		       CASE WHEN n >= 150 THEN $3::timestamptz - interval '1 min' ELSE $3::timestamptz - interval '1 h' END
		FROM recorders r, generate_series(0, 299) n WHERE r.name = $1`, name, e.sys, noon)
	if err != nil {
		t.Fatal(err)
	}
	p := e.recent(t, url.Values{})
	if len(p.Calls) != 100 || p.Next == nil || p.Calls[0].Talkgroup.ID != 1002 {
		t.Fatalf("first page: %d calls, next %v", len(p.Calls), p.Next)
	}
	p = e.recent(t, url.Values{"cursor": {*p.Next}})
	if len(p.Calls) != 50 || p.Next != nil {
		t.Errorf("second page: %d calls, next %v; want the last 50", len(p.Calls), p.Next)
	}
}

// TestIntegrationRecentCallsTimeout: a search that runs out of its time
// answers R73's message.
func TestIntegrationRecentCallsTimeout(t *testing.T) {
	e := newEnv(t)
	log := slog.New(slog.DiscardHandler)
	g := gate.New(e.st, e.dir, log, gate.WithSearchTimeout(time.Nanosecond))
	srv := httptest.NewServer(server.New(e.st, log, server.WithPublic(public.New(g, log))))
	defer srv.Close()
	f := &a10World{e: &env{srv: srv}}
	a := do(t, f.req("GET", public.RecentCallsPath, ""))
	var body struct{ Error, Message string }
	_ = json.Unmarshal(a.body, &body)
	if a.status != 503 || body.Error != "search_too_slow" ||
		body.Message != "That search took too long. Try a narrower time range." {
		t.Errorf("a search out of time: %s", a)
	}
}

// TestIntegrationRecentCallsBusyUnit: a unit heard on more calls than the
// gate reads as a list (ADR 0013) is filtered call by call, with the same
// answer: every page full, each call once.
func TestIntegrationRecentCallsBusyUnit(t *testing.T) {
	e := newEnv(t)
	name, _ := e.recorder(t)
	t0 := fixtureTime
	if _, err := e.st.Pool.Exec(context.Background(), `
		WITH c AS (
			INSERT INTO calls (recorder_id, sys_num, call_num, start_time, system_id, short_name, talkgroup, started_at, ended_at)
			SELECT r.id, 0, n, 0, $2, 'ex', 1001, $3::timestamptz + n * interval '1 s', $3::timestamptz + interval '2 h'
			FROM recorders r, generate_series(0, 2099) n WHERE r.name = $1
			RETURNING id, call_num)
		INSERT INTO transmissions (call_id, offset_ms, unit)
		SELECT id, 0, CASE WHEN call_num = 7 THEN 9200 ELSE 9100 END FROM c`, name, e.sys, t0); err != nil {
		t.Fatal(err)
	}
	q := url.Values{"from": {t0.Format(time.RFC3339)}, "to": {t0.Add(time.Hour).Format(time.RFC3339)}, "unit": {"9100"}}
	seen := map[string]bool{}
	for {
		p := e.recent(t, q)
		for _, c := range p.Calls {
			if seen[c.ID] || len(c.Transmissions) != 1 || c.Transmissions[0].Unit != 9100 {
				t.Fatalf("unit 9100: %+v (seen before: %v)", c, seen[c.ID])
			}
			seen[c.ID] = true
		}
		if p.Next == nil {
			if len(p.Calls) == 0 {
				t.Fatal("an empty last page")
			}
			break
		}
		if len(p.Calls) != 100 {
			t.Fatalf("a page of %d before the last", len(p.Calls))
		}
		q.Set("cursor", *p.Next)
	}
	if len(seen) != 2099 {
		t.Errorf("unit 9100 is on %d calls, want 2099", len(seen))
	}
	q.Del("cursor")
	q.Set("unit", "9200")
	if got := e.recent(t, q); len(got.Calls) != 1 || got.Next != nil {
		t.Errorf("unit 9200: %d calls", len(got.Calls))
	}
}
