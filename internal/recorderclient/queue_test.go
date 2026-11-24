package recorderclient

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
)

// clock is a test clock: real time plus an offset the test moves on.
type clock struct {
	mu  sync.Mutex
	off time.Duration
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().Add(c.off)
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.off += d
	c.mu.Unlock()
}

func rec(callNum int64, wav string, queued time.Time) Recording {
	return Recording{Meta: cp.RecordingMetadata{CallID: cp.CallID{CallNum: callNum, StartTime: 1764000000}, ShortName: "ex",
		PatchedTalkgroups: []int32{}, Transmissions: []cp.RecordingTransmission{}}, WAV: wav, Queued: queued}
}

func entryNums(es []*entry) []int64 {
	var out []int64
	for _, e := range es {
		out = append(out, e.Meta.CallNum)
	}
	return out
}

// TestQueueReopen: entries survive a restart, in the order they were
// queued, with their metadata; a write a crash interrupted is removed, and
// an unreadable entry is moved aside and counted.
func TestQueueReopen(t *testing.T) {
	dir := t.TempDir()
	log := slog.New(slog.DiscardHandler)
	q, err := openQueue(dir, log, NewMetrics(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	for i, n := range []int64{3, 1, 2} {
		r := rec(n, "/media/"+string(rune('a'+i))+".wav", t0.Add(time.Duration(n)*time.Second))
		r.Meta.Talkgroup = 1000 + int32(n)
		q.add(r)
	}
	// Two in the same instant keep the order they were added in.
	q.add(rec(10, "/media/x.wav", t0.Add(time.Hour)))
	q.add(rec(11, "/media/y.wav", t0.Add(time.Hour)))
	_ = os.WriteFile(filepath.Join(dir, "0001-half-written.json.tmp"), []byte(`{"form`), 0o640)
	_ = os.WriteFile(filepath.Join(dir, "0002-garbage.json"), []byte(`not json`), 0o640)

	m := NewMetrics()
	q2, err := openQueue(dir, log, m, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if got := entryNums(q2.entries); !slices.Equal(got, []int64{1, 2, 3, 10, 11}) {
		t.Fatalf("reopened queue = %v, want 1, 2, 3, 10, 11", got)
	}
	e := q2.entries[0]
	if e.WAV != "/media/b.wav" || e.Meta.Talkgroup != 1001 || !e.Queued.Equal(t0.Add(time.Second)) || e.file == "" {
		t.Errorf("entry = %+v", e)
	}
	if _, err := os.Stat(filepath.Join(dir, "0001-half-written.json.tmp")); !os.IsNotExist(err) {
		t.Error("half-written entry not removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "0002-garbage.json.bad")); err != nil {
		t.Error("unreadable entry not moved aside")
	}
	if m.Get(MetricQueueErrors, "op", "read") != 1 {
		t.Error("unreadable entry not counted")
	}
	q2.remove(e)
	q3, _ := openQueue(dir, log, NewMetrics(), time.Now)
	if got := entryNums(q3.entries); !slices.Equal(got, []int64{2, 3, 10, 11}) {
		t.Errorf("after remove and reopen = %v", got)
	}
}

func TestQueueState(t *testing.T) {
	c := &clock{}
	q, _ := openQueue(t.TempDir(), slog.New(slog.DiscardHandler), NewMetrics(), c.now)
	if s := q.state(); s != (cp.QueueState{}) {
		t.Errorf("empty queue = %+v", s)
	}
	q.add(rec(1, "/a.wav", c.now()))
	c.advance(30 * time.Minute)
	q.add(rec(2, "/b.wav", c.now()))
	c.advance(30 * time.Minute)
	if s := q.state(); s.Count != 2 || s.OldestAgeS < 3600 || s.OldestAgeS > 3601 {
		t.Errorf("state = %+v, want 2 entries, oldest 1 h", s)
	}
}

// orderServer stores uploads like the real server: the first upload of an
// identity is stored, a repeat with the same SHA-256 is a duplicate. While
// down, it answers 503. With dropFirst, it stores the first upload and then
// cuts the connection without answering, as a crash or network failure
// after the commit would.
type orderServer struct {
	mu        sync.Mutex
	down      bool
	refused   int // uploads answered 503 while down
	dropFirst bool
	stored    map[int64]string // call_num -> sha256
	order     []int64          // call_nums as stored
	attempts  []string         // sha256 of every upload received
}

func (s *orderServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	mr := multipart.NewReader(r.Body, params["boundary"])
	p, _ := mr.NextPart()
	var meta cp.RecordingMetadata
	_ = json.NewDecoder(p).Decode(&meta)
	p, _ = mr.NextPart()
	_, _ = io.Copy(io.Discard, p)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.down {
		s.refused++
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	s.attempts = append(s.attempts, meta.SHA256)
	if sum, ok := s.stored[meta.CallNum]; ok {
		code, resp := http.StatusOK, cp.UploadResponse{Status: cp.StatusDuplicate, SHA256: sum}
		if sum != meta.SHA256 {
			code, resp = http.StatusConflict, cp.UploadResponse{Error: "identity_conflict", SHA256: sum}
		}
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(resp)
		return
	}
	s.stored[meta.CallNum] = meta.SHA256
	s.order = append(s.order, meta.CallNum)
	if s.dropFirst {
		s.dropFirst = false
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
		return
	}
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(cp.UploadResponse{Status: cp.StatusStored, SHA256: meta.SHA256})
}

func (s *orderServer) get() (order []int64, attempts []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.order), slices.Clone(s.attempts)
}

// TestUploaderOutageOldestFirst is A28 at the unit level, on the real disk
// queue with a test clock: while the server is down for an hour, recordings
// queue up; when it's back they arrive oldest first, and an entry older
// than QUEUE_MAX_AGE is dropped and counted, its WAV deleted.
func TestUploaderOutageOldestFirst(t *testing.T) {
	srv := &orderServer{down: true, stored: map[int64]string{}}
	hs := httptest.NewServer(srv)
	defer hs.Close()
	c := &clock{}
	media := t.TempDir()
	u := newTestUploader(t, hs.URL, hs.Client(), t.TempDir(), c.now)
	var mu sync.Mutex
	var left []outcome
	u.done = func(_ Recording, o outcome) { mu.Lock(); left = append(left, o); mu.Unlock() }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go u.run(ctx)

	old := writeWAV(t, media, "old.wav")
	u.Enqueue(rec(100, old, c.now()))
	waitUntil(t, "an upload refused while the server is down", 10*time.Second, func() bool {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		return srv.refused > 0
	})
	c.advance(23*time.Hour + 30*time.Minute)
	var wavs []string
	for i := range 10 {
		w := writeWAV(t, media, "call"+string(rune('a'+i))+".wav")
		wavs = append(wavs, w)
		u.Enqueue(rec(int64(i+1), w, c.now()))
		c.advance(6 * time.Minute) // ten calls over the hour
	}
	if s := u.Queue(); s.Count != 11 {
		t.Fatalf("queue = %+v during the outage, want 11", s)
	}

	srv.mu.Lock()
	srv.down = false
	srv.mu.Unlock()
	u.Kick() // as the live socket's reconnect does
	waitUntil(t, "the queue to empty", 20*time.Second, func() bool { return u.Queue().Count == 0 })

	order, _ := srv.get()
	if !slices.Equal(order, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}) {
		t.Errorf("stored in order %v, want 1-10 oldest first, without the expired 100", order)
	}
	if n := u.m.Get(MetricQueueExpired); n != 1 {
		t.Errorf("expired = %d, want 1", n)
	}
	if exists(old) {
		t.Error("the expired entry's WAV was kept; a given-up recording's WAV is deleted")
	}
	for _, w := range wavs {
		if exists(w) {
			t.Errorf("%s still there after the server acknowledged it", w)
		}
	}
	if g := u.m.Gauge(MetricQueueEntries); g != 0 {
		t.Errorf("queue gauge = %v", g)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(left) != 11 || left[0] != outExpired || slices.Contains(left[1:], outExpired) {
		t.Errorf("outcomes %v, want the expired entry first and only once", left)
	}
}

// TestUploaderLostAckAcrossRestart: the server stores an upload but the
// answer is lost, and the client stops before it retries. The restarted
// client reopens the queue, encodes the WAV again to the same bytes, gets
// "duplicate", and clears the entry and the WAV.
func TestUploaderLostAckAcrossRestart(t *testing.T) {
	srv := &orderServer{dropFirst: true, stored: map[int64]string{}}
	hs := httptest.NewServer(srv)
	defer hs.Close()
	dir := t.TempDir()
	wav := writeWAV(t, t.TempDir(), "call.wav")

	u := newTestUploader(t, hs.URL, hs.Client(), dir, time.Now)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { u.run(ctx); close(done) }()
	u.Enqueue(rec(7, wav, time.Now()))
	waitUntil(t, "the first upload", 10*time.Second, func() bool { _, a := srv.get(); return len(a) == 1 })
	cancel() // the client stops (or crashes) before its retry
	<-done
	if files, _ := os.ReadDir(dir); len(files) != 1 || !strings.HasSuffix(files[0].Name(), ".json") || !exists(wav) {
		t.Fatalf("after the lost answer: queue %v, WAV exists %v; want the entry and the WAV kept", files, exists(wav))
	}

	u2 := newTestUploader(t, hs.URL, hs.Client(), dir, time.Now)
	got := make(chan outcome, 1)
	u2.done = func(_ Recording, o outcome) { got <- o }
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go u2.run(ctx2)
	select {
	case o := <-got:
		if o != outDuplicate {
			t.Errorf("outcome after restart %q, want duplicate", o)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the restarted client never sent the queued entry")
	}
	_, attempts := srv.get()
	if len(attempts) != 2 || attempts[0] != attempts[1] {
		t.Errorf("SHA-256 of the two uploads: %v; want the same twice", attempts)
	}
	if exists(wav) {
		t.Error("WAV not deleted after the duplicate acknowledgement")
	}
	if files, _ := os.ReadDir(dir); len(files) != 0 {
		t.Errorf("queue directory holds %v", files)
	}
}

func waitUntil(t *testing.T, what string, d time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
