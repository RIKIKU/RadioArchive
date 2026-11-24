package server_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
	"github.com/RIKIKU/RadioArchive/internal/gate"
	"github.com/RIKIKU/RadioArchive/internal/public"
	"github.com/RIKIKU/RadioArchive/internal/retention"
	"github.com/RIKIKU/RadioArchive/internal/server"
	"github.com/RIKIKU/RadioArchive/internal/store"
	"github.com/RIKIKU/RadioArchive/internal/store/storetest"
	"github.com/RIKIKU/RadioArchive/web"
)

// sink collects the live frames the server accepts, by call ID.
type sink struct {
	mu     sync.Mutex
	frames map[string][]cp.Frame
}

func (s *sink) Frame(callID string, f cp.Frame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.frames == nil {
		s.frames = map[string][]cp.Frame{}
	}
	s.frames[callID] = append(s.frames[callID], f)
}

func (s *sink) count(callID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.frames[callID])
}

func (s *sink) total() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, f := range s.frames {
		n += len(f)
	}
	return n
}

type env struct {
	st   *store.Store
	rs   *server.Recorders
	api  *server.API
	srv  *httptest.Server
	dir  string
	sink *sink
	sys  int64 // the test's fixture system, once recorder has made it
	// The retention job, on the test's clock and free space. Nothing runs
	// it on a schedule: tests trigger its runs.
	job   *retention.Job
	clock *testClock
	disk  *testDisk
}

func newEnv(t *testing.T) *env {
	t.Helper()
	return newEnvAt(t, "0.1.0-test")
}

// newEnvAt runs a server at serverVersion.
func newEnvAt(t *testing.T, serverVersion string) *env {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	st, err := store.Open(ctx, storetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	e := &env{st: st, dir: t.TempDir(), sink: &sink{}}
	e.rs, err = server.NewRecorders(st, slog.New(slog.DiscardHandler), server.RecordersConfig{
		RecordingsDir: e.dir, Live: e.sink, ServerVersion: serverVersion})
	if err != nil {
		t.Fatal(err)
	}
	go e.rs.Run(ctx)
	select {
	case <-e.rs.Listening():
	case <-time.After(10 * time.Second):
		t.Fatal("key listener never started")
	}
	log := slog.New(slog.DiscardHandler)
	admin, err := server.NewAdmin(st, log, testAdmin{}, e.dir)
	if err != nil {
		t.Fatal(err)
	}
	e.clock, e.disk = &testClock{}, &testDisk{}
	e.job, err = retention.New(retention.Config{Store: st, Dir: e.dir, Log: log, Now: e.clock.now,
		FreeSpace: e.disk.space, Location: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	admin.WithRetention(e.job)
	e.api = server.New(st, log, server.WithRecorders(e.rs), server.WithAdmin(admin),
		server.WithPublic(public.New(gate.New(st, e.dir, log), log)), server.WithApp(web.Serve(appBuild)))
	e.srv = httptest.NewServer(e.api)
	t.Cleanup(func() {
		e.srv.Close()
		cancel()
		st.Close()
	})
	return e
}

// recorder adds a recorder with a name unique to the test run, and the
// test fixture (SPEC.md section 8) for it to feed: a system of its own with
// talkgroups 1001, 1002 (public), 1003 (hidden) and 1004 (ignored), with
// the recorder's `ex` attached to it.
func (e *env) recorder(t *testing.T) (name, key string) {
	t.Helper()
	name = fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano())
	key, err := e.st.AddRecorder(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	e.sys = e.fixture(t)
	e.mustAdmin(t, "PUT", "/recorders/"+url.PathEscape(name)+"/attachments/ex", map[string]any{"system_id": e.sys}, 200)
	return name, key
}

func hdr(key string) http.Header {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+key)
	h.Set(cp.HeaderClientVersion, "0.1.0-test")
	h.Set(cp.HeaderProtocol, "1")
	return h
}

func (e *env) dial(t *testing.T, h http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.srv.URL, "http")+cp.LivePath, &websocket.DialOptions{HTTPHeader: h})
}

