package server_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
	"github.com/RIKIKU/RadioArchive/internal/public"
	"github.com/RIKIKU/RadioArchive/internal/recordings"
	"github.com/RIKIKU/RadioArchive/internal/retention"
)

// Retention (SPEC.md R88, P5, R35, A30; ADR 0005).
//
// The tests share one database with every other package's tests, which run
// at the same time, and retention deletes by age across it. So these tests
// keep to their own era: their calls started in 2001, and their clock is
// set then, while every other test's calls are from 2025 on. A nightly run
// at a 2001 "now" can only reach calls and rejections from before 2001, and
// the low-disk test's free space reads as plentiful the moment the oldest
// call left isn't from the era.

// testClock is the job's clock: the real time until set.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.t.IsZero() {
		return time.Now()
	}
	return c.t
}

func (c *testClock) set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

// testDisk is the job's free-space measure: half free until set.
type testDisk struct {
	mu sync.Mutex
	f  func() (retention.Space, error)
}

func (d *testDisk) space(string) (retention.Space, error) {
	d.mu.Lock()
	f := d.f
	d.mu.Unlock()
	if f == nil {
		return retention.Space{Free: 500, Total: 1000}, nil
	}
	return f()
}

func (d *testDisk) set(f func() (retention.Space, error)) {
	d.mu.Lock()
	d.f = f
	d.mu.Unlock()
}

var (
	// eraEnd: calls and rejections before it are these tests' alone.
	eraEnd = time.Date(2002, 1, 1, 0, 0, 0, 0, time.UTC)
	// eraNow is the tests' "now": the night of 1 March 2001, just after
	// the nightly run's time.
	eraNow = time.Date(2001, 3, 1, 3, 0, 30, 0, time.UTC)
	day    = 24 * time.Hour
)

