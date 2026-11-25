package limits

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestZipCaps is R72's caps: 2 zips at once per client (429) and 6 in all
// (503), each refusal waiting until the soonest zip in the way should be
// done at 2 MB/s; an admin's zip isn't counted.
func TestZipCaps(t *testing.T) {
	l, err := New(Defaults())
	if err != nil {
		t.Fatal(err)
	}
	start := func(c Client, size string, sent int) *Download {
		t.Helper()
		d, f, ok := l.Zip(c)
		if !ok {
			t.Fatalf("%s: %+v", c, f)
		}
		w := httptest.NewRecorder()
		pw := d.Writer(context.Background(), w)
		if size != "" {
			pw.Header().Set("Content-Length", size)
		}
		pw.WriteHeader(200)
		d.sent.Store(int64(sent))
		return d
	}
	a := start("192.0.2.1", "10000000", 0)  // 5 s left
	start("192.0.2.1", "10000000", 6000000) // 2 s left
	if _, f, ok := l.Zip("192.0.2.1"); ok || f.Status != 429 || f.Code != "too_many_downloads" ||
		f.Seconds() != 2 || f.Message != "Too many downloads at once: at most 2 from here. Try again in 2 seconds." {
		t.Fatalf("3rd: %+v", f)
	}
	if l.Hits(Zip) != 1 {
		t.Errorf("hits %d", l.Hits(Zip))
	}
	a.Done()
	a.Done()
	start("192.0.2.1", "", 0)
	if _, _, ok := l.Zip("192.0.2.1"); ok {
		t.Error("a second Done made room")
	}
	// An admin over the cap isn't counted.
	l.ZipAnyway("192.0.2.1").Done()
	start("198.51.100.1", "240000000", 0) // 2 minutes left
	start("198.51.100.1", "", 0)
	start("198.51.100.2", "300000000", 0)
	start("198.51.100.2", "", 0)
	if n := l.ZipsOpen(); n != 6 {
		t.Fatalf("%d open", n)
	}
	// The soonest of the 6 is the one with 2 s left.
	if _, f, ok := l.Zip("203.0.113.1"); ok || f.Status != 503 || f.Code != "downloads_busy" || f.Seconds() != 2 ||
		f.Message != "The radio is busy with other downloads. Try again in 2 seconds." {
		t.Fatalf("7th: %+v", f)
	}
	// With no size known, a minute.
	l2, _ := New(Defaults())
	l2.Zip("192.0.2.9")
	l2.Zip("192.0.2.9")
	if _, f, _ := l2.Zip("192.0.2.9"); f.Seconds() != 60 || !strings.HasSuffix(f.Message, "Try again in 1 minute.") {
		t.Errorf("unknown sizes: %+v", f)
	}
}

// TestZipSpeed is R72's speeds, measured: 1 MB at 2 MB/s takes about half
// a second, and two zips at once, under a total of 3 MB/s, take about as
// long as 1.5 MB at 3 MB/s, not at 4.
func TestZipSpeed(t *testing.T) {
	if testing.Short() {
		t.Skip("timed")
	}
	l, _ := New(Defaults())
	send := func(l *Limits, c Client, n int) time.Duration {
		d, _, ok := l.Zip(c)
		if !ok {
			t.Fatal("refused")
		}
		defer d.Done()
		w := httptest.NewRecorder()
		began := time.Now()
		if m, err := d.Writer(context.Background(), w).Write(bytes.Repeat([]byte{1}, n)); m != n || err != nil {
			t.Fatalf("wrote %d: %v", m, err)
		}
		if w.Body.Len() != n {
			t.Fatalf("got %d", w.Body.Len())
		}
		return time.Since(began)
	}
	if d := send(l, "192.0.2.1", 1*MB); d < 400*time.Millisecond || d > 900*time.Millisecond {
		t.Errorf("1 MB at 2 MB/s took %v", d)
	}

	cfg := Defaults()
	cfg.ZipSpeedTotal = 3 * MB
	l, _ = New(cfg)
	var wg sync.WaitGroup
	began := time.Now()
	for _, c := range []Client{"192.0.2.1", "192.0.2.2"} {
		wg.Go(func() { send(l, c, 1*MB+MB/2) })
	}
	wg.Wait()
	// 3 MB at 3 MB/s is 1 s; at 2 MB/s each it would be 0.75 s.
	if d := time.Since(began); d < 900*time.Millisecond || d > 1600*time.Millisecond {
		t.Errorf("two zips under 3 MB/s took %v", d)
	}
}

// TestZipGone: a write waiting its turn gives up when the listener goes.
func TestZipGone(t *testing.T) {
	cfg := Defaults()
	cfg.ZipSpeed = 1000
	l, _ := New(cfg)
	d, _, _ := l.Zip("192.0.2.1")
	defer d.Done()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	began := time.Now()
	if _, err := d.Writer(ctx, httptest.NewRecorder()).Write(make([]byte, 100_000)); err == nil || time.Since(began) > time.Second {
		t.Errorf("err %v after %v", err, time.Since(began))
	}
}
