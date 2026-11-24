package gate

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
	"github.com/RIKIKU/RadioArchive/internal/store"
	"github.com/RIKIKU/RadioArchive/internal/store/db"
)

// The live feed (SPEC.md P1, P2, R16, R23, R31, R32, R36, R37, R75, R76;
// ADR 0007). The recorder endpoints hand Live every live call's start,
// transmissions, end and frames. Live decides what listeners may have of
// each call, at every event, by reading the call's row through
// public_calls (whose one condition is call_state's, ADR 0004) and the
// catalogue, and holds the answer in memory, so a frame costs a map lookup
// and no query. It reads them again for every call it holds whenever the
// catalogue's generation moves (any talkgroup edit, by any writer), which it
// checks every tick: so hiding a talkgroup withdraws its calls within a
// tick or two.
//
// Listeners subscribe, each with a listening set. Live sends a call's
// events and frames to a subscription whose set names any talkgroup in its
// patch (R16), after the gate's decision. A subscription is a bounded
// queue: a full one drops frames (counted), and one too full for an event
// is closed, to resume by its last event ID (R37). Nothing a listener does
// can hold up a recorder or another listener.

// Server → listener message types (ADR 0007).
const (
	TypeWelcome       = "welcome"
	TypeCallStart     = "call_start"
	TypeTransmission  = "transmission"
	TypeCallEnd       = "call_end"
	TypeCallWithdrawn = "call_withdrawn"
	TypeCallDelayed   = "call_delayed"
	TypeReplay        = "replay"
	TypeListening     = "listening" // a set is taken: what follows is by it
	TypeNotice        = "notice"
)

// KindLiveFrame is a listener's binary message: one Opus frame of a call.
const KindLiveFrame = 0x01

// LiveFrameHeader is the size of a listener frame's header: kind, three
// reserved bytes, the call's ID (16 bytes, as in its text form) and the
// frame's sequence number (uint32, little-endian).
const LiveFrameHeader = 24

// Feed notices (R15, R71): the message type exists, and the notices are
// later slices'.
const (
	NoticeRecorders = "recorders" // "Some talkgroups aren't being received right now"
	NoticeRadioFull = "radio_full"
)

const (
	// ReplayWindow is how far back a resumed listener's missed events go
	// (R37).
	ReplayWindow = 5 * time.Minute
	// liveTick is how often Live checks the catalogue's generation, releases
	// delayed calls and forgets old ones. R31's deadline is 1 s.
	liveTick = 200 * time.Millisecond
	// liveIdle ends a live call nothing has been heard of for this long: its
	// recorder lost it (ADR 0002: the client ends a call's live audio after
	// 30 s without packets, and may send no call_end).
	liveIdle = time.Minute
	// recordingWait is how long past its delay a delayed call waits for its
	// recording before it's dropped from the live feed (R19 alerts at 10
	// minutes).
	recordingWait = 10 * time.Minute
	// maxRing bounds the replay buffer, whatever ReplayWindow holds.
	maxRing = 50_000
	// eventTimeout bounds the queries made at a live event.
	eventTimeout = 2 * time.Second

	// SubscriptionQueue is how many messages a listener may have waiting.
	// Frames are dropped once SubscriptionFrames are waiting, which leaves
	// room for events; a listener too far behind for an event is closed.
	SubscriptionQueue  = 1024
	SubscriptionFrames = 768
)

// LiveCounters are the live feed's totals, for metrics (R90) and tests.
type LiveCounters struct {
	FramesSent    atomic.Uint64 // frames queued for listeners
	FramesDropped atomic.Uint64 // frames a slow listener's full queue dropped
	SlowClosed    atomic.Uint64 // subscriptions closed for falling too far behind
	Withdrawn     atomic.Uint64 // calls withdrawn from listeners (R31)
	Released      atomic.Uint64 // delayed calls released (R23)
	CheckFailed   atomic.Uint64 // event or tick checks that couldn't read the database
}

type phase int

