package recorderclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

// trunk-recorder v5 renders every call's WAV at 16 kHz, P25's included
// (ADR 0046), and the client encodes each WAV at its own rate: 20 ms frames
// at that rate, the header's input rate, and the pre-skip and granules
// counted at 48 kHz, so the recording lasts as long as the WAV did.
func TestRecordingRates(t *testing.T) {
	for _, rate := range []int{8000, 16000} {
		t.Run(fmt.Sprint(rate), func(t *testing.T) {
			// 1.01 s of a WAV at rate, read as the uploader reads it.
			wav := filepath.Join(t.TempDir(), "call.wav")
			n := rate + rate/100
			if err := os.WriteFile(wav, fakerecorder.WAV(rate, make([]byte, 2*n)), 0o644); err != nil {
				t.Fatal(err)
			}
			got, pcm, err := ReadRecording(wav)
			if err != nil || got != rate || len(pcm) != n {
				t.Fatalf("ReadRecording = %d Hz, %d samples, %v; want %d, %d", got, len(pcm), err, rate, n)
			}
			var frames []int
			var made int
			newEnc := func(r int) (Encoder, error) {
				made = r
				return &rateEncoder{rate: r, frames: &frames}, nil
			}
			ogg, err := encodeOggOpus(newEnc, 1, rate, pcm)
			if err != nil {
				t.Fatal(err)
			}
			if made != rate || len(frames) != 51 || frames[0] != rate/50 {
				t.Errorf("encoder at %d Hz, %d frames of %d; want %d Hz, 51 of %d", made, len(frames), frames[0], rate, rate/50)
			}
			head := ogg[bytes.Index(ogg, []byte("OpusHead")):]
			preSkip := int(binary.LittleEndian.Uint16(head[10:]))
			if in := binary.LittleEndian.Uint32(head[12:]); in != uint32(rate) {
				t.Errorf("OpusHead input rate %d, want %d", in, rate)
			}
			if want := rate / 400 * 48000 / rate; preSkip != want {
				t.Errorf("pre-skip %d, want %d (the lookahead at 48 kHz)", preSkip, want)
			}
			last := bytes.LastIndex(ogg, []byte("OggS"))
			g := int(binary.LittleEndian.Uint64(ogg[last+6:]))
			if want := preSkip + 48480; g != want { // 1.01 s at 48 kHz
				t.Errorf("final granule %d, want %d: %.3f s of audio, want 1.010", g, want, float64(g-preSkip)/48000)
			}
		})
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
		{"rejected not_attached: the WAV is deleted", []func(cp.RecordingMetadata, string) (int, cp.UploadResponse){
			answer(200, cp.UploadResponse{Status: cp.StatusRejected, Reason: "not_attached"})}, outRejected, true, 1},
		{"rejected unknown_talkgroup: the WAV is deleted", []func(cp.RecordingMetadata, string) (int, cp.UploadResponse){
			answer(200, cp.UploadResponse{Status: cp.StatusRejected, Reason: "unknown_talkgroup"})}, outRejected, true, 1},
		{"rejected ignored_talkgroup: the WAV is deleted", []func(cp.RecordingMetadata, string) (int, cp.UploadResponse){
			answer(200, cp.UploadResponse{Status: cp.StatusRejected, Reason: "ignored_talkgroup"})}, outRejected, true, 1},
		{"rejected for an unknown reason: kept", []func(cp.RecordingMetadata, string) (int, cp.UploadResponse){
			answer(200, cp.UploadResponse{Status: cp.StatusRejected, Reason: "something_new"})}, outRejectedOther, false, 1},
		{"404: not retried, kept", []func(cp.RecordingMetadata, string) (int, cp.UploadResponse){
			answer(404, cp.UploadResponse{Error: "not_found"})}, outInvalid, false, 1},
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

// writeWAV writes 0.2 s of 16 kHz silence, as trunk-recorder v5 does.
func writeWAV(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, fakerecorder.WAV(16000, make([]byte, 6400)), 0o644); err != nil {
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
	// A rate the client doesn't take is dropped like a missing file.
	wav := filepath.Join(t.TempDir(), "11k.wav")
	_ = os.WriteFile(wav, fakerecorder.WAV(11025, make([]byte, 640)), 0o644)
	if o := u.process(context.Background(), &entry{Recording: Recording{WAV: wav, Queued: now}}); o != outNoFile {
		t.Errorf("11025 Hz recording: outcome %q, want %q", o, outNoFile)
	}
	if n := u.m.Get(MetricMalformed, "reason", "sample_rate"); n != 1 {
		t.Errorf("malformed{sample_rate} = %d, want 1", n)
	}
}

// ackThenCancel answers every upload "stored" with the request's own
// SHA-256, and cancels the client's context just before the answer reaches
// it: the client is stopping as the acknowledgement arrives.
type ackThenCancel struct {
	t      *testing.T
	cancel context.CancelFunc
}

func (a ackThenCancel) RoundTrip(r *http.Request) (*http.Response, error) {
	_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	p, err := multipart.NewReader(r.Body, params["boundary"]).NextPart()
	var meta cp.RecordingMetadata
	if err != nil || json.NewDecoder(p).Decode(&meta) != nil {
		a.t.Error("no metadata part")
	}
	b, _ := json.Marshal(cp.UploadResponse{Status: cp.StatusStored, SHA256: meta.SHA256})
	a.cancel()
	return &http.Response{StatusCode: http.StatusCreated, Status: "201 Created", Request: r,
		Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(b))}, nil
}

// TestUploadAckDuringStop: an acknowledgement that arrives as the client
// stops still deletes the WAV and the entry (issue #88). Before the fix the
// cancelled context outranked the server's answer, and the acknowledged
// WAV was kept.
func TestUploadAckDuringStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	wav := writeWAV(t, t.TempDir(), "call.wav")
	u := newTestUploader(t, "http://server.invalid", &http.Client{Transport: ackThenCancel{t, cancel}}, dir, time.Now)
	var got []outcome
	u.done = func(_ Recording, o outcome) { got = append(got, o) }
	u.Enqueue(rec(1, wav, time.Now()))
	u.run(ctx) // returns once ctx is cancelled
	if len(got) != 1 || got[0] != outStored {
		t.Errorf("outcomes %v, want [stored]", got)
	}
	if exists(wav) {
		t.Error("WAV kept after the server acknowledged it")
	}
	if files, _ := os.ReadDir(dir); len(files) != 0 || u.Queue().Count != 0 {
		t.Errorf("queue %d, files %v after the acknowledgement; want none", u.Queue().Count, files)
	}
}
