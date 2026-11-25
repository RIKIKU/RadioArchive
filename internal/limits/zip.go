package limits

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Zips (SPEC.md R72; ADR 0038): how many are sent at once, per client and
// in all, and how fast.

// zipChunk is the most of a zip written between two waits for the speed
// limits.
const zipChunk = 32 << 10

// zipUnknownWait is the wait a refusal gives when no zip in the way says
// how big it is.
const zipUnknownWait = time.Minute

// MB is a megabyte, as the speed settings count them: a million bytes.
const MB = 1_000_000

// pace is one speed limit (GCRA): each write reserves its bytes' time at
// perSec, after the reservations before it, and waits for its turn,
// letting a write through early by up to burst.
type pace struct {
	mu     sync.Mutex
	perSec float64
	burst  time.Duration
	next   time.Time
}

func newPace(bytesPerSec int64) *pace {
	return &pace{perSec: float64(bytesPerSec), burst: 50 * time.Millisecond}
}

// reserve takes n bytes' time and says how long to wait before sending them.
func (p *pace) reserve(now time.Time, n int) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.next.Before(now) {
		p.next = now
	}
	p.next = p.next.Add(time.Duration(float64(n) / p.perSec * float64(time.Second)))
	return p.next.Sub(now) - p.burst
}

// Download is one zip being sent: it holds its place in the counts until Done,
// and paces what's written through Writer.
type Download struct {
	l       *Limits
	c       Client
	counted bool
	own     *pace
	size    atomic.Int64 // its answer's Content-Length; 0 unknown
	sent    atomic.Int64
	once    sync.Once
}

// zips counts the zips being sent, by client and in all.
type zips struct {
	mu  sync.Mutex
	by  map[Client][]*Download
	all []*Download
}

// zipRoom says whether c may start one more: ok, or which cap is reached.
// Called with mu held.
func (l *Limits) zipRoom(c Client) (clientFull, allFull bool) {
	return len(l.zips.by[c]) >= l.cfg.ZipsPerClient, len(l.zips.all) >= l.cfg.ZipsTotal
}

// zipRefusal is a zip refused (R72, R73): 429 at the client's cap, 503 at
// the radio's, waiting until the soonest of the zips in the way should
// be done at the per-zip speed. Called with mu held.
func (l *Limits) zipRefusal(c Client, clientFull bool) Refusal {
	in := l.zips.all
	if clientFull {
		in = l.zips.by[c]
	}
	wait := time.Duration(-1)
	for _, z := range in {
		size := z.size.Load()
		if size <= 0 {
			continue
		}
		left := max(0, size-z.sent.Load())
		if d := time.Duration(float64(left) / float64(l.cfg.ZipSpeed) * float64(time.Second)); wait < 0 || d < wait {
			wait = d
		}
	}
	if wait < 0 {
		wait = zipUnknownWait
	}
	wait = max(wait, time.Second)
	if clientFull {
		return Refusal{Status: http.StatusTooManyRequests, Code: "too_many_downloads",
			Message:    fmt.Sprintf("Too many downloads at once: at most %d from here. Try again in %s.", l.cfg.ZipsPerClient, Wait(wait)),
			RetryAfter: wait}
	}
	return Refusal{Status: http.StatusServiceUnavailable, Code: "downloads_busy",
		Message: "The radio is busy with other downloads. Try again in " + Wait(wait) + ".", RetryAfter: wait}
}

// Zip starts a zip for c (R72), or says why it's refused. A refusal is
// counted in Hits.
func (l *Limits) Zip(c Client) (*Download, Refusal, bool) {
	l.zips.mu.Lock()
	defer l.zips.mu.Unlock()
	if clientFull, allFull := l.zipRoom(c); clientFull || allFull {
		l.Hit(Zip)
		return nil, l.zipRefusal(c, clientFull), false
	}
	z := &Download{l: l, c: c, counted: true, own: newPace(l.cfg.ZipSpeed)}
	l.zips.by[c] = append(l.zips.by[c], z)
	l.zips.all = append(l.zips.all, z)
	return z, Refusal{}, true
}

// ZipAnyway starts a zip that isn't counted, for an admin let in over a
// cap (R70). It's paced as every zip is.
func (l *Limits) ZipAnyway(c Client) *Download {
	return &Download{l: l, c: c, own: newPace(l.cfg.ZipSpeed)}
}

// ZipsOpen is how many counted zips are being sent.
func (l *Limits) ZipsOpen() int {
	l.zips.mu.Lock()
	defer l.zips.mu.Unlock()
	return len(l.zips.all)
}

func without(zs []*Download, z *Download) []*Download {
	for i, x := range zs {
		if x == z {
			return append(zs[:i:i], zs[i+1:]...)
		}
	}
	return zs
}

// Done ends the zip's place in the counts. It may be called more than once.
func (z *Download) Done() {
	z.once.Do(func() {
		if !z.counted {
			return
		}
		l := z.l
		l.zips.mu.Lock()
		defer l.zips.mu.Unlock()
		if l.zips.by[z.c] = without(l.zips.by[z.c], z); len(l.zips.by[z.c]) == 0 {
			delete(l.zips.by, z.c)
		}
		l.zips.all = without(l.zips.all, z)
	})
}

// Writer is w, writing the zip's answer at most at the per-zip speed and,
// with every other zip, at most at the total (R72). A write waits its
// turn, and gives up when ctx ends (the listener has gone).
func (z *Download) Writer(ctx context.Context, w http.ResponseWriter) http.ResponseWriter {
	return &pacedWriter{ResponseWriter: w, z: z, ctx: ctx}
}

type pacedWriter struct {
	http.ResponseWriter
	z   *Download
	ctx context.Context
}

// Unwrap lets http.ResponseController reach the connection.
func (p *pacedWriter) Unwrap() http.ResponseWriter { return p.ResponseWriter }

func (p *pacedWriter) WriteHeader(code int) {
	if n, err := strconv.ParseInt(p.Header().Get("Content-Length"), 10, 64); err == nil {
		p.z.size.Store(n)
	}
	p.ResponseWriter.WriteHeader(code)
}

func (p *pacedWriter) Write(b []byte) (int, error) {
	written := 0
	for len(b) > 0 {
		n := min(len(b), zipChunk)
		now := p.z.l.now()
		wait := max(p.z.own.reserve(now, n), p.z.l.zipPace.reserve(now, n))
		if wait > 0 {
			t := time.NewTimer(wait)
			select {
			case <-p.ctx.Done():
				t.Stop()
				return written, p.ctx.Err()
			case <-t.C:
			}
		}
		m, err := p.ResponseWriter.Write(b[:n])
		written += m
		p.z.sent.Add(int64(m))
		if err != nil {
			return written, err
		}
		b = b[n:]
	}
	return written, nil
}
