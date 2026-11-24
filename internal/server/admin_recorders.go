package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/RIKIKU/RadioArchive/internal/store/db"
)

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
	_, err := a.st.SetRecorderQuiet(r.Context(), db.SetRecorderQuietParams{Name: name, QuietAfterMinutes: quiet})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil, refuseAdmin(http.StatusNotFound, "recorder_not_found", "No active recorder is named %q.", name)
	}
	if err != nil {
		return 0, nil, dbErr("recorder", err)
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