// open dials and reads the welcome.
func (e *env) open(t *testing.T, key string) *websocket.Conn {
	t.Helper()
	c, _, err := e.dial(t, hdr(key))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, b, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var w cp.Welcome
	if json.Unmarshal(b, &w) != nil || w.Type != "welcome" || w.Protocol != 1 || w.ServerVersion == "" {
		t.Fatalf("welcome = %s", b)
	}
	return c
}

func send(t *testing.T, c *websocket.Conn, v any) {
	t.Helper()
	var typ websocket.MessageType = websocket.MessageText
	var b []byte
	switch x := v.(type) {
	case []byte:
		typ, b = websocket.MessageBinary, x
	case string:
		b = []byte(x)
	default:
		b, _ = json.Marshal(v)
	}
	if err := c.Write(context.Background(), typ, b); err != nil {
		t.Fatal(err)
	}
}

// closedWith waits for the server to close c and returns the close status
// and reason.
func closedWith(t *testing.T, c *websocket.Conn) (websocket.StatusCode, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		_, _, err := c.Read(ctx)
		if err == nil {
			continue
		}
		var ce websocket.CloseError
		if errors.As(err, &ce) {
			return ce.Code, ce.Reason
		}
		t.Fatalf("socket ended without a close frame: %v", err)
	}
}

var ident = cp.CallID{SysNum: 0, CallNum: 4182, StartTime: 1764000000}

func callStart(id cp.CallID) cp.CallStart {
	return cp.CallStart{Type: "call_start", CallID: id, ShortName: "ex", Talkgroup: 1001, Unit: 5001,
		PatchedTalkgroups: []int32{}, StartTimeMS: 1764000000120, FirstPCMMS: 1764000000415}
}

func frame(id cp.CallID, seq uint32) []byte {
	return cp.AppendFrame(nil, cp.Frame{CallID: id, Seq: seq, Packet: []byte{0x08, 1, 2}})
}

type callRow struct {
	id         string
	frames     int64
	talkgroup  int32
	patched    []int32
	path       *string
	size       *int64
	txs        [][2]int64 // offset, unit
	retiredRec bool
}

func (e *env) call(t *testing.T, recorder string, id cp.CallID) (callRow, bool) {
	t.Helper()
	ctx := context.Background()
	var r callRow
	err := e.st.Pool.QueryRow(ctx, `
		SELECT c.id::text, c.live_frames, c.talkgroup, c.patched_talkgroups, c.recording_path, c.recording_size,
		       r.retired_at IS NOT NULL
		FROM calls c JOIN recorders r ON r.id = c.recorder_id
		WHERE r.name = $1 AND c.sys_num = $2 AND c.call_num = $3 AND c.start_time = $4`,
		recorder, int32(id.SysNum), id.CallNum, id.StartTime).
		Scan(&r.id, &r.frames, &r.talkgroup, &r.patched, &r.path, &r.size, &r.retiredRec)
	if err != nil {
		return r, false
	}
	rows, err := e.st.Pool.Query(ctx, `SELECT offset_ms, unit FROM transmissions WHERE call_id = $1 ORDER BY offset_ms`, r.id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var o int32
		var u int64
		if err := rows.Scan(&o, &u); err != nil {
			t.Fatal(err)
		}
		r.txs = append(r.txs, [2]int64{int64(o), u})
	}
	return r, true
}

