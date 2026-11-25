package limits

import (
	"context"
	"strings"
	"testing"

	"github.com/RIKIKU/RadioArchive/internal/telemetry"
)

// TestMetrics (R90, R91): hits per limit, the sockets open, and the radio
// full once every socket it allows is open, for the 10-minute alert.
func TestMetrics(t *testing.T) {
	cfg := Defaults()
	cfg.LiveTotal = 2
	l, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	scrape := func() string {
		reg := telemetry.NewRegistry(telemetry.All)
		reg.Add(l)
		return string(reg.Write(context.Background()))
	}
	want := func(out string, lines ...string) {
		t.Helper()
		for _, w := range lines {
			if !strings.Contains(out, w+"\n") {
				t.Errorf("no %q in\n%s", w, out)
			}
		}
	}
	want(scrape(), "radioarchive_live_full 0", "radioarchive_live_sockets 0", "radioarchive_live_sockets_limit 2",
		`radioarchive_limit_hits_total{limit="live"} 0`)
	first, _, _ := l.Live("192.0.2.1")
	if _, _, ok := l.Live("198.51.100.1"); !ok {
		t.Fatal("the second socket was refused")
	}
	if _, _, ok := l.Live("203.0.113.1"); ok {
		t.Fatal("a third socket opened")
	}
	for range 3 {
		l.Take(Requests, "192.0.2.9")
	}
	want(scrape(), "radioarchive_live_full 1", "radioarchive_live_sockets 2", `radioarchive_limit_hits_total{limit="live"} 1`,
		`radioarchive_limit_hits_total{limit="requests"} 0`, "radioarchive_zips_open 0")
	first()
	want(scrape(), "radioarchive_live_full 0", "radioarchive_live_sockets 1")
}