// era clears what earlier runs of these tests left in the era, the
// retention setting and the jobs' runs, now and when the test ends, and
// sets the job's clock to eraNow.
func (e *env) era(t *testing.T) {
	t.Helper()
	clear := func() {
		ctx := context.Background()
		for _, q := range []string{
			`DELETE FROM calls WHERE started_at < $1`,
			`DELETE FROM rejected_calls WHERE rejected_at < $1`,
		} {
			if _, err := e.st.Pool.Exec(ctx, q, eraEnd); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := e.st.Pool.Exec(ctx, `DELETE FROM settings WHERE key = 'retention_days'; DELETE FROM jobs`); err != nil {
			t.Fatal(err)
		}
	}
	clear()
	t.Cleanup(clear)
	e.clock.set(eraNow)
}

// storeAt stores a recording on talkgroup tg for a call that started at
// started, and returns the call's ID.
func (e *env) storeAt(t *testing.T, key string, n int64, tg int32, started time.Time) string {
	t.Helper()
	if !started.Before(eraEnd) {
		t.Fatalf("a retention test's call must start before %s", eraEnd)
	}
	audio := oggBytes(byte(n))
	m := recording(cp.CallID{CallNum: n, StartTime: started.Unix()}, audio)
	m.Talkgroup, m.PatchedTalkgroups = tg, nil
	m.StartTimeMS, m.StopTimeMS = started.UnixMilli(), started.UnixMilli()+5000
	m.Transmissions = []cp.RecordingTransmission{{Unit: 5001, OffsetMS: 0, DurationMS: 5000}}
	code, resp := e.upload(t, key, m, audio)
	if code != 201 {
		t.Fatalf("upload of call %d: %d %+v", n, code, resp)
	}
	return resp.CallID
}

// stored says whether a call's row and its recording's file are there.
func (e *env) stored(t *testing.T, id string) (row, file bool) {
	t.Helper()
	var n int
	if err := e.st.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM calls WHERE id = $1::uuid`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	_, err := os.Stat(filepath.Join(e.dir, filepath.FromSlash(recordings.Path(id))))
	return n == 1, err == nil
}

func (e *env) wantGone(t *testing.T, why string, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if row, file := e.stored(t, id); row || file {
			t.Errorf("%s %s: row %v, file %v; want both gone", why, id, row, file)
		}
	}
}

func (e *env) wantKept(t *testing.T, why string, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if row, file := e.stored(t, id); !row || !file {
			t.Errorf("%s %s: row %v, file %v; want both kept", why, id, row, file)
		}
	}
}

// retentionView is GET /api/v1/admin/retention, as a client reads it.
type retentionView struct {
	Days        int       `json:"days"`
	DefaultDays int       `json:"default_days"`
	NextRun     time.Time `json:"next_run"`
	Disk        *struct {
		FreePercent float64 `json:"free_percent"`
		State       string  `json:"state"`
		Message     string  `json:"message"`
	} `json:"disk"`
	LastNightly   *retention.Run `json:"last_nightly"`
	LastShortened *retention.Run `json:"last_shortened"`
}

func (e *env) retentionState(t *testing.T) retentionView {
	t.Helper()
	return decodeJSON[retentionView](t, e.mustAdmin(t, "GET", "/retention", nil, 200))
}

// TestIntegrationA30Retention is A30's first half: with retention 30 days,
// the nightly run deletes recordings 31 days old and their rows, hidden ones
// alike (R35), and keeps 29-day-old ones. A deleted call then answers every
// public route exactly as a call that never existed (A10, R61), and the
// rejected calls of the same age are pruned with them (ADR 0003).
func TestIntegrationA30Retention(t *testing.T) {
	e := newEnv(t)
	name, key := e.recorder(t)
	e.era(t)
	ctx := context.Background()

	if st := e.retentionState(t); st.Days != 30 || st.DefaultDays != 30 || st.LastNightly != nil {
		t.Fatalf("retention before any change or run: %+v, want 30 days and no run", st)
	}
	oldPublic := e.storeAt(t, key, 1, 1001, eraNow.Add(-31*day))
	oldHidden := e.storeAt(t, key, 2, 1003, eraNow.Add(-31*day))
	older := e.storeAt(t, key, 3, 1001, eraNow.Add(-40*day))
	keptPublic := e.storeAt(t, key, 4, 1001, eraNow.Add(-29*day))
	keptHidden := e.storeAt(t, key, 5, 1003, eraNow.Add(-29*day))

	// Rejected calls: one older than the period and one within it.
	var rec int64
	if err := e.st.Pool.QueryRow(ctx, `SELECT id FROM recorders WHERE name = $1`, name).Scan(&rec); err != nil {
		t.Fatal(err)
	}
	for i, at := range []time.Time{eraNow.Add(-31 * day), eraNow.Add(-day)} {
		if _, err := e.st.Pool.Exec(ctx, `INSERT INTO rejected_calls
			(recorder_id, sys_num, call_num, start_time, short_name, talkgroup, reason, rejected_at)
			VALUES ($1, 0, $2, $3, 'other', 1001, 'not_attached', $4)`, rec, 100+i, at.Unix(), at); err != nil {
			t.Fatal(err)
		}
	}

	// The deleted public call is served before the run.
	if a := do(t, e.publicReq("GET", public.RecordingURL(oldPublic), "")); a.status != 200 {
		t.Fatalf("before the run, the 31-day-old public call's recording: %s", a)
	}

	res, err := e.job.RunNightly(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Days != 30 || !res.Cutoff.Equal(eraNow.Add(-30*day)) || res.Calls != 3 || res.Recordings != 3 ||
		res.Rejections != 1 || res.FileErrors != 0 {
		t.Errorf("the run: %+v, want 3 calls and recordings and 1 rejection at 30 days", res)
	}
	e.wantGone(t, "past retention", oldPublic, oldHidden, older)
	e.wantKept(t, "within retention", keptPublic, keptHidden)
	var rejections int
	if err := e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM rejected_calls WHERE recorder_id = $1`, rec).Scan(&rejections); err != nil {
		t.Fatal(err)
	}
	if rejections != 1 {
		t.Errorf("%d rejected calls left, want the 1 within retention", rejections)
	}
	if got := e.job.Stat.Expired.Load(); got != 3 {
		t.Errorf("expired recordings counted: %d, want 3", got)
	}

	// Deleted answers as never stored, on every public route (A10).
	never := "6f1d2c3b-4a59-4e8f-9d7c-0b1a2c3d4e5f"
	for _, probe := range []struct {
		name string
		req  func(id string) *http.Request
	}{
		{"recording", func(id string) *http.Request { return e.publicReq("GET", public.RecordingURL(id), "") }},
		{"recording HEAD", func(id string) *http.Request { return e.publicReq("HEAD", public.RecordingURL(id), "") }},
		{"call list", func(id string) *http.Request {
			return e.publicReq("POST", public.CallsPath, fmt.Sprintf(`{"ids":[%q]}`, id))
		}},
		{"call list QUERY", func(id string) *http.Request {
			return e.publicReq("QUERY", public.CallsPath, fmt.Sprintf(`{"ids":[%q]}`, id))
		}},
	} {
		for _, id := range []string{oldPublic, oldHidden} {
			if got, want := do(t, probe.req(id)), do(t, probe.req(never)); got.String() != want.String() {
				t.Errorf("%s of a deleted call answers\n%s\nbut a call never stored answers\n%s", probe.name, got, want)
			}
		}
	}
	if a := do(t, e.publicReq("GET", public.RecordingURL(keptPublic), "")); a.status != 200 {
		t.Errorf("the 29-day-old public call's recording: %s", a)
	}
	if code, _ := e.adminReq(t, "test admin", "GET", "/calls/"+oldHidden+"/recording", nil); code != 404 {
		t.Errorf("admin playback of a deleted hidden call = %d, want 404", code)
	}
	if code, _ := e.adminReq(t, "test admin", "GET", "/calls/"+keptHidden+"/recording", nil); code != 200 {
		t.Errorf("admin playback of a kept hidden call = %d, want 200", code)
	}

	// The run is recorded, and a second deletes nothing more.
	st := e.retentionState(t)
	if st.LastNightly == nil || !st.LastNightly.OK || !strings.Contains(string(st.LastNightly.Result), `"recordings":3`) {
		t.Errorf("last nightly run: %+v", st.LastNightly)
	}
	if res, err := e.job.RunNightly(ctx); err != nil || res.Calls != 0 {
		t.Errorf("a second run: %+v, %v; want nothing deleted", res, err)
	}
	e.wantKept(t, "within retention, after a second run", keptPublic, keptHidden)
}

