package gate

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/RIKIKU/RadioArchive/internal/store"
	"github.com/RIKIKU/RadioArchive/internal/store/db"
)

// Quiet talkgroups (SPEC.md R114-R117; ADR 0054). A public talkgroup is
// quiet when its system's recorders have been receiving for the whole
// quiet window since it was last heard publicly, or, never heard, for a
// whole window at all. Only the time they were receiving counts, so an
// outage stops the clock rather than emptying the lists (R115), and a new
// deployment hides nothing until it has received for one window (R116).
// The first public call on a quiet talkgroup brings it back (R117).
// Always show, on the talkgroup or its tag, and a delay (R23: a delayed
// talkgroup coming back would say a call had started) keep a talkgroup
// from ever being quiet.
//
// Quiet is a view, never a state: listeners' lists leave quiet talkgroups
// out, but the catalogue still carries them, marked, and the live feed and
// listening sets are untouched.

// The quiet window, a setting (R114).
const (
	DefaultQuietDays = 7
	MinQuietDays     = 1
	MaxQuietDays     = 90
	// QuietDaysKey is its key in the settings.
	QuietDaysKey = "quiet_window_days"
)

// ReceivingKept is how long the spans a system was receiving are kept.
// Longer than any window: a system that has received for less than a
// window in that time has nothing quiet.
const ReceivingKept = 400 * 24 * time.Hour

// quietFresh is how long a read of the quiet talkgroups is used for. Time
// passing makes a talkgroup quiet, so it's read again at least this often;
// a talkgroup heard is brought back at once (TalkgroupsHeard) when this
// server ingests its call, and within this on any other.
const quietFresh = 30 * time.Second

// TalkgroupKey names a talkgroup across systems.
type TalkgroupKey struct {
	System    int64
	Talkgroup int32
}

// Span is a time a system was receiving.
type Span struct {
	Start, End time.Time
}

// Quiet is what says which talkgroups are quiet, read at one moment.
type Quiet struct {
	At         time.Time
	WindowDays int
	// cutoff, by system: a talkgroup of it not heard publicly since then is
	// quiet. A system that hasn't received for a whole window has none.
	cutoff map[int64]time.Time
	// received, by system: how long it has been receiving, up to a window.
	received map[int64]time.Duration
	heard    map[TalkgroupKey]time.Time
}

// Window is the quiet window.
func (q *Quiet) Window() time.Duration { return time.Duration(q.WindowDays) * 24 * time.Hour }

// Of says whether a public talkgroup is quiet, and if so for how many
// whole days it hasn't been heard: since it was last heard, or the
// window, when it's never been heard publicly.
func (q *Quiet) Of(t CatalogueTalkgroup) (bool, int) {
	if q == nil || t.AlwaysShow || t.DelayMinutes > 0 {
		return false, 0
	}
	cut, ok := q.cutoff[t.System.ID]
	if !ok {
		return false, 0
	}
	h, heard := q.heard[TalkgroupKey{t.System.ID, t.ID}]
	if heard && !h.Before(cut) {
		return false, 0
	}
	if !heard {
		return true, q.WindowDays
	}
	return true, max(q.WindowDays, int(q.At.Sub(h)/(24*time.Hour)))
}

// Received is how long a system has been receiving, up to the window, and
// whether that's the whole window, so its talkgroups can be quiet (R116).
func (q *Quiet) Received(system int64) (time.Duration, bool) {
	_, full := q.cutoff[system]
	if full {
		return q.Window(), true
	}
	return q.received[system], false
}

// Shown filters talkgroups found by a search for query (R114): those not
// quiet, and quiet ones too when include is set or the query is exactly
// one's number, name or label.
func (q *Quiet) Shown(query string, ts []CatalogueTalkgroup, include bool) []CatalogueTalkgroup {
	if include {
		return ts
	}
	query = strings.ToLower(strings.TrimSpace(query))
	out := make([]CatalogueTalkgroup, 0, len(ts))
	for _, t := range ts {
		if quiet, _ := q.Of(t); !quiet || ExactMatch(t, query) {
			out = append(out, t)
		}
	}
	return out
}

// ExactMatch says whether a lower-case, trimmed query is exactly a
// talkgroup's number, name or label, ignoring case.
func ExactMatch(t CatalogueTalkgroup, query string) bool {
	return query != "" && (query == strconv.Itoa(int(t.ID)) || query == strings.ToLower(t.Name) ||
		query == strings.ToLower(t.Label))
}