const (
	phaseBlocked   phase = iota // never sent: not public, or not yet known to be
	phaseLive                   // started for listeners
	phaseDeferred               // a delayed talkgroup's: waits for its delay (R23)
	phaseEnded                  // ended for listeners; kept for replay and withdrawal
	phaseReleased               // a delayed call, released; kept likewise
	phaseWithdrawn              // withdrawn (R31); nothing more is sent
)

// liveCall is what Live holds of one call.
type liveCall struct {
	id    string
	idRaw [16]byte
	phase phase

	// What listeners are told, as of the last check that found the call
	// public. tgs is its patch then, own talkgroup first; a hidden
	// talkgroup is never in it.
	sys     int64
	tgs     []int32
	info    callInfo
	unit    uint32
	endedAt *time.Time
	delay   int32
	size    *int64

	checkedGen int64      // the catalogue generation of the last check
	startID    int64      // its call_start's event ID
	events     []ringItem // its call_start and transmissions, for a listener who adds its talkgroup
	lastSeen   time.Time  // real time of its last event or frame
	forgetAt   time.Time  // by the gate's clock, once ended
	dueAt      time.Time  // a deferred call's release, by the gate's clock
}

type ringItem struct {
	id   int64
	at   time.Time // by the gate's clock
	call *liveCall
	msg  []byte
	// withdrawal is a call_withdrawn: it's replayed to a set that matches
	// the call as it was last public.
	withdrawal bool
}

// Live is the live feed's hub. The zero value isn't usable: the gate makes
// one (Gate.Live).
type Live struct {
	g    *Gate
	Stat LiveCounters

	mu      sync.Mutex
	calls   map[string]*liveCall
	subs    map[*Subscription]struct{}
	ring    []ringItem
	nextID  int64
	pruned  int64 // the highest event ID no longer in the ring
	gen     int64 // the catalogue generation every held call was checked at
	running atomic.Bool
}

func newLive(g *Gate) *Live {
	// Event IDs start from the clock, in microseconds, so they keep rising
	// across restarts, and an ID from before this process is told apart
	// (R37, R87).
	first := g.now().UnixMilli() * 1000
	return &Live{g: g, calls: map[string]*liveCall{}, subs: map[*Subscription]struct{}{},
		nextID: first, pruned: first}
}

// Run checks the catalogue's generation every tick, rereading every held
// call when it has moved; releases delayed calls; ends calls nothing has
// been heard of; and forgets what's past the replay window. It returns when
// ctx ends.
func (l *Live) Run(ctx context.Context) {
	l.running.Store(true)
	t := time.NewTicker(liveTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			l.tick(ctx)
		}
	}
}

// Running says whether Run is running.
func (l *Live) Running() bool { return l.running.Load() }

// look reads what listeners may have of the calls: the catalogue, then
// their rows through public_calls. The catalogue is read first, so its
// generation is never newer than the rows.
type look struct {
	cat  *Catalogue
	rows map[string]db.GatePublicCallsRow
}

func (l *Live) look(ctx context.Context, ids []string) (look, error) {
	ctx, cancel := context.WithTimeout(ctx, eventTimeout)
	defer cancel()
	cat, err := l.g.Catalogue(ctx)
	if err != nil {
		return look{}, err
	}
	us := make([]pgtype.UUID, 0, len(ids))
	for _, id := range ids {
		us = append(us, uuidOf(id))
	}
	rows, err := l.g.q.GatePublicCalls(ctx, us)
	if err != nil {
		return look{}, fmt.Errorf("live calls: %s", store.SafeError(err))
	}
	lk := look{cat: cat, rows: make(map[string]db.GatePublicCallsRow, len(rows))}
	for _, r := range rows {
		lk.rows[uuidString(r.ID)] = r
	}
	return lk, nil
}

