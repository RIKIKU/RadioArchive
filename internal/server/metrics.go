package server

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/RIKIKU/RadioArchive/internal/store/db"
	"github.com/RIKIKU/RadioArchive/internal/telemetry"
)

// MetricsPath is where the metrics are served (R90, ADR 0040): outside the
// API's paths and the app's, behind METRICS_TOKEN. The reverse proxy denies
// it (docs/deploy.md); the monitoring stack scrapes it from inside.
const MetricsPath = "/metrics"

// WithMetrics serves reg's metrics at MetricsPath to a request with token
// as its bearer token. With no token it answers 404.
func WithMetrics(reg *telemetry.Registry, token string) Option {
	return func(a *API) {
		a.handle(MetricsRoute, "GET "+MetricsPath, reg.Handler(token))
	}
}

// How long a scrape reuses what it read from the database: each is a
// query a 15-second scrape shouldn't repeat every time.
const (
	rejectionsEvery = time.Minute
	missingEvery    = time.Minute
	overlapsEvery   = 5 * time.Minute
)

// MissingAfter is how long after its call ends a recording that hasn't
// arrived counts as missing (R19), and MissingWithin how far back calls are
// counted (ADR 0040).
const (
	MissingAfter  = 10 * time.Minute
	MissingWithin = 24 * time.Hour
)

// cached is a value read from the database, reused until it's older than
// every.
type cached[T any] struct {
	mu    sync.Mutex
	every time.Duration
	at    time.Time
	v     T
	ok    bool
}

// get returns the cached value, or read's when it's stale. A read that
// fails keeps the last value it had, if any.
func (c *cached[T]) get(now time.Time, read func() (T, error)) (T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.ok || now.Sub(c.at) >= c.every {
		if v, err := read(); err == nil {
			c.v, c.at, c.ok = v, now, true
		}
	}
	return c.v, c.ok
}

// metricsCache holds the Recorders' cached reads.
type metricsCache struct {
	unattached cached[[]db.UnattachedRejectionsRow]
	talkgroups cached[[]db.TalkgroupRejectionsRow]
	missing    cached[int64]
	overlaps   cached[[]db.RecorderOverlapsRow]
}

func newMetricsCache(fresh bool) *metricsCache {
	m := &metricsCache{}
	if !fresh {
		m.unattached.every, m.talkgroups.every = rejectionsEvery, rejectionsEvery
		m.missing.every, m.overlaps.every = missingEvery, overlapsEvery
	}
	return m
}

// Collect writes the recorder endpoints' metrics (R90): each recorder's
// signals and state (R6, R14), its client's queue (R5) from the last
// heartbeat, rejected calls (R12, R17, R18), recordings missing (R19),
// overlapping calls (R13) and the live delay (R38).
func (rs *Recorders) Collect(ctx context.Context, w *telemetry.Writer) {
	c := &rs.Stat
	w.Set(telemetry.ClientsTooOld, float64(c.ClientsTooOld.Load()))
	w.Set(telemetry.ClientsOutOfDate, float64(c.ClientsOutOfDate.Load()))
	w.Set(telemetry.FramesRefused, float64(c.FramesRefused.Load()))
	for kind, n := range map[string]uint64{
		"frames_unstarted": c.FramesUnstarted.Load(), "unknown_types": c.UnknownTypes.Load(),
		"protocol_closes": c.ProtocolCloses.Load(), "revoked": c.Revoked.Load(),
		"bad_heartbeats": rs.Sig.BadHeartbeats.Load(), "signal_check_failed": rs.Sig.CheckFailed.Load(),
		"calls_withdrawn": c.Withdrawn.Load(),
	} {
		w.Set(telemetry.RecorderErrors, float64(n), kind)
	}
	each(&c.Uploads, func(status string, n uint64) { w.Set(telemetry.Uploads, float64(n), status) })
	for _, reason := range []db.RejectionReason{db.RejectionReasonNotAttached, db.RejectionReasonUnknownTalkgroup, db.RejectionReasonIgnoredTalkgroup} {
		w.Set(telemetry.CallsRejected, float64(c.RejectedCount(string(reason))), string(reason))
	}
	for _, sig := range []string{SignalDisconnected, SignalDeaf, SignalQuiet} {
		w.Set(telemetry.SignalsTripped, float64(rs.Sig.TrippedCount(sig)), sig)
	}
	w.Bool(telemetry.RecorderNotice, rs.Sig.NoticeActive.Load())
	w.Histogram(telemetry.LiveDelay, c.LiveDelay)

	rs.collectRecorders(ctx, w)
	rs.collectCalls(ctx, w)
}

