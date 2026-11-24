//go:build cgo

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	sys     int64 // the fixture's system
	api     string
	client  *recorderclient.Client
	stopCli context.CancelFunc
}

// testAdmin stands in for admin sign-in (Authentik, not built yet): a
// request with an X-Test-Admin header is from that admin. It's in a _test.go
// file, so no binary contains it.
type testAdmin struct{}

func (testAdmin) Admin(r *http.Request) (string, bool) {
	v := r.Header.Get("X-Test-Admin")
	return v, v != ""
}

// admin calls the admin API and wants code.
func (r *rig) admin(t *testing.T, method, path string, body any, code int) []byte {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, r.api+server.AdminPath+path, rd)
	req.Header.Set("X-Test-Admin", "e2e")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != code {
		t.Fatalf("%s %s = %d %s, want %d", method, path, resp.StatusCode, b, code)
	}
	return b
}

// fixture sets up SPEC.md section 8's test fixture through the admin API:
// system EX (under an ID of its own, as the tests share a database) with
// 1001 and 1002 public, 1003 hidden and 1004 ignored, and this recorder's
// `ex` attached to it, as Bench's is.
func (r *rig) fixture(t *testing.T) {
	t.Helper()
	r.sys = rand.Int64N(1<<52) + 1
	r.admin(t, "POST", "/systems", map[string]any{"id": r.sys, "label": fmt.Sprintf("EX-%d", r.sys), "name": "Example network"}, 201)
	for _, tg := range []struct {
		id                      int
		label, name, tag, state string
	}{
		{1001, "DISP N", "Dispatch North", "dispatch", "public"},
		{1002, "FG 41", "Fireground 41", "fireground", "public"},
		{1003, "ADMIN", "Admin", "admin", "hidden"},
		{1004, "", "", "other", "ignored"},
	} {
		r.admin(t, "PUT", fmt.Sprintf("/systems/%d/talkgroups/%d", r.sys, tg.id),
			map[string]any{"label": tg.label, "name": tg.name, "tag": tg.tag, "state": tg.state}, 201)
	}
	r.admin(t, "PUT", "/recorders/"+url.PathEscape(r.name)+"/attachments/ex", map[string]any{"system_id": r.sys}, 200)
}

// rejections is what the Recorders page and the talkgroup admin will show
// of this rig's rejected calls (R12, R17, R18).
func (r *rig) rejections(t *testing.T) []string {
	t.Helper()
	var all server.Rejections
	if err := json.Unmarshal(r.admin(t, "GET", "/rejections", nil, 200), &all); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, u := range all.Unattached {
		if u.Recorder == r.name {
			out = append(out, u.Message)
		}
	}
	for _, x := range all.Talkgroups {
		if x.SystemID == r.sys {
			out = append(out, x.Message)
		}
	}
	return out
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
	admin, err := server.NewAdmin(st, log, testAdmin{})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(server.New(st, log, server.WithRecorders(rs), server.WithAdmin(admin)))
	t.Cleanup(srv.Close)
	r.api = srv.URL

	key, err := st.AddRecorder(ctx, r.name)
	if err != nil {
		t.Fatal(err)
	}
	r.fixture(t)
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
	state     string     // effective, now (R16)
	txs       [][2]int64 // offset, unit
}

