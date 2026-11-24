package recorderclient

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
	"math/rand/v2"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"sync"
	"time"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
	"github.com/RIKIKU/RadioArchive/internal/oggopus"
)

// maxAttempts bounds this slice's retries. Issue #36 replaces the in-memory
// queue with R5's disk queue, which keeps an entry until the server answers
// or QUEUE_MAX_AGE passes.
const maxAttempts = 10

// memUploader is a minimal Uploader: an in-memory FIFO, oldest first, one
// upload at a time, retried with backoff while the error is transient.
type memUploader struct {
	url    string // https://host/api/v1/recorder/recordings
	key    string
	http   *http.Client
	log    *slog.Logger
	m      *Metrics
	newEnc NewEncoder

	mu    sync.Mutex
	queue []Recording
	wake  chan struct{}

	// done is called after each entry leaves the queue (tests).
	done func(Recording, outcome)
}

func newMemUploader(url, key string, hc *http.Client, log *slog.Logger, m *Metrics, newEnc NewEncoder) *memUploader {
	return &memUploader{url: url, key: key, http: hc, log: log, m: m, newEnc: newEnc, wake: make(chan struct{}, 1)}
}

// Enqueue implements Uploader.
func (u *memUploader) Enqueue(r Recording) {
	u.mu.Lock()
	u.queue = append(u.queue, r)
	u.mu.Unlock()
	select {
	case u.wake <- struct{}{}:
	default:
	}
}

// Queue implements Uploader.
func (u *memUploader) Queue() cp.QueueState {
	u.mu.Lock()
	defer u.mu.Unlock()
	q := cp.QueueState{Count: len(u.queue)}
	if len(u.queue) > 0 {
		q.OldestAgeS = int64(time.Since(u.queue[0].Queued) / time.Second)
	}
	return q
}

func (u *memUploader) run(ctx context.Context) {
	for {
		u.mu.Lock()
		var r *Recording
		if len(u.queue) > 0 {
			r = &u.queue[0]
		}
		u.mu.Unlock()
		if r == nil {
			select {
			case <-ctx.Done():
				return
			case <-u.wake:
				continue
			}
		}
		o := u.process(ctx, *r)
		if ctx.Err() != nil {
			return
		}
		u.mu.Lock()
		rec := u.queue[0]
		u.queue = u.queue[1:]
		u.mu.Unlock()
		u.m.Inc(MetricUploads, "result", string(o))
		if u.done != nil {
			u.done(rec, o)
		}
	}
}

// outcome is how an entry left the queue.
type outcome string

const (
	outStored    outcome = "stored"    // acknowledged; the WAV is deleted
	outDuplicate outcome = "duplicate" // already stored; the WAV is deleted
	outRejected  outcome = "rejected"  // refused by the server's rules (R12, R17, R18)
	outConflict  outcome = "conflict"  // same identity stored with another file
	outInvalid   outcome = "invalid"   // 400, 413, 415: retrying won't help
	outNoFile    outcome = "unreadable"
	outGaveUp    outcome = "gave_up"
)

// verdict is one attempt's result.
type verdict int

const (
	vDone verdict = iota
	vRetry
	vPause // key or version refused: retry slowly
)

// process encodes the recording once and uploads it until the server gives
// a final answer, the attempts run out, or ctx ends.
func (u *memUploader) process(ctx context.Context, r Recording) outcome {
	log := u.log.With("sys_num", r.Meta.SysNum, "call_num", r.Meta.CallNum, "start_time", r.Meta.StartTime)
	ogg, err := u.encode(r.WAV)
	if err != nil {
		log.Error("can't read or encode the recording; it won't be uploaded", "file", r.WAV, "error", err)
		return outNoFile
	}
	sum := sha256.Sum256(ogg)
	meta := r.Meta
	meta.SHA256 = hex.EncodeToString(sum[:])
	meta.Size = int64(len(ogg))

	backoff := backoffMin
	for attempt := 1; ; attempt++ {
		o, v, err := u.attempt(ctx, meta, ogg)
		if v == vDone {
			if o == outStored || o == outDuplicate {
				// Acknowledged with our SHA-256: trunk-recorder's WAV has
				// done its job (owner, issue #31).
				if err := os.Remove(r.WAV); err != nil && !errors.Is(err, os.ErrNotExist) {
					log.Warn("recording uploaded but its WAV couldn't be deleted", "file", r.WAV, "error", err)
				}
			} else {
				log.Warn("the server refused the recording", "outcome", string(o), "error", err)
			}
			return o
		}
		if attempt >= maxAttempts {
			log.Error("giving up on the recording after repeated failures", "attempts", attempt, "error", err)
			return outGaveUp
		}
		if v == vPause {
			backoff = backoffMax
		}
		wait := backoff/2 + rand.N(backoff/2+1)
		log.Warn("recording upload failed; retrying", "error", err, "in", wait.Round(time.Millisecond))
		select {
		case <-ctx.Done():
			return outGaveUp
		case <-time.After(wait):
		}
		backoff = min(2*backoff, backoffMax)
	}
}

