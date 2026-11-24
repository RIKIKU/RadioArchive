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

// TestUploaderRejections: a call the server refuses on purpose (ADR 0003)
// leaves the queue with its WAV, counted by reason. An unexpected refusal
// leaves the queue but keeps its WAV for investigation, and is counted.
func TestUploaderRejections(t *testing.T) {
	for _, tt := range []struct {
		code      int
		resp      cp.UploadResponse
		want      outcome
		keptWAV   bool
		metric    string
		label     string
		labelName string
	}{
		{200, cp.UploadResponse{Status: cp.StatusRejected, Reason: "not_attached"}, outRejected, false, MetricQueueRejected, "reason", "not_attached"},
		{200, cp.UploadResponse{Status: cp.StatusRejected, Reason: "unknown_talkgroup"}, outRejected, false, MetricQueueRejected, "reason", "unknown_talkgroup"},
		{200, cp.UploadResponse{Status: cp.StatusRejected, Reason: "ignored_talkgroup"}, outRejected, false, MetricQueueRejected, "reason", "ignored_talkgroup"},
		{200, cp.UploadResponse{Status: cp.StatusRejected, Reason: "something_new"}, outRejectedOther, true, MetricQueueRefused, "result", "rejected_unexpected"},
		{409, cp.UploadResponse{Error: "identity_conflict", SHA256: "other"}, outConflict, true, MetricQueueRefused, "result", "conflict"},
		{422, cp.UploadResponse{Error: "unprocessable"}, outInvalid, true, MetricQueueRefused, "result", "invalid"},
	} {
		t.Run(string(tt.want)+"/"+tt.labelName, func(t *testing.T) {
			hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(tt.code)
				_ = json.NewEncoder(w).Encode(tt.resp)
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
			select {
			case o := <-got:
				if o != tt.want {
					t.Fatalf("outcome %q, want %q", o, tt.want)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("no outcome")
			}
			if exists(wav) != tt.keptWAV {
				t.Errorf("WAV kept = %v, want %v", exists(wav), tt.keptWAV)
			}
			if files, _ := os.ReadDir(dir); len(files) != 0 || u.Queue().Count != 0 {
				t.Errorf("entry left: %v", files)
			}
			if n := u.m.Get(tt.metric, tt.label, tt.labelName); n != 1 {
				t.Errorf("%s{%s=%q} = %d, want 1", tt.metric, tt.label, tt.labelName, n)
			}
		})
	}
}
