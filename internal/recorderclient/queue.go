package recorderclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
)

// The disk queue (SPEC.md R5).
//
// An entry is one small JSON file in the queue directory, written when the
// call ends. It points at trunk-recorder's WAV and holds the recording's
// metadata; the Ogg/Opus file is never stored, only made in memory for the
// upload. So there are two files per recording, and each is deleted at one
// point:
//
//   - the WAV, once the server acknowledges the upload with our SHA-256
//     (stored or duplicate), or when the entry outlives QUEUE_MAX_AGE and
//     the recording is given up (there's no reason to keep WAVs, owner).
//     A recording the server permanently refuses (rejected, conflict,
//     400/413/415) keeps its WAV, logged with its path and counted, since
//     that means a bug for a person to investigate. A client_too_old or
//     invalid-key refusal (401, 426) removes nothing: the entry waits for
//     the fix, until QUEUE_MAX_AGE;
//   - the entry, after its WAV on an acknowledgement or expiry, or when
//     the entry leaves the queue any other way (refused, unreadable).
//
// Crash safety. An entry is written to a temporary name, synced, renamed
// into place and the directory synced, so after a crash an entry is either
// whole or absent; temporary files are removed at start. The engine writes
// it before anything can delete the WAV. A crash at any later point leaves
// one of three states, and each is safe:
//
//   - entry and WAV, upload unacknowledged (or acknowledged, but the answer
//     lost): the entry is uploaded again. The Ogg/Opus encoding is
//     deterministic (the same encoder on the same PCM, and an Ogg serial
//     number derived from the call's identity), so a recording the server
//     already stored comes back "duplicate" and is cleared as acknowledged;
//   - entry without WAV (a crash between the two deletions): the WAV is only
//     ever deleted after an acknowledgement or on expiry, so the entry is
//     dropped as unreadable and nothing is lost;
//   - neither: done.
//
// Entries leave the queue oldest first, by the time they were queued.
const (
	queueExt     = ".json"
	queueTmpExt  = ".tmp"
	queueBadExt  = ".bad"
	queueFormat  = 1
	QueueMaxAge  = 24 * time.Hour // R5's default QUEUE_MAX_AGE
	queueDirMode = 0o750
)

// entryFile is an entry's file.
type entryFile struct {
	Format int                  `json:"format"`
	Queued time.Time            `json:"queued"`
	WAV    string               `json:"wav"`
	Meta   cp.RecordingMetadata `json:"meta"`
}

// entry is a queued recording. file is its name in the queue directory, or
// "" when it couldn't be written and lives in memory only.
type entry struct {
	Recording
	file string
}

// diskQueue is the queue's entries, oldest first, and their files.
type diskQueue struct {
	dir string
	log *slog.Logger
	m   *Metrics
	now func() time.Time

	mu      sync.Mutex
	entries []*entry
	seq     uint64 // tie-break for names queued in the same nanosecond
}

// openQueue opens the queue in dir, creating it if need be, and loads the
// entries left by an earlier run.
func openQueue(dir string, log *slog.Logger, m *Metrics, now func() time.Time) (*diskQueue, error) {
	if dir == "" {
		return nil, errors.New("the queue directory is not set")
	}
	if err := os.MkdirAll(dir, queueDirMode); err != nil {
		return nil, fmt.Errorf("queue: %w", err)
	}
	q := &diskQueue{dir: dir, log: log, m: m, now: now}
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("queue: %w", err)
	}
	for _, de := range des {
		name := de.Name()
		switch {
		case de.IsDir():
		case strings.HasSuffix(name, queueTmpExt):
			// A write that a crash interrupted, before its rename: the
			// engine never went on to anything that relies on it.
			_ = os.Remove(filepath.Join(dir, name))
		case strings.HasSuffix(name, queueExt):
			e, err := q.load(name)
			if err != nil {
				// Kept aside, not deleted: it may name a WAV worth
				// recovering by hand.
				m.Inc(MetricQueueErrors, "op", "read")
				log.Error("can't read a queue entry; moved it aside", "file", name, "error", err)
				_ = os.Rename(filepath.Join(dir, name), filepath.Join(dir, name+queueBadExt))
				continue
			}
			q.entries = append(q.entries, e)
		}
	}
	sort.SliceStable(q.entries, func(i, j int) bool {
		a, b := q.entries[i], q.entries[j]
		if !a.Queued.Equal(b.Queued) {
			return a.Queued.Before(b.Queued)
		}
		return a.file < b.file
	})
	if n := len(q.entries); n > 0 {
		log.Info("recordings queued by an earlier run", "count", n, "oldest", q.entries[0].Queued)
	}
	return q, nil
}

