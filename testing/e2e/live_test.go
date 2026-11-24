//go:build cgo

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/RIKIKU/RadioArchive/internal/gate"
	"github.com/RIKIKU/RadioArchive/internal/public"
)

// The live feed end to end (SPEC.md A4, A5; ADR 0007): the fake recorder's
// PCM through the recorder client's Opus encoder and the server's gate to
// listeners' sockets.

type heard struct {
	mu     sync.Mutex
	events []string            // "type call-ID"
	frames map[string]int      // by call ID
	first  map[string]struct{} // talkgroup names in call_starts
	notice [][]byte            // feed notices, as sent
}

// listen opens a listener with set and records what it hears.
func (r *rig) listen(t *testing.T, set ...int) *heard {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(r.api, "http")+public.LivePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	h := &heard{frames: map[string]int{}, first: map[string]struct{}{}}
	b, _ := json.Marshal(map[string]any{"type": "set", "talkgroups": set})
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
	took := make(chan struct{})
	go func() {
		var once sync.Once
		for {
			typ, b, err := c.Read(context.Background())
			if err != nil {
				return
			}
			h.mu.Lock()
			if typ == websocket.MessageBinary {
				if len(b) > gate.LiveFrameHeader {
					id := fmt.Sprintf("%x-%x-%x-%x-%x", b[4:8], b[8:10], b[10:12], b[12:14], b[14:20])
					h.frames[id]++
				}
			} else {
				var m struct {
					Type      string `json:"type"`
					CallID    string `json:"call_id"`
					Talkgroup struct {
						Name string `json:"name"`
					} `json:"talkgroup"`
				}
				_ = json.Unmarshal(b, &m)
				if m.Type == gate.TypeListening {
					once.Do(func() { close(took) })
				}
				if m.CallID != "" {
					h.events = append(h.events, m.Type+" "+m.CallID)
				}
				if m.Type == gate.TypeCallStart {
					h.first[m.Talkgroup.Name] = struct{}{}
				}
				if m.Type == gate.TypeNotice {
					h.notice = append(h.notice, b)
				}
			}
			h.mu.Unlock()
		}
	}()
	select {
	case <-took:
	case <-time.After(5 * time.Second):
		t.Fatal("the server never took the listening set")
	}
	return h
}

func (h *heard) of(callID string) ([]string, int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var ev []string
	for _, e := range h.events {
		if typ, id, _ := strings.Cut(e, " "); id == callID {
			ev = append(ev, typ)
		}
	}
	return ev, h.frames[callID]
}

func (h *heard) all() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.events)
}

// TestA5LiveFeedEndToEnd is SPEC.md A5: A with {1001} hears only 1001's
// call, its events and Opus frames, and B with {1002} only 1002's.
func TestA5LiveFeedEndToEnd(t *testing.T) {
	r := start(t)
	a := r.listen(t, 1001)
	b := r.listen(t, 1002)
	s := backToBack(9100, 2, time.Now().Unix())
	c := s.Calls["c9101"]
	c.Talkgroup = 1002
	s.Calls["c9101"] = c
	r.play(t, s, 1)
	cs := r.waitRecordings(t, 2)
	ids := map[int32]string{}
	for _, c := range cs {
		ids[c.talkgroup] = c.id
	}
	if len(ids) != 2 {
		t.Fatalf("stored %+v, want one call on each of 1001 and 1002", cs)
	}
	for _, x := range []struct {
		name       string
		h          *heard
		own, other int32
		ownName    string
	}{{"A", a, 1001, 1002, "Dispatch North"}, {"B", b, 1002, 1001, "Fireground 41"}} {
		waitFor(t, x.name+" hearing its call end", 10*time.Second, func() bool {
			ev, _ := x.h.of(ids[x.own])
			return slices.Contains(ev, gate.TypeCallEnd)
		})
		ev, fr := x.h.of(ids[x.own])
		if ev[0] != gate.TypeCallStart || fr < 10 {
			t.Errorf("%s heard its own call as %v with %d frames", x.name, ev, fr)
		}
		if ev, fr := x.h.of(ids[x.other]); len(ev) != 0 || fr != 0 {
			t.Errorf("%s heard the other set's call: %v, %d frames", x.name, ev, fr)
		}
		if _, ok := x.h.first[x.ownName]; !ok {
			t.Errorf("%s's call_start doesn't name %q: %v", x.name, x.ownName, x.h.all())
		}
	}
}

// TestA4PatchListenersEndToEnd is SPEC.md A4's listener half: A with
// {1002} and B with {1001} each hear the call on 1001 patched with 1002
// once; the call on 1001 patched with hidden 1003 reaches neither, nor C,
// whose set names 1003.
func TestA4PatchListenersEndToEnd(t *testing.T) {
	r := start(t)
	a, b, c := r.listen(t, 1002), r.listen(t, 1001), r.listen(t, 1003, 1001)
	r.replay(t, "a4-patch.json")
	cs := r.waitRecordings(t, 2)
	var pub, hid string
	for _, x := range cs {
		switch {
		case slices.Equal(x.patched, []int32{1002}):
			pub = x.id
		case slices.Equal(x.patched, []int32{1003}):
			hid = x.id
		}
	}
	if pub == "" || hid == "" {
		t.Fatalf("stored %+v", cs)
	}
	for name, h := range map[string]*heard{"A": a, "B": b, "C": c} {
		waitFor(t, name+" hearing the patched call end", 10*time.Second, func() bool {
			ev, _ := h.of(pub)
			return slices.Contains(ev, gate.TypeCallEnd)
		})
		ev, fr := h.of(pub)
		starts := 0
		for _, e := range ev {
			if e == gate.TypeCallStart {
				starts++
			}
		}
		if starts != 1 || ev[0] != gate.TypeCallStart || ev[len(ev)-1] != gate.TypeCallEnd || fr == 0 {
			t.Errorf("%s heard the call patched with 1002 as %v, %d frames; want it once", name, ev, fr)
		}
		if ev, fr := h.of(hid); len(ev) != 0 || fr != 0 {
			t.Errorf("%s heard the call patched with hidden 1003: %v, %d frames", name, ev, fr)
		}
	}
}
