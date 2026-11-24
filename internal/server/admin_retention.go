package server

import (
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/retention"
)

// Retention (R88), until the Settings page (R84) uses it.

// Retention is GET /api/v1/admin/retention: the period, the recordings
// volume's state, and the last runs.
type Retention struct {
	Days        int `json:"days"`
	DefaultDays int `json:"default_days"`
	MinDays     int `json:"min_days"`
	MaxDays     int `json:"max_days"`
	// NextRun is when the nightly job next deletes what's past the period.
	NextRun time.Time `json:"next_run"`
	// Disk is null when the volume can't be measured.
	Disk          *RetentionDisk `json:"disk"`
	LastNightly   *retention.Run `json:"last_nightly"`
	LastShortened *retention.Run `json:"last_shortened"`
}

// RetentionDisk is the recordings volume's free space, and the warning
// admins are shown below 15 % (R88).
type RetentionDisk struct {
	FreeBytes          uint64  `json:"free_bytes"`
	TotalBytes         uint64  `json:"total_bytes"`
	FreePercent        float64 `json:"free_percent"`
	MinFreePercent     float64 `json:"min_free_percent"`
	WarningFreePercent float64 `json:"warning_free_percent"`
	// State is ok, warning (below the warning level) or low (below
	// RECORDINGS_MIN_FREE: the oldest recordings are being deleted early).
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
}

func percent(f float64) float64 { return math.Round(f*1000) / 10 }

func (a *Admin) retentionState(r *http.Request) (Retention, error) {
	ctx := r.Context()
	days, err := a.ret.Days(ctx)
	if err != nil {
		return Retention{}, dbErr("retention", err)
	}
	out := Retention{Days: days, DefaultDays: retention.DefaultDays, MinDays: retention.MinDays,
		MaxDays: retention.MaxDays, NextRun: a.ret.NextRun(a.ret.Now()).UTC()}
	if d, err := a.ret.Disk(); err == nil {
		rd := &RetentionDisk{FreeBytes: d.Free, TotalBytes: d.Total, FreePercent: percent(d.FreeFraction()),
			MinFreePercent: percent(d.MinFree), WarningFreePercent: percent(d.WarnFree), State: string(d.State)}
		switch d.State {
		case retention.DiskWarning:
			rd.Message = fmt.Sprintf("The recordings volume has %g %% free. Below %g %%, the oldest recordings are deleted early.",
				rd.FreePercent, rd.MinFreePercent)
		case retention.DiskLow:
			rd.Message = fmt.Sprintf("The recordings volume has %g %% free, below %g %%: the oldest recordings are being deleted early (retention shortened).",
				rd.FreePercent, rd.MinFreePercent)
		}
		out.Disk = rd
	}
	if out.LastNightly, err = a.ret.LastRun(ctx, retention.NightlyJob); err != nil {
		return Retention{}, dbErr("retention", err)
	}
	if out.LastShortened, err = a.ret.LastRun(ctx, retention.LowDiskJob); err != nil {
		return Retention{}, dbErr("retention", err)
	}
	return out, nil
}

func (a *Admin) getRetention(r *http.Request, _ string) (int, any, error) {
	st, err := a.retentionState(r)
	return http.StatusOK, st, err
}

// RetentionChange is PUT /api/v1/admin/retention. A shorter period needs
// Confirm, once the admin has been told how many recordings will go.
type RetentionChange struct {
	Days    int  `json:"days"`
	Confirm bool `json:"confirm"`
}

// RetentionConfirm is the answer to a shorter period not yet confirmed
// (409): how many recordings, hidden ones included, the next nightly run
// will delete with it.
type RetentionConfirm struct {
	Code       string `json:"error"`
	Message    string `json:"message"`
	Days       int    `json:"days"`
	Recordings int64  `json:"recordings"`
}

// CodeConfirmRetention: shortening retention needs confirming.
const CodeConfirmRetention = "confirm_shorter_retention"

func (a *Admin) putRetention(r *http.Request, who string) (int, any, error) {
	var in RetentionChange
	if err := decode(r, &in); err != nil {
		return 0, nil, err
	}
	if in.Days < retention.MinDays || in.Days > retention.MaxDays {
		return 0, nil, refuseAdmin(http.StatusBadRequest, "invalid_days",
			"Retention must be %d to %d days.", retention.MinDays, retention.MaxDays)
	}
	ctx := r.Context()
	cur, err := a.ret.Days(ctx)
	if err != nil {
		return 0, nil, dbErr("retention", err)
	}
	if in.Days < cur && !in.Confirm {
		n, err := a.ret.RecordingsPast(ctx, in.Days)
		if err != nil {
			return 0, nil, dbErr("retention", err)
		}
		return http.StatusConflict, RetentionConfirm{Code: CodeConfirmRetention, Days: in.Days, Recordings: n,
			Message: fmt.Sprintf("Shortening retention from %d to %d days deletes %s at the next nightly run. Confirm?",
				cur, in.Days, recordingsCount(n))}, nil
	}
	if err := a.ret.SetDays(ctx, in.Days); err != nil {
		return 0, nil, dbErr("retention", err)
	}
	a.log.Info("admin changed retention", "admin", who, "from_days", cur, "days", in.Days)
	return a.getRetention(r, who)
}

func recordingsCount(n int64) string {
	if n == 1 {
		return "1 recording"
	}
	return fmt.Sprintf("%d recordings", n)
}
