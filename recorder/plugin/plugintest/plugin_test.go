//go:build linux

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
// to 127.0.0.1, so port 9850 on the host must be free. Linux only.
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
	"syscall"
	"testing"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/recorderclient"
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

// The fixture's config as it is: audioArchive on, recordings kept where
// trunk-recorder wrote them.
func TestPluginAgainstP25Fixture(t *testing.T) {
	dir, msgs := runFixture(t, nil)
	calls := checkRun(t, msgs)
	for i, want := range wantCalls {
		e := checkCall(t, calls[i], want)
		if e == nil {
			continue
		}
		// The recording named in call_end exists (audioArchive is on), and
		// trunk-recorder's own call JSON beside it agrees with call_end.
		if !strings.HasPrefix(e.Filename, "/app/media/ex/") {
			t.Fatalf("call %d: call_end filename %q", e.CallNum, e.Filename)
		}
		wav := checkRecording(t, dir, calls[i], e)
		compareCallJSON(t, fmt.Sprintf("call %d", e.CallNum), strings.TrimSuffix(wav, ".wav")+".json", e)
	}
}

// A recorder shared with another uploader (ADR 0045): audioArchive off, so
// trunk-recorder deletes each recording once call_end returns, and the
// plugin's handoffDir. Every recording must still be there, as the plugin's
// hard link, and trunk-recorder's own name for it must be gone.
func TestPluginHandoffWithAudioArchiveOff(t *testing.T) {
	dir, msgs := runFixture(t, func(dir string, c map[string]any) {
		if err := os.Mkdir(filepath.Join(dir, "media", "handoff"), 0o777); err != nil {
			t.Fatal(err)
		}
		c["systems"].([]any)[0].(map[string]any)["audioArchive"] = false
		c["plugins"].([]any)[0].(map[string]any)["handoffDir"] = "/app/media/handoff"
	})
	calls := checkRun(t, msgs)
	for i, want := range wantCalls {
		e := checkCall(t, calls[i], want)
		if e == nil {
			continue
		}
		label := fmt.Sprintf("call %d", e.CallNum)
		if !strings.HasPrefix(e.Filename, "/app/media/handoff/"+shortName+"_") {
			t.Fatalf("%s: call_end filename %q, want a link in handoffDir", label, e.Filename)
		}
		wav := checkRecording(t, dir, calls[i], e)
		// Nothing but trunk-recorder could have removed its own name, so one
		// link left means trunk-recorder deleted its file, as audioArchive
		// off says, and the link kept the data.
		var st syscall.Stat_t
		if err := syscall.Stat(wav, &st); err != nil || st.Nlink != 1 {
			t.Errorf("%s: %s has %d links (%v), want 1", label, wav, st.Nlink, err)
		}
		// trunk-recorder's call JSON (callLog is on) is still its own, beside
		// where its WAV was.
		jsons, _ := filepath.Glob(filepath.Join(dir, "media", shortName, "*", "*", "*", fmt.Sprintf("*-call_%d.json", e.CallNum)))
		if len(jsons) != 1 {
			t.Errorf("%s: trunk-recorder's call JSON: found %v", label, jsons)
		} else {
			compareCallJSON(t, label, jsons[0], e)
		}
	}
	// trunk-recorder deleted every WAV of its own.
	var left []string
	_ = filepath.WalkDir(filepath.Join(dir, "media", shortName), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".wav") {
			left = append(left, p)
		}
		return nil
	})
	if len(left) != 0 {
		t.Errorf("trunk-recorder left its own WAVs with audioArchive off: %v", left)
	}
	links, _ := os.ReadDir(filepath.Join(dir, "media", "handoff"))
	if len(links) != len(wantCalls) {
		t.Errorf("handoffDir holds %d files, want %d", len(links), len(wantCalls))
	}
	// The plugin says it hands recordings over, and doesn't warn about
	// audioArchive.
	log, _ := os.ReadFile(filepath.Join(dir, "trunk-recorder.log"))
	if !strings.Contains(string(log), "hard-linking each recording into /app/media/handoff") ||
		strings.Contains(string(log), "audioArchive is off") || strings.Contains(string(log), "can't hard-link") {
		t.Errorf("trunk-recorder's log doesn't show the hand-off working:\n%s", log)
	}
}