// cutoffOf is the moment after which a system has received for exactly
// window up to now, from its spans newest first; ok is false while it has
// received for less (R116). Overlapping spans are counted once. received
// is how long it has received, up to the window.
func cutoffOf(spans []Span, window time.Duration, now time.Time) (cut time.Time, received time.Duration, ok bool) {
	edge := now // what's before it hasn't been counted yet
	for _, s := range spans {
		end := s.End
		if end.After(edge) {
			end = edge
		}
		if !end.After(s.Start) {
			continue
		}
		received += end.Sub(s.Start)
		if received >= window {
			return s.Start.Add(received - window), window, true
		}
		edge = s.Start
	}
	return time.Time{}, received, false
}

// QuietDays is the quiet window in force, in days.
func QuietDays(ctx context.Context, q *db.Queries) (int, error) {
	v, err := q.GetSetting(ctx, QuietDaysKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultQuietDays, nil
	}
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < MinQuietDays || n > MaxQuietDays {
		return DefaultQuietDays, nil
	}
	return n, nil
}

// Quiet reads which talkgroups are quiet now, or answers the read made in
// the last quietFresh, unless a talkgroup it holds quiet has been heard.
func (g *Gate) Quiet(ctx context.Context) (*Quiet, error) {
	now := g.now()
	g.quietMu.Lock()
	defer g.quietMu.Unlock()
	if g.quiet != nil && !g.quietStale && now.Sub(g.quiet.At) < quietFresh && !now.Before(g.quiet.At) {
		return g.quiet, nil
	}
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	q, err := g.readQuiet(ctx, now)
	if err != nil {
		return nil, err
	}
	g.quiet, g.quietStale = q, false
	return q, nil
}

func (g *Gate) readQuiet(ctx context.Context, now time.Time) (*Quiet, error) {
	days, err := QuietDays(ctx, g.q)
	if err != nil {
		return nil, fmt.Errorf("quiet window: %s", store.SafeError(err))
	}
	spans, err := g.q.QuietReceiving(ctx, pgtype.Timestamptz{Time: now.Add(-ReceivingKept).UTC(), Valid: true})
	if err != nil {
		return nil, fmt.Errorf("receiving: %s", store.SafeError(err))
	}
	heard, err := g.q.QuietHeard(ctx)
	if err != nil {
		return nil, fmt.Errorf("heard: %s", store.SafeError(err))
	}
	q := &Quiet{At: now, WindowDays: days, cutoff: map[int64]time.Time{}, received: map[int64]time.Duration{},
		heard: make(map[TalkgroupKey]time.Time, len(heard))}
	window := q.Window()
	bySystem := map[int64][]Span{}
	for _, s := range spans {
		bySystem[s.SystemID] = append(bySystem[s.SystemID], Span{s.StartedAt.Time, s.EndedAt.Time})
	}
	for sys, ss := range bySystem {
		cut, received, ok := cutoffOf(ss, window, now)
		q.received[sys] = received
		if ok {
			q.cutoff[sys] = cut
		}
	}
	for _, h := range heard {
		q.heard[TalkgroupKey{h.SystemID, h.TalkgroupID}] = h.HeardAt.Time
	}
	return q, nil
}

// TalkgroupsHeard is told when ingest hears talkgroups of a system on a
// call whose patch is wholly public. If the last read holds any of them
// quiet, the next asks again, so a quiet talkgroup comes back at its first
// call (R117).
func (g *Gate) TalkgroupsHeard(system int64, talkgroups []int32) {
	g.quietMu.Lock()
	defer g.quietMu.Unlock()
	q := g.quiet
	if q == nil || g.quietStale {
		return
	}
	cut, ok := q.cutoff[system]
	if !ok {
		return
	}
	for _, tg := range talkgroups {
		if h, heard := q.heard[TalkgroupKey{system, tg}]; !heard || h.Before(cut) {
			g.quietStale = true
			return
		}
	}
}

// QuietChanged makes the next read ask again: the window has changed.
func (g *Gate) QuietChanged() {
	g.quietMu.Lock()
	g.quietStale = true
	g.quietMu.Unlock()
}

// QuietTalkgroups is every public talkgroup that's quiet now, with how
// many days it hasn't been heard, for the admin table (R114).
func (g *Gate) QuietTalkgroups(ctx context.Context) (map[TalkgroupKey]int, *Quiet, error) {
	c, err := g.Catalogue(ctx)
	if err != nil {
		return nil, nil, err
	}
	q, err := g.Quiet(ctx)
	if err != nil {
		return nil, nil, err
	}
	out := map[TalkgroupKey]int{}
	for _, t := range c.Talkgroups {
		if quiet, days := q.Of(t); quiet {
			out[TalkgroupKey{t.System.ID, t.ID}] = days
		}
	}
	return out, q, nil
}