// CallStarted is a live call's start, once its row is written. unit is the
// first unit heard, or 0.
func (l *Live) CallStarted(callID string, unit uint32) {
	id, ok := ParseCallID(callID)
	if !ok {
		return
	}
	l.mu.Lock()
	if c := l.calls[id]; c != nil {
		// Its recorder client reconnected and started it again (ADR 0002).
		c.lastSeen = time.Now()
		l.mu.Unlock()
		return
	}
	l.mu.Unlock()
	lk, err := l.look(context.Background(), []string{id})
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.calls[id] != nil {
		return
	}
	c := &liveCall{id: id, idRaw: uuidOf(id).Bytes, phase: phaseBlocked, unit: unit, lastSeen: time.Now()}
	l.calls[id] = c
	if err != nil {
		l.failed(err)
		return // fail closed: never sent
	}
	vis := l.refresh(c, lk)
	switch {
	case !vis:
	case c.delay > 0:
		c.phase = phaseDeferred
	default:
		c.phase = phaseLive
		c.startID = l.emit(c, TypeCallStart, nil)
	}
}

// Transmission is a change of unit within a live call, once it's stored.
// Its patch may have grown, so the call is checked again.
func (l *Live) Transmission(callID string, unit uint32, offsetMS int32) {
	c := l.recheck(callID)
	if c == nil {
		return
	}
	defer l.mu.Unlock()
	if c.phase != phaseLive {
		return
	}
	c.unit = unit
	l.emit(c, TypeTransmission, func(e *callEvent) { e.OffsetMS = &offsetMS })
}

// CallEnded is a live call's end, once it's stored.
func (l *Live) CallEnded(callID string) {
	c := l.recheck(callID)
	if c == nil {
		return
	}
	defer l.mu.Unlock()
	switch c.phase {
	case phaseLive:
		l.end(c)
	case phaseDeferred:
		if c.endedAt != nil {
			c.dueAt = c.endedAt.Add(time.Duration(c.delay) * time.Minute)
		}
	case phaseBlocked:
		delete(l.calls, c.id)
	}
}

// recheck reads a held call again at an event and applies it. It returns
// the call with l.mu held, for the caller to unlock, or nil when the call
// isn't held.
func (l *Live) recheck(callID string) *liveCall {
	id, ok := ParseCallID(callID)
	if !ok {
		return nil
	}
	l.mu.Lock()
	c := l.calls[id]
	if c == nil {
		l.mu.Unlock()
		return nil
	}
	c.lastSeen = time.Now()
	if c.phase == phaseWithdrawn || c.phase == phaseBlocked {
		return c // nothing more is sent: no need to look
	}
	l.mu.Unlock()
	lk, err := l.look(context.Background(), []string{id})
	l.mu.Lock()
	if err != nil {
		l.failed(err)
		l.hide(c) // fail closed
		return c
	}
	l.apply(c, lk)
	return c
}

func (l *Live) failed(err error) {
	l.Stat.CheckFailed.Add(1)
	l.g.log.Warn("live feed couldn't check a call; it isn't sent", "error", err)
}

// refresh takes what a look says of c. It reports whether c is public: its
// row is in public_calls and its talkgroup in the catalogue. Only then is
// what listeners are told of it updated, so it never names a hidden
// talkgroup.
func (l *Live) refresh(c *liveCall, lk look) bool {
	c.checkedGen = lk.cat.Generation
	r, ok := lk.rows[c.id]
	if !ok {
		return false
	}
	tg, ok := lk.cat.talkgroup(r.SystemID, r.Talkgroup)
	if !ok {
		return false
	}
	c.sys = r.SystemID
	c.tgs = append([]int32{r.Talkgroup}, r.PatchedTalkgroups...)
	c.delay = lk.cat.delayOf(r.SystemID, r.Talkgroup, r.PatchedTalkgroups)
	c.size = r.RecordingSize
	if r.EndedAt.Valid {
		t := r.EndedAt.Time.UTC()
		c.endedAt = &t
	}
	patched := r.PatchedTalkgroups
	if patched == nil {
		patched = []int32{}
	}
	c.info = callInfo{
		Talkgroup: liveTalkgroup{ID: tg.ID, Label: tg.Label, Name: tg.Name, Tag: tg.Tag, Groups: tg.Groups,
			LEDColour: nonEmpty(tg.LEDColour), Alert: nonEmpty(tg.Alert)},
		Patched:   patched,
		StartedAt: r.StartedAt.Time.UTC().Format(timeFormat),
		started:   r.StartedAt.Time.UTC(),
	}
	if len(lk.cat.Systems) > 1 {
		c.info.System = &liveSystem{ID: r.SystemID, Label: r.SystemLabel, Name: r.SystemName}
	}
	return true
}

