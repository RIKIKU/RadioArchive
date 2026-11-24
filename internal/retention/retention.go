// Package retention deletes recordings once their retention period is over
// (SPEC.md R88, P5, R35; ADR 0005): a nightly job, a check of the recordings
// volume's free space that deletes the oldest recordings early when it runs
// low, and the retention setting.
//
// Both runs hold one PostgreSQL advisory lock, so of several API servers on
// one database only one runs either at a time, and the jobs table records
// the last of each, so a nightly run is done once per night however many
// servers there are.
//
// A call's row is deleted, and committed, before its file is removed. So the
// gate (internal/gate) never finds a row whose file retention has removed
// for good: a public call is either there with its recording, or gone, and
// gone answers as a call that never existed. A file whose removal failed has
// no row, and the orphan sweep removes it later.
package retention

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/RIKIKU/RadioArchive/internal/recordings"
	"github.com/RIKIKU/RadioArchive/internal/store"
	"github.com/RIKIKU/RadioArchive/internal/store/db"
)

// The retention period, in days (R88).
const (
	DefaultDays = 30
	MinDays     = 1
	MaxDays     = 3650
)

// Free space on the recordings volume, as fractions (R88). Below MinFree
// (RECORDINGS_MIN_FREE) the oldest recordings are deleted early; below
// WarnFree admins are warned.
const (
	DefaultMinFree = 0.05
	WarnFree       = 0.15
)

// DefaultEarlyDeleteFloor is RECORDINGS_EARLY_DELETE_FLOOR's default: a
// call that started within it is never deleted early for low disk (R88).
const DefaultEarlyDeleteFloor = 24 * time.Hour

// DefaultOrphanGrace is how long a recording file with no row is kept
// before the nightly job removes it, and the same for a file in the
// upload directory. It's far longer than an upload takes: an upload writes
// its file (each write moves its modification time on), renames it into
// place and commits its row in one request.
const DefaultOrphanGrace = 24 * time.Hour

// IncomingDir is where uploads are written before they're renamed into
// place, under the recordings volume (server.RecordersConfig).
const IncomingDir = ".incoming"

// Job names in the jobs table.
const (
	NightlyJob = "nightly"
	LowDiskJob = "low_disk"
)

const (
	daysKey = "retention_days"
	batch   = 1000
	// LockKey is held by whichever server runs the nightly job or
	// the low-disk check, so only one does at a time.
	LockKey int64 = 7_243_037
)

// ErrBusy is a run refused because another holds the lock, on this server
// or another.
var ErrBusy = errors.New("another retention run is in progress")

// Space is a volume's size and what's free on it, in bytes.
type Space struct {
	Free, Total uint64
}

// FreeFraction is the share of the volume that's free, 0 to 1.
func (s Space) FreeFraction() float64 {
	if s.Total == 0 {
		return 0
	}
	return float64(s.Free) / float64(s.Total)
}

// FreeSpaceFunc measures the volume holding dir. Statfs is the real one;
// tests pass their own, so the low-disk path runs without filling a disk.
type FreeSpaceFunc func(dir string) (Space, error)

// Config configures the job.
type Config struct {
	Store *store.Store
	// Dir is the recordings volume (R20).
	Dir string
	Log *slog.Logger
	// Now is the job's clock (default time.Now). Tests control it.
	Now func() time.Time
	// FreeSpace measures the recordings volume (default Statfs).
	FreeSpace FreeSpaceFunc
	// MinFree is RECORDINGS_MIN_FREE as a fraction (default 0.05).
	MinFree float64
	// EarlyDeleteFloor is RECORDINGS_EARLY_DELETE_FLOOR (default 24 h):
	// low disk never deletes a call that started within it of now.
	EarlyDeleteFloor time.Duration
	// At is when the nightly job runs, since midnight in Location
	// (default 03:00 in time.Local, which TZ sets).
	At       time.Duration
	Location *time.Location
	// OrphanGrace is DefaultOrphanGrace unless set.
	OrphanGrace time.Duration
	// DiskEvery is how often the free space is checked (default a minute).
	DiskEvery time.Duration
}

// Counters are the job's totals since the server started, for metrics
// (R90, R91) and tests.
type Counters struct {
	Expired   atomic.Uint64 // recordings deleted by the nightly job
	Orphans   atomic.Uint64 // files with no row removed by the nightly job
	Shortened atomic.Uint64 // recordings deleted early for low disk: "retention shortened"
	Full      atomic.Uint64 // times "recordings volume full" was raised
}