func (e *env) publicReq(method, path, body string) *http.Request {
	f := &a10World{e: e}
	return f.req(method, path, body)
}

// TestIntegrationRetentionOrphans: the nightly run removes a recording file
// with no row once it's past the grace period, and keeps one within it, as
// an upload renamed into place whose row isn't committed yet would be. The
// same goes for the upload directory, where an upload in progress is
// written. A stored recording's file stays however old, and a file that
// isn't named as a recording is never touched.
func TestIntegrationRetentionOrphans(t *testing.T) {
	e := newEnv(t)
	_, key := e.recorder(t)
	e.era(t)
	ctx := context.Background()

	file := func(rel string, age time.Duration) string {
		t.Helper()
		p := filepath.Join(e.dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("OggS orphan"), 0o640); err != nil {
			t.Fatal(err)
		}
		if age >= 0 {
			at := eraNow.Add(-age)
			if err := os.Chtimes(p, at, at); err != nil {
				t.Fatal(err)
			}
		}
		return p
	}
	uuid := func() string {
		return fmt.Sprintf("%08x-%04x-4%03x-8%03x-%012x", rand.Uint32(), rand.Uint32N(1<<16), rand.Uint32N(1<<12),
			rand.Uint32N(1<<12), rand.Uint64N(1<<48))
	}
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }

	// A stored call (within retention) whose file was written long ago.
	stored := e.storeAt(t, key, 1, 1001, eraNow.Add(-2*day))
	storedFile := filepath.Join(e.dir, filepath.FromSlash(recordings.Path(stored)))
	if err := os.Chtimes(storedFile, eraNow.Add(-48*time.Hour), eraNow.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	grace := retention.DefaultOrphanGrace
	orphanOld := file(recordings.Path(uuid()), grace+time.Hour)
	orphanNew := file(recordings.Path(uuid()), grace-time.Hour) // renamed into place, row not committed yet
	orphanFuture := file(recordings.Path(uuid()), -1)           // written at the real time, after the job's clock
	incomingOld := file(retention.IncomingDir+"/upload-1", grace+time.Hour)
	incomingNew := file(retention.IncomingDir+"/upload-2", time.Minute) // an upload being written
	stray := file(recordings.Path(uuid())[:3]+"notes.txt", grace+time.Hour)

	res, err := e.job.RunNightly(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Orphans != 1 || res.Incoming != 1 || res.FileErrors != 0 {
		t.Errorf("the run: %+v, want 1 orphan and 1 abandoned upload removed", res)
	}
	for p, want := range map[string]bool{
		storedFile: true, orphanOld: false, orphanNew: true, orphanFuture: true,
		incomingOld: false, incomingNew: true, stray: true,
	} {
		if got := exists(p); got != want {
			t.Errorf("%s exists: %v, want %v", strings.TrimPrefix(p, e.dir), got, want)
		}
	}
	e.wantKept(t, "stored", stored)

	// The upload path still works: the incoming directory is still there.
	e.storeAt(t, key, 2, 1001, eraNow.Add(-day))
}

// TestIntegrationA30LowDisk is A30's second half: below RECORDINGS_MIN_FREE
// the oldest recordings are deleted, hidden ones alike, until it isn't, and
// counted as "retention shortened". Admins see the volume's state.
func TestIntegrationA30LowDisk(t *testing.T) {
	e := newEnv(t)
	_, key := e.recorder(t)
	e.era(t)
	ctx := context.Background()

	// Six recordings, 10 to 5 days old: well within retention.
	var ids []string
	for i := range 6 {
		tg := int32(1001)
		if i == 1 {
			tg = 1003
		}
		ids = append(ids, e.storeAt(t, key, int64(i+1), tg, eraNow.Add(-time.Duration(10-i)*day)))
	}

	// The volume: 1000 bytes, 25 free with the six recordings on it, and
	// 10 more for each one removed. So 2.5 % free, and above 5 % once three
	// are gone. It reads as plentiful once the oldest call isn't one of
	// this test's era, so the job can't reach another test's calls.
	var measured atomic.Int64
	e.disk.set(func() (retention.Space, error) {
		if measured.Add(1) > 100 {
			return retention.Space{}, errors.New("measured too often")
		}
		var inEra bool
		if err := e.st.Pool.QueryRow(ctx, `SELECT COALESCE(min(started_at) < $1, false) FROM calls`, eraEnd).Scan(&inEra); err != nil {
			return retention.Space{}, err
		}
		if !inEra {
			return retention.Space{Free: 500, Total: 1000}, nil
		}
		n := 0
		err := filepath.WalkDir(e.dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, ".opus") {
				n++
			}
			return err
		})
		return retention.Space{Free: uint64(25 + 10*(6-n)), Total: 1000}, err
	})

	st := e.retentionState(t)
	if st.Disk == nil || st.Disk.State != "low" || st.Disk.FreePercent != 2.5 || !strings.Contains(st.Disk.Message, "retention shortened") {
		t.Fatalf("disk before: %+v, want low at 2.5 %%", st.Disk)
	}

	res, err := e.job.CheckDisk(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Recordings != 3 || res.Calls != 3 || res.FreeBefore != 0.025 || res.FreeAfter != 0.055 ||
		res.OldestKept == nil || !res.OldestKept.Equal(eraNow.Add(-7*day)) {
		t.Errorf("the check: %+v, want the 3 oldest deleted, 7 days kept", res)
	}
	e.wantGone(t, "oldest, deleted early", ids[:3]...)
	e.wantKept(t, "newer, kept", ids[3:]...)
	if got := e.job.Stat.Shortened.Load(); got != 3 {
		t.Errorf("retention shortened counted %d recordings, want 3", got)
	}

	st = e.retentionState(t)
	if st.Disk == nil || st.Disk.State != "warning" || st.Disk.FreePercent != 5.5 || st.Disk.Message == "" {
		t.Errorf("disk after: %+v, want a warning at 5.5 %%", st.Disk)
	}
	if st.LastShortened == nil || !st.LastShortened.OK || !strings.Contains(string(st.LastShortened.Result), `"recordings":3`) {
		t.Errorf("last shortened: %+v", st.LastShortened)
	}

	// Above the minimum, a check deletes nothing.
	if res, err := e.job.CheckDisk(ctx); err != nil || res.Calls != 0 {
		t.Errorf("a check at 5.5 %%: %+v, %v; want nothing deleted", res, err)
	}
	e.wantKept(t, "after a check above the minimum", ids[3:]...)

	// The warning shows below 15 %, and nothing at or above it.
	for _, c := range []struct {
		free  uint64
		state string
	}{{100, "warning"}, {149, "warning"}, {150, "ok"}, {600, "ok"}} {
		e.disk.set(func() (retention.Space, error) { return retention.Space{Free: c.free, Total: 1000}, nil })
		st := e.retentionState(t)
		if st.Disk == nil || st.Disk.State != c.state || (c.state == "ok") != (st.Disk.Message == "") {
			t.Errorf("%d of 1000 free: %+v, want %s", c.free, st.Disk, c.state)
		}
		if res, err := e.job.CheckDisk(ctx); err != nil || res.Calls != 0 {
			t.Errorf("%d of 1000 free: check %+v, %v; want nothing deleted", c.free, res, err)
		}
	}

	// Unmeasurable: no disk state, and the check deletes nothing.
	e.disk.set(func() (retention.Space, error) { return retention.Space{}, errors.New("no statfs") })
	if st := e.retentionState(t); st.Disk != nil {
		t.Errorf("unmeasurable disk: %+v, want null", st.Disk)
	}
	if res, err := e.job.CheckDisk(ctx); err != nil || res.Calls != 0 {
		t.Errorf("unmeasurable disk: check %+v, %v", res, err)
	}
	e.wantKept(t, "at the end", ids[3:]...)
}

