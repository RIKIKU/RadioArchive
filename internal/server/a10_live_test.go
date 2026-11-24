package server_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/gate"
	"github.com/RIKIKU/RadioArchive/internal/public"
)

// The A10 harness for the live feed (SPEC.md A10, R16, R32; ADR 0004, ADR
// 0007): its probes are listeners, each with a listening set, around live
// calls on hidden 1003, on 1001 patched with 1003, on ignored 1004, and on
// 1001 whose patch grows into 1003 mid-call, beside a public call. No event
// or frame of 1003, or of a call patched with it, may reach any of them,
// even one whose set names 1003. TestIntegrationA10 checks every public
// route has probes here or in a10Probes.
var a10LiveProbes = map[string][]a10LiveProbe{
	"GET " + public.LivePath: {
		{name: "names 1003", set: tgs(1003)},
		{name: "names 1003 and 1001", set: tgs(1003, 1001), public: true},
		{name: "names 1003 in its system", public: true, set: func(e *env) []any {
			return []any{map[string]any{"system": e.sys, "id": 1003}, map[string]any{"system": e.sys, "id": 1001}}
		}},
		{name: "names every talkgroup", set: tgs(1001, 1002, 1003, 1004), public: true},
		{name: "names ignored 1004", set: tgs(1004)},
		{name: "adds 1003 mid-call", set: tgs(1001), mid: tgs(1001, 1003), public: true},
		{name: "resumes from before every call", set: tgs(1001, 1002, 1003, 1004), public: true, resume: true},
	},
}

type a10LiveProbe struct {
	name string
	set  func(e *env) []any
	// mid, when set, is the set the listener changes to while the hidden
	// calls are live.
	mid func(e *env) []any
	// public: its set names 1001, so it must hear the public call (the
	// probe works).
	public bool
	// resume: it joins once every call has ended, resuming from an event
	// ID from before them all.
	resume bool
}

func tgs(ids ...int) func(*env) []any {
	return func(*env) []any {
		out := make([]any, len(ids))
		for i, id := range ids {
			out[i] = id
		}
		return out
	}
}

func TestIntegrationA10Live(t *testing.T) {
	e := newEnv(t)
	rec := e.liveRecorder(t)
	probes := a10LiveProbes["GET "+public.LivePath]
	ls := make([]*listener, len(probes))
	var before int64
	for i, p := range probes {
		if !p.resume {
			ls[i] = e.listen(t, p.set(e), nil)
			before = ls[i].welcome.LastEventID
		}
	}
	now := time.Now().UnixMilli()

	pub, pubID := rec.start(1001, nil, 5001, now)
	rec.frames(pub, 0, 5, 'P')
	hid, hidID := rec.start(1003, nil, 5001, now)
	rec.frames(hid, 0, 5, 'H')
	rec.transmission(hid, 5150, 500, nil)
	pat, patID := rec.start(1001, []int32{1003}, 5001, now)
	rec.frames(pat, 0, 5, 'X')
	rec.transmission(pat, 5150, 500, []int32{1003})
	ign := rec.refused(1004, now) // never stored (R18)
	rec.frames(ign, 0, 5, 'I')
	grow, growID := rec.start(1001, nil, 5001, now)
	rec.frames(grow, 0, 5, 'g')
	rec.transmission(grow, 5150, 500, []int32{1003})
	rec.frames(grow, 5, 5, 'G')

	for i, p := range probes {
		if p.mid != nil {
			ls[i].set(p.mid(e), nil)
		}
	}
	rec.frames(hid, 5, 5, 'H')
	rec.frames(pat, 5, 5, 'X')
	rec.frames(pub, 5, 5, 'P')
	rec.end(hid, now+1000)
	rec.end(pat, now+1000)
	rec.end(ign, now+1000)
	rec.end(grow, now+1000)
	rec.end(pub, now+2000)

	for i, p := range probes {
		if p.resume {
			ls[i] = e.listen(t, p.set(e), &before)
			ls[i].wait("the replay", func(m liveMsg) bool { return m.Type == gate.TypeReplay })
		}
		if p.public {
			ls[i].wait("the public call's end", isEvent(gate.TypeCallEnd, pubID))
		}
	}
	quiet()

	secrets := []string{"1003", "1004", "Admin", "ADMIN", `"admin"`}
	ids := []string{hidID, patID}
	audio := [][]byte{[]byte("HHHH"), []byte("XXXX"), []byte("IIII"), []byte("GGGG")}
	for i, p := range probes {
		t.Run(p.name, func(t *testing.T) {
			l := ls[i]
			if p.public {
				ev, fr := l.of(pubID)
				if len(ev) == 0 || ev[0] != gate.TypeCallStart || ev[len(ev)-1] != gate.TypeCallEnd ||
					(fr == 0 && !p.resume) {
					t.Errorf("the public call: %v, %d frames; the probe doesn't work", ev, fr)
				}
			}
			for _, m := range append([]liveMsg{l.welcome}, l.all()...) {
				if m.Binary {
					if m.CallID == hidID || m.CallID == patID {
						t.Errorf("a frame of hidden call %s", m.CallID)
					}
					for _, a := range audio {
						if bytes.Contains(m.Packet, a) {
							t.Errorf("hidden audio %q in a frame of %s", a[:1], m.CallID)
						}
					}
					continue
				}
				text := scrub(m.Raw)
				for _, s := range secrets {
					if strings.Contains(text, s) {
						t.Errorf("%q in %s", s, m.Raw)
					}
				}
				for _, id := range ids {
					if bytes.Contains(m.Raw, []byte(id)) {
						t.Errorf("hidden call %s in %s", id, m.Raw)
					}
				}
				if m.CallID == growID && m.Type != gate.TypeCallStart && m.Type != gate.TypeCallWithdrawn {
					t.Errorf("after its patch grew into 1003: %s", m.Raw)
				}
			}
		})
	}
}
