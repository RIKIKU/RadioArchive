// Package limits is the server's heavy-use limits (SPEC.md R70-R73; ADR
// 0036): who a request's client is, and how much of the public API each
// client may use. Every count is kept in memory, so a restart starts them
// again (R70). Nothing here logs, and nothing it keeps leaves the process:
// a client's address is never written anywhere (R90, P4).
package limits

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Defaults are SPEC.md section 6.11's numbers.
const (
	DefaultRequests      = 120    // public API requests a minute per client (R73)
	DefaultPlayback      = 600    // recording playback requests a minute per client (R73)
	DefaultLivePerClient = 4      // listener WebSockets at once per client (R71)
	DefaultLiveTotal     = 50     // listener WebSockets at once in all (R71)
	DefaultZipsPerClient = 2      // zips at once per client (R72)
	DefaultZipsTotal     = 6      // zips at once in all (R72)
	DefaultZipSpeed      = 2 * MB // bytes a second, each zip (R72)
	DefaultZipSpeedTotal = 8 * MB // bytes a second, all zips together (R72)
)

// Config is the limits a deployment sets (section 6.11: every number is an
// environment variable, with these defaults).
type Config struct {
	// TrustedProxy is the one proxy whose X-Forwarded-For is read (R70);
	// the zero Addr is none.
	TrustedProxy netip.Addr
	// Requests is the public API requests a client may make a minute.
	Requests int
	// Playback is the recording playback requests a client may make a
	// minute, ranges and all.
	Playback int
	// LivePerClient is the listener WebSockets one client may have open at
	// once, and LiveTotal how many may be open in all.
	LivePerClient, LiveTotal int
	// ZipsPerClient is the zips one client may be sent at once, and
	// ZipsTotal how many may be sent in all.
	ZipsPerClient, ZipsTotal int
	// ZipSpeed is the most bytes a second each zip is sent at, and
	// ZipSpeedTotal the most for all zips together.
	ZipSpeed, ZipSpeedTotal int64
}

// Defaults is the SPEC's limits, with no trusted proxy.
func Defaults() Config {
	return Config{Requests: DefaultRequests, Playback: DefaultPlayback,
		LivePerClient: DefaultLivePerClient, LiveTotal: DefaultLiveTotal,
		ZipsPerClient: DefaultZipsPerClient, ZipsTotal: DefaultZipsTotal,
		ZipSpeed: DefaultZipSpeed, ZipSpeedTotal: DefaultZipSpeedTotal}
}

