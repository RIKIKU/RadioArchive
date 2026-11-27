// Package fakerecorder stands in for trunk-recorder and the recorder plugin
// in tests. It replays a scenario, a JSON timeline of calls, transmissions,
// call ends, heartbeats, sequence gaps and malformed datagrams, over the
// plugin -> client UDP protocol of ADR 0002, using fixture PCM files.
package fakerecorder

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"
	"time"

	rp "github.com/RIKIKU/RadioArchive/internal/recorderproto"
)

// Scenario is a scripted run of a recorder. Times are milliseconds after the
// scenario starts; EpochMS is the Unix time, in ms, of that start, so the
// timestamps in the messages are fixed and tests can assert them.
type Scenario struct {
	BootID  uint32          `json:"boot_id"`
	EpochMS int64           `json:"epoch_ms"`
	Calls   map[string]Call `json:"calls"`
	Events  []Event         `json:"events"`
}

// Call is one trunk-recorder call. StartTime is Unix seconds, as
// trunk-recorder's Call::get_start_time().
type Call struct {
	SysNum     uint16 `json:"sys_num"`
	CallNum    int64  `json:"call_num"`
	StartTime  int64  `json:"start_time"`
	ShortName  string `json:"short_name"`
	Talkgroup  int32  `json:"talkgroup"`
	Freq       uint32 `json:"freq"`
	TDMASlot   *int8  `json:"tdma_slot"`   // default -1
	SampleRate uint16 `json:"sample_rate"` // default 8000
}

// Event types.
const (
	EventTransmission = "transmission"  // a unit starts talking on a call
	EventAudio        = "audio"         // a fixture file sent as the call's PCM, paced in real time
	EventCallEnd      = "call_end"      // the recording is written
	EventCallWithdraw = "call_withdraw" // concluded without a recording (ADR 0053)
	EventHeartbeat    = "heartbeat"     // system_rates
	EventGap          = "gap"           // Count sequence numbers skipped, as if lost
	EventMalformed    = "malformed"     // Hex sent as a raw datagram
)

// Event is one step of a scenario.
type Event struct {
	AtMS int64  `json:"at_ms"`
	Type string `json:"type"`
	Call string `json:"call,omitempty"`

	// transmission, audio
	Unit uint32 `json:"unit,omitempty"`
	// transmission, call_end
	PatchedTalkgroups []int32 `json:"patched_talkgroups,omitempty"`
	// audio
	PCM          string `json:"pcm,omitempty"`
	ChunkSamples int    `json:"chunk_samples,omitempty"` // default 800 (100 ms at 8 kHz)
	// call_end
	Filename string `json:"filename,omitempty"`
	// call_withdraw
	Reason string `json:"reason,omitempty"`
	// heartbeat
	Systems []rp.SystemRate `json:"systems,omitempty"`
	// gap
	Count uint32 `json:"count,omitempty"`
	// malformed
	Hex string `json:"hex,omitempty"`
}

// Send is one datagram of a planned replay.
type Send struct {
	At       time.Duration
	Datagram []byte
	// Call is the scenario's name for the call the datagram is about, if
	// any, and FirstPCM marks the call's first audio datagram: where the
	// delay budget starts (R38, A6).
	Call     string
	FirstPCM bool
}

// LoadScenario reads and checks a scenario.
func LoadScenario(r io.Reader) (*Scenario, error) {
	var s Scenario
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("fakerecorder: scenario: %w", err)
	}
	for i, e := range s.Events {
		if e.AtMS < 0 {
			return nil, fmt.Errorf("fakerecorder: event %d: negative at_ms", i)
		}
		switch e.Type {
		case EventTransmission, EventAudio, EventCallEnd, EventCallWithdraw:
			if _, ok := s.Calls[e.Call]; !ok {
				return nil, fmt.Errorf("fakerecorder: event %d (%s): unknown call %q", i, e.Type, e.Call)
			}
		case EventHeartbeat, EventGap, EventMalformed:
		default:
			return nil, fmt.Errorf("fakerecorder: event %d: unknown type %q", i, e.Type)
		}
	}
	return &s, nil
}

