package recorderclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"math/rand/v2"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"time"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
	"github.com/RIKIKU/RadioArchive/internal/oggopus"
)

// uploader is the Uploader: R5's disk queue (queue.go), uploaded oldest
// first, one at a time. An entry is retried with backoff until the server
// gives a final answer or the entry is older than maxAge.
type uploader struct {
	q       *diskQueue
	url     string // https://host/api/v1/recorder/recordings
	key     string
	version string
	http    *http.Client
	log     *slog.Logger
	m       *Metrics
	newEnc  NewEncoder
	now     func() time.Time
	maxAge  time.Duration

	wake  chan struct{} // a new entry, for an idle queue
	retry chan struct{} // Kick: retry now

	// done is called after each entry leaves the queue (tests).
	done func(Recording, outcome)
}

func newUploader(q *diskQueue, url, key, version string, hc *http.Client, log *slog.Logger, m *Metrics,
	newEnc NewEncoder, now func() time.Time, maxAge time.Duration) *uploader {
	u := &uploader{q: q, url: url, key: key, version: version, http: hc, log: log, m: m, newEnc: newEnc,
		now: now, maxAge: maxAge, wake: make(chan struct{}, 1), retry: make(chan struct{}, 1)}
	m.SetGauge(MetricQueueEntries, func() float64 { return float64(q.state().Count) })
	m.SetGauge(MetricQueueOldestAge, func() float64 { return float64(q.state().OldestAgeS) })
	return u
}

// Enqueue implements Uploader. The entry is on disk when it returns.
func (u *uploader) Enqueue(r Recording) {
	u.q.add(r)
	signal(u.wake)
}

// Queue implements Uploader.
func (u *uploader) Queue() cp.QueueState { return u.q.state() }

// Kick ends a wait between retries early, with the backoff reset: the live
// socket has just reconnected, so the server is probably back. A new entry
// doesn't, or every call during an outage would reset the backoff.
func (u *uploader) Kick() { signal(u.retry) }

func signal(c chan struct{}) {
	select {
	case c <- struct{}{}:
	default:
	}
}

func (u *uploader) run(ctx context.Context) {
	for ctx.Err() == nil {
		u.expire()
		e := u.q.head()
		if e == nil {
			select {
			case <-ctx.Done():
				return
			case <-u.wake:
				continue
			}
		}
		o := u.process(ctx, e)
		if o == outCancelled {
			return
		}
		u.leave(e, o)
	}
}

// expire drops the entries older than maxAge, wherever they are in the
// queue, with their WAVs: the recording is given up, and there's no reason
// to keep trunk-recorder's files (owner).
func (u *uploader) expire() {
	for _, e := range u.q.expired(u.maxAge) {
		u.leave(e, outExpired)
	}
}

func (u *uploader) leave(e *entry, o outcome) {
	log := u.log.With("sys_num", e.Meta.SysNum, "call_num", e.Meta.CallNum, "start_time", e.Meta.StartTime)
	switch o {
	case outStored, outDuplicate, outRejected, outExpired:
		// Acknowledged with our SHA-256, refused on purpose (counted by
		// reason in attempt), or given up: trunk-recorder's WAV has no
		// further use (owner, issues #31 and #36). The WAV goes first, then
		// the entry: an entry left without its WAV after a crash is known
		// to be done.
		if o == outExpired {
			u.m.Inc(MetricQueueExpired)
			log.Error("dropped a recording that waited longer than QUEUE_MAX_AGE, and deleted its WAV",
				"file", e.WAV, "queued", e.Queued, "max_age", u.maxAge)
		}
		if err := os.Remove(e.WAV); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Warn("couldn't delete the recording's WAV", "file", e.WAV, "error", err)
		}
	case outRejectedOther, outConflict, outInvalid:
		// An unexpected permanent refusal: rare, and it means a bug. The
		// WAV is kept for a person to look at.
		u.m.Inc(MetricQueueRefused, "result", string(o))
		log.Error("the server permanently refused the recording; dropped its queue entry and kept its WAV for investigation",
			"outcome", string(o), "file", e.WAV)
	}
	u.q.remove(e)
	if o != outExpired {
		u.m.Inc(MetricUploads, "result", string(o))
	}
	if u.done != nil {
		u.done(e.Recording, o)
	}
}

// outcome is how an entry left the queue.
type outcome string

const (
	outStored    outcome = "stored"    // acknowledged; the WAV is deleted
	outDuplicate outcome = "duplicate" // already stored; the WAV is deleted
	outRejected  outcome = "rejected"  // refused on purpose (R12, R17, R18); the WAV is deleted
	// rejected for a reason this client doesn't know: the WAV is kept
	outRejectedOther outcome = "rejected_unexpected"
	outConflict      outcome = "conflict" // same identity stored with another file
	outInvalid       outcome = "invalid"  // 400, 413, 415: retrying won't help
	outNoFile        outcome = "unreadable"
	outExpired       outcome = "expired"   // older than QUEUE_MAX_AGE
	outCancelled     outcome = "cancelled" // the client is stopping; the entry stays
)

// verdict is one attempt's result.
type verdict int

const (
	vDone verdict = iota
	vRetry
	vPause // key or version refused: retry slowly
)