// TestIntegrationRetentionSetting: the period is a setting changed through
// the admin API. Lengthening it just saves it; shortening it needs
// confirming, and the answer says how many recordings will go, hidden ones
// included (R88).
func TestIntegrationRetentionSetting(t *testing.T) {
	e := newEnv(t)
	_, key := e.recorder(t)
	e.era(t)
	ctx := context.Background()

	e.storeAt(t, key, 1, 1001, eraNow.Add(-10*day))
	e.storeAt(t, key, 2, 1003, eraNow.Add(-10*day))
	recent := e.storeAt(t, key, 3, 1001, eraNow.Add(-3*day))

	st := e.retentionState(t)
	if st.Days != 30 || !st.NextRun.Equal(time.Date(2001, 3, 2, 3, 0, 0, 0, time.UTC)) {
		t.Errorf("retention: %+v, want 30 days, next run 2 March 03:00", st)
	}
	if b := e.mustAdmin(t, "PUT", "/retention", map[string]any{"days": 60}, 200); decodeJSON[struct{ Days int }](t, b).Days != 60 {
		t.Errorf("lengthening: %s", b)
	}
	b := e.mustAdmin(t, "PUT", "/retention", map[string]any{"days": 7}, 409)
	c := decodeJSON[struct {
		Error, Message   string
		Days, Recordings int
	}](t, b)
	if c.Error != "confirm_shorter_retention" || c.Days != 7 || c.Recordings != 2 ||
		!strings.Contains(c.Message, "2 recordings") || !strings.Contains(c.Message, "from 60 to 7 days") {
		t.Errorf("shortening unconfirmed: %s", b)
	}
	if st := e.retentionState(t); st.Days != 60 {
		t.Errorf("after an unconfirmed change: %d days, want 60", st.Days)
	}
	if b := e.mustAdmin(t, "PUT", "/retention", map[string]any{"days": 7, "confirm": true}, 200); decodeJSON[struct{ Days int }](t, b).Days != 7 {
		t.Errorf("shortening confirmed: %s", b)
	}
	for _, d := range []any{0, -1, 3651, "30"} {
		code := "invalid_days"
		if _, ok := d.(string); ok {
			code = "invalid_body"
		}
		e.wantRefusal(t, "PUT", "/retention", map[string]any{"days": d, "confirm": true}, 400, code)
	}
	e.wantRefusal(t, "PUT", "/retention", map[string]any{"days": 7, "please": true}, 400, "invalid_body")

	res, err := e.job.RunNightly(ctx)
	if err != nil || res.Days != 7 || res.Recordings != 2 {
		t.Errorf("the run at 7 days: %+v, %v; want the 2 calls 10 days old deleted", res, err)
	}
	e.wantKept(t, "3 days old", recent)
}

