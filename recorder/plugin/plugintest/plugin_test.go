// Package plugintest runs the real trunk-recorder, with the recorder plugin,
// against the synthetic P25 fixture (recorder/plugin/fixture/p25gen.cc) and
// checks what the plugin sends over UDP against ADR 0002, decoding every
// datagram with internal/recorderproto, the recorder client's decoder.
//
// It needs Docker, the plugin image and the p25gen binary, so it only runs
// when RADIOARCHIVE_PLUGIN_IMAGE names the image:
//
//	docker buildx build recorder/plugin -t radioarchive-recorder:dev --load
//	docker buildx build recorder/plugin --target p25gen --output type=local,dest=out
//	RADIOARCHIVE_PLUGIN_IMAGE=radioarchive-recorder:dev RADIOARCHIVE_P25GEN=$PWD/out/p25gen \
//	  go test -count=1 -v ./recorder/plugin/plugintest
//
// The container uses the host's network, because the plugin only ever sends
// to 127.0.0.1, so port 9850 on the host must be free.
package plugintest

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/recorderproto"
)

// The fixture's scenario, as p25gen.cc's header comment describes it.
const (
	shortName = "ex"
	voiceFreq = 851050000
	port      = 9850
)

type wantTx struct {
	unit    uint32
	seconds float64 // planned length of the transmission's voice
}

type wantCall struct {
	talkgroup int32
	patched   []int32
	txs       []wantTx
}

var wantCalls = []wantCall{
	{talkgroup: 1001, patched: []int32{}, txs: []wantTx{{5001, 2.9}, {5123, 2.2}}},
	{talkgroup: 1002, patched: []int32{1001}, txs: []wantTx{{5101, 2.2}}},
}

// call collects one call's messages, in the order they arrived.
type call struct {
	id            recorderproto.Identity
	order         []string // "tx:<unit>", "audio:<unit>" (once per run of a unit), "end"
	audio         []*recorderproto.Audio
	samples       map[uint32]int
	transmissions []*recorderproto.Transmission
	end           *recorderproto.CallEnd
}

func TestPluginAgainstP25Fixture(t *testing.T) {
	image := os.Getenv("RADIOARCHIVE_PLUGIN_IMAGE")
	if image == "" {
		t.Skip("RADIOARCHIVE_PLUGIN_IMAGE isn't set")
	}
	gen := os.Getenv("RADIOARCHIVE_P25GEN")
	if gen == "" {
		t.Fatal("RADIOARCHIVE_P25GEN must name the p25gen binary")
	}

	dir := t.TempDir()
	for _, d := range []string{"fixture", "media", "tmp"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o777); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command(gen, filepath.Join(dir, "fixture")).CombinedOutput(); err != nil {
		t.Fatalf("p25gen: %v\n%s", err, out)
	}
	_, self, _, _ := runtime.Caller(0)
	config := filepath.Join(filepath.Dir(self), "..", "fixture", "config.json")

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		t.Fatalf("listen on 127.0.0.1:%d: %v", port, err)
	}
	defer conn.Close()
	_ = conn.SetReadBuffer(4 << 20)

	// trunk-recorder exits by itself about 10 s after the fixture ends.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	name := fmt.Sprintf("radioarchive-plugintest-%d", os.Getpid())
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "--name", name, "--network", "host",
		"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		"-v", config+":/app/config.json:ro",
		"-v", filepath.Join(dir, "fixture")+":/app/fixture:ro",
		"-v", filepath.Join(dir, "media")+":/app/media",
		"-v", filepath.Join(dir, "tmp")+":/app/tmp",
		image)
	logPath := filepath.Join(dir, "trunk-recorder.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", name).Run()
		if t.Failed() {
			b, _ := os.ReadFile(logPath)
			t.Logf("trunk-recorder's log:\n%s", b)
		}
	})
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	var msgs []recorderproto.Message
	buf := make([]byte, 65536)
	running := true
	for running {
		select {
		case err := <-exited:
			running = false
			if err != nil && ctx.Err() != nil {
				t.Fatalf("trunk-recorder didn't exit within the time limit")
			}
		default:
		}
		_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		for {
			n, _, err := conn.ReadFromUDP(buf)
			if err != nil {
				break
			}
			m, err := recorderproto.Decode(buf[:n])
			if err != nil {
				t.Fatalf("datagram %d: %v", len(msgs), err)
			}
			msgs = append(msgs, m)
		}
	}
	if len(msgs) == 0 {
		t.Fatal("the plugin sent nothing")
	}

	checkEnvelope(t, msgs)
	calls, heartbeats := group(t, msgs)
	checkHeartbeats(t, heartbeats)
	if len(calls) != len(wantCalls) {
		t.Fatalf("got %d calls, want %d: %+v", len(calls), len(wantCalls), calls)
	}
	for i, want := range wantCalls {
		checkCall(t, dir, calls[i], want)
	}
}