type pending struct {
	at    int64 // ms
	order int   // tie-break: scenario order, then chunk order
	build func(seq uint32) ([]byte, error)
	gap   uint32 // sequence numbers skipped instead of a datagram
	raw   []byte // sent as is, using no sequence number
	call  string // the call it's about, if any
	pcm   bool   // an audio datagram
}

type callState struct {
	firstMS int64 // first transmission or audio
	lastMS  int64 // end of the last audio
	txs     []rp.CallEndTransmission
	patched []int32
}

// Plan turns a scenario into the datagrams to send, in order, with the
// times to send them. Fixture PCM files are read from fixtures.
//
// Sequence numbers follow send order, as the plugin's one shared counter
// does. A call_end lists the transmissions scripted for its call before it,
// each lasting until the next one or the end of the call's audio.
func Plan(s *Scenario, fixtures fs.FS) ([]Send, error) {
	var ps []pending
	state := map[string]*callState{}
	st := func(name string, at int64) *callState {
		cs := state[name]
		if cs == nil {
			cs = &callState{firstMS: at, lastMS: at}
			state[name] = cs
		}
		return cs
	}

	// Events in time order, so each call_end sees the transmissions before it.
	events := make([]int, len(s.Events))
	for i := range events {
		events[i] = i
	}
	sort.SliceStable(events, func(a, b int) bool { return s.Events[events[a]].AtMS < s.Events[events[b]].AtMS })

	order := 0
	for _, i := range events {
		e := s.Events[i]
		c := s.Calls[e.Call]
		slot := int8(-1)
		if c.TDMASlot != nil {
			slot = *c.TDMASlot
		}
		rate := c.SampleRate
		if rate == 0 {
			rate = 8000
		}
		id := rp.Identity{SysNum: c.SysNum, CallNum: c.CallNum, StartTime: c.StartTime, ShortName: c.ShortName}
		order++
		switch e.Type {
		case EventTransmission:
			cs := st(e.Call, e.AtMS)
			if n := len(cs.txs); n > 0 && cs.txs[n-1].StopTimeMS == 0 {
				cs.txs[n-1].StopTimeMS = s.EpochMS + e.AtMS
			}
			cs.txs = append(cs.txs, rp.CallEndTransmission{Unit: e.Unit, StartTimeMS: s.EpochMS + e.AtMS})
			cs.patched = union(cs.patched, e.PatchedTalkgroups)
			t := rp.Transmission{Identity: id, Talkgroup: c.Talkgroup, Unit: e.Unit, TimeMS: s.EpochMS + e.AtMS,
				Freq: c.Freq, TDMASlot: slot, PatchedTalkgroups: nonNil(e.PatchedTalkgroups)}
			ps = append(ps, pending{at: e.AtMS, order: order, build: control(s.BootID, t), call: e.Call})
		case EventAudio:
			pcm, err := fs.ReadFile(fixtures, e.PCM)
			if err != nil {
				return nil, fmt.Errorf("fakerecorder: event %d: %w", i, err)
			}
			if len(pcm) == 0 || len(pcm)%2 != 0 {
				return nil, fmt.Errorf("fakerecorder: %s: PCM must be whole 16-bit samples", e.PCM)
			}
			chunk := e.ChunkSamples
			if chunk == 0 {
				chunk = 800
			}
			if chunk < 1 || chunk > rp.MaxAudioSamples {
				return nil, fmt.Errorf("fakerecorder: event %d: chunk_samples must be 1-%d", i, rp.MaxAudioSamples)
			}
			cs := st(e.Call, e.AtMS)
			for off, k := 0, 0; off < len(pcm); off, k = off+2*chunk, k+1 {
				part := pcm[off:min(off+2*chunk, len(pcm))]
				at := e.AtMS + int64(off/2)*1000/int64(rate)
				a := rp.Audio{Identity: id, Talkgroup: c.Talkgroup, Unit: e.Unit, Freq: c.Freq,
					SampleRate: rate, TDMASlot: slot, PCM: part}
				ps = append(ps, pending{at: at, order: order, call: e.Call, pcm: true, build: func(seq uint32) ([]byte, error) {
					return rp.EncodeAudio(s.BootID, seq, a)
				}})
				order++
			}
			end := e.AtMS + int64(len(pcm)/2)*1000/int64(rate)
			cs.lastMS = max(cs.lastMS, end)
		case EventCallEnd:
			cs := st(e.Call, e.AtMS)
			txs := append([]rp.CallEndTransmission(nil), cs.txs...)
			if n := len(txs); n > 0 && txs[n-1].StopTimeMS == 0 {
				txs[n-1].StopTimeMS = s.EpochMS + cs.lastMS
			}
			ce := rp.CallEnd{Identity: id, Talkgroup: c.Talkgroup,
				StartTimeMS: s.EpochMS + cs.firstMS, StopTimeMS: s.EpochMS + cs.lastMS,
				Freq: c.Freq, TDMASlot: slot,
				PatchedTalkgroups: nonNil(union(cs.patched, e.PatchedTalkgroups)),
				Filename:          e.Filename, Transmissions: nonNilTx(txs)}
			ps = append(ps, pending{at: e.AtMS, order: order, build: control(s.BootID, ce), call: e.Call})
		case EventCallWithdraw:
			reason := e.Reason
			if reason == "" {
				reason = rp.WithdrawNoTransmissions
			}
			w := rp.CallWithdraw{Identity: id, Talkgroup: c.Talkgroup, TimeMS: s.EpochMS + e.AtMS, Reason: reason}
			ps = append(ps, pending{at: e.AtMS, order: order, build: control(s.BootID, w), call: e.Call})
		case EventHeartbeat:
			hb := rp.Heartbeat{TimeMS: s.EpochMS + e.AtMS, PluginVersion: "fake", Systems: e.Systems}
			if hb.Systems == nil {
				hb.Systems = []rp.SystemRate{}
			}
			ps = append(ps, pending{at: e.AtMS, order: order, build: control(s.BootID, hb)})
		case EventGap:
			if e.Count == 0 {
				return nil, fmt.Errorf("fakerecorder: event %d: gap needs a count", i)
			}
			ps = append(ps, pending{at: e.AtMS, order: order, gap: e.Count})
		case EventMalformed:
			raw, err := hex.DecodeString(strings.ReplaceAll(e.Hex, " ", ""))
			if err != nil || len(raw) == 0 {
				return nil, fmt.Errorf("fakerecorder: event %d: hex: %v", i, err)
			}
			ps = append(ps, pending{at: e.AtMS, order: order, raw: raw})
		}
	}

	sort.SliceStable(ps, func(a, b int) bool {
		if ps[a].at != ps[b].at {
			return ps[a].at < ps[b].at
		}
		return ps[a].order < ps[b].order
	})
	var out []Send
	var seq uint32
	heard := map[string]bool{} // calls with an audio datagram planned
	for _, p := range ps {
		at := time.Duration(p.at) * time.Millisecond
		switch {
		case p.gap > 0:
			seq += p.gap
		case p.raw != nil:
			out = append(out, Send{At: at, Datagram: p.raw})
		default:
			b, err := p.build(seq)
			if err != nil {
				return nil, err
			}
			seq++
			first := p.pcm && !heard[p.call]
			if p.pcm {
				heard[p.call] = true
			}
			out = append(out, Send{At: at, Datagram: b, Call: p.call, FirstPCM: first})
		}
	}
	return out, nil
}

func control(bootID uint32, v any) func(uint32) ([]byte, error) {
	return func(seq uint32) ([]byte, error) { return rp.EncodeControl(bootID, seq, v) }
}

func union(a, b []int32) []int32 {
	out := append([]int32(nil), a...)
	for _, x := range b {
		found := false
		for _, y := range out {
			found = found || x == y
		}
		if !found {
			out = append(out, x)
		}
	}
	return out
}

func nonNil(a []int32) []int32 {
	if a == nil {
		return []int32{}
	}
	return a
}

func nonNilTx(a []rp.CallEndTransmission) []rp.CallEndTransmission {
	if a == nil {
		return []rp.CallEndTransmission{}
	}
	return a
}