func (e *env) waitCall(t *testing.T, recorder string, id cp.CallID, ok func(callRow) bool) callRow {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		r, found := e.call(t, recorder, id)
		if found && ok(r) {
			return r
		}
		if time.Now().After(deadline) {
			t.Fatalf("call %+v never got there; last %+v (found %v)", id, r, found)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestIntegrationLiveRefusals(t *testing.T) {
	e := newEnv(t)
	_, key := e.recorder(t)
	for _, tt := range []struct {
		name string
		h    http.Header
		code int
		body string
	}{
		{"no key", http.Header{cp.HeaderProtocol: {"1"}}, 401, `"invalid_client_key"`},
		{"unknown key", hdr("rak_nope"), 401, `"invalid_client_key"`},
		{"protocol 2", func() http.Header { h := hdr(key); h.Set(cp.HeaderProtocol, "2"); return h }(), 426,
			`{"error":"unsupported_protocol","protocol":2,"supported":[1]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, resp, err := e.dial(t, tt.h)
			if err == nil || resp == nil {
				t.Fatalf("dial succeeded")
			}
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != tt.code || !strings.Contains(string(body), tt.body) {
				t.Errorf("got %d %s, want %d %s", resp.StatusCode, body, tt.code, tt.body)
			}
		})
	}
}

// TestIntegrationA29 is SPEC.md A29: a server at 1.4 refuses a 1.2 client,
// naming both versions, and counts it; a 1.3 client connects and is
// recorded as out of date; a 1.4 client is current. An upload from a 1.2
// client is refused the same way.
func TestIntegrationA29(t *testing.T) {
	e := newEnvAt(t, "1.4.0")
	name, key := e.recorder(t)
	withVersion := func(v string) http.Header { h := hdr(key); h.Set(cp.HeaderClientVersion, v); return h }

	_, resp, err := e.dial(t, withVersion("1.2.0"))
	if err == nil || resp == nil {
		t.Fatal("a 1.2 client connected")
	}
	body, _ := io.ReadAll(resp.Body)
	var er cp.ErrorResponse
	if resp.StatusCode != 426 || json.Unmarshal(body, &er) != nil || er.Error != "client_too_old" ||
		er.ClientVersion != "1.2.0" || er.ServerVersion != "1.4.0" {
		t.Errorf("1.2 client: %d %s, want 426 client_too_old naming 1.2.0 and 1.4.0", resp.StatusCode, body)
	}
	if n := e.rs.Stat.ClientsTooOld.Load(); n != 1 {
		t.Errorf("too-old count = %d, want 1", n)
	}

	clientRow := func() (v *string, out bool) {
		if err := e.st.Pool.QueryRow(context.Background(),
			`SELECT client_version, client_out_of_date FROM recorders WHERE name = $1`, name).Scan(&v, &out); err != nil {
			t.Fatal(err)
		}
		return v, out
	}
	if v, _ := clientRow(); v != nil {
		t.Errorf("a refused client's version was recorded: %s", *v)
	}

	c, _, err := e.dial(t, withVersion("1.3.0"))
	if err != nil {
		t.Fatalf("a 1.3 client was refused: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := c.Read(ctx); err != nil {
		t.Fatal(err)
	}
	if v, out := clientRow(); v == nil || *v != "1.3.0" || !out {
		t.Errorf("1.3 client recorded as %v, out of date %v; want 1.3.0, true", v, out)
	}
	if n := e.rs.Stat.ClientsOutOfDate.Load(); n != 1 {
		t.Errorf("out-of-date count = %d, want 1", n)
	}
	c.CloseNow()

	c, _, err = e.dial(t, withVersion("1.4.2"))
	if err != nil {
		t.Fatalf("a 1.4 client was refused: %v", err)
	}
	if _, _, err := c.Read(ctx); err != nil {
		t.Fatal(err)
	}
	if v, out := clientRow(); v == nil || *v != "1.4.2" || out {
		t.Errorf("1.4 client recorded as %v, out of date %v; want 1.4.2, false", v, out)
	}
	c.CloseNow()

	// Uploads are checked the same way (ADR 0002).
	audio := oggBytes(1)
	req := uploadRequest(t, e.srv.URL, withVersion("1.2.0"), recording(ident, audio), audio)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 426 || !strings.Contains(string(body), `"client_too_old"`) {
		t.Errorf("upload from 1.2: %d %s", resp.StatusCode, body)
	}
	if n := e.rs.Stat.ClientsTooOld.Load(); n != 2 {
		t.Errorf("too-old count = %d, want 2", n)
	}
}

func TestNewRecordersNeedsAVersion(t *testing.T) {
	if _, err := server.NewRecorders(nil, slog.New(slog.DiscardHandler), server.RecordersConfig{
		RecordingsDir: t.TempDir(), ServerVersion: "dev"}); err == nil {
		t.Error("a server version that isn't MAJOR.MINOR.PATCH was accepted")
	}
}

func TestIntegrationLiveCall(t *testing.T) {
	e := newEnv(t)
	name, key := e.recorder(t)
	c := e.open(t, key)

	other := cp.CallID{SysNum: 0, CallNum: 99, StartTime: 1764000000}
	send(t, c, frame(other, 0)) // no call_start: dropped, not a close
	send(t, c, callStart(ident))
	send(t, c, cp.Transmission{Type: "transmission", CallID: ident, Unit: 5001, OffsetMS: 0})
	for i := range 10 {
		send(t, c, frame(ident, uint32(i)))
	}
	send(t, c, cp.Transmission{Type: "transmission", CallID: ident, Unit: 5123, OffsetMS: 4830})
	send(t, c, `{"type":"heartbeat","client_version":"0.1.0-test","systems":[]}`)
	send(t, c, `{"type":"from_the_future","x":1}`)
	send(t, c, cp.CallEnd{Type: "call_end", CallID: ident, StopTimeMS: 1764000009870, PatchedTalkgroups: []int32{1002}})

	r := e.waitCall(t, name, ident, func(r callRow) bool { return r.frames == 10 })
	if len(r.txs) != 2 || r.txs[0] != [2]int64{0, 5001} || r.txs[1] != [2]int64{4830, 5123} {
		t.Errorf("transmissions = %v", r.txs)
	}
	if len(r.patched) != 1 || r.patched[0] != 1002 || r.talkgroup != 1001 {
		t.Errorf("call = %+v", r)
	}
	if n := e.sink.count(r.id); n != 10 {
		t.Errorf("live sink got %d frames for the call, want 10", n)
	}
	if e.rs.Stat.FramesUnstarted.Load() < 1 || e.rs.Stat.UnknownTypes.Load() < 1 {
		t.Errorf("unstarted %d, unknown types %d", e.rs.Stat.FramesUnstarted.Load(), e.rs.Stat.UnknownTypes.Load())
	}
}

func TestIntegrationLiveMalformed(t *testing.T) {
	e := newEnv(t)
	_, key := e.recorder(t)
	for _, tt := range []struct {
		name   string
		msg    any
		code   websocket.StatusCode
		reason string
	}{
		{"not JSON", "{nope", websocket.StatusInvalidFramePayloadData, "not a JSON object"},
		{"short binary", []byte{1, 0, 0}, websocket.StatusInvalidFramePayloadData, "shorter than 24"},
		{"missing field", `{"type":"call_start","sys_num":0,"call_num":1,"start_time":1,"talkgroup":1,"start_time_ms":1}`,
			websocket.StatusPolicyViolation, "missing field short_name"},
		{"wrong type", `{"type":"call_end","sys_num":"zero","call_num":1,"start_time":1}`,
			websocket.StatusPolicyViolation, "invalid field sys_num"},
		{"oversized packet", append(frame(ident, 0), make([]byte, 1300)...), websocket.StatusPolicyViolation, "opus packet"},
		{"text over 64 KB", `{"type":"heartbeat","pad":"` + strings.Repeat("x", 70<<10) + `"}`,
			websocket.StatusPolicyViolation, "over 65536"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := e.open(t, key)
			send(t, c, tt.msg)
			code, reason := closedWith(t, c)
			if code != tt.code || !strings.Contains(reason, tt.reason) {
				t.Errorf("closed %d %q, want %d %q", code, reason, tt.code, tt.reason)
			}
		})
	}
}

// upload posts a recording as the client does.
func (e *env) upload(t *testing.T, key string, meta cp.RecordingMetadata, audio []byte) (int, cp.UploadResponse) {
	t.Helper()
	resp, err := http.DefaultClient.Do(uploadRequest(t, e.srv.URL, hdr(key), meta, audio))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var ur cp.UploadResponse
	_ = json.NewDecoder(resp.Body).Decode(&ur)
	return resp.StatusCode, ur
}

func uploadRequest(t *testing.T, base string, hd http.Header, meta cp.RecordingMetadata, audio []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="metadata"`)
	h.Set("Content-Type", "application/json")
	p, _ := mw.CreatePart(h)
	_ = json.NewEncoder(p).Encode(meta)
	h = textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="audio"; filename="r.opus"`)
	h.Set("Content-Type", "audio/ogg")
	p, _ = mw.CreatePart(h)
	_, _ = p.Write(audio)
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, base+cp.RecordingsPath, &body)
	req.Header = hd
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func recording(id cp.CallID, audio []byte) cp.RecordingMetadata {
	sum := sha256.Sum256(audio)
	return cp.RecordingMetadata{CallID: id, ShortName: "ex", Talkgroup: 1001, PatchedTalkgroups: []int32{1002},
		StartTimeMS: 1764000000120, StopTimeMS: 1764000009870,
		Transmissions: []cp.RecordingTransmission{{Unit: 5001, OffsetMS: 292, DurationMS: 3788}, {Unit: 5123, OffsetMS: 4830, DurationMS: 4920}},
		SHA256:        hex.EncodeToString(sum[:]), Size: int64(len(audio))}
}

func oggBytes(seed byte) []byte { return append([]byte("OggS"), bytes.Repeat([]byte{seed}, 500)...) }

func TestIntegrationUpload(t *testing.T) {
	e := newEnv(t)
	name, key := e.recorder(t)

	// The live call reaches the server first, with one live transmission.
	c := e.open(t, key)
	send(t, c, callStart(ident))
	send(t, c, cp.Transmission{Type: "transmission", CallID: ident, Unit: 1, OffsetMS: 7})
	send(t, c, frame(ident, 0))
	send(t, c, cp.CallEnd{Type: "call_end", CallID: ident, StopTimeMS: 1764000009000, PatchedTalkgroups: []int32{}})
	live := e.waitCall(t, name, ident, func(r callRow) bool { return r.frames == 1 })

	audio := oggBytes(1)
	meta := recording(ident, audio)
	code, resp := e.upload(t, key, meta, audio)
	if code != 201 || resp.Status != "stored" || resp.SHA256 != meta.SHA256 || resp.Size != meta.Size {
		t.Fatalf("first upload: %d %+v", code, resp)
	}
	// Matched to the live call exactly by identity: the same call ID.
	if resp.CallID != live.id {
		t.Errorf("recording stored on call %s, live call is %s", resp.CallID, live.id)
	}
	r, _ := e.call(t, name, ident)
	if r.path == nil || *r.size != meta.Size || len(r.txs) != 2 || r.txs[0] != [2]int64{292, 5001} || r.frames != 1 {
		t.Errorf("call after upload = %+v (the recording's transmissions replace the live ones)", r)
	}
	b, err := os.ReadFile(filepath.Join(e.dir, filepath.FromSlash(*r.path)))
	if err != nil || !bytes.Equal(b, audio) {
		t.Errorf("stored file: %v", err)
	}
	if !strings.Contains(*r.path, resp.CallID) {
		t.Errorf("file %s isn't named by its opaque call ID", *r.path)
	}

	// A retry after a lost answer.
	code, resp = e.upload(t, key, meta, audio)
	if code != 200 || resp.Status != "duplicate" || resp.CallID != live.id {
		t.Errorf("second upload: %d %+v", code, resp)
	}
	// Another file for the same identity.
	other := oggBytes(2)
	code, resp = e.upload(t, key, recording(ident, other), other)
	if code != 409 || resp.Error != "identity_conflict" || resp.SHA256 != meta.SHA256 {
		t.Errorf("conflicting upload: %d %+v", code, resp)
	}
	var calls, files int
	_ = e.st.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM calls c JOIN recorders r ON r.id = c.recorder_id WHERE r.name = $1`, name).Scan(&calls)
	_ = filepath.WalkDir(e.dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files++
		}
		return nil
	})
	if calls != 1 || files != 1 {
		t.Errorf("%d calls and %d files, want 1 and 1", calls, files)
	}

	// A recording whose live call never arrived makes its own call.
	lone := cp.CallID{SysNum: 0, CallNum: 7, StartTime: 1764000100}
	a := oggBytes(3)
	if code, resp := e.upload(t, key, recording(lone, a), a); code != 201 || resp.CallID == live.id {
		t.Errorf("lone upload: %d %+v", code, resp)
	}
}