// One boot, and every sequence number from 0 with none missing.
func checkEnvelope(t *testing.T, msgs []recorderproto.Message) {
	t.Helper()
	boot := msgs[0].Header.BootID
	for i, m := range msgs {
		if m.Header.BootID != boot {
			t.Fatalf("message %d: boot_id %d, then %d", i, boot, m.Header.BootID)
		}
		if m.Header.Seq != uint32(i) {
			t.Fatalf("message %d has seq %d: messages were lost or reordered", i, m.Header.Seq)
		}
	}
	t.Logf("%d messages, boot_id %d, seq 0-%d with no gaps", len(msgs), boot, len(msgs)-1)
}

func group(t *testing.T, msgs []recorderproto.Message) ([]*call, []*recorderproto.Heartbeat) {
	t.Helper()
	var calls []*call
	var heartbeats []*recorderproto.Heartbeat
	find := func(id recorderproto.Identity) *call {
		for _, c := range calls {
			if c.id == id {
				return c
			}
		}
		c := &call{id: id, samples: map[uint32]int{}}
		calls = append(calls, c)
		return c
	}
	note := func(c *call, s string) {
		if len(c.order) == 0 || c.order[len(c.order)-1] != s {
			c.order = append(c.order, s)
		}
	}
	for _, m := range msgs {
		switch {
		case m.Audio != nil:
			c := find(m.Audio.Identity)
			if c.end != nil {
				t.Errorf("call %+v: audio after call_end", c.id)
			}
			c.audio = append(c.audio, m.Audio)
			c.samples[m.Audio.Unit] += len(m.Audio.PCM) / 2
			note(c, fmt.Sprintf("audio:%d", m.Audio.Unit))
		case m.Transmission != nil:
			c := find(m.Transmission.Identity)
			c.transmissions = append(c.transmissions, m.Transmission)
			note(c, fmt.Sprintf("tx:%d", m.Transmission.Unit))
		case m.CallEnd != nil:
			c := find(m.CallEnd.Identity)
			if c.end != nil {
				t.Errorf("call %+v: two call_ends", c.id)
			}
			c.end = m.CallEnd
			note(c, "end")
		case m.Heartbeat != nil:
			heartbeats = append(heartbeats, m.Heartbeat)
		}
	}
	return calls, heartbeats
}

func checkHeartbeats(t *testing.T, hbs []*recorderproto.Heartbeat) {
	t.Helper()
	// About every 3 s for the 40-odd seconds trunk-recorder runs.
	if len(hbs) < 8 {
		t.Errorf("got %d heartbeats, want at least 8", len(hbs))
	}
	decoding := 0
	for _, hb := range hbs {
		if hb.PluginVersion != "0.1.0" {
			t.Errorf("heartbeat plugin_version %q", hb.PluginVersion)
		}
		if len(hb.Systems) != 1 || hb.Systems[0].ShortName != shortName || hb.Systems[0].SystemType != "p25" || hb.Systems[0].SysNum != 0 {
			t.Fatalf("heartbeat systems %+v", hb.Systems)
		}
		if hb.Systems[0].DecodeRate > 10 {
			decoding++
		}
	}
	// The fixture sends 40 TSBKs a second while it runs, then nothing.
	if decoding < 5 {
		t.Errorf("only %d heartbeats show the control channel decoding", decoding)
	}
	if last := hbs[len(hbs)-1].Systems[0].DecodeRate; last != 0 {
		t.Errorf("last heartbeat's decode_rate is %v after the fixture ended, want 0", last)
	}
}

