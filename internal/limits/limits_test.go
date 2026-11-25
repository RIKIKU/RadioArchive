package limits

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"
)

func req(remote string, xff ...string) *http.Request {
	r := httptest.NewRequest("GET", "/api/v1/catalogue", nil)
	r.RemoteAddr = remote
	for _, v := range xff {
		r.Header.Add("X-Forwarded-For", v)
	}
	return r
}

// TestClientOf is R70's client: an IPv4 address or an IPv6 /64, from
// X-Forwarded-For only when the request is from the trusted proxy, and
// then its last address.
func TestClientOf(t *testing.T) {
	proxy := netip.MustParseAddr("10.0.0.2")
	for _, c := range []struct {
		name   string
		r      *http.Request
		proxy  netip.Addr
		client Client
	}{
		{"IPv4, no proxy", req("192.0.2.7:51000"), netip.Addr{}, "192.0.2.7"},
		{"a forwarded address with no proxy set isn't read", req("192.0.2.7:51000", "198.51.100.9"), netip.Addr{}, "192.0.2.7"},
		{"A21: from the trusted proxy, the forwarded address", req("10.0.0.2:40000", "198.51.100.9"), proxy, "198.51.100.9"},
		{"A21: from another address, its own", req("10.0.0.3:40000", "198.51.100.9"), proxy, "10.0.0.3"},
		{"the last of several, the one the proxy wrote", req("10.0.0.2:40000", "203.0.113.1, 198.51.100.9"), proxy, "198.51.100.9"},
		{"the last of several header lines", req("10.0.0.2:40000", "203.0.113.1", "198.51.100.9"), proxy, "198.51.100.9"},
		{"a forwarded address with a port", req("10.0.0.2:40000", "198.51.100.9:5555"), proxy, "198.51.100.9"},
		{"a forwarded IPv6 address with a port", req("10.0.0.2:40000", "[2001:db8:7:8::1]:5555"), proxy, "2001:db8:7:8::/64"},
		{"no forwarded address: the proxy's own", req("10.0.0.2:40000"), proxy, "10.0.0.2"},
		{"an unreadable last address: the proxy's own, never an earlier one", req("10.0.0.2:40000", "198.51.100.9, junk"), proxy, "10.0.0.2"},
		{"an empty last address: the proxy's own", req("10.0.0.2:40000", "198.51.100.9,"), proxy, "10.0.0.2"},
		{"IPv6: its /64", req("[2001:db8:1:2:aaaa::1]:443"), netip.Addr{}, "2001:db8:1:2::/64"},
		{"IPv6 with a zone: its /64", req("[fe80::1%eth0]:443"), netip.Addr{}, "fe80::/64"},
		{"IPv4-mapped IPv6 is its IPv4", req("[::ffff:192.0.2.7]:443"), netip.Addr{}, "192.0.2.7"},
		{"a trusted IPv6 proxy", req("[2001:db8::2]:443", "192.0.2.8"), netip.MustParseAddr("2001:db8::2"), "192.0.2.8"},
		{"a trusted proxy as IPv4-mapped", req("[::ffff:10.0.0.2]:443", "192.0.2.8"), proxy, "192.0.2.8"},
		{"an address that can't be read", req("pipe"), netip.Addr{}, unknownClient},
	} {
		if got := ClientOf(c.r, c.proxy); got != c.client {
			t.Errorf("%s: %q, want %q", c.name, got, c.client)
		}
	}
	// Two addresses in one /64 are one client; in two, two.
	a, b := ClientOf(req("[2001:db8:1:2::1]:1"), netip.Addr{}), ClientOf(req("[2001:db8:1:2:ffff:ffff:ffff:ffff]:1"), netip.Addr{})
	other := ClientOf(req("[2001:db8:1:3::1]:1"), netip.Addr{})
	if a != b || a == other {
		t.Errorf("one /64: %q and %q; another: %q", a, b, other)
	}
}

func TestParseTrustedProxy(t *testing.T) {
	for in, want := range map[string]string{"10.0.0.2": "10.0.0.2", "2001:db8::2": "2001:db8::2", "::ffff:10.0.0.2": "10.0.0.2"} {
		if a, err := ParseTrustedProxy(in); err != nil || a.String() != want {
			t.Errorf("%q: %v %v, want %s", in, a, err, want)
		}
	}
	for _, in := range []string{"10.0.0.0/24", "10.0.0.2/32", "proxy.example.test", "10.0.0.2:80", "fe80::1%eth0", ""} {
		if _, err := ParseTrustedProxy(in); err == nil {
			t.Errorf("%q was taken", in)
		}
	}
}

func TestFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	c, err := FromEnv(env(nil))
	if err != nil || c != Defaults() || c.Requests != 120 || c.Playback != 600 || c.TrustedProxy.IsValid() {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	c, err = FromEnv(env(map[string]string{"TRUSTED_PROXY": " 10.0.0.2 ", "LIMIT_REQUESTS": "5000", "LIMIT_PLAYBACK": "9000"}))
	if err != nil || c.TrustedProxy != netip.MustParseAddr("10.0.0.2") || c.Requests != 5000 || c.Playback != 9000 {
		t.Fatalf("set: %+v %v", c, err)
	}
	for k, v := range map[string]string{"TRUSTED_PROXY": "10.0.0.0/8", "LIMIT_REQUESTS": "0", "LIMIT_PLAYBACK": "lots"} {
		if _, err := FromEnv(env(map[string]string{k: v})); err == nil || !strings.HasPrefix(err.Error(), k+":") {
			t.Errorf("%s=%s: %v", k, v, err)
		}
	}
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

// TestTake is R73's 120 a minute: the 121st request in a client's minute
// is refused until the minute is up, saying when, and refusals aren't
// counted. Other clients and the playback limit count apart.
func TestTake(t *testing.T) {
	c := &clock{time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)}
	l, err := New(Defaults(), WithClock(c.now))
	if err != nil {
		t.Fatal(err)
	}
	for i := range 120 {
		if f, ok := l.Take(Requests, "192.0.2.1"); !ok {
			t.Fatalf("request %d refused: %+v", i+1, f)
		}
		c.t = c.t.Add(100 * time.Millisecond)
	}
	// 12 s into the minute.
	f, ok := l.Take(Requests, "192.0.2.1")
	if ok || f.Status != 429 || f.Code != "too_many_requests" || f.Seconds() != 48 ||
		f.Message != "Too many requests from here: at most 120 a minute. Try again in 48 seconds." {
		t.Fatalf("121st: %+v %v", f, ok)
	}
	if l.Hits(Requests) != 1 {
		t.Errorf("hits %d", l.Hits(Requests))
	}
	if _, ok := l.Take(Requests, "192.0.2.2"); !ok {
		t.Error("another client was refused")
	}
	if _, ok := l.Take(Playback, "192.0.2.1"); !ok {
		t.Error("playback was refused under the request limit")
	}
	c.t = c.t.Add(47 * time.Second)
	if _, ok := l.Take(Requests, "192.0.2.1"); ok {
		t.Error("let in a second early")
	}
	c.t = c.t.Add(time.Second)
	for i := range 120 {
		if _, ok := l.Take(Requests, "192.0.2.1"); !ok {
			t.Fatalf("next minute: request %d refused", i+1)
		}
	}
	if _, ok := l.Take(Requests, "192.0.2.1"); ok {
		t.Error("next minute's 121st let in")
	}

	for range 600 {
		l.Take(Playback, "192.0.2.3")
	}
	if f, ok := l.Take(Playback, "192.0.2.3"); ok || f.Code != "too_many_plays" ||
		!strings.HasPrefix(f.Message, "Too many recordings played from here: at most 600 requests a minute. Try again in ") {
		t.Errorf("601st playback: %+v", f)
	}
}

// TestSweep: a client's window is forgotten once it has ended.
func TestSweep(t *testing.T) {
	c := &clock{time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)}
	l, _ := New(Defaults(), WithClock(c.now))
	for i := range 1000 {
		l.Take(Requests, Client(strconv.Itoa(i)))
	}
	c.t = c.t.Add(2 * time.Minute)
	l.Take(Requests, "192.0.2.1")
	if n := len(l.requests.by); n != 1 {
		t.Errorf("%d windows kept", n)
	}
}

func TestRefusalWrite(t *testing.T) {
	w := httptest.NewRecorder()
	Refusal{Status: 429, Code: "too_many_requests", Message: "Too many.", RetryAfter: 1500 * time.Millisecond}.Write(w)
	if w.Code != 429 || w.Header().Get("Retry-After") != "2" || w.Header().Get("Cache-Control") != "no-store" ||
		w.Header().Get("Content-Type") != "application/json" ||
		w.Body.String() != `{"error":"too_many_requests","message":"Too many.","retry_after":2}`+"\n" {
		t.Errorf("%d %v %q", w.Code, w.Header(), w.Body)
	}
}

func TestWait(t *testing.T) {
	for d, want := range map[time.Duration]string{0: "1 second", 300 * time.Millisecond: "1 second", time.Second: "1 second",
		40 * time.Second: "40 seconds", 59*time.Second + time.Millisecond: "1 minute", time.Minute: "1 minute",
		61 * time.Second: "2 minutes", 2 * time.Minute: "2 minutes"} {
		if got := Wait(d); got != want {
			t.Errorf("%v: %q, want %q", d, got, want)
		}
	}
}