// collectRecorders writes each active recorder's signals and state.
func (rs *Recorders) collectRecorders(ctx context.Context, w *telemetry.Writer) {
	rows, err := rs.st.ListRecorderSignals(ctx)
	if err != nil {
		return
	}
	type beat struct {
		at      time.Time
		entries int
		oldest  int64
	}
	g := rs.sig
	now := g.now()
	g.mu.Lock()
	ss := g.evaluate(rows, now)
	beats := map[int64]beat{}
	for id, st := range g.recs {
		if st.connected && st.heartbeat {
			beats[id] = beat{st.beatAt, st.queue.Count, st.queue.OldestAgeS}
		}
	}
	g.mu.Unlock()
	for _, s := range ss {
		r, name := s.row, s.row.Name
		w.Bool(telemetry.RecorderConnected, s.connected, name)
		for _, sig := range []string{SignalDisconnected, SignalDeaf, SignalQuiet} {
			w.Bool(telemetry.RecorderSignal, s.tripped(sig), name, sig)
		}
		for _, why := range []string{DeafNoHeartbeat, DeafDecodeRate} {
			w.Bool(telemetry.RecorderDeaf, s.deaf == why, name, why)
		}
		if q := r.QuietAfterMinutes; q != nil {
			w.Set(telemetry.RecorderQuietAfter, float64(*q)*60, name)
		}
		w.Time(telemetry.RecorderLastCall, r.LastCallAt.Time, name)
		w.Time(telemetry.RecorderLastConnected, r.ClientSeenAt.Time, name)
		if v := r.ClientVersion; v != nil {
			w.Set(telemetry.RecorderClient, 1, name, *v)
		}
		w.Bool(telemetry.RecorderOutOfDate, r.ClientOutOfDate, name)
		for sn, rate := range s.systems {
			w.Set(telemetry.RecorderDecodeRate, rate.DecodeRate, name, sn)
		}
		if b, ok := beats[r.ID]; ok {
			w.Set(telemetry.RecorderQueueEntries, float64(b.entries), name)
			w.Set(telemetry.RecorderQueueOldest, float64(b.oldest), name)
			w.Time(telemetry.RecorderLastHeartbeat, b.at, name)
		}
	}
}

// collectCalls writes what the database says of calls: rejections over
// the retention period, recordings missing, and overlaps in the last day.
func (rs *Recorders) collectCalls(ctx context.Context, w *telemetry.Writer) {
	m := rs.metrics
	now := time.Now()
	if rows, ok := m.unattached.get(now, func() ([]db.UnattachedRejectionsRow, error) { return rs.st.UnattachedRejections(ctx) }); ok {
		for _, r := range rows {
			w.Set(telemetry.RejectedUnattached, float64(r.Calls), r.Recorder, r.ShortName)
		}
	}
	if rows, ok := m.talkgroups.get(now, func() ([]db.TalkgroupRejectionsRow, error) { return rs.st.TalkgroupRejections(ctx, nil) }); ok {
		for _, r := range rows {
			w.Set(telemetry.RejectedTalkgroup, float64(r.Calls), itoa(r.SystemID), itoa(int64(r.Talkgroup)), string(r.Reason))
		}
	}
	if n, ok := m.missing.get(now, func() (int64, error) {
		return rs.st.CountMissingRecordings(ctx, db.CountMissingRecordingsParams{
			EndedAfter: tstz(now.Add(-MissingWithin)), EndedBefore: tstz(now.Add(-MissingAfter))})
	}); ok {
		w.Set(telemetry.RecordingsMissing, float64(n))
	}
	if rows, ok := m.overlaps.get(now, func() ([]db.RecorderOverlapsRow, error) {
		return rs.st.RecorderOverlaps(ctx, db.RecorderOverlapsParams{Since: tstz(now.Add(-24 * time.Hour)), Until: tstz(now)})
	}); ok {
		for _, r := range rows {
			w.Set(telemetry.OverlappingCalls, float64(r.Calls), itoa(r.SystemID), itoa(int64(r.Talkgroup)), r.FirstRecorder, r.SecondRecorder)
		}
	}
}

func each(m *sync.Map, f func(string, uint64)) {
	m.Range(func(k, v any) bool {
		f(k.(string), v.(*atomic.Uint64).Load())
		return true
	})
}

func tstz(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
