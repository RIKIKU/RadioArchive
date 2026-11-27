package recorderclient

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
	rp "github.com/RIKIKU/RadioArchive/internal/recorderproto"
	"github.com/RIKIKU/RadioArchive/testing/fakerecorder"
)

// fakeEncoder stands in for libopus: one 3-byte packet per 20 ms frame,
// holding the frame's first sample.
type fakeEncoder struct{ closed bool }

func (*fakeEncoder) FrameSamples() int { return 160 }
func (*fakeEncoder) Lookahead() int    { return 52 }
func (*fakeEncoder) Encode(pcm []int16) ([]byte, error) {
	return []byte{0x08, byte(pcm[0]), byte(pcm[0] >> 8)}, nil
}
func (e *fakeEncoder) Close() { e.closed = true }

func newFake(int) (Encoder, error) { return &fakeEncoder{}, nil }

// sent is one message the engine gave the live socket, in order.
type sent struct {
	text  map[string]any
	frame *cp.Frame
}

type fakeLive struct{ out []sent }

func (l *fakeLive) SendText(v any) {
	b, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	l.out = append(l.out, sent{text: m})
}

func (l *fakeLive) SendFrame(b []byte) {
	f, err := cp.DecodeFrame(b)
	if err != nil {
		panic(err)
	}
	l.out = append(l.out, sent{frame: &f})
}

func (l *fakeLive) events(typ string) []map[string]any {
	var out []map[string]any
	for _, s := range l.out {
		if s.text != nil && s.text["type"] == typ {
			out = append(out, s.text)
		}
	}
	return out
}

type fakeUploader struct{ recs []Recording }

func (u *fakeUploader) Enqueue(r Recording)  { u.recs = append(u.recs, r) }
func (u *fakeUploader) Queue() cp.QueueState { return cp.QueueState{Count: len(u.recs)} }

type rig struct {
	e    *engine
	live *fakeLive
	up   *fakeUploader
	m    *Metrics
	now  time.Time

	removed []string // files the engine deleted
}

func newRig() *rig {
	r := &rig{live: &fakeLive{}, up: &fakeUploader{}, m: NewMetrics(), now: time.UnixMilli(1764000000000)}
	r.e = newEngine(slog.New(slog.DiscardHandler), r.m, r.live, r.up, newFake, func() time.Time { return r.now })
	r.e.remove = func(p string) error { r.removed = append(r.removed, p); return nil }
	return r
}

