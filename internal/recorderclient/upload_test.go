package recorderclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
	"github.com/RIKIKU/RadioArchive/testing/fakerecorder"
)

func TestReadWAV(t *testing.T) {
	pcm := []byte{1, 0, 2, 0, 0xff, 0xff}
	rate, got, err := readWAV(bytes.NewReader(fakerecorder.WAV(8000, pcm)))
	if err != nil || rate != 8000 || len(got) != 3 || got[0] != 1 || got[2] != -1 {
		t.Fatalf("readWAV = %d, %v, %v", rate, got, err)
	}
	// A data chunk whose size was never filled in runs to the end.
	b := fakerecorder.WAV(8000, pcm)
	binary.LittleEndian.PutUint32(b[40:], 0)
	if _, got, err := readWAV(bytes.NewReader(b)); err != nil || len(got) != 3 {
		t.Fatalf("unfinished data chunk: %v, %v", got, err)
	}
	// Stereo is refused.
	b = fakerecorder.WAV(8000, pcm)
	b[22] = 2
	if _, _, err := readWAV(bytes.NewReader(b)); err == nil {
		t.Error("stereo accepted")
	}
	if _, _, err := readWAV(bytes.NewReader([]byte("not a wav"))); err == nil {
		t.Error("garbage accepted")
	}
}

func TestEncodeOggOpus(t *testing.T) {
	pcm := make([]int16, 8000+80) // 1.01 s: 51 frames, the last half padding
	ogg, err := encodeOggOpus(newFake, 1, 8000, pcm)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(ogg, []byte("OggS")) || !bytes.Contains(ogg, []byte("OpusHead")) {
		t.Fatal("not Ogg/Opus")
	}
	// The last page's granule: pre-skip 52*6 plus 8080 samples at 48 kHz.
	last := bytes.LastIndex(ogg, []byte("OggS"))
	if g := binary.LittleEndian.Uint64(ogg[last+6:]); g != 52*6+8080*6 {
		t.Errorf("final granule %d, want %d", g, 52*6+8080*6)
	}
	if ogg[last+5]&0x04 == 0 {
		t.Error("last page not marked end of stream")
	}
}

// fakeServer answers uploads with the scripted responses in turn, checking
// each request as the server would.
type fakeServer struct {
	t       *testing.T
	answers []func(meta cp.RecordingMetadata, sum string) (int, cp.UploadResponse)
	n       atomic.Int32
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer rak_test" || r.Header.Get(cp.HeaderProtocol) != "1" {
		f.t.Errorf("headers = %v", r.Header)
	}
	_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	mr := multipart.NewReader(r.Body, params["boundary"])
	p, _ := mr.NextPart()
	var meta cp.RecordingMetadata
	if p.FormName() != "metadata" || json.NewDecoder(p).Decode(&meta) != nil {
		f.t.Error("first part isn't the metadata")
	}
	p, _ = mr.NextPart()
	if p.FormName() != "audio" || p.Header.Get("Content-Type") != "audio/ogg" {
		f.t.Error("second part isn't audio/ogg")
	}
	audio, _ := io.ReadAll(p)
	sum := sha256.Sum256(audio)
	if hex.EncodeToString(sum[:]) != meta.SHA256 || int64(len(audio)) != meta.Size {
		f.t.Error("metadata's sha256 or size doesn't match the audio")
	}
	i := int(f.n.Add(1)) - 1
	code, resp := f.answers[min(i, len(f.answers)-1)](meta, meta.SHA256)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(resp)
}

func runUpload(t *testing.T, answers ...func(cp.RecordingMetadata, string) (int, cp.UploadResponse)) (outcome, string, *fakeServer) {
	t.Helper()
	fs := &fakeServer{t: t, answers: answers}
	srv := httptest.NewServer(fs)
	defer srv.Close()
	wav := writeWAV(t, t.TempDir(), "call.wav")
	u := newTestUploader(t, srv.URL, srv.Client(), t.TempDir(), time.Now)
	got := make(chan outcome, 1)
	u.done = func(_ Recording, o outcome) { got <- o }
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go u.run(ctx)
	u.Enqueue(Recording{Meta: cp.RecordingMetadata{CallID: cp.CallID{CallNum: 1}, ShortName: "ex"}, WAV: wav, Queued: time.Now()})
	select {
	case o := <-got:
		if q := u.Queue(); q.Count != 0 {
			t.Errorf("queue = %+v after the entry left", q)
		}
		if left, _ := os.ReadDir(u.q.dir); len(left) != 0 {
			t.Errorf("queue directory holds %d files after the entry left", len(left))
		}
		return o, wav, fs
	case <-ctx.Done():
		t.Fatal("upload never finished")
		return "", "", nil
	}
}

