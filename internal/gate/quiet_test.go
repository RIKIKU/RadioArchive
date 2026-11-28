package gate

import (
	"slices"
	"testing"
	"time"
)

var quietNow = time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)

func days(n float64) time.Duration { return time.Duration(n * 24 * float64(time.Hour)) }

func ago(n float64) time.Time { return quietNow.Add(-days(n)) }

// The window counts only the time a system was receiving (R115), so an
// outage stops the clock; overlapping spans count once; less than a
// window of receiving is the new-deployment grace (R116).
func TestCutoffOf(t *testing.T) {
	week := days(7)
	for _, c := range []struct {
		name     string
		spans    []Span // newest first
		cut      time.Time
		received time.Duration
		ok       bool
	}{
		{"never received", nil, time.Time{}, 0, false},
		{"received all along", []Span{{ago(30), quietNow}}, ago(7), week, true},
		{"six days so far: the grace", []Span{{ago(6), quietNow}}, time.Time{}, days(6), false},
		{"exactly a window", []Span{{ago(7), quietNow}}, ago(7), week, true},
		// Received 2 days, an outage of 3, then 5 more before it: the
		// window reaches back past the outage, 3 days further.
		{"an outage stops the clock", []Span{{ago(2), quietNow}, {ago(20), ago(5)}}, ago(10), week, true},
		// Two servers' overlapping spans count once.
		{"overlaps count once", []Span{{ago(3), quietNow}, {ago(4), ago(1)}, {ago(10), ago(2)}}, ago(7), week, true},
		{"a span inside another", []Span{{ago(8), quietNow}, {ago(5), ago(4)}}, ago(7), week, true},
		// A span ending after now (another server's clock ahead) counts to now.
		{"ahead of now", []Span{{ago(8), quietNow.Add(time.Hour)}}, ago(7), week, true},
		{"outages leave too little", []Span{{ago(2), quietNow}, {ago(10), ago(8)}, {ago(30), ago(28)}}, time.Time{}, days(6), false},
	} {
		cut, received, ok := cutoffOf(c.spans, week, quietNow)
		if ok != c.ok || !cut.Equal(c.cut) || received != c.received {
			t.Errorf("%s: cutoff %v, received %v, %v; want %v, %v, %v", c.name, cut, received, ok, c.cut, c.received, c.ok)
		}
	}
}

func quietFixture() (*Quiet, CatalogueTalkgroup) {
	q := &Quiet{At: quietNow, WindowDays: 7,
		cutoff:   map[int64]time.Time{1: ago(7)},
		received: map[int64]time.Duration{1: days(7), 2: days(3)},
		heard:    map[TalkgroupKey]time.Time{}}
	return q, CatalogueTalkgroup{System: System{ID: 1}, ID: 1001, Name: "Dispatch North", Label: "DISP N", Tag: "dispatch"}
}

func TestQuietOf(t *testing.T) {
	q, tg := quietFixture()
	check := func(what string, tg CatalogueTalkgroup, quiet bool, n int) {
		t.Helper()
		if got, d := q.Of(tg); got != quiet || d != n {
			t.Errorf("%s: quiet %v, %d days; want %v, %d", what, got, d, quiet, n)
		}
	}
	check("never heard, a whole window received", tg, true, 7)
	q.heard[TalkgroupKey{1, 1001}] = ago(12.5)
	check("heard 12.5 days ago", tg, true, 12)
	q.heard[TalkgroupKey{1, 1001}] = ago(6.9)
	check("heard inside the window", tg, false, 0)
	q.heard[TalkgroupKey{1, 1001}] = ago(7)
	check("heard at the cutoff", tg, false, 0)
	delete(q.heard, TalkgroupKey{1, 1001})

	// Exemptions (R115): always show, the talkgroup's or its tag's (the
	// catalogue folds them into one), and a delay (R23).
	shown := tg
	shown.AlwaysShow = true
	check("always show", shown, false, 0)
	delayed := tg
	delayed.DelayMinutes = 5
	check("delayed", delayed, false, 0)

	// The grace (R116): a system that hasn't received for a whole window.
	other := tg
	other.System.ID = 2
	check("system 2, in its grace", other, false, 0)
	if d, full := q.Received(2); d != days(3) || full {
		t.Errorf("system 2 received %v, %v", d, full)
	}
	if d, full := q.Received(1); d != days(7) || !full {
		t.Errorf("system 1 received %v, %v", d, full)
	}

	var none *Quiet
	if got, _ := none.Of(tg); got {
		t.Error("no read says quiet")
	}
}

// The search (R114): no quiet talkgroups, apart from an exact number, name
// or label, unless asked for.
func TestQuietShown(t *testing.T) {
	q, quiet := quietFixture()
	heard := CatalogueTalkgroup{System: System{ID: 1}, ID: 1005, Name: "Dispatch South", Label: "DISP S", Tag: "dispatch"}
	q.heard[TalkgroupKey{1, 1005}] = ago(1)
	ts := []CatalogueTalkgroup{quiet, heard}
	ids := func(ts []CatalogueTalkgroup) []int32 {
		out := []int32{}
		for _, t := range ts {
			out = append(out, t.ID)
		}
		return out
	}
	for _, c := range []struct {
		q       string
		include bool
		want    []int32
	}{
		{"disp", false, []int32{1005}},
		{"disp", true, []int32{1001, 1005}},
		{"1001", false, []int32{1001, 1005}},
		{" Dispatch NORTH ", false, []int32{1001, 1005}},
		{"disp n", false, []int32{1001, 1005}},
		{"dispatch", false, []int32{1005}}, // its tag isn't its name
		{"", false, []int32{1005}},
	} {
		if got := ids(q.Shown(c.q, ts, c.include)); !slices.Equal(got, c.want) {
			t.Errorf("Shown(%q, %v) = %v, want %v", c.q, c.include, got, c.want)
		}
	}
}