// Job is the retention job.
type Job struct {
	cfg  Config
	Stat Counters

	mu      sync.Mutex
	diskErr string // the last free-space error logged, so it's logged once
	// full: a low-disk check stopped at the floor, still below MinFree.
	// It clears once the free space is back at MinFree.
	full bool
}

// New returns the job. Call Run to run it on its schedule.
func New(cfg Config) (*Job, error) {
	if cfg.Store == nil || cfg.Dir == "" || cfg.Log == nil {
		return nil, errors.New("retention needs the store, the recordings directory and a logger")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.FreeSpace == nil {
		cfg.FreeSpace = Statfs
	}
	if cfg.MinFree == 0 {
		cfg.MinFree = DefaultMinFree
	}
	if cfg.MinFree <= 0 || cfg.MinFree >= 1 {
		return nil, errors.New("RECORDINGS_MIN_FREE must be above 0 % and below 100 %")
	}
	if cfg.EarlyDeleteFloor == 0 {
		cfg.EarlyDeleteFloor = DefaultEarlyDeleteFloor
	}
	if cfg.EarlyDeleteFloor < 0 {
		return nil, errors.New("RECORDINGS_EARLY_DELETE_FLOOR can't be negative")
	}
	if cfg.At == 0 {
		cfg.At = 3 * time.Hour
	}
	if cfg.At < 0 || cfg.At >= 24*time.Hour {
		return nil, errors.New("the nightly time must be within a day")
	}
	if cfg.Location == nil {
		cfg.Location = time.Local
	}
	if cfg.OrphanGrace == 0 {
		cfg.OrphanGrace = DefaultOrphanGrace
	}
	if cfg.DiskEvery == 0 {
		cfg.DiskEvery = time.Minute
	}
	return &Job{cfg: cfg}, nil
}

// Now is the job's clock.
func (j *Job) Now() time.Time { return j.cfg.Now() }

// ParsePercent reads RECORDINGS_MIN_FREE: "5", "5%" or "2.5 %", above 0
// and below 100, as a fraction.
func ParsePercent(s string) (float64, error) {
	v := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "%"))
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f <= 0 || f >= 100 {
		return 0, fmt.Errorf("%q isn't a percentage above 0 and below 100", s)
	}
	return f / 100, nil
}

// ParseAt reads a "HH:MM" time of day.
func ParseAt(s string) (time.Duration, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, fmt.Errorf("%q isn't a time of day like 03:00", s)
	}
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute, nil
}

// The retention setting.

// Days is the retention period in force.
func (j *Job) Days(ctx context.Context) (int, error) {
	v, err := j.cfg.Store.GetSetting(ctx, daysKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultDays, nil
	}
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < MinDays || n > MaxDays {
		return DefaultDays, nil
	}
	return n, nil
}

// SetDays saves a retention period. It takes effect at the next nightly
// run.
func (j *Job) SetDays(ctx context.Context, days int) error {
	if days < MinDays || days > MaxDays {
		return fmt.Errorf("retention must be %d to %d days", MinDays, MaxDays)
	}
	return j.cfg.Store.SetSetting(ctx, db.SetSettingParams{Key: daysKey, Value: strconv.Itoa(days)})
}

// Cutoff is when a call must have started by to be kept at now: calls that
// started before it are past a period of days.
func Cutoff(now time.Time, days int) time.Time {
	return now.Add(-time.Duration(days) * 24 * time.Hour)
}

// RecordingsPast counts the recordings, hidden ones too, that a period of
// days would delete now.
func (j *Job) RecordingsPast(ctx context.Context, days int) (int64, error) {
	return j.cfg.Store.CountRecordingsBefore(ctx, tstz(Cutoff(j.Now(), days)))
}

// Free space.

// DiskState is how the recordings volume's free space stands.
type DiskState string

// Disk states.
const (
	DiskOK      DiskState = "ok"
	DiskWarning DiskState = "warning" // below WarnFree: admins are warned
	DiskLow     DiskState = "low"     // below MinFree: retention is being shortened
	// DiskFull is below MinFree with only calls within the early-delete
	// floor left: nothing more is deleted early, "recordings volume full".
	DiskFull DiskState = "full"
)

// Disk is the recordings volume as admins see it.
type Disk struct {
	Space
	State    DiskState
	MinFree  float64
	WarnFree float64
	// Floor is the early-delete floor.
	Floor time.Duration
}

