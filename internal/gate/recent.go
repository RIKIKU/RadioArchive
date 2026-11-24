package gate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/RIKIKU/RadioArchive/internal/store"
	"github.com/RIKIKU/RadioArchive/internal/store/db"
)

// Recent calls and search (SPEC.md R16, R23, R30, R32, R46, R73; ADR
// 0013): the public calls in a time range, by start time, filtered by
// talkgroup and unit, a page at a time. Every row comes from public_calls,
// and every row the call details (Calls) would hold back is held back here
// too: a talkgroup the catalogue doesn't have yet, and a delay that hasn't
// passed (R23). Nothing is cached, so hiding a talkgroup takes effect at
// the next request (R74).

// Bounds of a search (R73).
const (
	// MaxRange is the longest time range one search may cover: 31 days.
	MaxRange = 31 * 24 * time.Hour
	// MaxPage is the most calls one page holds.
	MaxPage = 100

	// maxUnitCalls is the most calls a unit filter reads as a list of
	// call IDs; a unit heard on more is filtered call by call.
	maxUnitCalls = 2000
)

// ErrTooSlow is a search that ran out of its time (R73's 5 s).
var ErrTooSlow = errors.New("that search took too long")

// Position is where a page ends: the last call given, by start time and
// ID, which is the order calls are paged in.
type Position struct {
	StartedAt time.Time
	ID        [16]byte
}

// Unit names a radio ID, in any system or in one.
type Unit struct {
	System *int64
	ID     int64
}

// Search is what a page of recent calls asks for.
type Search struct {
	// From and To are the range calls started in, From included and To
	// not. A zero To is now, or From + MaxRange when From is given; a zero
	// From is To - MaxRange. A range over MaxRange is the caller's to
	// refuse; the gate refuses it too.
	From, To time.Time
	// Talkgroups, when not nil, are the talkgroups a call must be on or
	// patched with (R16). Only public talkgroups match anything, so naming
	// a hidden one is naming one that doesn't exist. Nil is every public
	// talkgroup.
	Talkgroups *ListeningSet
	// Unit, when not nil, is a radio ID that must be heard on the call.
	Unit *Unit
	// Oldest pages oldest first; the default is newest first.
	Oldest bool
	// After continues from the end of an earlier page.
	After *Position
	// Limit is the most calls a page holds: MaxPage when zero.
	Limit int
}

// Page is a page of recent calls.
type Page struct {
	Calls []Call
	// Next is where the next page starts, or nil when this is the last.
	Next *Position
}

// Range fills in a search's default range, as RecentCalls reads it.
func (g *Gate) Range(s Search) (from, to time.Time) {
	from, to = s.From, s.To
	switch {
	case to.IsZero() && !from.IsZero():
		to = from.Add(MaxRange)
	case to.IsZero():
		to = g.now()
	}
	if from.IsZero() {
		from = to.Add(-MaxRange)
	}
	return from, to
}

// RecentCalls returns a page of the public calls a search finds. A search
// that runs out of its time answers ErrTooSlow.
func (g *Gate) RecentCalls(ctx context.Context, s Search) (Page, error) {
	from, to := g.Range(s)
	if !to.After(from) || to.Sub(from) > MaxRange {
		return Page{}, fmt.Errorf("a search covers more than nothing and at most %s", MaxRange)
	}
	limit := s.Limit
	if limit <= 0 || limit > MaxPage {
		limit = MaxPage
	}
	ctx, cancel := context.WithTimeout(ctx, g.searchTimeout)
	defer cancel()
	page, err := g.recentCalls(ctx, s, from, to, limit)
	if err != nil && tooSlow(ctx, err) {
		return Page{}, ErrTooSlow
	}
	return page, err
}

func tooSlow(ctx context.Context, err error) bool {
	var pgErr *pgconn.PgError
	return errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) ||
		(errors.As(err, &pgErr) && pgErr.Code == "57014") // query_canceled: statement_timeout
}