func TestIntegrationUploadRefusals(t *testing.T) {
	e := newEnv(t)
	_, key := e.recorder(t)
	audio := oggBytes(1)
	id := cp.CallID{CallNum: 1, StartTime: 1}

	bad := recording(id, audio)
	bad.Size++
	if code, resp := e.upload(t, key, bad, audio); code != 400 || resp.Error != "size_mismatch" {
		t.Errorf("size mismatch: %d %+v", code, resp)
	}
	bad = recording(id, audio)
	bad.SHA256 = strings.Repeat("0", 64)
	if code, resp := e.upload(t, key, bad, audio); code != 400 || resp.Error != "sha256_mismatch" {
		t.Errorf("sha mismatch: %d %+v", code, resp)
	}
	notOgg := []byte("RIFF....WAVE")
	if code, resp := e.upload(t, key, recording(id, notOgg), notOgg); code != 415 {
		t.Errorf("not Ogg: %d %+v", code, resp)
	}
	if code, _ := e.upload(t, "rak_nope", recording(id, audio), audio); code != 401 {
		t.Errorf("bad key: %d", code)
	}
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+cp.RecordingsPath, strings.NewReader("{}"))
	req.Header = hdr(key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 415 {
		t.Errorf("JSON body: %d, want 415", resp.StatusCode)
	}
	var n int
	_ = e.st.Pool.QueryRow(context.Background(), `SELECT count(*) FROM calls WHERE call_num = 1 AND start_time = 1`).Scan(&n)
	if n != 0 {
		t.Errorf("a refused upload stored %d calls", n)
	}
}