// Disk measures the recordings volume now.
func (j *Job) Disk() (Disk, error) {
	sp, err := j.cfg.FreeSpace(j.cfg.Dir)
	if err != nil {
		return Disk{}, err
	}
	d := Disk{Space: sp, State: DiskOK, MinFree: j.cfg.MinFree, WarnFree: WarnFree, Floor: j.cfg.EarlyDeleteFloor}
	j.mu.Lock()
	full := j.full
	j.mu.Unlock()
	switch f := sp.FreeFraction(); {
	case f < j.cfg.MinFree && full:
		d.State = DiskFull
	case f < j.cfg.MinFree:
		d.State = DiskLow
	case f < WarnFree:
		d.State = DiskWarning
	}
	return d, nil
}

// Runs.

// Run is one recorded run of a job, as the jobs table keeps it.
type Run struct {
	RanFor     time.Time       `json:"ran_for"`
	StartedAt  time.Time       `json:"started_at"`
	FinishedAt time.Time       `json:"finished_at"`
	OK         bool            `json:"ok"`
	Error      string          `json:"error,omitempty"`
	Result     json.RawMessage `json:"result"`
}

// LastRun is a job's last recorded run, or nil.
func (j *Job) LastRun(ctx context.Context, name string) (*Run, error) {
	r, err := j.cfg.Store.GetJob(ctx, name)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Run{RanFor: r.RanFor.Time.UTC(), StartedAt: r.StartedAt.Time.UTC(), FinishedAt: r.FinishedAt.Time.UTC(),
		OK: r.Ok, Error: r.Error, Result: r.Result}, nil
}

func (j *Job) save(ctx context.Context, name string, ranFor, started time.Time, result any, runErr error) error {
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}
	msg := ""
	if runErr != nil {
		msg = runErr.Error()
	}
	return j.cfg.Store.SaveJob(context.WithoutCancel(ctx), db.SaveJobParams{Name: name, RanFor: tstz(ranFor),
		StartedAt: tstz(started), FinishedAt: tstz(j.Now()), Ok: runErr == nil, Error: msg, Result: data})
}

// locked runs f holding the advisory lock, or returns ErrBusy. The lock is
// a session lock on one pooled connection, released when f returns, or by
// PostgreSQL if the server dies.
func (j *Job) locked(ctx context.Context, f func() error) error {
	conn, err := j.cfg.Store.Pool.Acquire(ctx)
	if err != nil {
		return errors.New(store.SafeError(err))
	}
	defer conn.Release()
	var ok bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", LockKey).Scan(&ok); err != nil {
		return errors.New(store.SafeError(err))
	}
	if !ok {
		return ErrBusy
	}
	defer func() {
		if _, err := conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", LockKey); err != nil {
			// The lock dies with the session: drop the connection.
			conn.Conn().Close(context.WithoutCancel(ctx))
		}
	}()
	return f()
}

// The schedule.

// runOn is the nightly run's time on the local date of t: the wall-clock
// time, so 03:00 stays 03:00 on a day the clocks change.
func (j *Job) runOn(t time.Time) time.Time {
	l := t.In(j.cfg.Location)
	h, m := int(j.cfg.At/time.Hour), int(j.cfg.At%time.Hour/time.Minute)
	return time.Date(l.Year(), l.Month(), l.Day(), h, m, 0, 0, j.cfg.Location)
}

// NextRun is the first scheduled nightly run after now.
func (j *Job) NextRun(now time.Time) time.Time {
	r := j.runOn(now)
	if !r.After(now) {
		r = j.runOn(now.In(j.cfg.Location).AddDate(0, 0, 1))
	}
	return r
}

// DeletesAt is when the nightly job will delete a call that started at
// started, with a period of days: the first scheduled run whose cut-off
// (Cutoff) is after the call's start, so the nightly run after its start
// plus the period (ADR 0005). Low disk may delete it sooner (R88); the
// public call details give this moment, so the browser needn't know the
// setting (ADR 0018).
func (j *Job) DeletesAt(days int, started time.Time) time.Time {
	return j.NextRun(started.Add(time.Duration(days) * 24 * time.Hour))
}

// previousRun is the last scheduled nightly run at or before now.
func (j *Job) previousRun(now time.Time) time.Time {
	r := j.runOn(now)
	if r.After(now) {
		r = j.runOn(now.In(j.cfg.Location).AddDate(0, 0, -1))
	}
	return r
}

