package retention

import (
	"errors"
	"testing"
	"time"
)

func TestParsePercent(t *testing.T) {
	for in, want := range map[string]float64{"5": 0.05, "5%": 0.05, " 2.5 % ": 0.025, "50": 0.5} {
		if got, err := ParsePercent(in); err != nil || got != want {
			t.Errorf("ParsePercent(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "0", "100", "-1", "five", "5%%", "0.05x"} {
		if _, err := ParsePercent(in); err == nil {
			t.Errorf("ParsePercent(%q) succeeded", in)
		}
	}
}

func TestParseAt(t *testing.T) {
	if got, err := ParseAt("03:00"); err != nil || got != 3*time.Hour {
		t.Errorf("ParseAt(03:00) = %v, %v", got, err)
	}
	if got, err := ParseAt("23:59"); err != nil || got != 23*time.Hour+59*time.Minute {
		t.Errorf("ParseAt(23:59) = %v, %v", got, err)
	}
	for _, in := range []string{"3", "24:00", "03:00:00", ""} {
		if _, err := ParseAt(in); err == nil {
			t.Errorf("ParseAt(%q) succeeded", in)
		}
	}
}

// The schedule is a local time of day, and holds across a daylight-saving
// change.
func TestSchedule(t *testing.T) {
	loc, err := time.LoadLocation("Australia/Sydney")
	if err != nil {
		t.Skip("no time zone database:", err)
	}
	j := &Job{cfg: Config{At: 3 * time.Hour, Location: loc}}
	at := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, c := range []struct{ now, next, prev string }{
		{"2026-11-24 02:59", "2026-11-24 03:00", "2026-11-23 03:00"},
		{"2026-11-24 03:00", "2026-11-25 03:00", "2026-11-24 03:00"},
		{"2026-11-24 23:00", "2026-11-25 03:00", "2026-11-24 03:00"},
		// Clocks go forward at 02:00 on 4 October 2026.
		{"2026-10-03 12:00", "2026-10-04 03:00", "2026-10-03 03:00"},
	} {
		now := at(c.now)
		if got := j.NextRun(now); !got.Equal(at(c.next)) {
			t.Errorf("NextRun(%s) = %s, want %s", c.now, got, c.next)
		}
		if got := j.previousRun(now); !got.Equal(at(c.prev)) {
			t.Errorf("previousRun(%s) = %s, want %s", c.now, got, c.prev)
		}
	}
}

func TestCutoff(t *testing.T) {
	now := time.Date(2026, 11, 24, 3, 0, 0, 0, time.UTC)
	if got, want := Cutoff(now, 30), time.Date(2026, 10, 25, 3, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("Cutoff = %s, want %s", got, want)
	}
}

// A call goes at the first nightly run whose cut-off is after its start:
// the run after its start plus the period (ADR 0005, ADR 0018). A19's call,
// recorded 29 days ago with 30 days' retention, is deleted in 1 to 2 days.
func TestDeletesAt(t *testing.T) {
	j := &Job{cfg: Config{At: 3 * time.Hour, Location: time.UTC}}
	for _, c := range []struct{ started, want string }{
		{"2026-10-25T02:59:59Z", "2026-11-24T03:00:00Z"},
		// Exactly on the cut-off: kept, as `started_at < cutoff` keeps it.
		{"2026-10-25T03:00:00Z", "2026-11-25T03:00:00Z"},
		{"2026-10-25T14:00:00Z", "2026-11-25T03:00:00Z"},
	} {
		started, _ := time.Parse(time.RFC3339, c.started)
		want, _ := time.Parse(time.RFC3339, c.want)
		got := j.DeletesAt(30, started)
		if !got.Equal(want) {
			t.Errorf("DeletesAt(30, %s) = %s, want %s", c.started, got, c.want)
		}
		// The run then deletes it, and the run before keeps it.
		if !started.Before(Cutoff(got, 30)) || started.Before(Cutoff(got.AddDate(0, 0, -1), 30)) {
			t.Errorf("%s: the run at %s doesn't delete it first", c.started, got)
		}
	}
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	if left := j.DeletesAt(30, now.AddDate(0, 0, -29)).Sub(now); left < 24*time.Hour || left >= 48*time.Hour {
		t.Errorf("A19: a call recorded 29 days ago is deleted in %s", left)
	}
}

func TestDiskState(t *testing.T) {
	var space Space
	var fail error
	j := &Job{cfg: Config{MinFree: 0.05, FreeSpace: func(string) (Space, error) { return space, fail }}}
	for _, c := range []struct {
		free  uint64
		state DiskState
	}{{0, DiskLow}, {49, DiskLow}, {50, DiskWarning}, {149, DiskWarning}, {150, DiskOK}, {1000, DiskOK}} {
		space = Space{Free: c.free, Total: 1000}
		d, err := j.Disk()
		if err != nil || d.State != c.state || d.MinFree != 0.05 || d.WarnFree != 0.15 {
			t.Errorf("%d of 1000 free: %+v, %v; want %s", c.free, d, err, c.state)
		}
	}
	// Full shows only while below the minimum.
	j.full = true
	for _, c := range []struct {
		free  uint64
		state DiskState
	}{{49, DiskFull}, {50, DiskWarning}, {150, DiskOK}} {
		space = Space{Free: c.free, Total: 1000}
		if d, err := j.Disk(); err != nil || d.State != c.state {
			t.Errorf("%d of 1000 free, full raised: %+v, %v; want %s", c.free, d, err, c.state)
		}
	}
	j.full = false
	if (Space{}).FreeFraction() != 0 {
		t.Error("an empty volume isn't 0 free")
	}
	fail = errors.New("no")
	if _, err := j.Disk(); err == nil {
		t.Error("Disk hid the measuring error")
	}
}

func TestNewChecks(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Error("New with no store succeeded")
	}
}
