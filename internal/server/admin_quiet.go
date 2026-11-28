package server

import (
	"context"
	"math"
	"net/http"
	"strconv"

	"github.com/RIKIKU/RadioArchive/internal/gate"
	"github.com/RIKIKU/RadioArchive/internal/store"
	"github.com/RIKIKU/RadioArchive/internal/store/db"
)

// Quiet talkgroups for admins (SPEC.md R114-R116; ADR 0054): the window,
// a setting on the Settings page, and which talkgroups are quiet, for the
// talkgroup table's badge. Admin pages always show every talkgroup.

// QuietSource says which talkgroups are quiet. The gate is the binary's.
type QuietSource interface {
	QuietTalkgroups(ctx context.Context) (map[gate.TalkgroupKey]int, *gate.Quiet, error)
	QuietChanged()
}

// WithQuiet serves the quiet window and marks quiet talkgroups in the
// talkgroup table.
func (a *Admin) WithQuiet(q QuietSource) *Admin {
	a.quiet = q
	return a
}

// QuietSettings is GET /api/v1/admin/quiet: the quiet window, and how long
// each system has been receiving towards it, since nothing of a system is
// quiet until it has received for one whole window (R116).
type QuietSettings struct {
	WindowDays  int           `json:"window_days"`
	DefaultDays int           `json:"default_days"`
	MinDays     int           `json:"min_days"`
	MaxDays     int           `json:"max_days"`
	Systems     []QuietSystem `json:"systems"`
	// Quiet is how many public talkgroups are quiet now.
	Quiet int `json:"quiet"`
}

// QuietSystem is a system and the receiving time counted towards its
// window: ReceivedHours up to the window's, Full once it's all of it.
type QuietSystem struct {
	ID            int64   `json:"id"`
	Label         string  `json:"label"`
	ReceivedHours float64 `json:"received_hours"`
	Full          bool    `json:"full"`
}

func (a *Admin) quietSettings(ctx context.Context) (QuietSettings, error) {
	quiet, q, err := a.quiet.QuietTalkgroups(ctx)
	if err != nil {
		return QuietSettings{}, dbErr("quiet", err)
	}
	systems, err := a.st.ListSystems(ctx)
	if err != nil {
		return QuietSettings{}, dbErr("systems", err)
	}
	out := QuietSettings{WindowDays: q.WindowDays, DefaultDays: gate.DefaultQuietDays, MinDays: gate.MinQuietDays,
		MaxDays: gate.MaxQuietDays, Systems: make([]QuietSystem, 0, len(systems)), Quiet: len(quiet)}
	for _, s := range systems {
		d, full := q.Received(s.ID)
		out.Systems = append(out.Systems, QuietSystem{ID: s.ID, Label: s.Label,
			ReceivedHours: math.Round(d.Hours()*10) / 10, Full: full})
	}
	return out, nil
}

func (a *Admin) getQuiet(r *http.Request, _ string) (int, any, error) {
	out, err := a.quietSettings(r.Context())
	return http.StatusOK, out, err
}

// QuietChange is PUT /api/v1/admin/quiet.
type QuietChange struct {
	WindowDays int `json:"window_days"`
}

// quietThing is the quiet window's thing in the audit log, under the
// settings' kind.
const quietThing = "quiet"

func (a *Admin) putQuiet(r *http.Request, who string) (int, any, error) {
	var in QuietChange
	if err := decode(r, &in); err != nil {
		return 0, nil, err
	}
	if in.WindowDays < gate.MinQuietDays || in.WindowDays > gate.MaxQuietDays {
		return 0, nil, refuseAdmin(http.StatusBadRequest, "invalid_window_days",
			"The quiet window must be %d to %d days.", gate.MinQuietDays, gate.MaxQuietDays)
	}
	ctx := r.Context()
	err := a.audited(r, func(q *db.Queries, au *store.Audit) error {
		if err := au.Lock(store.AuditRetention, quietThing); err != nil {
			return dbErr("quiet", err)
		}
		was, err := gate.QuietDays(ctx, q)
		if err != nil {
			return dbErr("quiet", err)
		}
		if was == in.WindowDays {
			return nil
		}
		if err := q.SetSetting(ctx, db.SetSettingParams{Key: gate.QuietDaysKey, Value: strconv.Itoa(in.WindowDays)}); err != nil {
			return dbErr("quiet", err)
		}
		return au.Record(store.Change{Kind: store.AuditRetention, Thing: quietThing, Label: "Quiet talkgroups",
			Before: map[string]int{"window_days": was}, After: map[string]int{"window_days": in.WindowDays}})
	})
	if err != nil {
		return 0, nil, err
	}
	a.quiet.QuietChanged()
	a.log.Info("admin changed the quiet window", "admin", who, "days", in.WindowDays)
	out, err := a.quietSettings(ctx)
	return http.StatusOK, out, err
}