// FromEnv reads the limits from the environment: TRUSTED_PROXY,
// LIMIT_REQUESTS, LIMIT_PLAYBACK, LIMIT_LIVE_PER_CLIENT, LIMIT_LIVE_TOTAL,
// LIMIT_ZIPS_PER_CLIENT and LIMIT_ZIPS_TOTAL (whole numbers), and
// LIMIT_ZIP_SPEED and LIMIT_ZIP_SPEED_TOTAL (MB/s, a million bytes a
// second, decimals allowed). Unset or empty is the default; a value that
// isn't one is refused, naming the variable.
func FromEnv(getenv func(string) string) (Config, error) {
	c := Defaults()
	if v := strings.TrimSpace(getenv("TRUSTED_PROXY")); v != "" {
		a, err := ParseTrustedProxy(v)
		if err != nil {
			return Config{}, fmt.Errorf("TRUSTED_PROXY: %w", err)
		}
		c.TrustedProxy = a
	}
	for _, n := range []struct {
		name string
		to   *int
	}{{"LIMIT_REQUESTS", &c.Requests}, {"LIMIT_PLAYBACK", &c.Playback},
		{"LIMIT_LIVE_PER_CLIENT", &c.LivePerClient}, {"LIMIT_LIVE_TOTAL", &c.LiveTotal},
		{"LIMIT_ZIPS_PER_CLIENT", &c.ZipsPerClient}, {"LIMIT_ZIPS_TOTAL", &c.ZipsTotal}} {
		v := strings.TrimSpace(getenv(n.name))
		if v == "" {
			continue
		}
		i, err := strconv.Atoi(v)
		if err != nil || i < 1 {
			return Config{}, fmt.Errorf("%s: %q isn't a whole number above 0", n.name, v)
		}
		*n.to = i
	}
	for _, n := range []struct {
		name string
		to   *int64
	}{{"LIMIT_ZIP_SPEED", &c.ZipSpeed}, {"LIMIT_ZIP_SPEED_TOTAL", &c.ZipSpeedTotal}} {
		v := strings.TrimSpace(getenv(n.name))
		if v == "" {
			continue
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || !(f >= 0.001) || math.IsInf(f, 0) {
			return Config{}, fmt.Errorf("%s: %q isn't a speed in MB/s, like 2 or 0.5", n.name, v)
		}
		*n.to = int64(f * MB)
	}
	return c, nil
}

// ParseTrustedProxy reads TRUSTED_PROXY: one exact address, IPv4 or IPv6,
// never a range (R70).
func ParseTrustedProxy(s string) (netip.Addr, error) {
	if strings.Contains(s, "/") {
		return netip.Addr{}, fmt.Errorf("%q is a range; give the proxy's one address", s)
	}
	a, err := netip.ParseAddr(s)
	if err != nil || a.Zone() != "" {
		return netip.Addr{}, fmt.Errorf("%q isn't an IP address", s)
	}
	return a.Unmap(), nil
}

// Client is who a limit counts: one IPv4 address, or one IPv6 /64 (R70),
// in text form. It's a key in memory only; it's never logged or stored.
type Client string

// unknownClient is a request whose address can't be read (a Unix socket,
// a test's pipe): all such requests are one client.
const unknownClient Client = "unknown"

// ClientOf is the client a request counts as, given the trusted proxy (the
// zero Addr for none). A request from the trusted proxy is its last
// X-Forwarded-For address, the one the proxy wrote; the addresses before
// it are whatever the listener sent, and are never read. With no usable
// last address, or from anywhere else, it's the connecting address.
func ClientOf(r *http.Request, proxy netip.Addr) Client {
	a, ok := hostAddr(r.RemoteAddr)
	if !ok {
		return unknownClient
	}
	if proxy.IsValid() && a == proxy {
		if f, ok := lastForwarded(r.Header.Values("X-Forwarded-For")); ok {
			a = f
		}
	}
	return clientOf(a)
}

// clientOf is an address's client: itself for IPv4, its /64 for IPv6.
func clientOf(a netip.Addr) Client {
	if a.Is4() {
		return Client(a.String())
	}
	p, err := a.WithZone("").Prefix(64)
	if err != nil {
		return unknownClient
	}
	return Client(p.String())
}

// hostAddr is the address in a host:port, or a bare address, unmapped.
func hostAddr(s string) (netip.Addr, bool) {
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().Unmap(), true
	}
	if a, err := netip.ParseAddr(strings.Trim(s, "[]")); err == nil {
		return a.Unmap(), true
	}
	return netip.Addr{}, false
}

// lastForwarded is the last address in X-Forwarded-For, over all its
// header lines, with or without a port.
func lastForwarded(vs []string) (netip.Addr, bool) {
	if len(vs) == 0 {
		return netip.Addr{}, false
	}
	list := vs[len(vs)-1]
	last := strings.TrimSpace(list[strings.LastIndex(list, ",")+1:])
	return hostAddr(last)
}

// Refusal is a request refused over a limit (R73, R78): its status,
// Retry-After and body.
type Refusal struct {
	Status  int
	Code    string
	Message string
	// RetryAfter is how long until the limit would let the request in.
	RetryAfter time.Duration
}

// Seconds is RetryAfter in whole seconds, rounded up, at least 1, as
// Retry-After says it.
func (f Refusal) Seconds() int {
	return max(1, int(math.Ceil(f.RetryAfter.Seconds())))
}

// refusalBody is a refusal's body: R78's code and message, and the wait.
type refusalBody struct {
	Code       string `json:"error"`
	Message    string `json:"message"`
	RetryAfter int    `json:"retry_after"`
}

// Write answers the request with the refusal, never cached.
func (f Refusal) Write(w http.ResponseWriter) {
	b, _ := json.Marshal(refusalBody{Code: f.Code, Message: f.Message, RetryAfter: f.Seconds()})
	b = append(b, '\n')
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-store")
	h.Set("Retry-After", strconv.Itoa(f.Seconds()))
	h.Set("Content-Length", strconv.Itoa(len(b)))
	w.WriteHeader(f.Status)
	_, _ = w.Write(b)
}