func TestIntegrationRotateKey(t *testing.T) {
	e := newEnv(t)
	name, old := e.recorder(t)
	c := e.open(t, old)
	send(t, c, callStart(ident))
	e.waitCall(t, name, ident, func(callRow) bool { return true })

	key, err := e.st.RotateRecorderKey(context.Background(), name)
	if err != nil || key == old {
		t.Fatal(err)
	}
	// The live socket that used the old key closes at once.
	start := time.Now()
	code, reason := closedWith(t, c)
	if code != websocket.StatusPolicyViolation || reason != "client key revoked" {
		t.Errorf("closed %d %q, want 1008 \"client key revoked\"", code, reason)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("took %s to close", d)
	}
	if _, resp, err := e.dial(t, hdr(old)); err == nil || resp.StatusCode != 401 {
		t.Error("the old key still connects")
	}
	audio := oggBytes(4)
	if code, _ := e.upload(t, old, recording(ident, audio), audio); code != 401 {
		t.Errorf("old key upload: %d, want 401", code)
	}
	// The new key works, on the same recorder.
	e.open(t, key)
	if code, resp := e.upload(t, key, recording(ident, audio), audio); code != 201 {
		t.Errorf("new key upload: %d %+v", code, resp)
	}
}

func TestIntegrationRetire(t *testing.T) {
	e := newEnv(t)
	name, key := e.recorder(t)
	c := e.open(t, key)
	send(t, c, callStart(ident))
	audio := oggBytes(5)
	if code, _ := e.upload(t, key, recording(ident, audio), audio); code != 201 {
		t.Fatal("upload failed")
	}

	if err := e.st.RetireRecorderByName(context.Background(), name); err != nil {
		t.Fatal(err)
	}
	if code, reason := closedWith(t, c); code != websocket.StatusPolicyViolation || reason != "client key revoked" {
		t.Errorf("closed %d %q", code, reason)
	}
	if _, resp, err := e.dial(t, hdr(key)); err == nil || resp.StatusCode != 401 {
		t.Error("a retired recorder still connects")
	}
	if code, _ := e.upload(t, key, recording(ident, audio), audio); code != 401 {
		t.Errorf("retired upload: %d, want 401", code)
	}
	// Its calls stay.
	r, ok := e.call(t, name, ident)
	if !ok || r.path == nil || !r.retiredRec {
		t.Errorf("call after retiring = %+v, found %v", r, ok)
	}
	if err := e.st.RetireRecorderByName(context.Background(), name); err != store.ErrNoActiveRecorder {
		t.Errorf("retiring twice: %v", err)
	}
	if _, err := e.st.RotateRecorderKey(context.Background(), name); err != store.ErrNoActiveRecorder {
		t.Errorf("rotating a retired recorder: %v", err)
	}
}

func TestIntegrationOneConnectionPerKey(t *testing.T) {
	e := newEnv(t)
	_, key := e.recorder(t)
	first := e.open(t, key)
	e.open(t, key)
	if code, reason := closedWith(t, first); code != websocket.StatusPolicyViolation || !strings.Contains(reason, "replaced") {
		t.Errorf("first socket closed %d %q", code, reason)
	}
}