// TestIntegrationRetentionLock: a run needs the advisory lock, so while
// another server holds it, neither the nightly run nor the low-disk check
// runs here.
func TestIntegrationRetentionLock(t *testing.T) {
	e := newEnv(t)
	_, key := e.recorder(t)
	e.era(t)
	ctx := context.Background()
	old := e.storeAt(t, key, 1, 1001, eraNow.Add(-31*day))

	other, err := e.st.Pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Exec(ctx, `SELECT pg_advisory_lock($1)`, retention.LockKey); err != nil {
		t.Fatal(err)
	}
	if _, err := e.job.RunNightly(ctx); !errors.Is(err, retention.ErrBusy) {
		t.Errorf("nightly run while locked: %v, want ErrBusy", err)
	}
	e.disk.set(func() (retention.Space, error) { return retention.Space{Free: 1, Total: 1000}, nil })
	if _, err := e.job.CheckDisk(ctx); !errors.Is(err, retention.ErrBusy) {
		t.Errorf("low-disk check while locked: %v, want ErrBusy", err)
	}
	e.disk.set(nil)
	e.wantKept(t, "while locked", old)

	if _, err := other.Exec(ctx, `SELECT pg_advisory_unlock($1)`, retention.LockKey); err != nil {
		t.Fatal(err)
	}
	other.Release()
	if res, err := e.job.RunNightly(ctx); err != nil || res.Recordings != 1 {
		t.Errorf("nightly run once unlocked: %+v, %v", res, err)
	}
	e.wantGone(t, "once unlocked", old)
}