func checkCall(t *testing.T, dir string, c *call, want wantCall) {
	t.Helper()
	label := fmt.Sprintf("call %d (talkgroup %d)", c.id.CallNum, want.talkgroup)
	if c.id.ShortName != shortName || c.id.SysNum != 0 || c.id.StartTime < 1_700_000_000 {
		t.Errorf("%s: identity %+v", label, c.id)
	}

	// The order of events: each unit's transmission message before its
	// audio, units in turn, call_end last.
	var wantOrder []string
	for _, tx := range want.txs {
		wantOrder = append(wantOrder, fmt.Sprintf("tx:%d", tx.unit), fmt.Sprintf("audio:%d", tx.unit))
	}
	wantOrder = append(wantOrder, "end")
	if !slices.Equal(c.order, wantOrder) {
		t.Errorf("%s: messages in the order %v, want %v", label, c.order, wantOrder)
	}

	// Audio: every packet carries the full identity and the call's channel.
	var energy float64
	var n int
	for _, a := range c.audio {
		if a.Talkgroup != want.talkgroup || a.Freq != voiceFreq || a.TDMASlot != -1 || a.SampleRate != 8000 {
			t.Fatalf("%s: audio packet %+v", label, a)
		}
		for i := 0; i+1 < len(a.PCM); i += 2 {
			s := float64(int16(binary.LittleEndian.Uint16(a.PCM[i:])))
			energy += s * s
			n++
		}
	}
	if rms := math.Sqrt(energy / float64(max(n, 1))); rms < 100 {
		t.Errorf("%s: audio RMS %.0f; the fixture's tones should decode to more than silence", label, rms)
	}
	for _, tx := range want.txs {
		got := float64(c.samples[tx.unit]) / 8000
		if got < 0.8*tx.seconds || got > 1.3*tx.seconds {
			t.Errorf("%s: unit %d: %.2f s of audio, want about %.1f s", label, tx.unit, got, tx.seconds)
		}
	}

	for _, tx := range c.transmissions {
		if tx.Talkgroup != want.talkgroup || tx.Freq != voiceFreq || tx.TDMASlot != -1 || !slices.Equal(nonNil(tx.PatchedTalkgroups), want.patched) {
			t.Errorf("%s: transmission %+v", label, tx)
		}
		if tx.TimeMS/1000 < c.id.StartTime-1 {
			t.Errorf("%s: transmission at %d ms, before the call started at %d s", label, tx.TimeMS, c.id.StartTime)
		}
	}

	e := c.end
	if e == nil {
		t.Errorf("%s: no call_end", label)
		return
	}
	if e.Talkgroup != want.talkgroup || e.Freq != voiceFreq || e.TDMASlot != -1 || !slices.Equal(nonNil(e.PatchedTalkgroups), want.patched) {
		t.Errorf("%s: call_end %+v", label, e)
	}
	var units []uint32
	for _, tx := range e.Transmissions {
		units = append(units, tx.Unit)
		if tx.StopTimeMS <= tx.StartTimeMS {
			t.Errorf("%s: call_end transmission %+v", label, tx)
		}
	}
	var wantUnits []uint32
	for _, tx := range want.txs {
		wantUnits = append(wantUnits, tx.unit)
	}
	if !slices.Equal(units, wantUnits) {
		t.Errorf("%s: call_end transmissions by %v, want %v", label, units, wantUnits)
	}
	if e.StartTimeMS < c.id.StartTime*1000 || e.StopTimeMS <= e.StartTimeMS {
		t.Errorf("%s: call_end times %d-%d ms for a call started at %d s", label, e.StartTimeMS, e.StopTimeMS, c.id.StartTime)
	}

	// The recording named in call_end exists (audioArchive is on), and
	// trunk-recorder's own call JSON beside it agrees with call_end.
	if !strings.HasPrefix(e.Filename, "/app/media/") || !strings.HasSuffix(e.Filename, fmt.Sprintf("-call_%d.wav", c.id.CallNum)) {
		t.Fatalf("%s: call_end filename %q", label, e.Filename)
	}
	wav := filepath.Join(dir, "media", strings.TrimPrefix(e.Filename, "/app/media/"))
	if st, err := os.Stat(wav); err != nil || st.Size() <= 44 {
		t.Errorf("%s: the recording %s: %v", label, wav, err)
	}
	compareCallJSON(t, label, strings.TrimSuffix(wav, ".wav")+".json", e)
	t.Logf("%s: identity %+v, %d audio packets, transmissions %v, call_end %s", label, c.id, len(c.audio), units, filepath.Base(e.Filename))
}

// trunk-recorder's call JSON (callLog), the same comparison made by hand on
// a real recorder.
func compareCallJSON(t *testing.T, label, path string, e *recorderproto.CallEnd) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("%s: trunk-recorder's call JSON: %v", label, err)
		return
	}
	var j struct {
		Talkgroup   int32   `json:"talkgroup"`
		Freq        uint32  `json:"freq"`
		StartTimeMS int64   `json:"start_time_ms"`
		StopTimeMS  int64   `json:"stop_time_ms"`
		ShortName   string  `json:"short_name"`
		Patched     []int32 `json:"patched_talkgroups"`
		SrcList     []struct {
			Src uint32 `json:"src"`
		} `json:"srcList"`
	}
	if err := json.Unmarshal(b, &j); err != nil {
		t.Errorf("%s: %s: %v", label, path, err)
		return
	}
	var src []uint32
	for _, s := range j.SrcList {
		src = append(src, s.Src)
	}
	var units []uint32
	for _, tx := range e.Transmissions {
		units = append(units, tx.Unit)
	}
	// The call JSON lists the call's own talkgroup among its patched ones.
	var patched []int32
	for _, tg := range j.Patched {
		if tg != j.Talkgroup {
			patched = append(patched, tg)
		}
	}
	if j.Talkgroup != e.Talkgroup || j.Freq != e.Freq || j.StartTimeMS != e.StartTimeMS || j.StopTimeMS != e.StopTimeMS ||
		j.ShortName != e.ShortName || !slices.Equal(src, units) || !slices.Equal(nonNil(patched), nonNil(e.PatchedTalkgroups)) {
		t.Errorf("%s: call_end %+v doesn't match trunk-recorder's call JSON %s", label, e, b)
	}
}

func nonNil(s []int32) []int32 {
	if s == nil {
		return []int32{}
	}
	return s
}