// apply takes a later look at a held call: one no longer public, or newly
// delayed while live, is withdrawn (R31).
func (l *Live) apply(c *liveCall, lk look) {
	vis := l.refresh(c, lk)
	switch c.phase {
	case phaseLive:
		if !vis || c.delay > 0 {
			l.withdraw(c)
		}
	case phaseEnded, phaseReleased:
		if !vis {
			l.withdraw(c)
		}
	case phaseDeferred:
		if !vis {
			c.phase = phaseBlocked // nobody was told of it
		}
	}
}

// hide takes a held call out of the feed when it can't be checked.
func (l *Live) hide(c *liveCall) {
	switch c.phase {
	case phaseLive, phaseEnded, phaseReleased:
		l.withdraw(c)
	case phaseDeferred:
		c.phase = phaseBlocked
	}
}

func (l *Live) end(c *liveCall) {
	c.phase = phaseEnded
	c.forgetAt = l.g.now().Add(ReplayWindow)
	l.emit(c, TypeCallEnd, func(e *callEvent) {
		if c.endedAt != nil {
			s := c.endedAt.Format(timeFormat)
			d := c.endedAt.Sub(c.info.started).Milliseconds()
			e.EndedAt, e.DurationMS = &s, &d
		}
	})
}

// withdraw tells every listener who has c that it's withdrawn, and nothing
// else: not why, not its talkgroup (R31).
func (l *Live) withdraw(c *liveCall) {
	c.phase = phaseWithdrawn
	c.forgetAt = l.g.now().Add(ReplayWindow)
	c.events = nil
	l.Stat.Withdrawn.Add(1)
	l.nextID++
	msg, _ := json.Marshal(withdrawnEvent{Type: TypeCallWithdrawn, EventID: l.nextID, CallID: c.id})
	it := ringItem{id: l.nextID, at: l.g.now(), call: c, msg: msg, withdrawal: true}
	l.ring = append(l.ring, it)
	for s := range l.subs {
		if s.sent[c] {
			delete(s.sent, c)
			s.event(msg)
		}
	}
}

// emit sends one of c's events to the listeners who have it, or, for its
// first (a start, or a delayed call's release), to those whose set names
// its patch. It returns the event's ID.
func (l *Live) emit(c *liveCall, typ string, more func(*callEvent)) int64 {
	l.nextID++
	e := callEvent{Type: typ, EventID: l.nextID, CallID: c.id, callInfo: c.info}
	if c.unit != 0 {
		e.Unit = &liveUnit{ID: int64(c.unit), Name: strconv.FormatUint(uint64(c.unit), 10)}
	}
	if more != nil {
		more(&e)
	}
	msg, err := json.Marshal(e)
	if err != nil {
		panic(err) // plain data
	}
	it := ringItem{id: l.nextID, at: l.g.now(), call: c, msg: msg}
	l.ring = append(l.ring, it)
	first := typ == TypeCallStart || typ == TypeCallDelayed
	if typ == TypeCallStart || typ == TypeTransmission {
		c.events = append(c.events, it)
	}
	for s := range l.subs {
		switch {
		case s.sent[c]:
			s.event(msg)
		case first && s.matches(c):
			s.sent[c] = true
			s.event(msg)
		}
	}
	return it.id
}