// runCounter counts the nightly runs logged.
type runCounter struct {
	slog.Handler
	n *atomic.Int64
}

func (h runCounter) Handle(ctx context.Context, r slog.Record) error {
	if r.Message == "nightly retention run" {
		h.n.Add(1)
	}
	return nil
}

func (runCounter) Enabled(context.Context, slog.Level) bool { return true }

func (h runCounter) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h runCounter) WithGroup(string) slog.Handler      { return h }

// TestIntegrationRetentionSchedule: on its schedule, of two servers on one
// database the nightly run is done once a night, a night missed while no
// server ran is made up at once, and a run done isn't repeated.
func TestIntegrationRetentionSchedule(t *testing.T) {
	e := newEnv(t)
	_, key := e.recorder(t)
	e.era(t)
	old := e.storeAt(t, key, 1, 1001, eraNow.Add(-31*day))

	var runs atomic.Int64
	log := slog.New(runCounter{Handler: slog.DiscardHandler, n: &runs})
	var servers []*retention.Job
	for range 2 {
		j, err := retention.New(retention.Config{Store: e.st, Dir: e.dir, Log: log, Now: e.clock.now,
			FreeSpace: e.disk.space, Location: time.UTC, DiskEvery: 5 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		servers = append(servers, j)
	}
	runFor := func(d time.Duration) {
		ctx, cancel := context.WithTimeout(context.Background(), d)
		defer cancel()
		var wg sync.WaitGroup
		for _, j := range servers {
			wg.Add(1)
			go func() { defer wg.Done(); j.Run(ctx) }()
		}
		wg.Wait()
	}
	lastRanFor := func() time.Time {
		r, err := e.job.LastRun(context.Background(), retention.NightlyJob)
		if err != nil || r == nil {
			t.Fatalf("last nightly run: %+v, %v", r, err)
		}
		return r.RanFor
	}

	// No run yet: one is made up at once, for tonight's 03:00.
	runFor(500 * time.Millisecond)
	if got := runs.Load(); got != 1 {
		t.Errorf("%d nightly runs, want 1", got)
	}
	if got := lastRanFor(); !got.Equal(time.Date(2001, 3, 1, 3, 0, 0, 0, time.UTC)) {
		t.Errorf("the run was for %s, want 1 March 03:00", got)
	}
	e.wantGone(t, "after the scheduled run", old)

	// Later the same night: nothing more.
	e.clock.set(eraNow.Add(20 * time.Hour))
	runFor(200 * time.Millisecond)
	if got := runs.Load(); got != 1 {
		t.Errorf("%d nightly runs before the next night, want 1", got)
	}

	// The next night, once.
	e.clock.set(eraNow.Add(day))
	runFor(500 * time.Millisecond)
	if got := runs.Load(); got != 2 {
		t.Errorf("%d nightly runs after the next night, want 2", got)
	}
	if got := lastRanFor(); !got.Equal(time.Date(2001, 3, 2, 3, 0, 0, 0, time.UTC)) {
		t.Errorf("the run was for %s, want 2 March 03:00", got)
	}
}
