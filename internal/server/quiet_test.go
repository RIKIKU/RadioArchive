package server_test

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/public"
	"github.com/RIKIKU/RadioArchive/internal/server"
)

// Quiet talkgroups (SPEC.md R114-R117, A44-A47; ADR 0054).

// quietOf is the catalogue's quiet talkgroups, with their days.
func (e *env) quietOf(t *testing.T) map[int32]int {
	t.Helper()
	c, _ := e.catalogue(t)
	out := map[int32]int{}
	for _, x := range c.Talkgroups {
		if x.Quiet {
			out[x.ID] = x.NotHeardDays
		} else if x.NotHeardDays != 0 {
			t.Errorf("%d isn't quiet but has not_heard_days %d", x.ID, x.NotHeardDays)
		}
	}
	return out
}

func (e *env) searchQuiet(t *testing.T, q string, include bool) []int32 {
	t.Helper()
	path := public.SearchPath + "?q=" + url.QueryEscape(q)
	if include {
		path += "&" + public.IncludeQuiet + "=true"
	}
	a := e.pub(t, "GET", path, "")
	if a.status != 200 {
		t.Fatalf("search %q: %s", q, a)
	}
	return catalogueIDs(decodeJSON[public.SearchResponse](t, a.body).Talkgroups)
}

// receiving records that the system was receiving over each span, as
// RecordReceiving does a minute at a time, and has the gate read again.
func (e *env) receiving(t *testing.T, sys int64, spans ...[2]time.Time) {
	t.Helper()
	ctx := context.Background()
	if _, err := e.st.Pool.Exec(ctx, `DELETE FROM system_receiving WHERE system_id = $1`, sys); err != nil {
		t.Fatal(err)
	}
	for _, s := range spans {
		if _, err := e.st.Pool.Exec(ctx, `INSERT INTO system_receiving (system_id, started_at, ended_at) VALUES ($1, $2, $3)`,
			sys, s[0], s[1]); err != nil {
			t.Fatal(err)
		}
	}
	e.gate.QuietChanged()
}

// heardAt sets when a talkgroup was last heard publicly.
func (e *env) heardAt(t *testing.T, sys int64, tg int32, at time.Time) {
	t.Helper()
	if _, err := e.st.Pool.Exec(context.Background(), `
		INSERT INTO talkgroup_last_heard (system_id, talkgroup_id, last_heard_at, public_heard_at) VALUES ($1, $2, $3, $3)
		ON CONFLICT (system_id, talkgroup_id) DO UPDATE SET last_heard_at = $3, public_heard_at = $3`, sys, tg, at); err != nil {
		t.Fatal(err)
	}
	e.gate.QuietChanged()
}

func sameQuiet(got, want map[int32]int) bool {
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}

