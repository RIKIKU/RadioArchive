package recorderclient

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
)

// TestUploaderKeepsEntryWhileTooOld: a client_too_old refusal (426) removes
// nothing. The entry and its WAV wait, and go up once the server accepts
// the client (here, as if it were upgraded).
func TestUploaderKeepsEntryWhileTooOld(t *testing.T) {
	var mu sync.Mutex
	accept, refused := false, 0
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(r.Body, params["boundary"])
		p, _ := mr.NextPart()
		var meta cp.RecordingMetadata
		_ = json.NewDecoder(p).Decode(&meta)
		mu.Lock()
		defer mu.Unlock()
		if !accept {
			refused++
			w.WriteHeader(http.StatusUpgradeRequired)
			_ = json.NewEncoder(w).Encode(cp.ErrorResponse{Error: cp.ErrClientTooOld, ClientVersion: "1.2.0", ServerVersion: "1.4.0"})
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(cp.UploadResponse{Status: cp.StatusStored, SHA256: meta.SHA256})
	}))
	defer hs.Close()
	dir := t.TempDir()
	wav := writeWAV(t, t.TempDir(), "call.wav")
	u := newTestUploader(t, hs.URL, hs.Client(), dir, time.Now)
	got := make(chan outcome, 1)
	u.done = func(_ Recording, o outcome) { got <- o }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go u.run(ctx)
	u.Enqueue(rec(1, wav, time.Now()))
	waitUntil(t, "the refusal", 10*time.Second, func() bool { mu.Lock(); defer mu.Unlock(); return refused > 0 })
	time.Sleep(200 * time.Millisecond)
	if files, _ := os.ReadDir(dir); len(files) != 1 || u.Queue().Count != 1 || !exists(wav) {
		t.Fatalf("after client_too_old: queue %d, files %v, WAV %v; want the entry and the WAV kept",
			u.Queue().Count, files, exists(wav))
	}
	select {
	case o := <-got:
		t.Fatalf("the entry left the queue (%s) on client_too_old", o)
	default:
	}

	mu.Lock()
	accept = true
	mu.Unlock()
	u.Kick()
	select {
	case o := <-got:
		if o != outStored {
			t.Errorf("outcome %q, want stored", o)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("never uploaded after the client was accepted")
	}
	if exists(wav) || u.Queue().Count != 0 {
		t.Error("WAV or entry left after the upload")
	}
}

// TestUploaderPermanentRefusal: a rejected recording leaves the queue but
// keeps its WAV, and is counted.
func TestUploaderPermanentRefusal(t *testing.T) {
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(cp.UploadResponse{Status: cp.StatusRejected, Reason: "not_attached"})
	}))
	defer hs.Close()
	wav := writeWAV(t, t.TempDir(), "call.wav")
	u := newTestUploader(t, hs.URL, hs.Client(), t.TempDir(), time.Now)
	got := make(chan outcome, 1)
	u.done = func(_ Recording, o outcome) { got <- o }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go u.run(ctx)
	u.Enqueue(rec(1, wav, time.Now()))
	select {
	case o := <-got:
		if o != outRejected {
			t.Fatalf("outcome %q, want rejected", o)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no outcome")
	}
	if !exists(wav) || u.Queue().Count != 0 {
		t.Error("want the entry gone and the WAV kept")
	}
	if n := u.m.Get(MetricQueueRefused, "result", "rejected"); n != 1 {
		t.Errorf("refused{rejected} = %d, want 1", n)
	}
}