// Run checks the free space every DiskEvery and does the nightly run when
// it's due, until ctx ends. A nightly run missed while no server was up is
// made up at the next start.
func (j *Job) Run(ctx context.Context) {
	for {
		if _, err := j.CheckDisk(ctx); err != nil && ctx.Err() == nil && !errors.Is(err, ErrBusy) {
			j.cfg.Log.Error("low-disk check failed", "error", err)
		}
		if err := j.nightlyIfDue(ctx); err != nil && ctx.Err() == nil && !errors.Is(err, ErrBusy) {
			j.cfg.Log.Error("nightly retention check failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(j.cfg.DiskEvery):
		}
	}
}

func (j *Job) nightlyIfDue(ctx context.Context) error {
	due := func() (bool, error) {
		last, err := j.LastRun(ctx, NightlyJob)
		if err != nil {
			return false, errors.New(store.SafeError(err))
		}
		return last == nil || last.RanFor.Before(j.previousRun(j.Now())), nil
	}
	if ok, err := due(); err != nil || !ok {
		return err
	}
	return j.locked(ctx, func() error {
		// Another server may have done it while this one waited.
		if ok, err := due(); err != nil || !ok {
			return err
		}
		_, err := j.nightly(ctx, j.previousRun(j.Now()))
		return err
	})
}

// The nightly run.

// NightlyResult is what one nightly run did. It names no talkgroup (P1).
type NightlyResult struct {
	Days       int       `json:"days"`
	Cutoff     time.Time `json:"cutoff"`
	Calls      int64     `json:"calls"`      // call rows deleted
	Recordings int64     `json:"recordings"` // of which had a recording
	Rejections int64     `json:"rejections"` // rejected-call rows pruned
	Orphans    int64     `json:"orphans"`    // recording files with no row removed
	Incoming   int64     `json:"incoming"`   // abandoned upload files removed
	FileErrors int64     `json:"file_errors"`
}

// RunNightly does a nightly run now, by the job's clock. It's how tests
// trigger the run: the binary only runs it on its schedule (Run), and no
// route, setting or signal reaches it.
func (j *Job) RunNightly(ctx context.Context) (NightlyResult, error) {
	var res NightlyResult
	err := j.locked(ctx, func() error {
		var err error
		res, err = j.nightly(ctx, j.Now())
		return err
	})
	return res, err
}

// nightly does the run, holding the lock, and records it as the run for
// ranFor.
func (j *Job) nightly(ctx context.Context, ranFor time.Time) (res NightlyResult, err error) {
	started := j.Now()
	defer func() {
		if serr := j.save(ctx, NightlyJob, ranFor, started, res, err); serr != nil && err == nil {
			err = errors.New(store.SafeError(serr))
		}
		if err != nil {
			j.cfg.Log.Error("nightly retention run failed", "error", err, "calls", res.Calls, "recordings", res.Recordings)
			return
		}
		j.cfg.Log.Info("nightly retention run", "days", res.Days, "cutoff", res.Cutoff, "calls", res.Calls,
			"recordings", res.Recordings, "rejections", res.Rejections, "orphans", res.Orphans,
			"incoming", res.Incoming, "file_errors", res.FileErrors)
	}()
	days, err := j.Days(ctx)
	if err != nil {
		return res, fmt.Errorf("retention setting: %s", store.SafeError(err))
	}
	res.Days = days
	res.Cutoff = Cutoff(started, days).UTC()

	// 1. Calls past the period, hidden and ignored ones alike (R35, R30),
	// in batches: each batch's rows are committed, then their files go.
	for {
		paths, err := j.cfg.Store.DeleteCallsBefore(ctx, db.DeleteCallsBeforeParams{Cutoff: tstz(res.Cutoff), Batch: batch})
		if err != nil {
			return res, fmt.Errorf("delete calls: %s", store.SafeError(err))
		}
		res.Calls += int64(len(paths))
		for _, p := range paths {
			if p == nil {
				continue
			}
			res.Recordings++
			j.Stat.Expired.Add(1)
			if !j.remove(*p) {
				res.FileErrors++
			}
		}
		if len(paths) < batch {
			break
		}
	}
	// 2. Rejected calls are counted over the same period (ADR 0003).
	n, err := j.cfg.Store.PruneRejectedCalls(ctx, tstz(res.Cutoff))
	if err != nil {
		return res, fmt.Errorf("prune rejected calls: %s", store.SafeError(err))
	}
	res.Rejections = n
	// 3. Files with no row, once they're past the grace period.
	if err := j.sweep(ctx, started, &res); err != nil {
		return res, err
	}
	return res, nil
}

// remove deletes a deleted call's recording file. A file already gone is
// fine. One that can't be removed has no row now, so the orphan sweep
// tries again.
func (j *Job) remove(rel string) bool {
	if !filepath.IsLocal(filepath.FromSlash(rel)) {
		j.cfg.Log.Error("a deleted call's recording path isn't on the volume")
		return false
	}
	err := os.Remove(filepath.Join(j.cfg.Dir, filepath.FromSlash(rel)))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		j.cfg.Log.Error("can't remove a deleted call's recording", "error", err)
		return false
	}
	return true
}