func loadScenario(t *testing.T, name string) *fakerecorder.Scenario {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "testing", "fakerecorder", "testdata", "scenarios", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s, err := fakerecorder.LoadScenario(f)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

var fixtures = os.DirFS(filepath.Join("..", "..", "testing", "fakerecorder", "testdata", "fixtures"))

// replay feeds a scenario's datagrams to the engine on the scenario's clock.
func (r *rig) replay(t *testing.T, s *fakerecorder.Scenario) {
	t.Helper()
	plan, err := fakerecorder.Plan(s, fixtures)
	if err != nil {
		t.Fatal(err)
	}
	start := r.now
	for _, p := range plan {
		r.now = start.Add(p.At)
		r.e.handle(p.Datagram)
	}
}

func num(v any) int64 { f, _ := v.(float64); return int64(f) }

func callNums(evs []map[string]any) []int64 {
	var out []int64
	for _, e := range evs {
		out = append(out, num(e["call_num"]))
	}
	slices.Sort(out)
	return out
}

func TestEngineAcceptanceScenario(t *testing.T) {
	r := newRig()
	r.replay(t, loadScenario(t, "acceptance.json"))

	// Every call but the patched copy (call 4) starts, ends and uploads.
	want := []int64{1, 2, 3, 5, 6}
	if got := callNums(r.live.events(cp.TypeCallStart)); !slices.Equal(got, want) {
		t.Errorf("call_start for %v, want %v", got, want)
	}
	if got := callNums(r.live.events(cp.TypeCallEnd)); !slices.Equal(got, want) {
		t.Errorf("call_end for %v, want %v", got, want)
	}
	var uploaded []int64
	for _, rec := range r.up.recs {
		uploaded = append(uploaded, rec.Meta.CallNum)
	}
	slices.Sort(uploaded)
	if !slices.Equal(uploaded, want) {
		t.Errorf("uploads for %v, want %v", uploaded, want)
	}

	if n := r.m.Get(MetricPatchCopies); n != 1 {
		t.Errorf("patch copies = %d, want 1", n)
	}
	if n := r.m.Get(MetricLost); n != 3 {
		t.Errorf("lost = %d, want 3 (the scenario's gap)", n)
	}
	if n := r.m.Get(MetricMalformed, "reason", "magic"); n != 1 {
		t.Errorf("malformed{magic} = %d, want 1", n)
	}
	if n := r.m.Get(MetricMalformed, "reason", "sample_rate"); n != 1 {
		t.Errorf("malformed{sample_rate} = %d, want 1", n)
	}

	// Frames: each call's go out numbered from 0, after its call_start and
	// before its call_end. Calls 1 and 2 have 1.5 s of audio: 75 frames.
	started := map[int64]bool{}
	ended := map[int64]bool{}
	frames := map[int64]uint32{}
	for _, s := range r.live.out {
		switch {
		case s.frame != nil:
			n := s.frame.CallNum
			if !started[n] || ended[n] {
				t.Fatalf("call %d: frame outside call_start..call_end", n)
			}
			if s.frame.Seq != frames[n] {
				t.Fatalf("call %d: frame_seq %d, want %d", n, s.frame.Seq, frames[n])
			}
			frames[n]++
		case s.text["type"] == cp.TypeCallStart:
			started[num(s.text["call_num"])] = true
		case s.text["type"] == cp.TypeCallEnd:
			ended[num(s.text["call_num"])] = true
		}
	}
	if frames[1] != 75 || frames[2] != 75 || frames[4] != 0 {
		t.Errorf("frames = %v, want 75 for calls 1 and 2 and none for the copy", frames)
	}

	// Call 1's transmissions, live and in the recording.
	var live []int64
	for _, e := range r.live.events(cp.TypeTransmission) {
		if num(e["call_num"]) == 1 {
			live = append(live, num(e["unit"]), num(e["offset_ms"]))
		}
	}
	if !slices.Equal(live, []int64{5001, 0, 5123, 1000}) {
		t.Errorf("call 1 live transmissions (unit, offset) = %v", live)
	}
	for _, rec := range r.up.recs {
		switch rec.Meta.CallNum {
		case 1:
			want := []cp.RecordingTransmission{{Unit: 5001, OffsetMS: 0, DurationMS: 1000}, {Unit: 5123, OffsetMS: 1000, DurationMS: 500}}
			if !slices.Equal(rec.Meta.Transmissions, want) {
				t.Errorf("call 1 recording transmissions = %+v, want %+v", rec.Meta.Transmissions, want)
			}
			if rec.WAV != "/app/media/ex/1001-1764000000_851012500.0-call_1.wav" || rec.Meta.Talkgroup != 1001 {
				t.Errorf("call 1 recording = %+v", rec)
			}
		case 3:
			if !slices.Equal(rec.Meta.PatchedTalkgroups, []int32{1002}) {
				t.Errorf("call 3 patched = %v, want [1002]", rec.Meta.PatchedTalkgroups)
			}
		}
	}
	// The copy's WAV is deleted when its call_end arrives, and only its:
	// the others are deleted by the uploader once acknowledged.
	if want := []string{"/app/media/ex/1002-1764000002_851037500.0-call_4.wav"}; !slices.Equal(r.removed, want) {
		t.Errorf("engine deleted %v, want %v", r.removed, want)
	}
	if len(r.e.calls) != 0 || len(r.e.copies) != 0 {
		t.Errorf("%d calls and %d copies still tracked", len(r.e.calls), len(r.e.copies))
	}

	// The heartbeat relays the plugin's last systems and the counters.
	hb := r.e.heartbeat()
	if len(hb.Systems) != 2 || hb.Systems[0].DecodeRate != 0 || hb.UDP.Lost != 3 || hb.UDP.PatchCopies != 1 || hb.UDP.Malformed != 2 {
		t.Errorf("heartbeat = %+v", hb)
	}
	r.now = r.now.Add(11 * time.Second)
	if hb := r.e.heartbeat(); len(hb.Systems) != 0 || hb.Plugin.LastHeartbeatMS == 0 {
		t.Errorf("heartbeat after the plugin went quiet = %+v, want no systems", hb)
	}
}

func audio(t *testing.T, seq uint32, callNum int64, rate uint16, samples int) []byte {
	t.Helper()
	b, err := rp.EncodeAudio(1, seq, rp.Audio{
		Identity:  rp.Identity{SysNum: 0, CallNum: callNum, StartTime: 1764000000, ShortName: "ex"},
		Talkgroup: 1001, Unit: 7, Freq: 851000000, SampleRate: rate, TDMASlot: -1,
		PCM: make([]byte, 2*samples),
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// pcmEncoder is a fake encoder that keeps the PCM it was given.
type pcmEncoder struct {
	rate int
	pcm  *[]int16
}

func (e *pcmEncoder) FrameSamples() int { return e.rate / 50 }
func (e *pcmEncoder) Lookahead() int    { return e.rate / 400 }
func (e *pcmEncoder) Encode(pcm []int16) ([]byte, error) {
	*e.pcm = append(*e.pcm, pcm...)
	return []byte{0x08}, nil
}
func (*pcmEncoder) Close() {}

// Live audio is encoded at the loudness trunk-recorder gives the call's
// recording, not at the decoder's own, quieter level (ADR 0047).
func TestEngineLevelsLiveAudio(t *testing.T) {
	for _, rate := range SampleRates {
		t.Run(fmt.Sprint(rate), func(t *testing.T) {
			r := newRig()
			var got []int16
			r.e.newEnc = func(rate int) (Encoder, error) { return &pcmEncoder{rate: rate, pcm: &got}, nil }
			in := speech(rate, 8, -35, 3)
			const chunk = 320 // the plugin's datagrams don't fall on frame boundaries
			for seq, off := 0, 0; off < len(in); seq, off = seq+1, off+chunk {
				pcm := in[off:min(off+chunk, len(in))]
				b := make([]byte, 2*len(pcm))
				for i, v := range pcm {
					b[2*i], b[2*i+1] = byte(v), byte(uint16(v)>>8)
				}
				d, err := rp.EncodeAudio(1, uint32(seq), rp.Audio{
					Identity:  rp.Identity{SysNum: 0, CallNum: 1, StartTime: 1764000000, ShortName: "ex"},
					Talkgroup: 1001, Unit: 7, Freq: 851000000, SampleRate: uint16(rate), TDMASlot: -1, PCM: b,
				})
				if err != nil {
					t.Fatal(err)
				}
				r.e.handle(d)
			}
			before, after := Loudness(in, rate), Loudness(got, rate)
			if math.Abs(after-LiveLoudness) > 2 {
				t.Errorf("live audio at %.1f LUFS encoded at %.1f LUFS, want %.0f±2", before, after, LiveLoudness)
			}
		})
	}
}

// rateEncoder is a fake encoder that, like libopus, takes 20 ms at its own
// rate, and records the frames it was given.
type rateEncoder struct {
	rate   int
	frames *[]int
}

func (e *rateEncoder) FrameSamples() int { return e.rate / 50 }
func (e *rateEncoder) Lookahead() int    { return e.rate / 400 }
func (e *rateEncoder) Encode(pcm []int16) ([]byte, error) {
	*e.frames = append(*e.frames, len(pcm))
	return []byte{0x08}, nil
}
func (*rateEncoder) Close() {}

// Live audio is encoded at the rate its datagrams carry, in 20 ms frames
// at that rate, so neither 8 nor 16 kHz plays at the wrong speed (ADR 0046).
func TestEngineLiveRates(t *testing.T) {
	for _, tt := range []struct {
		rate  uint16
		frame int
	}{{8000, 160}, {16000, 320}} {
		t.Run(fmt.Sprint(tt.rate), func(t *testing.T) {
			r := newRig()
			var rates, frames []int
			r.e.newEnc = func(rate int) (Encoder, error) {
				rates = append(rates, rate)
				return &rateEncoder{rate: rate, frames: &frames}, nil
			}
			n := int(tt.rate) / 1000 * 30 // 30 ms: one frame and half of the next
			r.e.handle(audio(t, 0, 1, tt.rate, n))
			r.e.handle(audio(t, 1, 1, tt.rate, n)) // 60 ms in all: three frames
			if !slices.Equal(rates, []int{int(tt.rate)}) {
				t.Errorf("encoders made at %v Hz, want one at %d", rates, tt.rate)
			}
			if !slices.Equal(frames, []int{tt.frame, tt.frame, tt.frame}) {
				t.Errorf("frames of %v samples, want three of %d (20 ms)", frames, tt.frame)
			}
			if got := len(r.live.out); got != 1+3 {
				t.Errorf("%d messages, want call_start and three frames", got)
			}
			// The same call at another rate is dropped: its PCM would play
			// at the wrong speed through this call's encoder.
			other := uint16(24000 - int(tt.rate)) // 16000 for 8000, 8000 for 16000
			r.e.handle(audio(t, 2, 1, other, 2*n))
			if n := r.m.Get(MetricMalformed, "reason", "sample_rate"); n != 1 {
				t.Errorf("malformed{sample_rate} = %d, want 1", n)
			}
			if len(frames) != 3 {
				t.Errorf("%d frames after audio at %d Hz, want still 3", len(frames), other)
			}
		})
	}
}

func TestEngineSequence(t *testing.T) {
	r := newRig()
	r.e.handle(audio(t, 10, 1, 8000, 160))
	r.e.handle(audio(t, 13, 1, 8000, 160)) // 11 and 12 lost
	r.e.handle(audio(t, 12, 1, 8000, 160)) // late: dropped
	if n := r.m.Get(MetricLost); n != 2 {
		t.Errorf("lost = %d, want 2", n)
	}
	if n := r.m.Get(MetricLate); n != 1 {
		t.Errorf("late = %d, want 1", n)
	}
	if got := len(r.live.out); got != 1+2 { // call_start and two frames
		t.Errorf("%d messages, want 3", got)
	}
	// A new boot ID is trunk-recorder restarting: no gap counted.
	b, _ := rp.EncodeControl(2, 0, rp.Heartbeat{TimeMS: 1, Systems: []rp.SystemRate{}})
	r.e.handle(b)
	if n := r.m.Get(MetricLost); n != 2 {
		t.Errorf("lost after a reboot = %d, want 2", n)
	}
	// Wraparound at 2^32 isn't a gap.
	r.e.handle(audio(t, 0xffffffff, 1, 8000, 160)) // boot 1 again: resets
	r.e.handle(audio(t, 0, 1, 8000, 160))
	if n := r.m.Get(MetricLost); n != 2 {
		t.Errorf("lost after wraparound = %d, want 2", n)
	}
}

func TestEngineIdleAndReconnect(t *testing.T) {
	r := newRig()
	r.e.handle(audio(t, 0, 1, 8000, 200)) // one frame and 40 samples left over
	r.e.reconnected()
	if starts := r.live.events(cp.TypeCallStart); len(starts) != 2 {
		t.Fatalf("%d call_starts after a reconnect, want 2 (the call is resent)", len(starts))
	}
	r.now = r.now.Add(29 * time.Second)
	r.e.tick()
	if len(r.e.calls) != 1 {
		t.Fatal("call ended before 30 s")
	}
	r.now = r.now.Add(2 * time.Second)
	r.e.tick()
	if len(r.e.calls) != 0 || len(r.live.events(cp.TypeCallEnd)) != 1 {
		t.Fatal("call not ended after 30 s with no packets")
	}
	if len(r.up.recs) != 0 {
		t.Error("an idle end uploaded a recording")
	}
	frames := 0
	for _, s := range r.live.out {
		if s.frame != nil {
			frames++
		}
	}
	if frames != 2 { // the leftover, padded
		t.Errorf("%d frames, want 2", frames)
	}
}

func TestEngineCallEndOnly(t *testing.T) {
	// Every live packet lost (or the client restarted): the call still
	// starts and ends on the server, and its recording uploads.
	r := newRig()
	ce := rp.CallEnd{Identity: rp.Identity{CallNum: 9, StartTime: 1764000000, ShortName: "ex"}, Talkgroup: 1001,
		StartTimeMS: 1764000000100, StopTimeMS: 1764000002100, Freq: 851000000, TDMASlot: -1,
		PatchedTalkgroups: []int32{1001, 1005}, Filename: "/x.wav",
		Transmissions: []rp.CallEndTransmission{{Unit: 3, StartTimeMS: 1764000000100, StopTimeMS: 1764000002100}}}
	b, _ := rp.EncodeControl(1, 0, ce)
	r.e.handle(b)
	starts := r.live.events(cp.TypeCallStart)
	if len(starts) != 1 || num(starts[0]["start_time_ms"]) != 1764000000100 || len(r.live.events(cp.TypeCallEnd)) != 1 {
		t.Fatalf("live = %v", r.live.out)
	}
	if len(r.up.recs) != 1 || !slices.Equal(r.up.recs[0].Meta.PatchedTalkgroups, []int32{1005}) {
		t.Fatalf("uploads = %+v (patched talkgroups exclude the call's own)", r.up.recs)
	}
}

// trunk-recorder concluded a call without a recording (ADR 0053): its live
// audio ends, the server is told to withdraw it, and nothing is uploaded.
func TestEngineWithdraw(t *testing.T) {
	r := newRig()
	withdraw := func(seq uint32, callNum int64) {
		b, err := rp.EncodeControl(1, seq, rp.CallWithdraw{Identity: rp.Identity{CallNum: callNum, StartTime: 1764000000, ShortName: "ex"},
			Talkgroup: 1001, TimeMS: 1764000009000, Reason: rp.WithdrawNoTransmissions})
		if err != nil {
			t.Fatal(err)
		}
		r.e.handle(b)
	}
	r.e.handle(audio(t, 0, 1, 8000, 200)) // call 1 starts live: a frame and 40 samples left over
	r.now = r.now.Add(2 * time.Second)
	withdraw(1, 1)
	ends := r.live.events(cp.TypeCallEnd)
	if len(ends) != 1 || ends[0]["withdrawn"] != true || num(ends[0]["call_num"]) != 1 ||
		num(ends[0]["stop_time_ms"]) != 1764000000000 {
		t.Fatalf("call_end = %v, want one, withdrawn, stopped at its last packet", ends)
	}
	if len(r.e.calls) != 0 || len(r.up.recs) != 0 {
		t.Fatalf("calls %d, uploads %d after a withdrawal", len(r.e.calls), len(r.up.recs))
	}
	frames := 0
	for _, s := range r.live.out {
		if s.frame != nil {
			frames++
		}
	}
	if frames != 2 { // the leftover, padded, before the call_end
		t.Errorf("%d frames, want 2", frames)
	}

	// One the client no longer holds (it restarted, or ended the call's
	// live audio after 30 s idle) is still sent: the server may hold it.
	withdraw(2, 7)
	if ends := r.live.events(cp.TypeCallEnd); len(ends) != 2 || num(ends[1]["call_num"]) != 7 || ends[1]["withdrawn"] != true ||
		num(ends[1]["stop_time_ms"]) != 1764000009000 {
		t.Fatalf("call_ends = %v", ends)
	}
	if n := r.m.Get(MetricWithdrawn, "reason", rp.WithdrawNoTransmissions); n != 2 {
		t.Errorf("withdrawn = %d, want 2", n)
	}

	// A plain call_end still uploads, and isn't withdrawn.
	ce := rp.CallEnd{Identity: rp.Identity{CallNum: 9, StartTime: 1, ShortName: "ex"}, Talkgroup: 1001,
		StartTimeMS: 1764000000100, StopTimeMS: 1764000002100, TDMASlot: -1, Filename: "/x.wav"}
	b, _ := rp.EncodeControl(1, 3, ce)
	r.e.handle(b)
	if ends := r.live.events(cp.TypeCallEnd); len(ends) != 3 || ends[2]["withdrawn"] != nil || len(r.up.recs) != 1 {
		t.Fatalf("call_ends = %v, uploads %d", ends, len(r.up.recs))
	}
}

func TestEngineChannelReuseIsNotACopy(t *testing.T) {
	// Same frequency and slot, overlapping, but no shared patch: both stay.
	r := newRig()
	mk := func(seq uint32, callNum int64, tg int32) []byte {
		b, _ := rp.EncodeAudio(1, seq, rp.Audio{Identity: rp.Identity{CallNum: callNum, StartTime: 1, ShortName: "ex"},
			Talkgroup: tg, Freq: 851000000, SampleRate: 8000, TDMASlot: -1, PCM: make([]byte, 320)})
		return b
	}
	r.e.handle(mk(0, 1, 1001))
	r.e.handle(mk(1, 2, 1002))
	if len(r.live.events(cp.TypeCallStart)) != 2 || r.m.Get(MetricPatchCopies) != 0 {
		t.Errorf("channel reuse dropped a call")
	}
}