func (u *memUploader) encode(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rate, pcm, err := readWAV(f)
	if err != nil {
		return nil, err
	}
	if rate != SampleRate {
		u.m.Inc(MetricMalformed, "reason", "sample_rate")
		return nil, fmt.Errorf("recording is %d Hz; v1 takes %d Hz (P25) only", rate, SampleRate)
	}
	return encodeOggOpus(u.newEnc, rate, pcm)
}

// encodeOggOpus encodes a whole recording with R4's settings.
func encodeOggOpus(newEnc NewEncoder, rate int, pcm []int16) ([]byte, error) {
	enc, err := newEnc(rate)
	if err != nil {
		return nil, err
	}
	defer enc.Close()
	scale := 48000 / rate
	var buf bytes.Buffer
	w, err := oggopus.NewWriter(&buf, rand.Uint32(), uint16(enc.Lookahead()*scale), uint32(rate))
	if err != nil {
		return nil, err
	}
	n := enc.FrameSamples()
	frame := make([]int16, n)
	for off := 0; off < len(pcm); off += n {
		clear(frame)
		copy(frame, pcm[off:min(off+n, len(pcm))])
		pkt, err := enc.Encode(frame)
		if err != nil {
			return nil, err
		}
		if err := w.WritePacket(pkt, n*scale); err != nil {
			return nil, err
		}
	}
	if err := w.Close(uint64(len(pcm) * scale)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (u *memUploader) attempt(ctx context.Context, meta cp.RecordingMetadata, ogg []byte) (outcome, verdict, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mj, err := json.Marshal(meta)
	if err != nil {
		return outInvalid, vDone, err
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="metadata"`)
	h.Set("Content-Type", "application/json")
	p, _ := mw.CreatePart(h)
	_, _ = p.Write(mj)
	h = textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="audio"; filename="recording.opus"`)
	h.Set("Content-Type", "audio/ogg")
	p, _ = mw.CreatePart(h)
	_, _ = p.Write(ogg)
	_ = mw.Close()

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.url, &body)
	if err != nil {
		return outInvalid, vDone, err
	}
	req.Header = headers(u.key)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := u.http.Do(req)
	if err != nil {
		return "", vRetry, err
	}
	defer resp.Body.Close()
	var ur cp.UploadResponse
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	_ = json.Unmarshal(raw, &ur)
	status := fmt.Errorf("%s %s", resp.Status, bytes.TrimSpace(raw))

	switch {
	case resp.StatusCode == http.StatusCreated && ur.Status == cp.StatusStored:
		if ur.SHA256 != meta.SHA256 {
			return "", vRetry, fmt.Errorf("the server stored a different SHA-256 (%s)", ur.SHA256)
		}
		return outStored, vDone, nil
	case resp.StatusCode == http.StatusOK && ur.Status == cp.StatusDuplicate:
		if ur.SHA256 != meta.SHA256 {
			return outConflict, vDone, status
		}
		return outDuplicate, vDone, nil
	case resp.StatusCode == http.StatusOK && ur.Status == cp.StatusRejected:
		return outRejected, vDone, status
	case resp.StatusCode == http.StatusConflict:
		return outConflict, vDone, status
	case resp.StatusCode == http.StatusBadRequest, resp.StatusCode == http.StatusRequestEntityTooLarge,
		resp.StatusCode == http.StatusUnsupportedMediaType:
		return outInvalid, vDone, status
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusUpgradeRequired:
		return "", vPause, status
	default:
		return "", vRetry, status
	}
}
