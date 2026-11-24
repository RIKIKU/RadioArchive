//go:build cgo

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
	"github.com/RIKIKU/RadioArchive/internal/opus"
	"github.com/RIKIKU/RadioArchive/internal/recorderclient"
	"github.com/RIKIKU/RadioArchive/internal/server"
	"github.com/RIKIKU/RadioArchive/internal/store"
	"github.com/RIKIKU/RadioArchive/internal/store/storetest"
	"github.com/RIKIKU/RadioArchive/testing/fakerecorder"
)

// sink records the live frames the server accepted, by call ID.
type sink struct {
	mu     sync.Mutex
	frames map[string][]cp.Frame
}

func (s *sink) Frame(callID string, f cp.Frame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frames[callID] = append(s.frames[callID], f)
}

func (s *sink) get(callID string) []cp.Frame {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.frames[callID])
}

type rig struct {
	st      *store.Store
	sink    *sink
	dir     string // recordings volume
	name    string
	client  *recorderclient.Client
	stopCli context.CancelFunc
}

func newEncoder(rate int) (recorderclient.Encoder, error) {
	e, err := opus.NewEncoder(rate)
	if err != nil {
		return nil, err
	}
	return e, nil
}

func waitFor(t *testing.T, what string, d time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// start runs the server and a recorder client for a new recorder.
func start(t *testing.T) *rig {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	st, err := store.Open(ctx, storetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	// The key listener holds a connection until ctx ends, so cancel first.
	t.Cleanup(func() { cancel(); st.Close() })
	r := &rig{st: st, sink: &sink{frames: map[string][]cp.Frame{}}, dir: t.TempDir(),
		name: fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano())}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	rs, err := server.NewRecorders(st, log, server.RecordersConfig{RecordingsDir: r.dir, Live: r.sink, ServerVersion: "e2e"})
	if err != nil {
		t.Fatal(err)
	}
	go rs.Run(ctx)
	<-rs.Listening()
	srv := httptest.NewServer(server.New(st, log, server.WithRecorders(rs)))
	t.Cleanup(srv.Close)

	key, err := st.AddRecorder(ctx, r.name)
	if err != nil {
		t.Fatal(err)
	}
	r.client, err = recorderclient.New(recorderclient.Config{ServerURL: srv.URL, ClientKey: key,
		UDPAddr: "127.0.0.1:0", NewEncoder: newEncoder, Log: log})
	if err != nil {
		t.Fatal(err)
	}
	cctx, stop := context.WithCancel(ctx)
	r.stopCli = stop
	done := make(chan struct{})
	go func() { _ = r.client.Run(cctx); close(done) }()
	t.Cleanup(func() { stop(); <-done })
	waitFor(t, "the client's live connection", 10*time.Second, r.client.LiveConnected)
	return r
}

// replay writes the scenario's WAVs where its call_ends say (moved under a
// temporary directory), then sends it to the client in real time.
func (r *rig) replay(t *testing.T, scenario string) map[int64]string {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "fakerecorder", "testdata", "scenarios", scenario))
	if err != nil {
		t.Fatal(err)
	}
	s, err := fakerecorder.LoadScenario(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	media := t.TempDir()
	wavs := map[int64]string{}
	for i, e := range s.Events {
		if e.Filename != "" {
			s.Events[i].Filename = filepath.Join(media, filepath.FromSlash(e.Filename))
			wavs[s.Calls[e.Call].CallNum] = s.Events[i].Filename
		}
	}
	fixtures := os.DirFS(filepath.Join("..", "fakerecorder", "testdata", "fixtures"))
	if err := fakerecorder.WriteRecordings(s, fixtures, ""); err != nil {
		t.Fatal(err)
	}
	plan, err := fakerecorder.Plan(s, fixtures)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := fakerecorder.Dial(r.client.UDPAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := fakerecorder.Replay(context.Background(), conn, plan, 1); err != nil {
		t.Fatal(err)
	}
	return wavs
}

type stored struct {
	id        string
	callNum   int64
	talkgroup int32
	patched   []int32
	frames    int64
	path      *string
	txs       [][2]int64 // offset, unit
}

func (r *rig) calls(t *testing.T) []stored {
	t.Helper()
	ctx := context.Background()
	rows, err := r.st.Pool.Query(ctx, `
		SELECT c.id::text, c.call_num, c.talkgroup, c.patched_talkgroups, c.live_frames, c.recording_path
		FROM calls c JOIN recorders rec ON rec.id = c.recorder_id
		WHERE rec.name = $1 ORDER BY c.call_num`, r.name)
	if err != nil {
		t.Fatal(err)
	}
	var out []stored
	for rows.Next() {
		var s stored
		if err := rows.Scan(&s.id, &s.callNum, &s.talkgroup, &s.patched, &s.frames, &s.path); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	rows.Close()
	for i := range out {
		trs, err := r.st.Pool.Query(ctx, `SELECT offset_ms, unit FROM transmissions WHERE call_id = $1 ORDER BY offset_ms`, out[i].id)
		if err != nil {
			t.Fatal(err)
		}
		for trs.Next() {
			var o int32
			var u int64
			_ = trs.Scan(&o, &u)
			out[i].txs = append(out[i].txs, [2]int64{int64(o), u})
		}
		trs.Close()
	}
	return out
}

func (r *rig) waitRecordings(t *testing.T, n int) []stored {
	t.Helper()
	var cs []stored
	waitFor(t, fmt.Sprintf("%d recordings", n), 30*time.Second, func() bool {
		cs = r.calls(t)
		got := 0
		for _, c := range cs {
			if c.path != nil {
				got++
			}
		}
		return got >= n
	})
	return cs
}

// TestA1ExactMatch is SPEC.md A1: two overlapping calls on talkgroup 1001
// give 2 calls, each linked to its live frames by the same (recorder,
// sys_num, call_num, start_time), each with its own transmissions.
func TestA1ExactMatch(t *testing.T) {
	r := start(t)
	wavs := r.replay(t, "a1-overlapping.json")
	cs := r.waitRecordings(t, 2)

	if len(cs) != 2 || cs[0].callNum != 1 || cs[1].callNum != 2 {
		t.Fatalf("calls = %+v, want call_num 1 and 2", cs)
	}
	wantTx := map[int64][][2]int64{1: {{0, 5001}, {1000, 5123}}, 2: {{0, 5100}}}
	for _, c := range cs {
		if c.talkgroup != 1001 {
			t.Errorf("call %d: talkgroup %d", c.callNum, c.talkgroup)
		}
		// Live frames: 1.5 s of audio each, 20 ms a frame. All of them
		// reached the server under this call's ID, carrying its identity.
		frames := r.sink.get(c.id)
		if c.frames != 75 || len(frames) != 75 {
			t.Errorf("call %d: %d live frames stored, %d seen by the live sink; want 75", c.callNum, c.frames, len(frames))
		}
		for i, f := range frames {
			if f.CallNum != c.callNum || f.StartTime != 1764000000 || f.SysNum != 0 || f.Seq != uint32(i) {
				t.Fatalf("call %d: frame %d = %+v", c.callNum, i, f.CallID)
			}
		}
		if !slices.Equal(c.txs, wantTx[c.callNum]) {
			t.Errorf("call %d: transmissions %v, want %v", c.callNum, c.txs, wantTx[c.callNum])
		}
		// The recording is on the same row, as an Ogg/Opus file named by
		// the call's opaque ID; trunk-recorder's WAV is gone.
		b, err := os.ReadFile(filepath.Join(r.dir, filepath.FromSlash(*c.path)))
		if err != nil || !bytes.HasPrefix(b, []byte("OggS")) || !bytes.Contains(b[:200], []byte("OpusHead")) {
			t.Errorf("call %d: recording %s: %v", c.callNum, *c.path, err)
		}
		if !strings.Contains(*c.path, c.id) {
			t.Errorf("call %d: file %s isn't named by its call ID", c.callNum, *c.path)
		}
		if _, err := os.Stat(wavs[c.callNum]); !os.IsNotExist(err) {
			t.Errorf("call %d: WAV still there after the server acknowledged it", c.callNum)
		}
	}
	if cs[0].id == cs[1].id {
		t.Error("both calls have one ID")
	}
}

// TestAcceptanceScenario replays the whole acceptance scenario: the patched
// copy never reaches the server, and malformed input changes nothing else.
func TestAcceptanceScenario(t *testing.T) {
	r := start(t)
	r.replay(t, "acceptance.json")
	cs := r.waitRecordings(t, 5)
	var nums []int64
	for _, c := range cs {
		nums = append(nums, c.callNum)
		if c.callNum == 3 && !slices.Equal(c.patched, []int32{1002}) {
			t.Errorf("patched call: patched talkgroups %v, want [1002]", c.patched)
		}
	}
	if !slices.Equal(nums, []int64{1, 2, 3, 5, 6}) {
		t.Errorf("stored calls %v, want 1, 2, 3, 5, 6 (not the copy, 4)", nums)
	}
	m := r.client.Metrics()
	if m.Get(recorderclient.MetricPatchCopies) != 1 || m.Get(recorderclient.MetricLost) != 3 ||
		m.Get(recorderclient.MetricMalformed, "reason", "magic") != 1 {
		var b bytes.Buffer
		m.WriteText(&b)
		t.Errorf("client metrics:\n%s", b.String())
	}
}

// TestRotateKeyDisconnectsClient: rotating the key closes the running
// client's live socket at once, and it can't get back in with the old key.
func TestRotateKeyDisconnectsClient(t *testing.T) {
	r := start(t)
	if _, err := r.st.RotateRecorderKey(context.Background(), r.name); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the client to be disconnected", 3*time.Second, func() bool { return !r.client.LiveConnected() })
	time.Sleep(1500 * time.Millisecond) // past the first reconnect
	if r.client.LiveConnected() {
		t.Error("the client reconnected with its old key")
	}
}

// TestRetireDisconnectsClient: a retired recorder's client is cut off and
// its calls stay.
func TestRetireDisconnectsClient(t *testing.T) {
	r := start(t)
	r.replay(t, "a1-overlapping.json")
	r.waitRecordings(t, 2)
	if err := r.st.RetireRecorderByName(context.Background(), r.name); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the client to be disconnected", 3*time.Second, func() bool { return !r.client.LiveConnected() })
	if cs := r.calls(t); len(cs) != 2 {
		t.Errorf("%d calls after retiring, want 2", len(cs))
	}
}