// Wait says a wait in words, as a refusal's message does: "1 second", "40
// seconds", "2 minutes" (rounded up to whole minutes from a minute on).
func Wait(d time.Duration) string {
	s := max(1, int(math.Ceil(d.Seconds())))
	if s < 60 {
		return plural(s, "second")
	}
	return plural((s+59)/60, "minute")
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// Kind is which limit a public route is under (R71-R73). Every public
// route has one; the zero Kind is none, which the server refuses to mount.
type Kind int

const (
	_ Kind = iota
	// Requests is the public API's requests a minute (R73).
	Requests
	// Playback is recording playback's requests a minute (R73).
	Playback
	// Live is the listener's WebSocket (R71).
	Live
	// Zip is a zip download (R72).
	Zip
)

func (k Kind) String() string {
	switch k {
	case Requests:
		return "requests"
	case Playback:
		return "playback"
	case Live:
		return "live"
	case Zip:
		return "zip"
	}
	return "none"
}

// Limits is a deployment's limits and their counts.
type Limits struct {
	cfg      Config
	requests *window
	playback *window
	live     *slots
	zips     zips
	zipPace  *pace // all zips together
	now      func() time.Time
	// hits counts refusals per kind, for M6's metrics (R90).
	hits [Zip + 1]atomic.Int64
}

// Option changes how Limits works, for tests.
type Option func(*Limits)

// WithClock sets the clock the windows run on.
func WithClock(now func() time.Time) Option {
	return func(l *Limits) {
		l.requests.now, l.playback.now, l.now = now, now, now
	}
}

// New returns the limits cfg sets, with every count at nothing.
func New(cfg Config, opts ...Option) (*Limits, error) {
	if cfg.Requests < 1 || cfg.Playback < 1 || cfg.LivePerClient < 1 || cfg.LiveTotal < 1 ||
		cfg.ZipsPerClient < 1 || cfg.ZipsTotal < 1 || cfg.ZipSpeed < 1 || cfg.ZipSpeedTotal < 1 {
		return nil, errors.New("limits: every limit must be above 0")
	}
	l := &Limits{cfg: cfg,
		requests: newWindow(cfg.Requests, time.Minute),
		playback: newWindow(cfg.Playback, time.Minute),
		live:     newSlots(cfg.LivePerClient, cfg.LiveTotal),
		zips:     zips{by: map[Client][]*Download{}},
		zipPace:  newPace(cfg.ZipSpeedTotal),
		now:      time.Now}
	for _, o := range opts {
		o(l)
	}
	return l, nil
}

// Config is the limits in force.
func (l *Limits) Config() Config { return l.cfg }

// Client is the client r counts as.
func (l *Limits) Client(r *http.Request) Client { return ClientOf(r, l.cfg.TrustedProxy) }

// Hits is how many requests have been refused under a kind of limit since
// the start.
func (l *Limits) Hits(k Kind) int64 {
	if k < Requests || k > Zip {
		return 0
	}
	return l.hits[k].Load()
}

// Take counts one request of a kind counted a minute (Requests or
// Playback) for c, or says why it's refused. A refused request isn't
// counted.
func (l *Limits) Take(k Kind, c Client) (Refusal, bool) {
	var (
		w         *window
		code, msg string
	)
	switch k {
	case Requests:
		w, code, msg = l.requests, "too_many_requests", fmt.Sprintf("Too many requests from here: at most %d a minute.", l.cfg.Requests)
	case Playback:
		w, code, msg = l.playback, "too_many_plays", fmt.Sprintf("Too many recordings played from here: at most %d requests a minute.", l.cfg.Playback)
	default:
		return Refusal{}, true
	}
	wait, ok := w.take(c)
	if ok {
		return Refusal{}, true
	}
	l.hits[k].Add(1)
	return Refusal{Status: http.StatusTooManyRequests, Code: code,
		Message: msg + " Try again in " + Wait(wait) + ".", RetryAfter: wait}, false
}

// Hit counts a refusal of kind k made elsewhere (the live socket's, a
// zip's), for the metrics.
func (l *Limits) Hit(k Kind) {
	if k >= Requests && k <= Zip {
		l.hits[k].Add(1)
	}
}

// window counts each client's requests in a fixed window that starts at
// its first request (ADR 0036): at most max in each per.
type window struct {
	max   int
	per   time.Duration
	now   func() time.Time
	mu    sync.Mutex
	by    map[Client]*count
	swept time.Time
}

type count struct {
	start time.Time
	n     int
}

func newWindow(max int, per time.Duration) *window {
	return &window{max: max, per: per, now: time.Now, by: map[Client]*count{}}
}

// take counts one request for c, or says how long until c's window ends.
func (w *window) take(c Client) (time.Duration, bool) {
	now := w.now()
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sweep(now)
	n := w.by[c]
	if n == nil || !now.Before(n.start.Add(w.per)) {
		n = &count{start: now}
		w.by[c] = n
	}
	if n.n >= w.max {
		return n.start.Add(w.per).Sub(now), false
	}
	n.n++
	return 0, true
}

// sweep forgets the windows that have ended, once a window's length, so
// memory holds only the clients of the last minute or two.
func (w *window) sweep(now time.Time) {
	if now.Sub(w.swept) < w.per {
		return
	}
	w.swept = now
	for c, n := range w.by {
		if !now.Before(n.start.Add(w.per)) {
			delete(w.by, c)
		}
	}
}
