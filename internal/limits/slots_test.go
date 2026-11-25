package limits

import (
	"fmt"
	"testing"
)

// TestLive is R71's caps: 4 sockets per client and 50 in all, refused
// with 429 and 503 respectively, each asking to wait 30 s; closing one
// makes room; the check opens nothing.
func TestLive(t *testing.T) {
	l, err := New(Defaults())
	if err != nil {
		t.Fatal(err)
	}
	var open []func()
	for i := range 4 {
		r, f, ok := l.Live("192.0.2.1")
		if !ok {
			t.Fatalf("socket %d: %+v", i+1, f)
		}
		open = append(open, r)
	}
	f, ok := l.LiveCheck("192.0.2.1")
	if _, g, ok2 := l.Live("192.0.2.1"); ok || ok2 || f != g || f.Status != 429 || f.Code != "too_many_live" || f.Seconds() != 30 ||
		f.Message != "Too many live connections from here: at most 4 at once. Try again in 30 seconds." {
		t.Fatalf("5th: %+v / %+v", f, g)
	}
	if l.Hits(Live) != 1 {
		t.Errorf("hits %d: the check counted, or the refusal didn't", l.Hits(Live))
	}
	// Closing one, twice, makes room for one.
	open[0]()
	open[0]()
	if _, _, ok := l.Live("192.0.2.1"); !ok {
		t.Error("no room after closing one")
	}
	if _, _, ok := l.Live("192.0.2.1"); ok {
		t.Error("a second closing made more room")
	}

	// 46 more, from 12 clients: 50 in all.
	for i := range 46 {
		if _, f, ok := l.Live(Client(fmt.Sprintf("198.51.100.%d", i/4))); !ok {
			t.Fatalf("socket %d: %+v", i+5, f)
		}
	}
	if n := l.LiveOpen(); n != 50 {
		t.Fatalf("%d open", n)
	}
	f, ok = l.LiveCheck("203.0.113.1")
	if _, g, ok2 := l.Live("203.0.113.1"); ok || ok2 || f != g || f.Status != 503 || f.Code != "radio_full" || f.Seconds() != 30 ||
		f.Message != "The radio is full right now. Try again in 30 seconds." {
		t.Fatalf("51st: %+v / %+v", f, g)
	}
	open[1]()
	if _, f, ok := l.Live("203.0.113.1"); !ok {
		t.Errorf("no room after one closed: %+v", f)
	}
}