// process encodes the recording once and uploads it until the server gives
// a final answer, the entry gets too old, or ctx ends.
func (u *uploader) process(ctx context.Context, e *entry) outcome {
	log := u.log.With("sys_num", e.Meta.SysNum, "call_num", e.Meta.CallNum, "start_time", e.Meta.StartTime)
	ogg, err := u.encode(e.Recording)
	if errors.Is(err, os.ErrNotExist) {
		// Only an acknowledgement deletes it, so after a crash this entry
		// was done; or someone removed the file.
		log.Warn("the recording's WAV is gone; dropping its queue entry", "file", e.WAV)
		return outNoFile
	}
	if err != nil {
		log.Error("can't read or encode the recording; it won't be uploaded", "file", e.WAV, "error", err)
		return outNoFile
	}
	sum := sha256.Sum256(ogg)
	meta := e.Meta
	meta.SHA256 = hex.EncodeToString(sum[:])
	meta.Size = int64(len(ogg))

	// A Kick from before this entry's first attempt is stale.
	select {
	case <-u.retry:
	default:
	}
	backoff := backoffMin
	for {
		if u.now().Sub(e.Queued) > u.maxAge {
			return outExpired
		}
		o, v, err := u.attempt(ctx, meta, ogg)
		// A final answer counts even if ctx ended while it arrived: the
		// server has acted on it, so dropping it would keep a WAV the
		// server already acknowledged (issue #88).
		if v != vDone && ctx.Err() != nil {
			return outCancelled
		}
		if v == vDone {
			if o != outStored && o != outDuplicate {
				log.Warn("the server refused the recording", "outcome", string(o), "file", e.WAV, "error", err)
			}
			return o
		}
		if v == vPause {
			backoff = backoffMax
		}
		wait := backoff/2 + rand.N(backoff/2+1)
		log.Warn("recording upload failed; it stays queued and is retried", "error", err, "in", wait.Round(time.Millisecond))
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return outCancelled
		case <-u.retry:
			t.Stop()
			backoff = backoffMin
		case <-t.C:
			backoff = min(2*backoff, backoffMax)
		}
		// An entry further back may have aged out meanwhile.
		for _, x := range u.q.expired(u.maxAge) {
			if x != e {
				u.leave(x, outExpired)
			}
		}
	}
}

func (u *uploader) encode(r Recording) ([]byte, error) {
	rate, pcm, err := ReadRecording(r.WAV)
	if errors.Is(err, ErrSampleRate) {
		u.m.Inc(MetricMalformed, "reason", "sample_rate")
	}
	if err != nil {
		return nil, err
	}
	// Encoded at the WAV's own rate, never resampled (ADR 0046).
	return encodeOggOpus(u.newEnc, oggSerial(r.Meta.CallID), rate, pcm)
}

// ErrSampleRate is ReadRecording's error for a WAV at a rate the client
// doesn't take (SampleRates).
var ErrSampleRate = errors.New("unsupported sample rate")

// ReadRecording reads trunk-recorder's WAV at path as the uploader does:
// 16-bit mono PCM at one of SampleRates. The plugin's CI test reads the
// real trunk-recorder's recordings with it.
func ReadRecording(path string) (rate int, pcm []int16, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, nil, err
	}
	defer f.Close()
	rate, pcm, err = readWAV(f)
	if err != nil {
		return 0, nil, err
	}
	if !supportedRate(rate) {
		return 0, nil, fmt.Errorf("recording is %d Hz; the client takes %v Hz: %w", rate, SampleRates, ErrSampleRate)
	}
	return rate, pcm, nil
}

// oggSerial is the recording's Ogg stream serial number, derived from the
// call's identity so that encoding the same WAV again gives the same bytes,
// and the same SHA-256 (queue.go, crash safety).
func oggSerial(id cp.CallID) uint32 {
	h := fnv.New32a()
	var b [18]byte
	binary.LittleEndian.PutUint16(b[0:], id.SysNum)
	binary.LittleEndian.PutUint64(b[2:], uint64(id.CallNum))
	binary.LittleEndian.PutUint64(b[10:], uint64(id.StartTime))
	_, _ = h.Write(b[:])
	return h.Sum32()
}

// encodeOggOpus encodes a whole recording with R4's settings. The same
// encoder, serial and PCM always give the same bytes.
func encodeOggOpus(newEnc NewEncoder, serial uint32, rate int, pcm []int16) ([]byte, error) {
	enc, err := newEnc(rate)
	if err != nil {
		return nil, err
	}
	defer enc.Close()
	scale := 48000 / rate
	var buf bytes.Buffer
	w, err := oggopus.NewWriter(&buf, serial, uint16(enc.Lookahead()*scale), uint32(rate))
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

func (u *uploader) attempt(ctx context.Context, meta cp.RecordingMetadata, ogg []byte) (outcome, verdict, error) {
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
	req.Header = headers(u.key, u.version)
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
		if deliberateRejections[ur.Reason] {
			u.m.Inc(MetricQueueRejected, "reason", ur.Reason)
			return outRejected, vDone, status
		}
		return outRejectedOther, vDone, status
	case resp.StatusCode == http.StatusConflict:
		return outConflict, vDone, status
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusUpgradeRequired:
		// Key or version refused (client_too_old): the entry waits for
		// the fix, until QUEUE_MAX_AGE.
		return "", vPause, status
	case resp.StatusCode == http.StatusRequestTimeout, resp.StatusCode == http.StatusTooManyRequests:
		return "", vRetry, status
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		// 400, 413, 415 and any other 4xx: retrying won't help.
		return outInvalid, vDone, status
	default:
		return "", vRetry, status
	}
}

// deliberateRejections are the reasons the server refuses a call on
// purpose: the recorder's shortName isn't attached to a system (R12), or the
// talkgroup is unknown or ignored (R17, R18). See ADR 0003.
var deliberateRejections = map[string]bool{
	"not_attached":      true,
	"unknown_talkgroup": true,
	"ignored_talkgroup": true,
}
