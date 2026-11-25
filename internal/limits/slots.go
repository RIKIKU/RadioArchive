package limits

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

// LiveRetry is how long a refused listener waits before trying again (R71:
// "retries every 30 s"), as Retry-After says.
const LiveRetry = 30 * time.Second

// slots counts what each client has open at once, and what's open in all
// (ADR 0037): listener WebSockets, and zips.
type slots struct {
	perClient, total int
	mu               sync.Mutex
	by               map[Client]int
	n                int
}

func newSlots(perClient, total int) *slots {
	return &slots{perClient: perClient, total: total, by: map[Client]int{}}
}

// room says whether c may open one more: ok, or which cap is reached.
// Called with mu held.
func (s *slots) room(c Client) (clientFull, allFull bool) {
	return s.by[c] >= s.perClient, s.n >= s.total
}

// take opens one for c if there's room, returning how to close it.
func (s *slots) take(c Client) (release func(), clientFull, allFull bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if clientFull, allFull = s.room(c); clientFull || allFull {
		return nil, clientFull, allFull
	}
	s.by[c]++
	s.n++
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.n--
			if s.by[c]--; s.by[c] <= 0 {
				delete(s.by, c)
			}
		})
	}, false, false
}

// peek says whether c could open one now, opening nothing.
func (s *slots) peek(c Client) (clientFull, allFull bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.room(c)
}

// open is how many are open in all.
func (s *slots) open() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

// liveRefusal is a listener socket refused (R71, R78): the client's own
// cap, 429; the radio's, 503, "The radio is full right now".
func (l *Limits) liveRefusal(clientFull bool) Refusal {
	if clientFull {
		return Refusal{Status: http.StatusTooManyRequests, Code: "too_many_live",
			Message: fmt.Sprintf("Too many live connections from here: at most %d at once. Try again in %s.",
				l.cfg.LivePerClient, Wait(LiveRetry)), RetryAfter: LiveRetry}
	}
	return Refusal{Status: http.StatusServiceUnavailable, Code: "radio_full",
		Message: "The radio is full right now. Try again in " + Wait(LiveRetry) + ".", RetryAfter: LiveRetry}
}

// Live opens a listener socket for c (R71), returning how to close it, or
// the refusal when c has its cap open or the radio has its total. A
// refusal is counted in Hits. Existing sockets are never closed by it.
func (l *Limits) Live(c Client) (release func(), f Refusal, ok bool) {
	release, clientFull, allFull := l.live.take(c)
	if clientFull || allFull {
		l.Hit(Live)
		return nil, l.liveRefusal(clientFull), false
	}
	return release, Refusal{}, true
}

// LiveCheck is what Live would answer for c now, opening nothing and
// counting nothing: the browser's way to learn why its socket was
// refused (ADR 0037).
func (l *Limits) LiveCheck(c Client) (Refusal, bool) {
	clientFull, allFull := l.live.peek(c)
	if clientFull || allFull {
		return l.liveRefusal(clientFull), false
	}
	return Refusal{}, true
}

// LiveOpen is how many listener sockets are open, for M6's metrics and the
// radio-full alert (R91). An admin let in over a cap isn't counted.
func (l *Limits) LiveOpen() int { return l.live.open() }