func (g *Gate) recentCalls(ctx context.Context, s Search, from, to time.Time, limit int) (Page, error) {
	// The catalogue is read first, as Calls reads it: a talkgroup it
	// doesn't have yet keeps its calls out until it does.
	cat, err := g.Catalogue(ctx)
	if err != nil {
		return Page{}, err
	}
	var named map[talkgroupKey]bool
	var ids []int32
	if s.Talkgroups != nil {
		named, ids = cat.resolve(*s.Talkgroups)
		if len(named) == 0 {
			// No public talkgroup is named: nothing can match. A hidden
			// talkgroup's ID answers here, as one that doesn't exist does.
			return Page{Calls: []Call{}}, nil
		}
	}

	// One read-only transaction, with the search's time as its statement
	// timeout too, and custom plans: the filters left out are NULLs the
	// planner folds away, rather than a generic plan that must allow for
	// every filter (ADR 0013).
	tx, err := g.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return Page{}, fmt.Errorf("recent calls: %s", store.SafeError(err))
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	timeout := max(time.Until(deadline(ctx)).Milliseconds(), 1)
	for _, set := range []string{fmt.Sprintf("SET LOCAL statement_timeout = %d", timeout),
		"SET LOCAL plan_cache_mode = force_custom_plan"} {
		if _, err := tx.Exec(ctx, set); err != nil {
			return Page{}, fmt.Errorf("recent calls: %s", store.SafeError(err))
		}
	}
	q := g.q.WithTx(tx)

	var cursorAt time.Time
	var cursorID [16]byte
	switch {
	case s.After != nil:
		cursorAt, cursorID = s.After.StartedAt, s.After.ID
	case s.Oldest:
		// Just before from, at the largest ID: (started_at, id) > it is
		// started_at >= from, as timestamps are in microseconds.
		cursorAt, cursorID = from.Add(-time.Microsecond), [16]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
			0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	default:
		// At to, with the smallest ID: (started_at, id) < it is
		// started_at < to.
		cursorAt = to
	}
	// A unit filter is the unit's calls, read first from the index, when
	// they're few: the search then costs what the unit has, cold or warm.
	// A unit heard on more than maxUnitCalls is common enough that reading
	// the range newest (or oldest) first finds a page soon, so it's asked
	// call by call instead (ADR 0013).
	var unit, unitSystem *int64
	var callIDs []pgtype.UUID
	if s.Unit != nil {
		unitSystem = s.Unit.System
		ids, err := q.GateUnitCalls(ctx, db.GateUnitCallsParams{Unit: s.Unit.ID, RowLimit: maxUnitCalls + 1})
		if err != nil {
			return Page{}, fmt.Errorf("recent calls: %s", store.SafeError(err))
		}
		// A unit never heard still runs the search, with no IDs, so it
		// costs what a unit heard only on hidden calls costs.
		if len(ids) <= maxUnitCalls {
			callIDs = append([]pgtype.UUID{}, ids...)
		} else {
			unit = &s.Unit.ID
		}
	}
	var tgs []int32
	if s.Talkgroups != nil {
		tgs = ids
	}

	out := Page{Calls: make([]Call, 0, limit)}
	batch := int32(limit + 1)
	for {
		rows, err := g.searchBatch(ctx, q, s.Oldest, from, to, cursorAt, cursorID, tgs, callIDs, unit, unitSystem, batch)
		if err != nil {
			return Page{}, err
		}
		for _, r := range rows {
			cursorAt, cursorID = r.StartedAt.Time, r.ID.Bytes
			// The call's talkgroup and system as the catalogue has them.
			// One it doesn't have yet is held back, as Calls holds it.
			t, ok := cat.talkgroup(r.SystemID, r.Talkgroup)
			if !ok {
				continue
			}
			r.SystemLabel, r.SystemName = t.System.Label, t.System.Name
			r.TalkgroupLabel, r.TalkgroupName, r.Tag = t.Label, t.Name, &t.Tag
			c, err := callOf(r)
			if err != nil {
				return Page{}, err
			}
			if g.held(cat, r.SystemID, c) || (named != nil && !matches(named, r.SystemID, c)) {
				continue
			}
			cat.nameUnits(r.SystemID, &c)
			if len(out.Calls) == limit {
				// There's at least one more: the next page starts after
				// the last one given.
				last := out.Calls[limit-1]
				out.Next = &Position{StartedAt: last.StartedAt, ID: uuidOf(last.ID).Bytes}
				return out, tx.Commit(ctx)
			}
			out.Calls = append(out.Calls, c)
		}
		if len(rows) < int(batch) {
			return out, tx.Commit(ctx)
		}
	}
}

