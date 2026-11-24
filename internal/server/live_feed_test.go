package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
	"github.com/RIKIKU/RadioArchive/internal/gate"
	"github.com/RIKIKU/RadioArchive/internal/public"
	"github.com/RIKIKU/RadioArchive/internal/server"
)

// The live feed to listeners (SPEC.md R16, R23, R31, R36, R37; A4, A5,
// A11, A14; ADR 0007), through the recorder's socket, the gate's live feed
// and the listener's socket.

// liveMsg is any message a listener receives.
type liveMsg struct {
	Binary bool
	Raw    []byte
	At     time.Time // when the test read it

	Type        string `json:"type"`
	EventID     int64  `json:"event_id"`
	LastEventID int64  `json:"last_event_id"`
	CallID      string `json:"call_id"`
	Complete    bool   `json:"complete"`
	Talkgroup   struct {
		ID     int32    `json:"id"`
		Label  string   `json:"label"`
		Name   string   `json:"name"`
		Tag    string   `json:"tag"`
		Groups []string `json:"groups"`
	} `json:"talkgroup"`
	Patched []int32 `json:"patched_talkgroups"`
	Unit    *struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"unit"`
	System       *json.RawMessage `json:"system"`
	StartedAt    string           `json:"started_at"`
	EndedAt      *string          `json:"ended_at"`
	OffsetMS     *int32           `json:"offset_ms"`
	DelayMinutes *int32           `json:"delay_minutes"`
	Recording    *struct {
		URL string `json:"url"`
	} `json:"recording"`

	// A frame's call, sequence number and packet.
	Seq    uint32
	Packet []byte
}

// listener is one listener's socket, read into a list as messages arrive.
type listener struct {
	t    *testing.T
	conn *websocket.Conn

	mu      sync.Mutex
	msgs    []liveMsg
	closed  error
	welcome liveMsg
}

func (e *env) liveURL() string {
	return "ws" + strings.TrimPrefix(e.srv.URL, "http") + public.LivePath
}

// listen opens a listener's socket and sends its set (and, if given, the
// last event ID it had).
func (e *env) listen(t *testing.T, set []any, resume *int64) *listener {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, e.liveURL(), nil)
	if err != nil {
		t.Fatal(err)
	}
	c.SetReadLimit(1 << 20)
	l := &listener{t: t, conn: c}
	t.Cleanup(func() { c.CloseNow() })
	_, b, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &l.welcome); err != nil || l.welcome.Type != gate.TypeWelcome {
		t.Fatalf("welcome = %s", b)
	}
	go l.read()
	if set != nil {
		l.set(set, resume)
	}
	return l
}

func (l *listener) closedWith() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closed
}

// set sends a set and waits for the server to say it's listening by it.
func (l *listener) set(set []any, resume *int64) {
	l.t.Helper()
	m := map[string]any{"type": "set", "talkgroups": set}
	if resume != nil {
		m["last_event_id"] = *resume
	}
	b, _ := json.Marshal(m)
	acks := l.count(gate.TypeListening)
	if err := l.conn.Write(context.Background(), websocket.MessageText, b); err != nil {
		l.t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for l.count(gate.TypeListening) == acks {
		if time.Now().After(deadline) {
			l.t.Fatalf("the server never took the set; got %s", l.summary())
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (l *listener) count(typ string) int {
	n := 0
	for _, m := range l.all() {
		if !m.Binary && m.Type == typ {
			n++
		}
	}
	return n
}

var (
	eventIDs   = regexp.MustCompile(`"(last_)?event_id":\d+`)
	callIDs    = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	timestamps = regexp.MustCompile(`\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z`)
)

// scrub blanks what in a message is random or a time, which may happen to
// contain "1003".
func scrub(b []byte) string {
	s := eventIDs.ReplaceAllString(string(b), `"event_id":N`)
	s = callIDs.ReplaceAllString(s, "<call>")
	return timestamps.ReplaceAllString(s, "<time>")
}

func (l *listener) read() {
	for {
		typ, b, err := l.conn.Read(context.Background())
		if err != nil {
			l.mu.Lock()
			l.closed = err
			l.mu.Unlock()
			return
		}
		m := liveMsg{Raw: b, At: time.Now(), Binary: typ == websocket.MessageBinary}
		if m.Binary {
			if len(b) >= gate.LiveFrameHeader && b[0] == gate.KindLiveFrame {
				m.CallID = fmt.Sprintf("%x-%x-%x-%x-%x", b[4:8], b[8:10], b[10:12], b[12:14], b[14:20])
				m.Seq = uint32(b[20]) | uint32(b[21])<<8 | uint32(b[22])<<16 | uint32(b[23])<<24
				m.Packet = b[gate.LiveFrameHeader:]
			}
		} else {
			_ = json.Unmarshal(b, &m)
		}
		l.mu.Lock()
		l.msgs = append(l.msgs, m)
		l.mu.Unlock()
	}
}

func (l *listener) all() []liveMsg {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.msgs)
}

// wait waits for a message ok accepts and returns it.
func (l *listener) wait(what string, ok func(liveMsg) bool) liveMsg {
	l.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		for _, m := range l.all() {
			if ok(m) {
				return m
			}
		}
		if time.Now().After(deadline) {
			l.t.Fatalf("listener never got %s; got %s", what, l.summary())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (l *listener) summary() string {
	var b strings.Builder
	for _, m := range l.all() {
		if m.Binary {
			fmt.Fprintf(&b, "\n  frame %s #%d", m.CallID, m.Seq)
		} else {
			fmt.Fprintf(&b, "\n  %s", m.Raw)
		}
	}
	return b.String()
}

// of is what the listener got of one call: events by type, and frames.
func (l *listener) of(callID string) (events []string, frames int) {
	for _, m := range l.all() {
		if m.CallID != callID {
			continue
		}
		if m.Binary {
			frames++
		} else {
			events = append(events, m.Type)
		}
	}
	return events, frames
}

func isEvent(typ, callID string) func(liveMsg) bool {
	return func(m liveMsg) bool { return !m.Binary && m.Type == typ && m.CallID == callID }
}

// liveCall plays one call on the recorder's socket, as the client sends it.
type liveRec struct {
	e    *env
	t    *testing.T
	name string
	key  string
	c    *websocket.Conn
	n    int64
}

func (e *env) liveRecorder(t *testing.T) *liveRec {
	t.Helper()
	name, key := e.recorder(t)
	return &liveRec{e: e, t: t, name: name, key: key, c: e.open(t, key), n: 5000}
}

// start starts a call on talkgroup tg patched with patched, and returns
// its identity and the call ID the server gave it.
func (r *liveRec) start(tg int32, patched []int32, unit uint32, startMS int64) (cp.CallID, string) {
	r.t.Helper()
	r.n++
	id := cp.CallID{SysNum: 0, CallNum: r.n, StartTime: startMS / 1000}
	if patched == nil {
		patched = []int32{}
	}
	send(r.t, r.c, cp.CallStart{Type: "call_start", CallID: id, ShortName: "ex", Talkgroup: tg, Unit: unit,
		PatchedTalkgroups: patched, StartTimeMS: startMS, FirstPCMMS: startMS + 100})
	row := r.e.waitCall(r.t, r.name, id, func(callRow) bool { return true })
	return id, row.id
}

// refused starts a call the server refuses, as on an ignored talkgroup,
// and waits for the refusal to be counted.
func (r *liveRec) refused(tg int32, startMS int64) cp.CallID {
	r.t.Helper()
	r.n++
	id := cp.CallID{SysNum: 0, CallNum: r.n, StartTime: startMS / 1000}
	n := r.e.rs.Stat.RejectedCount("ignored_talkgroup")
	send(r.t, r.c, cp.CallStart{Type: "call_start", CallID: id, ShortName: "ex", Talkgroup: tg, Unit: 5001,
		PatchedTalkgroups: []int32{}, StartTimeMS: startMS, FirstPCMMS: startMS + 100})
	deadline := time.Now().Add(10 * time.Second)
	for r.e.rs.Stat.RejectedCount("ignored_talkgroup") == n {
		if time.Now().After(deadline) {
			r.t.Fatal("the call was never refused")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return id
}

// frames sends n frames of call id, from seq, each packet filled with fill.
func (r *liveRec) frames(id cp.CallID, seq uint32, n int, fill byte) {
	r.t.Helper()
	for i := range n {
		send(r.t, r.c, cp.AppendFrame(nil, cp.Frame{CallID: id, Seq: seq + uint32(i), Packet: bytes.Repeat([]byte{fill}, 40)}))
	}
}

func (r *liveRec) transmission(id cp.CallID, unit uint32, offsetMS int64, patched []int32) {
	r.t.Helper()
	send(r.t, r.c, cp.Transmission{Type: "transmission", CallID: id, Unit: unit, OffsetMS: offsetMS, PatchedTalkgroups: patched})
}

func (r *liveRec) end(id cp.CallID, stopMS int64) {
	r.t.Helper()
	send(r.t, r.c, cp.CallEnd{Type: "call_end", CallID: id, StopTimeMS: stopMS, PatchedTalkgroups: []int32{}})
}

// quiet waits long enough for anything still on its way to a listener to
// arrive. Each listener has a barrier of its own where it can (a call on a
// talkgroup it names); this covers listeners that name nothing public.
func quiet() { time.Sleep(400 * time.Millisecond) }

// A5: A with {1001} gets only 1001's events and frames, B with {1002} only
// 1002's. Each event names its talkgroup, group and unit (R36, R76).
func TestIntegrationLiveA5(t *testing.T) {
	e := newEnv(t)
	rec := e.liveRecorder(t)
	a := e.listen(t, []any{1001}, nil)
	b := e.listen(t, []any{map[string]any{"id": 1002}}, nil) // as the browser sends it
	now := time.Now().UnixMilli()

	x, xid := rec.start(1001, nil, 5001, now)
	rec.frames(x, 0, 5, 'x')
	rec.transmission(x, 5123, 1500, nil)
	rec.end(x, now+3000)
	y, yid := rec.start(1002, nil, 5150, now+100)
	rec.frames(y, 0, 7, 'y')
	rec.end(y, now+4000)

	a.wait("1001's end", isEvent(gate.TypeCallEnd, xid))
	b.wait("1002's end", isEvent(gate.TypeCallEnd, yid))
	quiet()
	for _, c := range []struct {
		l          *listener
		own, other string
		events     []string
		frames     int
	}{
		{a, xid, yid, []string{"call_start", "transmission", "call_end"}, 5},
		{b, yid, xid, []string{"call_start", "call_end"}, 7},
	} {
		ev, fr := c.l.of(c.own)
		if !slices.Equal(ev, c.events) || fr != c.frames {
			t.Errorf("own call: events %v, %d frames; want %v, %d", ev, fr, c.events, c.frames)
		}
		if ev, fr := c.l.of(c.other); len(ev) != 0 || fr != 0 {
			t.Errorf("the other set's call reached it: %v, %d frames", ev, fr)
		}
	}
	start := a.wait("1001's start", isEvent(gate.TypeCallStart, xid))
	if start.Talkgroup.ID != 1001 || start.Talkgroup.Name != "Dispatch North" || start.Talkgroup.Label != "DISP N" ||
		!slices.Equal(start.Talkgroup.Groups, []string{"North"}) || start.Unit == nil || start.Unit.ID != 5001 ||
		start.Unit.Name != "5001" || start.StartedAt == "" || start.EventID <= a.welcome.LastEventID || start.System != nil {
		t.Errorf("call_start = %s", start.Raw)
	}
	tx := a.wait("1001's transmission", isEvent(gate.TypeTransmission, xid))
	if tx.Unit == nil || tx.Unit.ID != 5123 || tx.OffsetMS == nil || *tx.OffsetMS != 1500 || tx.EventID <= start.EventID {
		t.Errorf("transmission = %s", tx.Raw)
	}
	end := a.wait("1001's end", isEvent(gate.TypeCallEnd, xid))
	if end.EndedAt == nil || end.Talkgroup.Name != "Dispatch North" {
		t.Errorf("call_end = %s", end.Raw)
	}
}

// A4, the listener half (R16): A with {1002} and B with {1001} each get the
// call on 1001 patched with 1002 once; a call on 1001 patched with 1003
// (hidden) reaches no listener, not even one naming 1003.
func TestIntegrationLiveA4(t *testing.T) {
	e := newEnv(t)
	rec := e.liveRecorder(t)
	a := e.listen(t, []any{1002}, nil)
	b := e.listen(t, []any{1001}, nil)
	c := e.listen(t, []any{1003, 1001, 1002}, nil)
	now := time.Now().UnixMilli()

	h, hid := rec.start(1001, []int32{1003}, 5001, now)
	rec.frames(h, 0, 5, 'h')
	rec.end(h, now+1000)
	p, pid := rec.start(1001, []int32{1002}, 5001, now+2000)
	rec.frames(p, 0, 5, 'p')
	rec.end(p, now+3000)

	for _, l := range []*listener{a, b, c} {
		l.wait("the patched call's end", isEvent(gate.TypeCallEnd, pid))
	}
	for name, l := range map[string]*listener{"A": a, "B": b, "C": c} {
		ev, fr := l.of(pid)
		if !slices.Equal(ev, []string{"call_start", "call_end"}) || fr != 5 {
			t.Errorf("%s got the call patched with 1002 as %v, %d frames; want it once", name, ev, fr)
		}
		if ev, fr := l.of(hid); len(ev) != 0 || fr != 0 {
			t.Errorf("%s got the call patched with hidden 1003: %v, %d frames", name, ev, fr)
		}
		for _, m := range l.all() {
			if m.Binary && bytes.Contains(m.Packet, []byte("hhhh")) {
				t.Errorf("%s got the hidden call's audio", name)
			}
		}
	}
	st := a.wait("the patched call's start", isEvent(gate.TypeCallStart, pid))
	if st.Talkgroup.ID != 1001 || !slices.Equal(st.Patched, []int32{1002}) {
		t.Errorf("call_start = %s", st.Raw)
	}
}

// A11, the server half (R31): with a listener hearing a live call on 1001
// and a second queued, hiding 1001 withdraws both within 1 s, stops the
// first's frames, and the call details API no longer returns either;
// making it public again returns them. The withdrawal says nothing but the
// call's ID.
func TestIntegrationLiveA11(t *testing.T) {
	e := newEnv(t)
	rec := e.liveRecorder(t)
	l := e.listen(t, []any{1001, 1002}, nil)
	now := time.Now().UnixMilli()

	one, oneID := rec.start(1001, nil, 5001, now)
	rec.frames(one, 0, 5, '1')
	two, twoID := rec.start(1001, nil, 5002, now+500)
	rec.frames(two, 0, 2, '2')
	l.wait("the second call's start", isEvent(gate.TypeCallStart, twoID))
	l.wait("frames", func(m liveMsg) bool { return m.Binary && m.CallID == twoID && m.Seq == 1 })

	ids := fmt.Sprintf(`{"ids":[%q,%q]}`, oneID, twoID)
	if n := len(e.callDetails(t, ids)); n != 2 {
		t.Fatalf("call details lists %d calls before hiding, want 2", n)
	}

	hidden := time.Now()
	e.mustAdmin(t, "PATCH", fmt.Sprintf("/systems/%d/talkgroups/1001", e.sys), map[string]any{"state": "hidden"}, 200)
	for _, id := range []string{oneID, twoID} {
		w := l.wait("the withdrawal", isEvent(gate.TypeCallWithdrawn, id))
		if d := w.At.Sub(hidden); d > time.Second {
			t.Errorf("call %s withdrawn %v after hiding, want within 1 s", id, d)
		}
		var keys map[string]any
		_ = json.Unmarshal(w.Raw, &keys)
		if len(keys) != 3 {
			t.Errorf("the withdrawal says more than the call ending: %s", w.Raw)
		}
	}

	// Its frames stop: more of them, then a call on 1002 as a barrier.
	rec.frames(one, 5, 20, '1')
	rec.transmission(one, 5003, 2000, nil)
	rec.end(two, now+3000)
	bar, barID := rec.start(1002, nil, 5001, now+4000)
	l.wait("the barrier", isEvent(gate.TypeCallStart, barID))
	rec.end(bar, now+5000)
	for _, m := range l.all() {
		if m.Binary && m.CallID == oneID && m.Seq >= 5 {
			t.Errorf("frame %d of the hidden call reached the listener", m.Seq)
		}
	}
	for _, id := range []string{oneID, twoID} {
		ev, _ := l.of(id)
		if !slices.Equal(ev, []string{"call_start", "call_withdrawn"}) {
			t.Errorf("call %s: events %v after withdrawal", id, ev)
		}
	}
	if n := len(e.callDetails(t, ids)); n != 0 {
		t.Errorf("call details lists %d of hidden 1001's calls, want 0", n)
	}

	e.mustAdmin(t, "PATCH", fmt.Sprintf("/systems/%d/talkgroups/1001", e.sys), map[string]any{"state": "public"}, 200)
	if n := len(e.callDetails(t, ids)); n != 2 {
		t.Errorf("call details lists %d calls once 1001 is public again, want 2", n)
	}
	// A withdrawn call stays withdrawn from the live feed.
	rec.frames(one, 25, 5, '1')
	rec.end(one, now+6000)
	quiet()
	if ev, _ := l.of(oneID); !slices.Equal(ev, []string{"call_start", "call_withdrawn"}) {
		t.Errorf("after 1001 was made public again, the withdrawn call got %v", ev)
	}
}

// A patch that grows into a hidden talkgroup mid-call (R16, R31) withdraws
// the call at that event, before any later frame; and an ignored one too.
func TestIntegrationLivePatchGrows(t *testing.T) {
	e := newEnv(t)
	rec := e.liveRecorder(t)
	l := e.listen(t, []any{1001, 1002, 1003}, nil)
	now := time.Now().UnixMilli()
	for _, into := range []int32{1003, 1004} {
		c, cid := rec.start(1001, nil, 5001, now)
		rec.frames(c, 0, 3, 'g')
		rec.transmission(c, 5002, 1000, []int32{into})
		rec.frames(c, 3, 10, 'G')
		rec.end(c, now+2000)
		l.wait("the withdrawal", isEvent(gate.TypeCallWithdrawn, cid))
		bar, barID := rec.start(1002, nil, 5001, now+3000)
		l.wait("the barrier", isEvent(gate.TypeCallStart, barID))
		rec.end(bar, now+4000)
		ev, fr := l.of(cid)
		if !slices.Equal(ev, []string{"call_start", "call_withdrawn"}) || fr != 3 {
			t.Errorf("patched into %d: %v, %d frames; want the start, 3 frames, the withdrawal", into, ev, fr)
		}
		now += 10_000
	}
	for _, m := range l.all() {
		if !m.Binary && strings.Contains(scrub(m.Raw), "1003") {
			t.Errorf("a message names 1003: %s", m.Raw)
		}
		if m.Binary && bytes.Contains(m.Packet, []byte("GGG")) {
			t.Error("audio after the patch grew reached the listener")
		}
	}
}

// A14, the server half (R23): with 1001 delayed 5 minutes and a call ending
// at 12:00, no listener gets anything of it before 12:05, and the call
// details API doesn't list it; at 12:05 a listener with {1001} gets it as a
// finished call, delayed 5 min, to play from its recording.
func TestIntegrationLiveA14(t *testing.T) {
	e := newEnv(t)
	rec := e.liveRecorder(t)
	noon := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	e.clock.set(noon)
	e.mustAdmin(t, "PATCH", fmt.Sprintf("/systems/%d/talkgroups/1001", e.sys), map[string]any{"delay_minutes": 5}, 200)
	l := e.listen(t, []any{1001, 1002}, nil)
	other := e.listen(t, []any{1001}, nil)

	startMS := noon.Add(-20 * time.Second).UnixMilli()
	d, did := rec.start(1001, nil, 5001, startMS)
	rec.frames(d, 0, 10, 'd')
	rec.transmission(d, 5002, 5000, nil)
	rec.end(d, noon.UnixMilli())
	audio := oggBytes('d')
	m := recording(d, audio)
	m.PatchedTalkgroups, m.StartTimeMS, m.StopTimeMS = []int32{}, startMS, noon.UnixMilli()
	m.Transmissions = []cp.RecordingTransmission{{Unit: 5001, OffsetMS: 0, DurationMS: 5000}, {Unit: 5002, OffsetMS: 5000, DurationMS: 15000}}
	if code, resp := e.upload(t, rec.key, m, audio); code != 201 && code != 200 {
		t.Fatalf("upload: %d %+v", code, resp)
	}

	barrier := func(at time.Time) {
		t.Helper()
		b, bid := rec.start(1002, nil, 5001, at.UnixMilli())
		l.wait("the barrier", isEvent(gate.TypeCallStart, bid))
		rec.end(b, at.UnixMilli()+1000)
	}
	ids := fmt.Sprintf(`{"ids":[%q]}`, did)
	for _, at := range []time.Time{noon, noon.Add(4*time.Minute + 59*time.Second)} {
		e.clock.set(at)
		time.Sleep(3 * 200 * time.Millisecond) // three of the feed's ticks
		barrier(at)
		for name, x := range map[string]*listener{"{1001, 1002}": l, "{1001}": other} {
			if ev, fr := x.of(did); len(ev) != 0 || fr != 0 {
				t.Errorf("at %s, %s got the delayed call: %v, %d frames", at.Format("15:04:05"), name, ev, fr)
			}
		}
		if n := len(e.callDetails(t, ids)); n != 0 {
			t.Errorf("at %s, the call details API lists the delayed call", at.Format("15:04:05"))
		}
		if code, _ := e.get(t, public.RecordingURL(did)); code != 404 {
			t.Errorf("at %s, its recording answers %d, want 404", at.Format("15:04:05"), code)
		}
	}

	e.clock.set(noon.Add(5 * time.Minute))
	for name, x := range map[string]*listener{"{1001, 1002}": l, "{1001}": other} {
		got := x.wait("the delayed call", isEvent(gate.TypeCallDelayed, did))
		if got.DelayMinutes == nil || *got.DelayMinutes != 5 || got.Recording == nil ||
			got.Recording.URL != public.RecordingURL(did) || got.EndedAt == nil || got.Talkgroup.Name != "Dispatch North" {
			t.Errorf("%s: call_delayed = %s", name, got.Raw)
		}
		if ev, fr := x.of(did); !slices.Equal(ev, []string{"call_delayed"}) || fr != 0 {
			t.Errorf("%s got %v and %d frames of the delayed call, want only call_delayed", name, ev, fr)
		}
	}
	if n := len(e.callDetails(t, ids)); n != 1 {
		t.Error("at 12:05, the call details API doesn't list the call")
	}
	if code, body := e.get(t, public.RecordingURL(did)); code != 200 || !bytes.Equal(body, audio) {
		t.Errorf("at 12:05, its recording answers %d", code)
	}
}

// restartLive starts the gate and its live feed afresh on the same
// database and clock, as a server restart does, with only the public API in
// front of it: what the old feed held in memory is gone.
func (e *env) restartLive(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	log := slog.New(slog.DiscardHandler)
	g := gate.New(e.st, e.dir, log, gate.WithClock(e.clock.now))
	go g.Live().Run(ctx)
	srv := httptest.NewServer(server.New(e.st, log, server.WithPublic(public.New(g, log))))
	t.Cleanup(func() {
		srv.Close()
		cancel()
	})
	e.gate, e.live, e.srv = g, g.Live(), srv
}

// Issue #67 (R23, R37): a delayed call's release still pending at a
// restart is sent as if nothing had happened, through the gate. With 1001
// and 1002 delayed 5 minutes, calls on each ending at 12:00, 1002 hidden and
// the server restarted at 12:02, a listener with {1001, 1002} gets only
// 1001's call, and only at 12:05.
func TestIntegrationLiveDelayedRestart(t *testing.T) {
	e := newEnv(t)
	rec := e.liveRecorder(t)
	noon := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	e.clock.set(noon)
	for _, tg := range []int{1001, 1002} {
		e.mustAdmin(t, "PATCH", fmt.Sprintf("/systems/%d/talkgroups/%d", e.sys, tg), map[string]any{"delay_minutes": 5}, 200)
	}
	startMS := noon.Add(-20 * time.Second).UnixMilli()
	call := func(tg int32, fill byte) string {
		t.Helper()
		id, callID := rec.start(tg, nil, 5001, startMS)
		rec.frames(id, 0, 10, fill)
		rec.end(id, noon.UnixMilli())
		audio := oggBytes(fill)
		m := recording(id, audio)
		m.Talkgroup, m.PatchedTalkgroups, m.StartTimeMS, m.StopTimeMS = tg, []int32{}, startMS, noon.UnixMilli()
		m.Transmissions = []cp.RecordingTransmission{{Unit: 5001, OffsetMS: 0, DurationMS: 20000}}
		if code, resp := e.upload(t, rec.key, m, audio); code != 201 && code != 200 {
			t.Fatalf("upload: %d %+v", code, resp)
		}
		return callID
	}
	shown, hidden := call(1001, 'a'), call(1002, 'b')

	e.clock.set(noon.Add(2 * time.Minute))
	e.mustAdmin(t, "PATCH", fmt.Sprintf("/systems/%d/talkgroups/1002", e.sys), map[string]any{"state": "hidden"}, 200)
	e.restartLive(t)
	deadline := time.Now().Add(10 * time.Second)
	for e.live.Stat.Recovered.Load() < 1 {
		if time.Now().After(deadline) {
			t.Fatal("the restarted feed never recovered the pending call")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if n := e.live.Stat.Recovered.Load(); n != 1 {
		t.Errorf("recovered %d calls, want 1 (the hidden talkgroup's stays out)", n)
	}
	l := e.listen(t, []any{1001, 1002}, nil)

	e.clock.set(noon.Add(4*time.Minute + 59*time.Second))
	time.Sleep(3 * 200 * time.Millisecond) // three of the feed's ticks
	if ev, _ := l.of(shown); len(ev) != 0 {
		t.Errorf("at 12:04:59 the listener got the delayed call: %v", ev)
	}

	e.clock.set(noon.Add(5 * time.Minute))
	got := l.wait("the delayed call", isEvent(gate.TypeCallDelayed, shown))
	if got.DelayMinutes == nil || *got.DelayMinutes != 5 || got.Recording == nil ||
		got.Recording.URL != public.RecordingURL(shown) || got.EndedAt == nil || got.Talkgroup.Name != "Dispatch North" {
		t.Errorf("call_delayed = %s", got.Raw)
	}
	time.Sleep(3 * 200 * time.Millisecond)
	if ev, fr := l.of(hidden); len(ev) != 0 || fr != 0 {
		t.Errorf("the hidden talkgroup's call reached the listener: %v, %d frames", ev, fr)
	}
	for _, m := range l.all() {
		if bytes.Contains(m.Raw, []byte(hidden)) {
			t.Errorf("a message names the hidden call: %s", m.Raw)
		}
	}
}

// R37: reconnecting with the last event ID replays the events missed in
// the last 5 minutes (not its frames, nor another set's events), and none
// older.
func TestIntegrationLiveResume(t *testing.T) {
	e := newEnv(t)
	rec := e.liveRecorder(t)
	l := e.listen(t, []any{1001}, nil)
	now := time.Now().UnixMilli()
	one, oneID := rec.start(1001, nil, 5001, now)
	last := l.wait("the start", isEvent(gate.TypeCallStart, oneID)).EventID
	l.conn.CloseNow()

	rec.frames(one, 0, 5, '1')
	rec.transmission(one, 5002, 1000, nil)
	rec.end(one, now+2000)
	two, twoID := rec.start(1001, nil, 5003, now+3000)
	rec.end(two, now+4000)
	three, threeID := rec.start(1002, nil, 5004, now+5000)
	rec.end(three, now+6000)
	time.Sleep(100 * time.Millisecond)

	back := e.listen(t, []any{1001}, &last)
	done := back.wait("the end of the replay", func(m liveMsg) bool { return m.Type == gate.TypeReplay })
	if !done.Complete {
		t.Errorf("replay = %s, want complete", done.Raw)
	}
	var got []string
	for _, m := range back.all() {
		if m.Binary {
			t.Errorf("a missed frame was replayed: %s #%d", m.CallID, m.Seq)
			continue
		}
		if m.Type == gate.TypeReplay {
			break
		}
		if m.EventID <= last {
			t.Errorf("event %d, from before the last one it had, was replayed: %s", m.EventID, m.Raw)
		}
		got = append(got, m.Type+" "+map[string]string{oneID: "one", twoID: "two", threeID: "three"}[m.CallID])
	}
	if want := []string{"transmission one", "call_end one", "call_start two", "call_end two"}; !slices.Equal(got, want) {
		t.Errorf("replayed %v, want %v", got, want)
	}

	// Six minutes on, they're older than the window: nothing is replayed,
	// and the replay says so.
	e.clock.set(time.Now().Add(6 * time.Minute))
	time.Sleep(3 * 200 * time.Millisecond)
	late := e.listen(t, []any{1001}, &last)
	done = late.wait("the end of the replay", func(m liveMsg) bool { return m.Type == gate.TypeReplay })
	if done.Complete {
		t.Errorf("replay after 6 minutes = %s, want incomplete", done.Raw)
	}
	for _, m := range late.all() {
		if m.Type != gate.TypeReplay && m.Type != gate.TypeListening {
			t.Errorf("an event older than 5 minutes was replayed: %s", m.Raw)
		}
	}
	// A last event ID from before this server started can't be resumed.
	old := int64(1)
	if m := e.listen(t, []any{1001}, &old).wait("the replay", func(m liveMsg) bool { return m.Type == gate.TypeReplay }); m.Complete {
		t.Errorf("a resume from before the server started = %s, want incomplete", m.Raw)
	}
}

// A listener that adds a talkgroup mid-call is sent the call so far, from
// then on; one that drops it stops getting its frames.
func TestIntegrationLiveSetChange(t *testing.T) {
	e := newEnv(t)
	rec := e.liveRecorder(t)
	l := e.listen(t, []any{1002}, nil)
	now := time.Now().UnixMilli()
	c, cid := rec.start(1001, nil, 5001, now)
	rec.transmission(c, 5002, 500, nil)
	rec.frames(c, 0, 3, 'a')
	time.Sleep(100 * time.Millisecond)
	l.set([]any{1002, map[string]any{"system": e.sys, "id": 1001}}, nil)
	l.wait("the start", isEvent(gate.TypeCallStart, cid))
	l.wait("the transmission", isEvent(gate.TypeTransmission, cid))
	rec.frames(c, 3, 3, 'b')
	l.wait("a frame", func(m liveMsg) bool { return m.Binary && m.CallID == cid && m.Seq == 5 })
	if _, fr := l.of(cid); fr != 3 {
		t.Errorf("%d frames, want the 3 sent after it added 1001", fr)
	}
	l.set([]any{1002}, nil)
	time.Sleep(100 * time.Millisecond)
	rec.frames(c, 6, 3, 'c')
	rec.end(c, now+2000)
	bar, barID := rec.start(1002, nil, 5001, now+3000)
	l.wait("the barrier", isEvent(gate.TypeCallStart, barID))
	rec.end(bar, now+4000)
	if ev, fr := l.of(cid); fr != 3 || slices.Contains(ev, "call_end") {
		t.Errorf("after dropping 1001: %v, %d frames", ev, fr)
	}
	// Another system's 1001 isn't this one's.
	l.set([]any{map[string]any{"system": e.sys + 1, "id": 1001}}, nil)
	d, did := rec.start(1001, nil, 5001, now+5000)
	rec.end(d, now+6000)
	quiet()
	if ev, _ := l.of(did); len(ev) != 0 {
		t.Errorf("a set naming another system's 1001 got this one's: %v", ev)
	}
}

// A slow listener never holds up the recorder or other listeners: its
// frames are dropped and counted, and one too far behind for an event is
// closed.
func TestIntegrationLiveSlowListener(t *testing.T) {
	e := newEnv(t)
	rec := e.liveRecorder(t)
	slow := e.live.Subscribe() // never read
	defer slow.Close()
	slow.Listen(gate.ListeningSet{Any: []int32{1001}}, nil)
	fast := e.listen(t, []any{1001}, nil)
	now := time.Now().UnixMilli()

	c, cid := rec.start(1001, nil, 5001, now)
	const n = 1000
	for i := 0; i < n; i += 50 {
		rec.frames(c, uint32(i), 50, 'f')
		time.Sleep(10 * time.Millisecond)
	}
	rec.end(c, now+20_000)
	row := e.waitCall(t, rec.name, c, func(r callRow) bool { return r.frames == n })
	if row.frames != n {
		t.Errorf("the recorder's frames: %d stored, want %d", row.frames, n)
	}
	fast.wait("the end", isEvent(gate.TypeCallEnd, cid))
	if _, fr := fast.of(cid); fr != n {
		t.Errorf("the other listener got %d frames, want %d", fr, n)
	}
	// Its queue holds "listening" and the call's start, then frames up to
	// SubscriptionFrames waiting; every later frame is dropped.
	if want := uint64(n - (gate.SubscriptionFrames - 2)); slow.Dropped() != want {
		t.Errorf("the slow listener dropped %d frames, want %d", slow.Dropped(), want)
	}
	if e.live.Stat.FramesDropped.Load() < slow.Dropped() {
		t.Errorf("dropped frames counted %d, want at least %d", e.live.Stat.FramesDropped.Load(), slow.Dropped())
	}
	select {
	case <-slow.Done():
		t.Fatal("closed with room for events")
	default:
	}
	// Events past the room left close it.
	c2, _ := rec.start(1001, nil, 5001, now+30_000)
	for i := range gate.SubscriptionQueue - gate.SubscriptionFrames {
		rec.transmission(c2, 5001, int64(i), nil)
	}
	select {
	case <-slow.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("a listener too far behind for an event wasn't closed")
	}
	if e.live.Stat.SlowClosed.Load() != 1 {
		t.Errorf("slow closes counted %d, want 1", e.live.Stat.SlowClosed.Load())
	}
}

// Bad messages from a listener close its socket, as for a broken client;
// unknown types are ignored.
func TestIntegrationLiveBadMessages(t *testing.T) {
	e := newEnv(t)
	for _, c := range []struct {
		name string
		typ  websocket.MessageType
		msg  string
		code websocket.StatusCode
	}{
		{"binary", websocket.MessageBinary, "x", websocket.StatusUnsupportedData},
		{"not JSON", websocket.MessageText, "{", websocket.StatusInvalidFramePayloadData},
		{"a set of strings", websocket.MessageText, `{"type":"set","talkgroups":["1001"]}`, websocket.StatusPolicyViolation},
		{"an entry without an ID", websocket.MessageText, `{"type":"set","talkgroups":[{"system":1}]}`, websocket.StatusPolicyViolation},
		{"an entry with more", websocket.MessageText, `{"type":"set","talkgroups":[{"system":1,"id":1001,"x":1}]}`, websocket.StatusPolicyViolation},
		{"too many", websocket.MessageText, `{"type":"set","talkgroups":[` + strings.Repeat("1,", public.MaxListeningSet) + `1]}`, websocket.StatusPolicyViolation},
	} {
		t.Run(c.name, func(t *testing.T) {
			l := e.listen(t, nil, nil)
			if err := l.conn.Write(context.Background(), websocket.MessageText, []byte(`{"type":"from_the_future"}`)); err != nil {
				t.Fatal(err)
			}
			if err := l.conn.Write(context.Background(), c.typ, []byte(c.msg)); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				l.mu.Lock()
				err := l.closed
				l.mu.Unlock()
				if err != nil {
					if got := websocket.CloseStatus(err); got != c.code {
						t.Errorf("closed with %d (%v), want %d", got, err, c.code)
					}
					return
				}
				if time.Now().After(deadline) {
					t.Fatal("never closed")
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

func (e *env) callDetails(t *testing.T, body string) []public.Call {
	t.Helper()
	req, _ := http.NewRequest("POST", e.srv.URL+public.CallsPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	a := do(t, req)
	if a.status != 200 {
		t.Fatalf("call details: %s", a)
	}
	var r public.CallsResponse
	if err := json.Unmarshal(a.body, &r); err != nil {
		t.Fatal(err)
	}
	return r.Calls
}

func (e *env) get(t *testing.T, path string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", e.srv.URL+path, nil)
	a := do(t, req)
	return a.status, a.body
}