// orphan is a recording file the sweep may remove.
type orphan struct {
	rel  string
	path string
	info fs.FileInfo
}

// sweep removes recording files that have no row and abandoned upload
// files, each only once it was last written longer than the grace period
// before now. An upload in progress, or one renamed into place whose row
// isn't committed yet, was written within it. A file dated after now (the
// clock moved back) is kept.
func (j *Job) sweep(ctx context.Context, now time.Time, res *NightlyResult) error {
	before := now.Add(-j.cfg.OrphanGrace)
	old := func(info fs.FileInfo) bool { return info.Mode().IsRegular() && info.ModTime().Before(before) }

	// Abandoned uploads: a server that stopped mid-upload. A file is
	// written to, then renamed out of here in the same request.
	inc := filepath.Join(j.cfg.Dir, IncomingDir)
	entries, err := os.ReadDir(inc)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read the upload directory: %w", err)
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !old(info) {
			continue
		}
		if err := j.removeIfSame(filepath.Join(inc, e.Name()), info); err != nil {
			res.FileErrors++
			continue
		}
		res.Incoming++
	}

	// Recording files: <id[:2]>/<id>.opus (recordings.Path).
	shards, err := os.ReadDir(j.cfg.Dir)
	if err != nil {
		return fmt.Errorf("read the recordings directory: %w", err)
	}
	var pending []orphan
	for _, s := range shards {
		if !s.IsDir() || len(s.Name()) != 2 {
			continue
		}
		files, err := os.ReadDir(filepath.Join(j.cfg.Dir, s.Name()))
		if err != nil {
			return fmt.Errorf("read the recordings directory: %w", err)
		}
		for _, f := range files {
			id, ok := strings.CutSuffix(f.Name(), ".opus")
			var u pgtype.UUID
			if !ok || u.Scan(id) != nil || recordings.Path(id) != s.Name()+"/"+f.Name() {
				continue // not a recording's name: not ours to remove
			}
			info, err := f.Info()
			if err != nil || !old(info) {
				continue
			}
			pending = append(pending, orphan{rel: s.Name() + "/" + f.Name(),
				path: filepath.Join(j.cfg.Dir, s.Name(), f.Name()), info: info})
			if len(pending) == batch {
				if err := j.removeOrphans(ctx, pending, res); err != nil {
					return err
				}
				pending = pending[:0]
			}
		}
	}
	return j.removeOrphans(ctx, pending, res)
}

// removeOrphans removes those of files that no call's row names.
func (j *Job) removeOrphans(ctx context.Context, files []orphan, res *NightlyResult) error {
	if len(files) == 0 {
		return nil
	}
	ids := make([]pgtype.UUID, len(files))
	for i, f := range files {
		_ = ids[i].Scan(strings.TrimSuffix(filepath.Base(f.path), ".opus"))
	}
	stored, err := j.cfg.Store.StoredRecordingPaths(ctx, ids)
	if err != nil {
		return fmt.Errorf("orphan check: %s", store.SafeError(err))
	}
	has := make(map[string]bool, len(stored))
	for _, p := range stored {
		has[p] = true
	}
	for _, f := range files {
		if has[f.rel] {
			continue
		}
		if err := j.removeIfSame(f.path, f.info); err != nil {
			res.FileErrors++
			continue
		}
		res.Orphans++
		j.Stat.Orphans.Add(1)
	}
	return nil
}

// removeIfSame removes path if it's still the file it was when it was
// found old (an upload renamed into place since replaced it) and still as
// old. A file already gone is fine.
func (j *Job) removeIfSame(path string, was fs.FileInfo) error {
	now, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !os.SameFile(was, now) || !now.ModTime().Equal(was.ModTime()) {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		j.cfg.Log.Error("can't remove an orphaned recording file", "error", err)
		return err
	}
	return nil
}

// Low disk.