// runFixture runs trunk-recorder with the plugin against the fixture and
// returns the test's directory and every message the plugin sent. edit, if
// given, changes the fixture's config.json before the run.
func runFixture(t *testing.T, edit func(dir string, config map[string]any)) (string, []recorderproto.Message) {
	t.Helper()
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
	if edit != nil {
		b, err := os.ReadFile(config)
		if err != nil {
			t.Fatal(err)
		}
		var c map[string]any
		if err := json.Unmarshal(b, &c); err != nil {
			t.Fatal(err)
		}
		edit(dir, c)
		if b, err = json.MarshalIndent(c, "", "  "); err != nil {
			t.Fatal(err)
		}
		config = filepath.Join(dir, "config.json")
		if err := os.WriteFile(config, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}

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
	return dir, msgs
}

// checkRun checks what every run shares, and returns the calls in order.
func checkRun(t *testing.T, msgs []recorderproto.Message) []*call {
	t.Helper()
	checkEnvelope(t, msgs)
	calls, heartbeats := group(t, msgs)
	checkHeartbeats(t, heartbeats)
	if len(calls) != len(wantCalls) {
		t.Fatalf("got %d calls, want %d: %+v", len(calls), len(wantCalls), calls)
	}
	return calls
}

// checkRecording checks that the WAV call_end names exists, under the test's
// directory, and returns its path there. The recorder client reads it as it
// would for an upload: trunk-recorder v5.2.1 renders every call's WAV at
// 16 kHz with ffmpeg, while the live audio of a P25 call is 8 kHz (ADR
// 0046), and both must hold the same length of audio, so that neither
// plays at the wrong speed.
func checkRecording(t *testing.T, dir string, c *call, e *recorderproto.CallEnd) string {
	t.Helper()
	if !strings.HasSuffix(e.Filename, fmt.Sprintf("-call_%d.wav", e.CallNum)) {
		t.Fatalf("call %d: call_end filename %q", e.CallNum, e.Filename)
	}
	wav := filepath.Join(dir, "media", strings.TrimPrefix(e.Filename, "/app/media/"))
	if st, err := os.Stat(wav); err != nil || st.Size() <= 44 {
		t.Errorf("call %d: the recording %s: %v", e.CallNum, wav, err)
		return wav
	}
	rate, pcm, err := recorderclient.ReadRecording(wav)
	if err != nil {
		t.Errorf("call %d: the recorder client can't read the recording: %v", e.CallNum, err)
		return wav
	}
	if rate != 16000 {
		t.Errorf("call %d: the recording is %d Hz, want trunk-recorder v5's 16000", e.CallNum, rate)
	}
	live := 0
	for _, n := range c.samples {
		live += n
	}
	liveS, wavS := float64(live)/8000, float64(len(pcm))/float64(rate)
	t.Logf("call %d: %.2f s of live audio at 8 kHz, a %.2f s recording at %d Hz", e.CallNum, liveS, wavS, rate)
	if math.Abs(wavS-liveS) > 0.1*liveS+0.25 {
		t.Errorf("call %d: the recording lasts %.2f s, the live audio %.2f s", e.CallNum, wavS, liveS)
	}
	checkLoudness(t, c, e.CallNum, rate, pcm)
	return wav
}

// heldDown is a recording loudness, in LUFS, well under any real call's (-17
// to -22.5 LUFS): loudnorm held it down to its peak.
const heldDown = -24.0

// checkLoudness checks that the call's live audio, levelled as the recorder
// client levels it, plays about as loud as its recording. trunk-recorder v5
// renders the recording with ffmpeg's loudnorm (to -16 LUFS by default) but
// streams the decoder's own level to the plugin, which for the fixture's
// first call is 6 LU quieter (ADR 0047).
//
// loudnorm also keeps a call's true peak under -0.1 dBTP. When one click
// stands far over a quiet call, as at the start of the fixture's second
// call, it gives the whole call only the gain the click allows and the
// recording stays quiet; the live leveller limits each frame instead, so
// such a call is only logged.
func checkLoudness(t *testing.T, c *call, callNum int64, rate int, rec []int16) {
	t.Helper()
	var live []int16
	for _, a := range c.audio {
		for i := 0; i+1 < len(a.PCM); i += 2 {
			live = append(live, int16(binary.LittleEndian.Uint16(a.PCM[i:])))
		}
	}
	raw := recorderclient.Loudness(live, 8000)
	levelled := recorderclient.Loudness(recorderclient.LevelLive(live, 8000), 8000)
	recorded := recorderclient.Loudness(rec, rate)
	t.Logf("call %d: live audio %.1f LUFS as the plugin sends it, %.1f LUFS levelled; the recording %.1f LUFS",
		callNum, raw, levelled, recorded)
	if recorded < heldDown {
		t.Logf("call %d: loudnorm held the recording down to its peak; not compared", callNum)
		return
	}
	if math.Abs(levelled-recorded) > 3 {
		t.Errorf("call %d: live audio levelled to %.1f LUFS, but the recording is %.1f LUFS: they'd play at different loudness",
			callNum, levelled, recorded)
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

// checkCall checks one call's messages, and returns its call_end (nil if
// there was none).
func checkCall(t *testing.T, c *call, want wantCall) *recorderproto.CallEnd {
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
		return nil
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

	t.Logf("%s: identity %+v, %d audio packets, transmissions %v, call_end %s", label, c.id, len(c.audio), units, filepath.Base(e.Filename))
	return e
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