func (q *diskQueue) load(name string) (*entry, error) {
	b, err := os.ReadFile(filepath.Join(q.dir, name))
	if err != nil {
		return nil, err
	}
	var f entryFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	if f.Format != queueFormat {
		return nil, fmt.Errorf("format %d, want %d", f.Format, queueFormat)
	}
	if f.WAV == "" || f.Queued.IsZero() {
		return nil, errors.New("no WAV or queued time")
	}
	return &entry{Recording: Recording{Meta: f.Meta, WAV: f.WAV, Queued: f.Queued}, file: name}, nil
}

// add writes r's entry and appends it. If the entry can't be written, it's
// kept in memory, so it's still uploaded unless the client stops first.
func (q *diskQueue) add(r Recording) {
	q.mu.Lock()
	q.seq++
	seq := q.seq
	q.mu.Unlock()
	name := fmt.Sprintf("%019d-%06d-%d-%d-%d%s", r.Queued.UnixNano(), seq%1e6,
		r.Meta.SysNum, r.Meta.CallNum, r.Meta.StartTime, queueExt)
	e := &entry{Recording: r}
	if err := q.write(name, r); err != nil {
		q.m.Inc(MetricQueueErrors, "op", "write")
		q.log.Error("can't write the recording's queue entry; it's queued in memory only, and lost if the client stops",
			"wav", r.WAV, "error", err)
	} else {
		e.file = name
	}
	q.mu.Lock()
	q.entries = append(q.entries, e)
	q.mu.Unlock()
}

func (q *diskQueue) write(name string, r Recording) error {
	b, err := json.Marshal(entryFile{Format: queueFormat, Queued: r.Queued, WAV: r.WAV, Meta: r.Meta})
	if err != nil {
		return err
	}
	tmp := filepath.Join(q.dir, name+queueTmpExt)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, filepath.Join(q.dir, name))
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return syncDir(q.dir)
}

// syncDir makes a rename in dir durable.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// head returns the oldest entry, or nil.
func (q *diskQueue) head() *entry {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.entries) == 0 {
		return nil
	}
	return q.entries[0]
}

// remove deletes e's file and takes it out of the queue. The deletion isn't
// synced: if a crash undoes it, the entry is uploaded again and answered
// "duplicate".
func (q *diskQueue) remove(e *entry) {
	if e.file != "" {
		if err := os.Remove(filepath.Join(q.dir, e.file)); err != nil && !errors.Is(err, os.ErrNotExist) {
			q.m.Inc(MetricQueueErrors, "op", "remove")
			q.log.Error("can't delete a queue entry; it will be sent again after a restart", "file", e.file, "error", err)
		}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, x := range q.entries {
		if x == e {
			q.entries = append(q.entries[:i], q.entries[i+1:]...)
			return
		}
	}
}

// expired returns the entries older than maxAge.
func (q *diskQueue) expired(maxAge time.Duration) []*entry {
	now := q.now()
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []*entry
	for _, e := range q.entries {
		if now.Sub(e.Queued) > maxAge {
			out = append(out, e)
		}
	}
	return out
}

// state is the queue's size and the age of its oldest entry.
func (q *diskQueue) state() cp.QueueState {
	now := q.now()
	q.mu.Lock()
	defer q.mu.Unlock()
	s := cp.QueueState{Count: len(q.entries)}
	if len(q.entries) > 0 {
		s.OldestAgeS = max(0, int64(now.Sub(q.entries[0].Queued)/time.Second))
	}
	return s
}