// TestIntegrationQuietTalkgroups: the window, the grace, the outage rule,
// the exemptions, the search's rules and the first call bringing one back.
func TestIntegrationQuietTalkgroups(t *testing.T) {
	e := newEnv(t)
	_, key := e.recorder(t)
	sys := e.sys
	now := time.Now().UTC().Truncate(time.Second)
	e.clock.set(now)
	day := 24 * time.Hour
	ago := func(d float64) time.Time { return now.Add(-time.Duration(d * float64(day))) }
	tg := func(id int) string { return fmt.Sprintf("/systems/%d/talkgroups/%d", sys, id) }
	check := func(what string, want map[int32]int) {
		t.Helper()
		if got := e.quietOf(t); !sameQuiet(got, want) {
			t.Errorf("%s: quiet %v, want %v", what, got, want)
		}
	}

	// A44, the grace: nothing has been received, so nothing is quiet, and
	// 6 days of receiving isn't a whole window.
	c, _ := e.catalogue(t)
	if c.QuietWindowDays != 7 {
		t.Errorf("window %d days, want the default 7", c.QuietWindowDays)
	}
	check("a new deployment", map[int32]int{})
	e.receiving(t, sys, [2]time.Time{ago(6), now})
	check("6 days received", map[int32]int{})

	// A whole window received: never-heard public talkgroups are quiet,
	// said as the window's days. Hidden 1003 and ignored 1004 aren't in
	// the catalogue at all.
	e.receiving(t, sys, [2]time.Time{ago(30), now})
	check("a whole window", map[int32]int{1001: 7, 1002: 7})

	// Heard 3 days ago: not quiet. Heard 12.5 days ago: quiet, 12 days.
	e.heardAt(t, sys, 1001, ago(3))
	check("1001 heard 3 days ago", map[int32]int{1002: 7})
	e.heardAt(t, sys, 1001, ago(12.5))
	check("1001 heard 12.5 days ago", map[int32]int{1001: 12, 1002: 7})

	// A45, the outage rule: 12.5 days since it was heard, but the system
	// was out for 8 of them, so only 4.5 days of receiving count. 1002,
	// never heard, is quiet: the system has received for a window.
	e.receiving(t, sys, [2]time.Time{ago(2), now}, [2]time.Time{ago(30), ago(10)})
	check("an outage of 8 days", map[int32]int{1002: 7})
	// Out for 4 of them: 8.5 days count, and it's quiet again.
	e.receiving(t, sys, [2]time.Time{ago(2), now}, [2]time.Time{ago(30), ago(6)})
	check("an outage of 4 days", map[int32]int{1001: 12, 1002: 7})

	// A46, the exemptions: a tag's always show, the talkgroup's own, and a
	// delay.
	tags := decodeJSON[[]server.Label](t, e.mustAdmin(t, "GET", "/tags", nil, 200))
	var fireground int64
	for _, x := range tags {
		if x.AlwaysShow == nil || *x.AlwaysShow {
			t.Errorf("tag %+v: always show should be false", x)
		}
		if x.Name == "fireground" {
			fireground = x.ID
		}
	}
	l := decodeJSON[server.Label](t, e.mustAdmin(t, "PATCH", fmt.Sprintf("/tags/%d", fireground), map[string]any{"always_show": true}, 200))
	if l.AlwaysShow == nil || !*l.AlwaysShow {
		t.Errorf("patched tag = %+v", l)
	}
	check("the fireground tag shown always", map[int32]int{1001: 12})
	got := decodeJSON[server.Talkgroup](t, e.mustAdmin(t, "PATCH", tg(1001), map[string]any{"always_show": true}, 200))
	if !got.AlwaysShow {
		t.Errorf("patched talkgroup = %+v", got)
	}
	check("1001 shown always", map[int32]int{})
	e.mustAdmin(t, "PATCH", tg(1001), map[string]any{"always_show": false}, 200)
	e.mustAdmin(t, "PATCH", tg(1001), map[string]any{"delay_minutes": 5}, 200)
	check("1001 delayed", map[int32]int{})
	e.mustAdmin(t, "PATCH", tg(1001), map[string]any{"delay_minutes": 0}, 200)
	e.mustAdmin(t, "PATCH", fmt.Sprintf("/tags/%d", fireground), map[string]any{"always_show": false}, 200)
	check("exemptions off", map[int32]int{1001: 12, 1002: 7})

	// The search leaves quiet talkgroups out, apart from an exact number,
	// name or label, unless asked for them.
	for _, s := range []struct {
		q       string
		include bool
		want    []int32
	}{
		{"disp", false, []int32{}},
		{"disp", true, []int32{1001}},
		{"1001", false, []int32{1001}},
		{"dispatch north", false, []int32{1001}},
		{"FG 41", false, []int32{1002}},
		{"fireground", false, []int32{}},
		{"", false, []int32{}},
		{"", true, []int32{1001, 1002}},
	} {
		if got := e.searchQuiet(t, s.q, s.include); !slices.Equal(got, s.want) {
			t.Errorf("search %q include %v = %v, want %v", s.q, s.include, got, s.want)
		}
	}
	if a := e.pub(t, "GET", public.SearchPath+"?q=x&"+public.IncludeQuiet+"=yes", ""); a.status != 400 {
		t.Errorf("include_quiet=yes = %s", a)
	}
	// A fireground link still finds a quiet channel (R101).
	if a := e.pub(t, "GET", public.FiregroundURL("41"), ""); a.status != 200 {
		t.Errorf("fireground 41, quiet = %s", a)
	}

	// The admin table shows every talkgroup, quiet ones marked.
	table := decodeJSON[server.Table](t, e.mustAdmin(t, "GET", "/talkgroups", nil, 200))
	marked := map[int32]int{}
	for _, r := range table.Talkgroups {
		if r.SystemID == sys && r.Quiet {
			marked[r.ID] = r.NotHeardDays
		}
	}
	if len(table.Talkgroups) < 4 || !sameQuiet(marked, map[int32]int{1001: 12, 1002: 7}) {
		t.Errorf("admin table: %d rows, quiet %v", len(table.Talkgroups), marked)
	}

	// A47, the first call: a call on 1002 patched with hidden 1003 brings
	// nothing back, since listeners can't have it (P1). A public call on
	// 1001 brings it back at once, with no wait for the next read.
	e.uploadAt(t, key, 1, 1002, []int32{1003}, now.Add(-time.Minute), 5001)
	check("a call patched with a hidden talkgroup", map[int32]int{1001: 12, 1002: 7})
	e.uploadAt(t, key, 2, 1001, nil, now.Add(-time.Minute), 5001)
	check("a call on 1001", map[int32]int{1002: 7})

	// The window is a setting: 30 days makes a whole window of receiving
	// 30 days, which the system hasn't had since its outage.
	st := decodeJSON[server.QuietSettings](t, e.mustAdmin(t, "GET", "/quiet", nil, 200))
	if st.WindowDays != 7 || st.DefaultDays != 7 || st.Quiet != 1 || len(st.Systems) != 1 || !st.Systems[0].Full {
		t.Errorf("quiet settings = %+v", st)
	}
	e.mustAdmin(t, "PUT", "/quiet", map[string]any{"window_days": 0}, 400)
	st = decodeJSON[server.QuietSettings](t, e.mustAdmin(t, "PUT", "/quiet", map[string]any{"window_days": 30}, 200))
	if st.WindowDays != 30 || st.Quiet != 0 || st.Systems[0].Full || st.Systems[0].ReceivedHours != 26*24 {
		t.Errorf("a 30-day window = %+v", st)
	}
	if c, _ = e.catalogue(t); c.QuietWindowDays != 30 {
		t.Errorf("catalogue window %d", c.QuietWindowDays)
	}
	check("a 30-day window", map[int32]int{})
}