// Frame is one of a live call's Opus frames. It costs a lookup, and a
// non-blocking send per listener who has the call.
func (l *Live) Frame(callID string, f cp.Frame) {
	l.mu.Lock()
	defer l.mu.Unlock()
	c := l.calls[callID]
	if c == nil {
		return
	}
	c.lastSeen = time.Now()
	if c.phase != phaseLive {
		return
	}
	var msg []byte
	for s := range l.subs {
		if !s.sent[c] {
			continue
		}
		if msg == nil {
			msg = make([]byte, LiveFrameHeader, LiveFrameHeader+len(f.Packet))
			msg[0] = KindLiveFrame
			copy(msg[4:20], c.idRaw[:])
			binary.LittleEndian.PutUint32(msg[20:], f.Seq)
			msg = append(msg, f.Packet...)
		}
		s.frame(msg)
	}
}

func (l *Live) tick(ctx context.Context) {
	gen, err := l.g.q.GateCatalogueGeneration(ctx)
	if err != nil {
		if ctx.Err() == nil {
			l.failed(fmt.Errorf("catalogue generation: %s", store.SafeError(err)))
		}
		return
	}
	now := l.g.now()
	l.mu.Lock()
	var stale, due []string
	for id, c := range l.calls {
		switch c.phase {
		case phaseLive, phaseDeferred, phaseEnded, phaseReleased:
			if c.checkedGen < gen {
				stale = append(stale, id)
			}
		}
		if c.phase == phaseDeferred && !c.dueAt.IsZero() && !now.Before(c.dueAt) {
			due = append(due, id)
		}
	}
	l.mu.Unlock()

	if len(stale) > 0 {
		lk, err := l.look(ctx, stale)
		if err != nil {
			l.failed(err) // tried again next tick
		} else {
			l.mu.Lock()
			for _, id := range stale {
				if c := l.calls[id]; c != nil && c.checkedGen < lk.cat.Generation {
					l.apply(c, lk)
				}
			}
			l.mu.Unlock()
		}
	}
	if len(due) > 0 {
		l.release(ctx, due)
	}
	l.prune()
}

// release sends delayed calls whose delay has passed (R23), as finished
// calls to play from their recordings, once their recordings are in.
func (l *Live) release(ctx context.Context, ids []string) {
	lk, err := l.look(ctx, ids)
	if err != nil {
		l.failed(err)
		return
	}
	now := l.g.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, id := range ids {
		c := l.calls[id]
		if c == nil || c.phase != phaseDeferred {
			continue
		}
		if !l.refresh(c, lk) || c.endedAt == nil {
			c.phase = phaseBlocked
			continue
		}
		c.dueAt = c.endedAt.Add(time.Duration(c.delay) * time.Minute)
		if now.Before(c.dueAt) { // its delay grew meanwhile
			continue
		}
		if c.size == nil {
			if now.After(c.dueAt.Add(recordingWait)) {
				c.phase = phaseBlocked // its recording never came
			}
			continue
		}
		c.phase = phaseReleased
		c.forgetAt = now.Add(ReplayWindow)
		l.Stat.Released.Add(1)
		delay, size := c.delay, *c.size
		l.emit(c, TypeCallDelayed, func(e *callEvent) {
			s := c.endedAt.Format(timeFormat)
			d := c.endedAt.Sub(c.info.started).Milliseconds()
			e.EndedAt, e.DurationMS, e.DelayMinutes = &s, &d, &delay
			e.Recording = &liveRecording{URL: "/api/v1/calls/" + c.id + "/recording", Type: "audio/ogg", Size: size}
		})
	}
}

// prune ends calls nothing has been heard of, forgets calls past the replay
// window, and drops events older than it.
func (l *Live) prune() {
	now, real := l.g.now(), time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	for id, c := range l.calls {
		idle := real.Sub(c.lastSeen) > liveIdle
		switch c.phase {
		case phaseLive:
			if idle {
				l.end(c)
			}
		case phaseBlocked:
			if idle {
				delete(l.calls, id)
			}
		case phaseDeferred:
			if idle && c.dueAt.IsZero() {
				delete(l.calls, id) // it never ended
			}
		case phaseEnded, phaseReleased, phaseWithdrawn:
			if !now.Before(c.forgetAt) {
				delete(l.calls, id)
				for s := range l.subs {
					delete(s.sent, c)
				}
			}
		}
	}
	cut := 0
	for cut < len(l.ring) && (now.Sub(l.ring[cut].at) > ReplayWindow || len(l.ring)-cut > maxRing) {
		l.pruned = l.ring[cut].id
		cut++
	}
	if cut > 0 {
		l.ring = append(l.ring[:0:0], l.ring[cut:]...)
	}
}