// ShortenResult is what one low-disk check deleted early.
type ShortenResult struct {
	FreeBefore float64 `json:"free_before"` // fractions of the volume
	FreeAfter  float64 `json:"free_after"`
	Calls      int64   `json:"calls"`
	Recordings int64   `json:"recordings"`
	// OldestKept is when the oldest call left started: retention, as
	// shortened.
	OldestKept *time.Time `json:"oldest_kept,omitempty"`
	FileErrors int64      `json:"file_errors"`
	// Full: still below MinFree with only calls within the floor left.
	Full bool `json:"full"`
}

// CheckDisk measures the recordings volume and, below MinFree, deletes
// the oldest calls, hidden ones alike, one at a time until it isn't:
// "retention shortened" (R88). It never deletes a call that started within
// the early-delete floor: with only those left and the volume still below
// MinFree, it stops and raises "recordings volume full", which clears once
// the free space is back at MinFree. It's the low-disk run the schedule
// does every DiskEvery, and how tests trigger it.
func (j *Job) CheckDisk(ctx context.Context) (ShortenResult, error) {
	var res ShortenResult
	d, err := j.Disk()
	if err != nil {
		j.mu.Lock()
		first := err.Error() != j.diskErr
		j.diskErr = err.Error()
		j.mu.Unlock()
		if first {
			j.cfg.Log.Error("can't measure the recordings volume's free space", "error", err)
		}
		return res, nil // logged once; nothing to decide on
	}
	j.mu.Lock()
	j.diskErr = ""
	j.mu.Unlock()
	if !d.below() {
		j.setFull(false, d)
		return res, nil
	}
	err = j.locked(ctx, func() error {
		started := j.Now()
		res, err = j.shorten(ctx)
		if res.Calls > 0 || err != nil {
			if serr := j.save(ctx, LowDiskJob, started, started, res, err); serr != nil && err == nil {
				err = errors.New(store.SafeError(serr))
			}
		}
		return err
	})
	return res, err
}

func (j *Job) shorten(ctx context.Context) (ShortenResult, error) {
	var res ShortenResult
	d, err := j.Disk()
	if err != nil {
		return res, fmt.Errorf("free space: %w", err)
	}
	res.FreeBefore, res.FreeAfter = d.FreeFraction(), d.FreeFraction()
	defer func() {
		if res.Calls == 0 {
			return
		}
		j.cfg.Log.Warn("retention shortened: recordings deleted early for low disk",
			"recordings", res.Recordings, "calls", res.Calls,
			"free_before", res.FreeBefore, "free_after", res.FreeAfter, "min_free", j.cfg.MinFree,
			"oldest_kept", res.OldestKept, "full", res.Full)
	}()
	cutoff := tstz(j.Now().Add(-j.cfg.EarlyDeleteFloor))
	for d.below() {
		row, err := j.cfg.Store.DeleteOldestCall(ctx, cutoff)
		if errors.Is(err, pgx.ErrNoRows) {
			res.Full = true
			break
		}
		if err != nil {
			return res, fmt.Errorf("delete the oldest call: %s", store.SafeError(err))
		}
		res.Calls++
		if row.RecordingPath != nil {
			res.Recordings++
			j.Stat.Shortened.Add(1)
			if !j.remove(*row.RecordingPath) {
				res.FileErrors++
			}
		}
		if d, err = j.Disk(); err != nil {
			return res, fmt.Errorf("free space: %w", err)
		}
		res.FreeAfter = d.FreeFraction()
	}
	j.setFull(res.Full, d)
	if res.Calls > 0 {
		oldest, err := j.cfg.Store.OldestCallStart(ctx)
		if err == nil && oldest.Valid {
			t := oldest.Time.UTC()
			res.OldestKept = &t
		}
	}
	return res, nil
}

// below is free space below MinFree: low, or full.
func (d Disk) below() bool { return d.FreeFraction() < d.MinFree }

// setFull raises or clears "recordings volume full", logging and counting
// it when it's raised and logging when it clears.
func (j *Job) setFull(full bool, d Disk) {
	j.mu.Lock()
	was := j.full
	j.full = full
	j.mu.Unlock()
	switch {
	case full && !was:
		j.Stat.Full.Add(1)
		j.cfg.Log.Error("recordings volume full: still low on space with only recordings within the early-delete floor left, which aren't deleted early",
			"free", d.FreeFraction(), "min_free", j.cfg.MinFree, "floor", j.cfg.EarlyDeleteFloor)
	case !full && was:
		j.cfg.Log.Info("recordings volume full cleared", "free", d.FreeFraction(), "min_free", j.cfg.MinFree)
	}
}

func tstz(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
