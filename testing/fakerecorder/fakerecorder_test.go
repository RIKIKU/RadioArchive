package fakerecorder_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	rp "github.com/RIKIKU/RadioArchive/internal/recorderproto"
	"github.com/RIKIKU/RadioArchive/testing/fakerecorder"
)

const (
	bootID = 305419896
	epoch  = int64(1764000000000)
)

var fixtureDir = filepath.Join("testdata", "fixtures")

func loadPlan(t *testing.T) []fakerecorder.Send {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "scenarios", "acceptance.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s, err := fakerecorder.LoadScenario(f)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := fakerecorder.Plan(s, os.DirFS(fixtureDir))
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

// listen replays plan to a UDP listener on loopback, as the recorder client
// would receive it, and returns every datagram that arrived.
func listen(t *testing.T, plan []fakerecorder.Send, speed float64) [][]byte {
	t.Helper()
	ln, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	// As the client does (ADR 0002): a large receive buffer.
	_ = ln.SetReadBuffer(4 << 20)

	conn, err := fakerecorder.Dial(ln.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	got := make(chan [][]byte, 1)
	go func() {
		var out [][]byte
		buf := make([]byte, 65536)
		for len(out) < len(plan) {
			_ = ln.SetReadDeadline(time.Now().Add(10 * time.Second))
			n, err := ln.Read(buf)
			if err != nil {
				break
			}
			out = append(out, bytes.Clone(buf[:n]))
		}
		got <- out
	}()
	if err := fakerecorder.Replay(context.Background(), conn, plan, speed); err != nil {
		t.Fatal(err)
	}
	return <-got
}

type callKey struct {
	sys      uint16
	num, sec int64
}

type received struct {
	audio     map[callKey][]rp.Audio
	audioIdx  map[callKey][]int // position of each audio datagram in arrival order
	tx        map[callKey][]rp.Transmission
	ends      map[callKey]rp.CallEnd
	beats     []rp.Heartbeat
	malformed []string
	lost      uint32
}

func decodeAll(t *testing.T, dgrams [][]byte) received {
	t.Helper()
	r := received{
		audio: map[callKey][]rp.Audio{}, audioIdx: map[callKey][]int{},
		tx: map[callKey][]rp.Transmission{}, ends: map[callKey]rp.CallEnd{},
	}
	key := func(id rp.Identity) callKey { return callKey{id.SysNum, id.CallNum, id.StartTime} }
	var next uint32
	for i, b := range dgrams {
		if len(b) > rp.MaxDatagram {
			t.Errorf("datagram %d is %d bytes, max %d", i, len(b), rp.MaxDatagram)
		}
		m, err := rp.Decode(b)
		var de *rp.DecodeError
		if errors.As(err, &de) {
			r.malformed = append(r.malformed, de.Reason)
			continue
		}
		if err != nil {
			t.Fatalf("datagram %d: %v", i, err)
		}
		if m.Header.BootID != bootID {
			t.Errorf("datagram %d: boot_id %d", i, m.Header.BootID)
		}
		// One shared counter: any forward jump is loss.
		if m.Header.Seq < next {
			t.Errorf("datagram %d: seq %d went backwards (want >= %d)", i, m.Header.Seq, next)
		}
		r.lost += m.Header.Seq - next
		next = m.Header.Seq + 1
		switch {
		case m.Audio != nil:
			k := key(m.Audio.Identity)
			r.audio[k] = append(r.audio[k], *m.Audio)
			r.audioIdx[k] = append(r.audioIdx[k], i)
		case m.Transmission != nil:
			k := key(m.Transmission.Identity)
			r.tx[k] = append(r.tx[k], *m.Transmission)
		case m.CallEnd != nil:
			r.ends[key(m.CallEnd.Identity)] = *m.CallEnd
		case m.Heartbeat != nil:
			r.beats = append(r.beats, *m.Heartbeat)
		}
	}
	return r
}

func fixture(t *testing.T, names ...string) []byte {
	t.Helper()
	var b []byte
	for _, n := range names {
		p, err := os.ReadFile(filepath.Join(fixtureDir, n))
		if err != nil {
			t.Fatal(err)
		}
		b = append(b, p...)
	}
	return b
}

func pcm(as []rp.Audio) []byte {
	var b []byte
	for _, a := range as {
		b = append(b, a.PCM...)
	}
	return b
}

var (
	callA       = callKey{0, 1, 1764000000}
	callB       = callKey{0, 2, 1764000000}
	callPatch   = callKey{0, 3, 1764000002}
	callCopy    = callKey{0, 4, 1764000002}
	callUnknown = callKey{0, 5, 1764000004}
	callOther   = callKey{1, 6, 1764000004}
)

func TestReplayAcceptanceScenario(t *testing.T) {
	plan := loadPlan(t)
	dgrams := listen(t, plan, 0)
	if len(dgrams) != len(plan) {
		t.Fatalf("listener got %d datagrams, sent %d", len(dgrams), len(plan))
	}
	for i := range plan {
		if !bytes.Equal(dgrams[i], plan[i].Datagram) {
			t.Fatalf("datagram %d arrived changed or out of order", i)
		}
	}
	r := decodeAll(t, dgrams)

	t.Run("gaps and malformed", func(t *testing.T) {
		if r.lost != 3 {
			t.Errorf("lost %d sequence numbers, want 3", r.lost)
		}
		// A bad magic, and an audio datagram at 11,025 Hz, a rate no
		// trunk-recorder recorder produces.
		if !slices.Equal(r.malformed, []string{"magic", "sample_rate"}) {
			t.Errorf("malformed reasons %v, want [magic sample_rate]", r.malformed)
		}
	})

	t.Run("every call's audio arrives whole", func(t *testing.T) {
		want := map[callKey][]string{
			callA:       {"tone-440hz-1s.pcm", "noise-bursts-500ms.pcm"},
			callB:       {"tone-880hz-1500ms.pcm"},
			callPatch:   {"tone-440hz-1s.pcm"},
			callCopy:    {"tone-440hz-1s.pcm"},
			callUnknown: {"noise-bursts-500ms.pcm"},
			callOther:   {"noise-bursts-500ms.pcm"},
		}
		if len(r.audio) != len(want) {
			t.Errorf("audio for %d calls, want %d", len(r.audio), len(want))
		}
		for k, files := range want {
			if !bytes.Equal(pcm(r.audio[k]), fixture(t, files...)) {
				t.Errorf("call %v: PCM differs from %v", k, files)
			}
			for _, a := range r.audio[k] {
				if a.SampleRate != 8000 || a.TDMASlot != -1 {
					t.Errorf("call %v: rate %d slot %d, want P25's 8000 and -1", k, a.SampleRate, a.TDMASlot)
				}
			}
		}
		// Chunking: 800 samples by default, 1600 where the scenario says.
		if n := len(r.audio[callA]); n != 15 {
			t.Errorf("call A: %d packets, want 15", n)
		}
		if n := len(r.audio[callB]); n != 8 {
			t.Errorf("call B: %d packets, want 8", n)
		}
	})

	t.Run("overlapping calls on one talkgroup", func(t *testing.T) {
		a, b := r.audioIdx[callA], r.audioIdx[callB]
		if !(b[0] > a[0] && b[0] < a[len(a)-1]) {
			t.Errorf("call B's audio doesn't start during call A's: A %v, B %v", a, b)
		}
		if r.audio[callA][0].Talkgroup != 1001 || r.audio[callB][0].Talkgroup != 1001 {
			t.Error("A and B aren't both on 1001")
		}
	})

	t.Run("transmissions and call_end", func(t *testing.T) {
		var units []uint32
		var times []int64
		for _, tx := range r.tx[callA] {
			units = append(units, tx.Unit)
			times = append(times, tx.TimeMS)
		}
		if !slices.Equal(units, []uint32{5001, 5123}) || !slices.Equal(times, []int64{epoch, epoch + 1000}) {
			t.Errorf("call A transmissions: units %v at %v", units, times)
		}
		for i, a := range r.audio[callA] {
			want := uint32(5001)
			if i >= 10 {
				want = 5123
			}
			if a.Unit != want {
				t.Errorf("call A packet %d: unit %d, want %d", i, a.Unit, want)
			}
		}
		end, ok := r.ends[callA]
		if !ok {
			t.Fatal("no call_end for call A")
		}
		wantEnd := rp.CallEnd{
			Identity:  rp.Identity{SysNum: 0, CallNum: 1, StartTime: 1764000000, ShortName: "ex"},
			Talkgroup: 1001, StartTimeMS: epoch, StopTimeMS: epoch + 1500,
			Freq: 851012500, TDMASlot: -1, PatchedTalkgroups: []int32{},
			Filename: "/app/media/ex/1001-1764000000_851012500.0-call_1.wav",
			Transmissions: []rp.CallEndTransmission{
				{Unit: 5001, StartTimeMS: epoch, StopTimeMS: epoch + 1000},
				{Unit: 5123, StartTimeMS: epoch + 1000, StopTimeMS: epoch + 1500},
			},
		}
		if !reflect.DeepEqual(end, wantEnd) {
			t.Errorf("call A call_end\n got %+v\nwant %+v", end, wantEnd)
		}
		if len(r.ends) != 6 {
			t.Errorf("%d call_ends, want 6", len(r.ends))
		}
	})

	t.Run("patch and its copy on the same channel", func(t *testing.T) {
		p, c := r.ends[callPatch], r.ends[callCopy]
		if p.Talkgroup != 1001 || !slices.Equal(p.PatchedTalkgroups, []int32{1002}) {
			t.Errorf("patch: TG %d patched %v", p.Talkgroup, p.PatchedTalkgroups)
		}
		if c.Talkgroup != 1002 || !slices.Equal(c.PatchedTalkgroups, []int32{1001}) {
			t.Errorf("copy: TG %d patched %v", c.Talkgroup, c.PatchedTalkgroups)
		}
		if p.Freq != c.Freq || p.TDMASlot != c.TDMASlot {
			t.Errorf("patch and copy on different channels: %d/%d and %d/%d", p.Freq, p.TDMASlot, c.Freq, c.TDMASlot)
		}
		if got := r.tx[callPatch][0].PatchedTalkgroups; !slices.Equal(got, []int32{1002}) {
			t.Errorf("patch transmission: patched %v", got)
		}
		pa, ca := r.audioIdx[callPatch], r.audioIdx[callCopy]
		if !(ca[0] > pa[0] && ca[0] < pa[len(pa)-1]) {
			t.Error("the copy doesn't overlap the patched call")
		}
	})

	t.Run("unknown talkgroup and unattached shortName", func(t *testing.T) {
		if a := r.audio[callUnknown][0]; a.Talkgroup != 4711 || a.ShortName != "ex" {
			t.Errorf("unknown TG call: TG %d on %q", a.Talkgroup, a.ShortName)
		}
		if a := r.audio[callOther][0]; a.ShortName != "other" || a.SysNum != 1 {
			t.Errorf("unattached call: %q sys %d", a.ShortName, a.SysNum)
		}
	})

	t.Run("heartbeats with a 0 decode rate", func(t *testing.T) {
		if len(r.beats) != 3 {
			t.Fatalf("%d heartbeats, want 3", len(r.beats))
		}
		var rates []float64
		for _, hb := range r.beats {
			for _, s := range hb.Systems {
				if s.ShortName == "ex" {
					rates = append(rates, s.DecodeRate)
				}
			}
		}
		if !slices.Equal(rates, []float64{38.5, 0, 0}) {
			t.Errorf("ex decode rates %v, want [38.5 0 0]", rates)
		}
		if r.beats[1].TimeMS != epoch+3000 {
			t.Errorf("second heartbeat at %d", r.beats[1].TimeMS)
		}
	})
}

// The replay keeps the scenario's timing, scaled by speed.
func TestReplayKeepsTiming(t *testing.T) {
	plan := loadPlan(t)
	last := plan[len(plan)-1].At
	if last != 6*time.Second {
		t.Fatalf("scenario ends at %v, want 6s", last)
	}
	start := time.Now()
	if got := listen(t, plan, 30); len(got) != len(plan) {
		t.Fatalf("got %d datagrams, want %d", len(got), len(plan))
	}
	if el := time.Since(start); el < last/30 {
		t.Errorf("replay at speed 30 took %v, want at least %v", el, last/30)
	}
}

// The committed fixture files are exactly what the generator writes.
func TestFixturesReproducible(t *testing.T) {
	for name, want := range fakerecorder.Fixtures() {
		got, err := os.ReadFile(filepath.Join(fixtureDir, name))
		if err != nil {
			t.Fatalf("%v (run: go run ./testing/fakerecorder/cmd/genfixtures)", err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s differs from the generator's output (run: go run ./testing/fakerecorder/cmd/genfixtures)", name)
		}
		if len(want)%2 != 0 || len(want) > 32000 {
			t.Errorf("%s: %d bytes", name, len(want))
		}
	}
}

func TestDialRefusesNonLoopback(t *testing.T) {
	if _, err := fakerecorder.Dial("192.0.2.1:9850"); err == nil {
		t.Fatal("dialled a non-loopback address")
	}
}

func TestLoadScenarioRejects(t *testing.T) {
	for name, js := range map[string]string{
		"unknown call":  `{"calls":{},"events":[{"at_ms":0,"type":"audio","call":"x"}]}`,
		"unknown type":  `{"calls":{},"events":[{"at_ms":0,"type":"nope"}]}`,
		"unknown field": `{"calls":{},"events":[],"extra":1}`,
		"negative time": `{"calls":{},"events":[{"at_ms":-1,"type":"heartbeat"}]}`,
	} {
		if _, err := fakerecorder.LoadScenario(bytes.NewReader([]byte(js))); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