func deadline(ctx context.Context) time.Time {
	d, ok := ctx.Deadline()
	if !ok {
		return time.Now().Add(queryTimeout)
	}
	return d
}

func (g *Gate) searchBatch(ctx context.Context, q *db.Queries, oldest bool, from, to, cursorAt time.Time,
	cursorID [16]byte, tgs []int32, callIDs []pgtype.UUID, unit, unitSystem *int64, limit int32) ([]db.GatePublicCallsRow, error) {
	ts := func(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
	id := pgtype.UUID{Bytes: cursorID, Valid: true}
	var out []db.GatePublicCallsRow
	if oldest {
		rows, err := q.GateRecentCallsOldest(ctx, db.GateRecentCallsOldestParams{StartedFrom: ts(from), StartedTo: ts(to),
			CursorAt: ts(cursorAt), CursorID: id, Talkgroups: tgs, Unit: unit, CallIds: callIDs, UnitSystem: unitSystem, RowLimit: limit})
		if err != nil {
			return nil, fmt.Errorf("recent calls: %s", store.SafeError(err))
		}
		for _, r := range rows {
			out = append(out, rowOf(db.GateRecentCallsNewestRow(r)))
		}
		return out, nil
	}
	rows, err := q.GateRecentCallsNewest(ctx, db.GateRecentCallsNewestParams{StartedFrom: ts(from), StartedTo: ts(to),
		CursorAt: ts(cursorAt), CursorID: id, Talkgroups: tgs, Unit: unit, CallIds: callIDs, UnitSystem: unitSystem, RowLimit: limit})
	if err != nil {
		return nil, fmt.Errorf("recent calls: %s", store.SafeError(err))
	}
	for _, r := range rows {
		out = append(out, rowOf(r))
	}
	return out, nil
}

// rowOf is a search's row as the call details' row, its names still to be
// filled from the catalogue.
func rowOf(r db.GateRecentCallsNewestRow) db.GatePublicCallsRow {
	return db.GatePublicCallsRow{ID: r.ID, SystemID: r.SystemID, Talkgroup: r.Talkgroup,
		PatchedTalkgroups: r.PatchedTalkgroups, StartedAt: r.StartedAt, EndedAt: r.EndedAt,
		RecordingSize: r.RecordingSize, Transmissions: r.Transmissions}
}

// resolve finds the public talkgroups a set names: by system and ID, and
// the IDs alone, which the query matches (a superset when an ID is in
// several systems). A talkgroup the catalogue doesn't hold names nothing.
func (c *Catalogue) resolve(set ListeningSet) (map[talkgroupKey]bool, []int32) {
	inAny := make(map[int32]bool, len(set.Any))
	for _, tg := range set.Any {
		inAny[tg] = true
	}
	named := map[talkgroupKey]bool{}
	seen := map[int32]bool{}
	var ids []int32
	add := func(k talkgroupKey) {
		named[k] = true
		if !seen[k.talkgroup] {
			seen[k.talkgroup] = true
			ids = append(ids, k.talkgroup)
		}
	}
	for _, t := range c.Talkgroups {
		if inAny[t.ID] {
			add(talkgroupKey{t.System.ID, t.ID})
		}
	}
	for sys, tgs := range set.InSystem {
		for _, tg := range tgs {
			if _, ok := c.talkgroup(sys, tg); ok {
				add(talkgroupKey{sys, tg})
			}
		}
	}
	return named, ids
}

// matches says whether a call is on a named talkgroup or patched with one
// (R16), in its own system.
func matches(named map[talkgroupKey]bool, system int64, c Call) bool {
	if named[talkgroupKey{system, c.Talkgroup.ID}] {
		return true
	}
	for _, tg := range c.Patched {
		if named[talkgroupKey{system, tg}] {
			return true
		}
	}
	return false
}