// Message is one message for a listener: an event (text) or a frame
// (binary).
type Message struct {
	Binary bool
	Data   []byte
}

// ListeningSet is the talkgroups a listener hears: by ID in any system, and
// by system and ID.
type ListeningSet struct {
	Any      []int32
	InSystem map[int64][]int32
}

// Subscription is one listener's feed.
type Subscription struct {
	l       *Live
	c       chan Message
	done    chan struct{}
	dropped atomic.Uint64
	welcome []byte

	// Guarded by l.mu.
	any       map[int32]bool
	inSystem  map[talkgroupKey]bool
	sent      map[*liveCall]bool // calls this listener has been sent the start of
	listening bool
	closed    bool
}

// Subscribe starts a listener's feed, with nothing in its set: it gets no
// events until Listen.
func (l *Live) Subscribe() *Subscription {
	s := &Subscription{l: l, c: make(chan Message, SubscriptionQueue), done: make(chan struct{}),
		sent: map[*liveCall]bool{}}
	l.mu.Lock()
	l.subs[s] = struct{}{}
	last := l.nextID
	l.mu.Unlock()
	s.welcome, _ = json.Marshal(welcome{Type: TypeWelcome, Protocol: LiveProtocol, LastEventID: last})
	return s
}

// Welcome is the listener's first message. Its last event ID is the
// latest event when the listener subscribed, so a listener has one to
// resume from even before its first event.
func (s *Subscription) Welcome() []byte { return s.welcome }

// Messages are the listener's messages, in order.
func (s *Subscription) Messages() <-chan Message { return s.c }

// Done is closed when the feed drops the listener for falling too far
// behind. It may resume by its last event ID.
func (s *Subscription) Done() <-chan struct{} { return s.done }

// Dropped is how many of this listener's frames its full queue dropped.
func (s *Subscription) Dropped() uint64 { return s.dropped.Load() }

// Close ends the feed.
func (s *Subscription) Close() {
	s.l.mu.Lock()
	defer s.l.mu.Unlock()
	delete(s.l.subs, s)
	s.closed = true
}

func (s *Subscription) matches(c *liveCall) bool {
	for _, tg := range c.tgs {
		if s.any[tg] || s.inSystem[talkgroupKey{c.sys, tg}] {
			return true
		}
	}
	return false
}

// event queues an event; one that doesn't fit closes the subscription.
// Called with l.mu held.
func (s *Subscription) event(msg []byte) {
	if s.closed {
		return
	}
	select {
	case s.c <- Message{Data: msg}:
	default:
		s.closed = true
		delete(s.l.subs, s)
		s.l.Stat.SlowClosed.Add(1)
		close(s.done)
	}
}

// frame queues a frame unless the queue is too full. Called with l.mu held.
func (s *Subscription) frame(msg []byte) {
	if s.closed {
		return
	}
	if len(s.c) >= SubscriptionFrames {
		s.dropped.Add(1)
		s.l.Stat.FramesDropped.Add(1)
		return
	}
	select {
	case s.c <- Message{Binary: true, Data: msg}:
		s.l.Stat.FramesSent.Add(1)
	default:
		s.dropped.Add(1)
		s.l.Stat.FramesDropped.Add(1)
	}
}