func answer(code int, resp cp.UploadResponse) func(cp.RecordingMetadata, string) (int, cp.UploadResponse) {
	return func(_ cp.RecordingMetadata, sum string) (int, cp.UploadResponse) {
		if resp.SHA256 == "ours" {
			resp.SHA256 = sum
		}
		return code, resp
	}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestUploadOutcomes(t *testing.T) {
	tests := []struct {
		name    string
		answers []func(cp.RecordingMetadata, string) (int, cp.UploadResponse)
		want    outcome
		deleted bool
		tries   int32
	}{
		{"stored: the WAV is deleted", []func(cp.RecordingMetadata, string) (int, cp.UploadResponse){
			answer(201, cp.UploadResponse{Status: cp.StatusStored, CallID: "x", SHA256: "ours"})}, outStored, true, 1},
		{"duplicate with our hash: the WAV is deleted", []func(cp.RecordingMetadata, string) (int, cp.UploadResponse){
			answer(200, cp.UploadResponse{Status: cp.StatusDuplicate, SHA256: "ours"})}, outDuplicate, true, 1},
		{"rejected: kept", []func(cp.RecordingMetadata, string) (int, cp.UploadResponse){
			answer(200, cp.UploadResponse{Status: cp.StatusRejected, Reason: "not_attached"})}, outRejected, false, 1},
		{"conflict: kept", []func(cp.RecordingMetadata, string) (int, cp.UploadResponse){
			answer(409, cp.UploadResponse{Error: "identity_conflict", SHA256: "other"})}, outConflict, false, 1},
		{"400: not retried", []func(cp.RecordingMetadata, string) (int, cp.UploadResponse){
			answer(400, cp.UploadResponse{Error: "size_mismatch"})}, outInvalid, false, 1},
		{"503 then stored: retried", []func(cp.RecordingMetadata, string) (int, cp.UploadResponse){
			answer(503, cp.UploadResponse{Error: "unavailable"}),
			answer(201, cp.UploadResponse{Status: cp.StatusStored, SHA256: "ours"})}, outStored, true, 2},
		{"stored with another hash: retried", []func(cp.RecordingMetadata, string) (int, cp.UploadResponse){
			answer(201, cp.UploadResponse{Status: cp.StatusStored, SHA256: "other"}),
			answer(200, cp.UploadResponse{Status: cp.StatusDuplicate, SHA256: "ours"})}, outDuplicate, true, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o, wav, fs := runUpload(t, tt.answers...)
			if o != tt.want {
				t.Errorf("outcome %q, want %q", o, tt.want)
			}
			if exists(wav) == tt.deleted {
				t.Errorf("WAV exists = %v, want %v", exists(wav), !tt.deleted)
			}
			if n := fs.n.Load(); n != tt.tries {
				t.Errorf("%d attempts, want %d", n, tt.tries)
			}
		})
	}
}

// writeWAV writes 0.2 s of 8 kHz silence, as trunk-recorder would.
func writeWAV(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, fakerecorder.WAV(8000, make([]byte, 3200)), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func newTestUploader(t *testing.T, base string, hc *http.Client, dir string, now func() time.Time) *uploader {
	t.Helper()
	m := NewMetrics()
	q, err := openQueue(dir, slog.New(slog.DiscardHandler), m, now)
	if err != nil {
		t.Fatal(err)
	}
	return newUploader(q, base+cp.RecordingsPath, "rak_test", "0.1.0-test", hc, slog.New(slog.DiscardHandler), m,
		newFake, now, QueueMaxAge)
}

func TestUploadMissingWAV(t *testing.T) {
	u := newTestUploader(t, "http://127.0.0.1:1", http.DefaultClient, t.TempDir(), time.Now)
	now := time.Now()
	if o := u.process(context.Background(), &entry{Recording: Recording{WAV: filepath.Join(t.TempDir(), "gone.wav"), Queued: now}}); o != outNoFile {
		t.Errorf("outcome %q, want %q", o, outNoFile)
	}
	wav := filepath.Join(t.TempDir(), "16k.wav")
	_ = os.WriteFile(wav, fakerecorder.WAV(16000, make([]byte, 640)), 0o644)
	if o := u.process(context.Background(), &entry{Recording: Recording{WAV: wav, Queued: now}}); o != outNoFile {
		t.Errorf("16 kHz recording: outcome %q, want %q", o, outNoFile)
	}
	if n := u.m.Get(MetricMalformed, "reason", "sample_rate"); n != 1 {
		t.Errorf("malformed{sample_rate} = %d, want 1", n)
	}
}
