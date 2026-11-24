// Package gate is the hidden-talkgroup gate (SPEC.md P1, R16, R30, R32; ADR
// 0004): the only way a public surface reads calls, talkgroups or
// recordings.
//
// Every read goes through the public_calls view, whose one condition is
// call_state's (ADR 0003), evaluated on the request: a call whose patch has
// a hidden or ignored talkgroup isn't there, and a call that isn't there
// can't be told apart from one that never existed. The gate returns no
// reason, no state and no error that says which.
//
// The public endpoints (internal/public) may import this package and the
// standard library's HTTP and encoding packages, but not the store, the
// database or the file system; a test there enforces it. So a surface can
// show only what the gate gives it.
package gate

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/RIKIKU/RadioArchive/internal/recordings"
	"github.com/RIKIKU/RadioArchive/internal/store"
	"github.com/RIKIKU/RadioArchive/internal/store/db"
)

// ErrUnavailable is the gate's only answer for a call the public may not
// have: deleted, never stored, hidden, ignored, patched with a hidden or
// ignored talkgroup, or (for its recording) not recorded yet.
var ErrUnavailable = errors.New("this call isn't available")

// CacheControl is how a public recording may be cached (R74). A recording
// never changes once stored.
const CacheControl = "private, max-age=31536000, immutable"

// queryTimeout bounds every public query (R73).
const queryTimeout = 5 * time.Second

// Gate reads what the public may see.
type Gate struct {
	q   *db.Queries
	dir string
	log *slog.Logger

	now func() time.Time

	catMu sync.Mutex
	cat   *Catalogue // the last built, at its generation

	live *Live
}

// Option configures the gate.
type Option func(*Gate)

// WithClock sets the gate's clock (default time.Now), which says when a
// delayed talkgroup's call becomes public (R23) and how old a live event
// is (R37). Tests control it.
func WithClock(now func() time.Time) Option {
	return func(g *Gate) { g.now = now }
}

// New returns the gate over st, with the recordings volume at
// recordingsDir.
func New(st *store.Store, recordingsDir string, log *slog.Logger, opts ...Option) *Gate {
	g := &Gate{q: st.Queries, dir: recordingsDir, log: log, now: time.Now}
	for _, o := range opts {
		o(g)
	}
	g.live = newLive(g)
	return g
}

// Live is the live feed (ADR 0007): the recorder endpoints hand it every
// live call's events and frames, and listeners subscribe to it.
func (g *Gate) Live() *Live { return g.live }

// delayHolds says whether a delayed talkgroup's call is still kept from the
// public (R23): until its delay has passed since it ended, and so for as
// long as it's in progress.
func (g *Gate) delayHolds(delayMinutes int32, ended *time.Time) bool {
	if delayMinutes <= 0 {
		return false
	}
	return ended == nil || g.now().Before(ended.Add(time.Duration(delayMinutes)*time.Minute))
}

// MaxCalls is the most call IDs one call-list request may name (R73).
const MaxCalls = 500

// System is a call's system as listeners see it.
type System struct {
	ID    int64
	Label string
	Name  string
}

// Talkgroup is a call's talkgroup as listeners see it.
type Talkgroup struct {
	ID    int32
	Label string
	// Name is what's shown (R24): the name, else the label, else the ID.
	Name string
	Tag  string
}

// Transmission is one unit's key-up in a call.
type Transmission struct {
	Unit       int64
	OffsetMS   int32
	DurationMS *int32
}

// Call is a public call.
type Call struct {
	ID        string
	System    System
	Talkgroup Talkgroup
	// Patched are the other talkgroups of its patch (R16). None is hidden
	// or ignored, or the call wouldn't be public.
	Patched       []int32
	StartedAt     time.Time
	EndedAt       *time.Time // nil while in progress
	RecordingSize *int64     // nil until the recording arrives
	Transmissions []Transmission
}

// ParseCallID reads a call ID as the API shows it (a UUID, R60), giving it
// in canonical form. Any other string names no call.
func ParseCallID(s string) (string, bool) {
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return "", false
	}
	s = strings.ToLower(s)
	if _, err := hex.DecodeString(strings.ReplaceAll(s, "-", "")); err != nil {
		return "", false
	}
	return s, true
}

func uuidOf(id string) pgtype.UUID {
	var u pgtype.UUID
	_ = u.Scan(id) // a canonical ID from ParseCallID always scans
	return u
}