// Listen sets the listener's set; it may change at any time. The first
// time, resume (when not nil) is the last event ID the listener had: the
// events after it, from the last ReplayWindow, are sent first, then a
// replay message saying whether that's all it missed (R37). Missed frames
// are never sent again. A live call the new set names that the listener
// doesn't have is sent its start and transmissions so far; frames of calls
// it no longer names stop.
func (s *Subscription) Listen(set ListeningSet, resume *int64) {
	l := s.l
	l.mu.Lock()
	defer l.mu.Unlock()
	if s.closed {
		return
	}
	s.any = make(map[int32]bool, len(set.Any))
	for _, tg := range set.Any {
		s.any[tg] = true
	}
	s.inSystem = map[talkgroupKey]bool{}
	for sys, tgs := range set.InSystem {
		for _, tg := range tgs {
			s.inSystem[talkgroupKey{sys, tg}] = true
		}
	}
	first := !s.listening
	s.listening = true
	had := int64(-1) // events up to this one the listener has
	if first && resume != nil {
		had = *resume
		s.replay(*resume)
	}
	for c := range s.sent {
		if !s.matches(c) {
			delete(s.sent, c)
		}
	}
	for _, c := range l.calls {
		if c.phase != phaseLive || s.sent[c] || !s.matches(c) {
			continue
		}
		s.sent[c] = true
		if c.startID <= had {
			continue // it had the call before it reconnected
		}
		for _, it := range c.events {
			s.event(it.msg)
		}
	}
	s.event(listeningMsg)
}

var listeningMsg, _ = json.Marshal(struct {
	Type string `json:"type"`
}{TypeListening})

// replay sends the events after last. Called with l.mu held.
func (s *Subscription) replay(last int64) {
	l := s.l
	cutoff := l.g.now().Add(-ReplayWindow)
	for _, it := range l.ring {
		if it.id <= last || it.at.Before(cutoff) {
			continue
		}
		c := it.call
		if !s.matches(c) {
			continue
		}
		if !it.withdrawal {
			if c.phase == phaseWithdrawn || c.phase == phaseBlocked {
				continue
			}
			s.sent[c] = true
		}
		s.event(it.msg)
	}
	msg, _ := json.Marshal(replayDone{Type: TypeReplay, Complete: last >= l.pruned})
	s.event(msg)
}

// The messages (ADR 0007).

const timeFormat = "2006-01-02T15:04:05.000Z07:00"

type liveSystem struct {
	ID    int64  `json:"id"`
	Label string `json:"label"`
	Name  string `json:"name"`
}

type liveTalkgroup struct {
	ID        int32    `json:"id"`
	Label     string   `json:"label"`
	Name      string   `json:"name"`
	Tag       string   `json:"tag"`
	Groups    []string `json:"groups"`
	LEDColour *string  `json:"led_colour"`
	Alert     *string  `json:"alert"`
}

type liveUnit struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type liveRecording struct {
	URL  string `json:"url"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

// callInfo is what every call event says of its call (R36, R76).
type callInfo struct {
	System    *liveSystem   `json:"system,omitempty"` // only when there are several (R15)
	Talkgroup liveTalkgroup `json:"talkgroup"`
	Patched   []int32       `json:"patched_talkgroups"`
	StartedAt string        `json:"started_at"`
	started   time.Time
}

type callEvent struct {
	Type    string `json:"type"`
	EventID int64  `json:"event_id"`
	CallID  string `json:"call_id"`
	callInfo
	Unit         *liveUnit      `json:"unit"`
	OffsetMS     *int32         `json:"offset_ms,omitempty"`
	EndedAt      *string        `json:"ended_at,omitempty"`
	DurationMS   *int64         `json:"duration_ms,omitempty"`
	DelayMinutes *int32         `json:"delay_minutes,omitempty"`
	Recording    *liveRecording `json:"recording,omitempty"`
}

type withdrawnEvent struct {
	Type    string `json:"type"`
	EventID int64  `json:"event_id"`
	CallID  string `json:"call_id"`
}

type replayDone struct {
	Type     string `json:"type"`
	Complete bool   `json:"complete"`
}

// LiveProtocol is the listener protocol's version (ADR 0007).
const LiveProtocol = 1

type welcome struct {
	Type        string `json:"type"`
	Protocol    int    `json:"protocol"`
	LastEventID int64  `json:"last_event_id"`
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