// TestIntegrationQuietReceiving: a system is receiving while a recorder
// attached to it is connected, its heartbeat fresh and its control
// channel decoding; each minute extends its span, and an outage starts a
// new one (R115).
func TestIntegrationQuietReceiving(t *testing.T) {
	e := newEnv(t)
	name, key := e.recorder(t)
	ctx := context.Background()
	spans := func() [][2]time.Time {
		t.Helper()
		rows, err := e.st.Pool.Query(ctx, `SELECT started_at, ended_at FROM system_receiving WHERE system_id = $1 ORDER BY started_at`, e.sys)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out [][2]time.Time
		for rows.Next() {
			var s [2]time.Time
			if err := rows.Scan(&s[0], &s[1]); err != nil {
				t.Fatal(err)
			}
			out = append(out, [2]time.Time{s[0].UTC(), s[1].UTC()})
		}
		return out
	}
	t0 := time.Now().UTC().Truncate(time.Second)
	at := func(d time.Duration) time.Time { return t0.Add(d) }

	// Not connected: nothing.
	e.clock.set(t0)
	e.rs.RecordReceiving(ctx)
	if s := spans(); len(s) != 0 {
		t.Fatalf("receiving before it connected: %v", s)
	}

	c := e.open(t, key)
	send(t, c, heartbeat(rate("ex", "p25", 38.5)))
	e.waitStatus(t, name, "decoding", func(s server.RecorderStatus) bool {
		return len(s.Systems) == 1 && s.Systems[0].DecodeRate != nil
	})
	e.rs.RecordReceiving(ctx)
	e.clock.set(at(20 * time.Second))
	e.rs.RecordReceiving(ctx)
	if s := spans(); len(s) != 1 || !s[0][0].Equal(t0) || !s[0][1].Equal(at(20*time.Second)) {
		t.Fatalf("one span from t0 to 20 s: %v", s)
	}

	// A heartbeat 40 s old is stale: nothing's extended.
	e.clock.set(at(40 * time.Second))
	e.rs.RecordReceiving(ctx)
	if s := spans(); len(s) != 1 || !s[0][1].Equal(at(20*time.Second)) {
		t.Fatalf("a stale heartbeat extended it: %v", s)
	}

	// A decode rate near 0 (deaf, control channel lost) isn't receiving.
	e.clock.set(at(50 * time.Second))
	send(t, c, heartbeat(rate("ex", "p25", 0)))
	e.waitStatus(t, name, "deaf", func(s server.RecorderStatus) bool { return s.Signals.Deaf })
	e.rs.RecordReceiving(ctx)
	if s := spans(); len(s) != 1 || !s[0][1].Equal(at(20*time.Second)) {
		t.Fatalf("a deaf recorder extended it: %v", s)
	}

	// Decoding again after more than the gap: a new span.
	e.clock.set(at(5 * time.Minute))
	send(t, c, heartbeat(rate("ex", "p25", 38.5)))
	e.waitStatus(t, name, "decoding", func(s server.RecorderStatus) bool { return !s.Signals.Deaf })
	e.rs.RecordReceiving(ctx)
	e.clock.set(at(6 * time.Minute))
	send(t, c, heartbeat(rate("ex", "p25", 38.5)))
	time.Sleep(500 * time.Millisecond)
	e.rs.RecordReceiving(ctx)
	if s := spans(); len(s) != 2 || !s[1][0].Equal(at(5*time.Minute)) || !s[1][1].Equal(at(6*time.Minute)) {
		t.Fatalf("after the outage: %v", s)
	}
}