func uuidString(u pgtype.UUID) string {
	b := u.Bytes
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Calls returns the public calls among ids, each once, in the order first
// named. The rest are left out, whatever the reason, as R66 drops them.
// Strings that aren't call IDs name no call. It's an error to name more
// than MaxCalls.
func (g *Gate) Calls(ctx context.Context, ids []string) ([]Call, error) {
	if len(ids) > MaxCalls {
		return nil, fmt.Errorf("at most %d call IDs", MaxCalls)
	}
	var order []string
	var us []pgtype.UUID
	for _, s := range ids {
		id, ok := ParseCallID(s)
		if !ok || slices.Contains(order, id) {
			continue
		}
		order = append(order, id)
		us = append(us, uuidOf(id))
	}
	if len(us) == 0 {
		return []Call{}, nil
	}
	// The catalogue is read first, so it's never newer than the calls: a
	// talkgroup it doesn't have yet keeps its calls out until it does.
	cat, err := g.Catalogue(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	rows, err := g.q.GatePublicCalls(ctx, us)
	if err != nil {
		return nil, fmt.Errorf("public calls: %s", store.SafeError(err))
	}
	byID := make(map[string]Call, len(rows))
	for _, r := range rows {
		c, err := callOf(r)
		if err != nil {
			return nil, err
		}
		if g.held(cat, r.SystemID, c) {
			continue
		}
		byID[c.ID] = c
	}
	out := make([]Call, 0, len(byID))
	for _, id := range order {
		if c, ok := byID[id]; ok {
			out = append(out, c)
		}
	}
	return out, nil
}

// held says whether a public call is kept from the public for now: its
// talkgroup isn't in the catalogue yet, or a delay in its patch hasn't
// passed (R23).
func (g *Gate) held(cat *Catalogue, system int64, c Call) bool {
	if _, ok := cat.talkgroup(system, c.Talkgroup.ID); !ok {
		return true
	}
	return g.delayHolds(cat.delayOf(system, c.Talkgroup.ID, c.Patched), c.EndedAt)
}

func callOf(r db.GatePublicCallsRow) (Call, error) {
	var txs []struct {
		Unit       int64  `json:"unit"`
		OffsetMS   int32  `json:"offset_ms"`
		DurationMS *int32 `json:"duration_ms"`
	}
	if err := json.Unmarshal(r.Transmissions, &txs); err != nil {
		return Call{}, fmt.Errorf("transmissions: %w", err)
	}
	c := Call{
		ID:     uuidString(r.ID),
		System: System{ID: r.SystemID, Label: r.SystemLabel, Name: r.SystemName},
		Talkgroup: Talkgroup{ID: r.Talkgroup, Label: r.TalkgroupLabel,
			Name: displayName(r.Talkgroup, r.TalkgroupName, r.TalkgroupLabel), Tag: deref(r.Tag)},
		Patched:       r.PatchedTalkgroups,
		StartedAt:     r.StartedAt.Time.UTC(),
		RecordingSize: r.RecordingSize,
		Transmissions: make([]Transmission, 0, len(txs)),
	}
	if c.Patched == nil {
		c.Patched = []int32{}
	}
	if r.EndedAt.Valid {
		t := r.EndedAt.Time.UTC()
		c.EndedAt = &t
	}
	for _, t := range txs {
		c.Transmissions = append(c.Transmissions, Transmission(t))
	}
	return c, nil
}

// displayName is R24's talkgroup name, with section 13's fallback to the ID.
func displayName(id int32, name, label string) string {
	switch {
	case name != "":
		return name
	case label != "":
		return label
	}
	return fmt.Sprint(id)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Recording is a public call's open recording.
type Recording struct {
	f *recordings.File
}

// Recording opens a public call's recording, or answers ErrUnavailable.
func (g *Gate) Recording(ctx context.Context, callID string) (*Recording, error) {
	id, ok := ParseCallID(callID)
	if !ok {
		return nil, ErrUnavailable
	}
	// A delayed talkgroup's recording waits for its delay (R23). Only then
	// is a second query needed.
	cat, err := g.Catalogue(ctx)
	if err != nil {
		return nil, err
	}
	if cat.delayed {
		cs, err := g.Calls(ctx, []string{id})
		if err != nil {
			return nil, err
		}
		if len(cs) == 0 {
			return nil, ErrUnavailable
		}
	}
	return g.open(ctx, id)
}

// Download opens a public call's recording to download (R62), with the
// call it's of, which names the file (R63), or answers ErrUnavailable
// exactly as Recording does.
func (g *Gate) Download(ctx context.Context, callID string) (*Recording, Call, error) {
	id, ok := ParseCallID(callID)
	if !ok {
		return nil, Call{}, ErrUnavailable
	}
	// Calls holds a delayed call back (R23) and gives what names the file.
	cs, err := g.Calls(ctx, []string{id})
	if err != nil {
		return nil, Call{}, err
	}
	if len(cs) == 0 || cs[0].RecordingSize == nil {
		return nil, Call{}, ErrUnavailable
	}
	// The recording is read through public_calls again, so a call hidden
	// in between is still kept back.
	rec, err := g.open(ctx, id)
	if err != nil {
		return nil, Call{}, err
	}
	return rec, cs[0], nil
}

// open opens the recording of call id, a canonical ID, if public_calls
// has it.
func (g *Gate) open(ctx context.Context, id string) (*Recording, error) {
	qctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	row, err := g.q.GatePublicRecording(qctx, uuidOf(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUnavailable
	}
	if err != nil {
		return nil, fmt.Errorf("public recording: %s", store.SafeError(err))
	}
	f, err := recordings.Open(g.dir, row.RecordingPath, row.RecordingAt.Time)
	if errors.Is(err, recordings.ErrMissing) {
		// Its row outlived its file, as while retention deletes both
		// (R88): it's as good as deleted.
		g.log.Warn("a public call's recording file is missing", "call", id)
		return nil, ErrUnavailable
	}
	if err != nil {
		return nil, err
	}
	return &Recording{f: f}, nil
}

// Serve answers r with the recording, with range requests and CacheControl.
func (r *Recording) Serve(w http.ResponseWriter, req *http.Request) {
	r.f.Serve(w, req, CacheControl)
}

// Close closes the recording.
func (r *Recording) Close() error { return r.f.Close() }