func (r *rig) calls(t *testing.T) []stored {
	t.Helper()
	ctx := context.Background()
	rows, err := r.st.Pool.Query(ctx, `
		SELECT c.id::text, c.call_num, c.talkgroup, c.patched_talkgroups, c.live_frames, c.recording_path,
		       call_state(c.system_id, c.talkgroup, c.patched_talkgroups)::text
		FROM calls c JOIN recorders rec ON rec.id = c.recorder_id
		WHERE rec.name = $1 ORDER BY c.call_num`, r.name)
	if err != nil {
		t.Fatal(err)
	}
	var out []stored
	for rows.Next() {
		var s stored
		if err := rows.Scan(&s.id, &s.callNum, &s.talkgroup, &s.patched, &s.frames, &s.path, &s.state); err != nil {
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
	r.waitRecordings(t, 2)
	// The live socket and the upload are separate connections, so a
	// recording can be stored before its live call_end (which records the
	// frame count) has been processed.
	var cs []stored
	waitFor(t, "both live calls to end", 10*time.Second, func() bool {
		cs = r.calls(t)
		return len(cs) == 2 && cs[0].frames > 0 && cs[1].frames > 0
	})

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

// allGone waits until every WAV in wavs has been deleted.
func allGone(t *testing.T, wavs map[int64]string) {
	t.Helper()
	waitFor(t, "every WAV to be deleted", 10*time.Second, func() bool {
		for _, p := range wavs {
			if _, err := os.Stat(p); !os.IsNotExist(err) {
				return false
			}
		}
		return true
	})
}

// TestAcceptanceScenario replays the whole acceptance scenario: the patched
// copy never reaches the server, the calls from an unattached shortName and
// on an unknown talkgroup are refused, and malformed input changes nothing
// else.
func TestAcceptanceScenario(t *testing.T) {
	r := start(t)
	wavs := r.replay(t, "acceptance.json")
	r.waitRecordings(t, 3)
	r.rejected(t, 2)
	if _, ok := wavs[4]; !ok {
		t.Fatal("the scenario has no WAV for the copy")
	}
	// The stored calls' WAVs are gone once acknowledged, and the dropped
	// copy's (call 4) when its call_end arrived. The client keeps a refused
	// recording's WAV (5 and 6).
	allGone(t, map[int64]string{1: wavs[1], 2: wavs[2], 3: wavs[3], 4: wavs[4]})
	cs := r.calls(t)
	var nums []int64
	for _, c := range cs {
		nums = append(nums, c.callNum)
		if c.callNum == 3 && !slices.Equal(c.patched, []int32{1002}) {
			t.Errorf("patched call: patched talkgroups %v, want [1002]", c.patched)
		}
	}
	if !slices.Equal(nums, []int64{1, 2, 3}) {
		t.Errorf("stored calls %v, want 1, 2, 3 (not the copy, 4, nor 5 and 6, refused)", nums)
	}
	want := []string{r.name + " / other: 1 call rejected, not attached to a system", "TG 4711: 1 call rejected, not in the list"}
	if got := r.rejections(t); !slices.Equal(got, want) {
		t.Errorf("rejections %q, want %q", got, want)
	}
	m := r.client.Metrics()
	if m.Get(recorderclient.MetricPatchCopies) != 1 || m.Get(recorderclient.MetricLost) != 3 ||
		m.Get(recorderclient.MetricMalformed, "reason", "magic") != 1 {
		var b bytes.Buffer
		m.WriteText(&b)
		t.Errorf("client metrics:\n%s", b.String())
	}
}

// rejected waits for n uploads the server refused.
func (r *rig) rejected(t *testing.T, n uint64) {
	t.Helper()
	waitFor(t, fmt.Sprintf("%d refused uploads", n), 30*time.Second, func() bool {
		return r.client.Metrics().Get(recorderclient.MetricUploads, "result", "rejected") >= n
	})
}

// TestA2UnattachedAndUnknown is SPEC.md A2: with auto-create off, calls from
// Bench/other and on talkgroup 4711 aren't stored, and each shows 1 rejected
// call with R12's and R17's wording. With auto-create on, 4711 heard again
// is created public and unreviewed, and its call is stored.
func TestA2UnattachedAndUnknown(t *testing.T) {
	r := start(t)
	r.replay(t, "a2-unattached-unknown.json")
	r.rejected(t, 2)
	if cs := r.calls(t); len(cs) != 0 {
		t.Errorf("stored %+v, want nothing", cs)
	}
	want := []string{r.name + " / other: 1 call rejected, not attached to a system", "TG 4711: 1 call rejected, not in the list"}
	if got := r.rejections(t); !slices.Equal(got, want) {
		t.Errorf("rejections %q, want %q", got, want)
	}

	r.admin(t, "PATCH", fmt.Sprintf("/systems/%d", r.sys), map[string]any{"auto_create": true}, 200)
	r.replay(t, "a2-heard-again.json")
	cs := r.waitRecordings(t, 1)
	if len(cs) != 1 || cs[0].talkgroup != 4711 || cs[0].callNum != 7 || cs[0].state != "public" {
		t.Errorf("stored %+v, want call 7 on 4711, public", cs)
	}
	var tg server.Talkgroup
	if err := json.Unmarshal(r.admin(t, "GET", fmt.Sprintf("/systems/%d/talkgroups/4711", r.sys), nil, 200), &tg); err != nil {
		t.Fatal(err)
	}
	if tg.State != "public" || !tg.Unreviewed {
		t.Errorf("talkgroup 4711 = %+v, want public and unreviewed", tg)
	}
	if got := r.rejections(t); !slices.Equal(got, want) {
		t.Errorf("rejections after auto-create %q, want them unchanged %q", got, want)
	}
}

// TestA3Ignored is SPEC.md A3: a call on 1004 stores nothing, and a
// rejection is counted.
func TestA3Ignored(t *testing.T) {
	r := start(t)
	r.replay(t, "a3-ignored.json")
	r.rejected(t, 1)
	if cs := r.calls(t); len(cs) != 0 {
		t.Errorf("stored %+v, want nothing", cs)
	}
	if got, want := r.rejections(t), []string{"TG 1004: 1 call rejected, ignored"}; !slices.Equal(got, want) {
		t.Errorf("rejections %q, want %q", got, want)
	}
}

// TestA4PatchStorage is the storage half of SPEC.md A4: a call on 1001
// patched with 1002 is stored once, public; one patched with the hidden 1003
// is stored once, and its effective state is hidden. (Who hears them is the
// live feed's, in M2.)
func TestA4PatchStorage(t *testing.T) {
	r := start(t)
	r.replay(t, "a4-patch.json")
	cs := r.waitRecordings(t, 2)
	if len(cs) != 2 {
		t.Fatalf("stored %+v, want 2 calls", cs)
	}
	for i, want := range []struct {
		patched []int32
		state   string
	}{{[]int32{1002}, "public"}, {[]int32{1003}, "hidden"}} {
		c := cs[i]
		if c.talkgroup != 1001 || !slices.Equal(c.patched, want.patched) || c.state != want.state {
			t.Errorf("call %d: talkgroup %d, patched %v, state %s; want 1001, %v, %s",
				c.callNum, c.talkgroup, c.patched, c.state, want.patched, want.state)
		}
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
