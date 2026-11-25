package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/RIKIKU/RadioArchive/internal/store"
	"github.com/RIKIKU/RadioArchive/internal/store/db"
)

// OverlapWindow is how far back the Recorders page looks for overlapping
// calls (R13: "in the last 24 h").
const OverlapWindow = 24 * time.Hour

// Overlap is two recorders' overlapping calls on one system's talkgroup
// (R13; ADR 0027): Recorders are the two by name, in name order, and
// Message is R13's words for it.
type Overlap struct {
	SystemID    int64     `json:"system_id"`
	SystemLabel string    `json:"system_label"`
	Talkgroup   int32     `json:"talkgroup"`
	Recorders   [2]string `json:"recorders"`
	Calls       int64     `json:"calls"`
	Message     string    `json:"message"`
}

// Overlaps is GET /api/v1/admin/overlaps: the last 24 hours' overlapping
// calls, the most first. Since is where the 24 hours begin.
type Overlaps struct {
	Since    time.Time `json:"since"`
	Overlaps []Overlap `json:"overlaps"`
}

// overlaps counts, over the 24 hours up to the admin API's clock, the
// calls two recorders recorded on the same system and talkgroup at
// overlapping times. Reading changes nothing, so it's neither audited nor
// logged per request.
func (a *Admin) overlaps(r *http.Request, _ string) (int, any, error) {
	until := a.now().UTC()
	since := until.Add(-OverlapWindow)
	rows, err := a.st.RecorderOverlaps(r.Context(), db.RecorderOverlapsParams{
		Since: pgtype.Timestamptz{Time: since, Valid: true}, Until: pgtype.Timestamptz{Time: until, Valid: true}})
	if err != nil {
		return 0, nil, dbErr("overlaps", err)
	}
	out := Overlaps{Since: since, Overlaps: make([]Overlap, 0, len(rows))}
	for _, o := range rows {
		names := [2]string{o.FirstRecorder, o.SecondRecorder}
		if names[1] < names[0] {
			names[0], names[1] = names[1], names[0]
		}
		calls := "1 overlapping call"
		if o.Calls != 1 {
			calls = fmt.Sprintf("%d overlapping calls", o.Calls)
		}
		out.Overlaps = append(out.Overlaps, Overlap{SystemID: o.SystemID, SystemLabel: o.SystemLabel, Talkgroup: o.Talkgroup,
			Recorders: names, Calls: o.Calls,
			Message: fmt.Sprintf("%s and %s both recorded TG %d: %s in the last 24 h", names[0], names[1], o.Talkgroup, calls)})
	}
	return http.StatusOK, out, nil
}

// The Recorders page's data (SPEC.md R14; ADR 0010): each active
// recorder's state and signals, and its quiet threshold, the one thing an
// admin sets here. Keys stay the shell's (R10). The page itself is M4's.

// MaxQuietMinutes is the longest quiet threshold, a week.
const MaxQuietMinutes = 7 * 24 * 60

// WithRecorders serves the recorders' state and signals from rs.
func (a *Admin) WithRecorders(rs *Recorders) *Admin {
	a.rs = rs
	return a
}

func (a *Admin) listRecorders(r *http.Request, _ string) (int, any, error) {
	all, err := a.rs.Status(r.Context())
	if err != nil {
		return 0, nil, dbErr("recorders", err)
	}
	return http.StatusOK, all, nil
}

func (a *Admin) recorderStatus(r *http.Request, name string) (RecorderStatus, error) {
	all, err := a.rs.Status(r.Context())
	if err != nil {
		return RecorderStatus{}, dbErr("recorders", err)
	}
	for _, s := range all {
		if s.Name == name {
			return s, nil
		}
	}
	return RecorderStatus{}, refuseAdmin(http.StatusNotFound, "recorder_not_found", "No active recorder is named %q.", name)
}

func (a *Admin) getRecorder(r *http.Request, _ string) (int, any, error) {
	s, err := a.recorderStatus(r, r.PathValue("recorder"))
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, s, nil
}

// patchRecorder sets the quiet threshold: {"quiet_after_minutes": N} with
// N from 1 to MaxQuietMinutes, or null for off (the default).
func (a *Admin) patchRecorder(r *http.Request, who string) (int, any, error) {
	name := r.PathValue("recorder")
	var in struct {
		QuietAfterMinutes json.RawMessage `json:"quiet_after_minutes"`
	}
	if err := decode(r, &in); err != nil {
		return 0, nil, err
	}
	if in.QuietAfterMinutes == nil {
		return 0, nil, refuseAdmin(http.StatusBadRequest, "invalid_body", "Nothing to change: send quiet_after_minutes.")
	}
	var quiet *int32
	if !bytes.Equal(bytes.TrimSpace(in.QuietAfterMinutes), []byte("null")) {
		var n int32
		if err := json.Unmarshal(in.QuietAfterMinutes, &n); err != nil || n < 1 || n > MaxQuietMinutes {
			return 0, nil, refuseAdmin(http.StatusBadRequest, "invalid_quiet_after_minutes",
				"quiet_after_minutes is a whole number of minutes from 1 to %d, or null for off.", MaxQuietMinutes)
		}
		quiet = &n
	}
	ctx := r.Context()
	notFound := refuseAdmin(http.StatusNotFound, "recorder_not_found", "No active recorder is named %q.", name)
	err := a.audited(r, func(q *db.Queries, au *store.Audit) error {
		if err := au.Lock(store.AuditRecorder, name); err != nil {
			return dbErr("recorder", err)
		}
		was, err := q.RecorderQuiet(ctx, name)
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound
		}
		if err != nil {
			return dbErr("recorder", err)
		}
		if _, err := q.SetRecorderQuiet(ctx, db.SetRecorderQuietParams{Name: name, QuietAfterMinutes: quiet}); errors.Is(err, pgx.ErrNoRows) {
			return notFound
		} else if err != nil {
			return dbErr("recorder", err)
		}
		return au.Record(store.Change{Kind: store.AuditRecorder, Thing: name, Label: name,
			Before: map[string]*int32{"quiet_after_minutes": was}, After: map[string]*int32{"quiet_after_minutes": quiet}})
	})
	if err != nil {
		return 0, nil, err
	}
	if quiet == nil {
		a.log.Info("admin turned a recorder's quiet signal off", "admin", who, "recorder", name)
	} else {
		a.log.Info("admin set a recorder's quiet threshold", "admin", who, "recorder", name, "minutes", *quiet)
	}
	a.rs.sig.poke()
	s, err := a.recorderStatus(r, name)
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, s, nil
}
