// Package upgrade reads the automatic upgrade's state for admins and the
// metrics (SPEC.md R87, R89; ADR 0044). The upgrade script
// (deploy/auto-upgrade/auto-upgrade.sh) writes it to a status file on the
// host; the API only reads it, from a folder mounted read-only.
package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/RIKIKU/RadioArchive/internal/telemetry"
)

// Stuck is how long a run may stay in one of its upgrading steps before it
// counts as stopped part way.
const Stuck = 2 * time.Hour

type failure struct {
	At         int64  `json:"at"`
	From       string `json:"from"`
	To         string `json:"to"`
	Step       string `json:"step"`
	Reason     string `json:"reason"`
	RolledBack bool   `json:"rolled_back"`
	Restored   bool   `json:"restored"`
	ClearedAt   int64  `json:"cleared_at"`
}

type upgraded struct {
	At   int64  `json:"at"`
	From string `json:"from"`
	To   string `json:"to"`
}

type file struct {
	Enabled         bool      `json:"enabled"`
	State           string    `json:"state"`
	RunningSince    int64     `json:"running_since"`
	TargetVersion   string    `json:"target_version"`
	LastCheckAt     int64     `json:"last_check_at"`
	LastCheckResult string    `json:"last_check_result"`
	LastCheckNote   string    `json:"last_check_note"`
	RunningVersion  string    `json:"running_version"`
	LatestVersion   string    `json:"latest_version"`
	LastFailure     *failure  `json:"last_failure"`
	LastUpgrade     *upgraded `json:"last_upgrade"`
}

// States.
const (
	StateOff     = "off"     // not switched on, or no status file
	StateOK      = "ok"      // on, and the last upgrade (if any) worked
	StateRunning = "running" // a run is upgrading now
	StateFailing = "failing" // an upgrade failed, or a run stopped part way
)

// Status is the automatic upgrade's state as admins see it.
type Status struct {
	State          string     `json:"state"`
	LastCheckAt    *time.Time `json:"last_check_at"`
	LastResult     string     `json:"last_result,omitempty"`
	RunningVersion string     `json:"running_version,omitempty"`
	LatestVersion  string     `json:"latest_version,omitempty"`
	LastUpgradeAt  *time.Time `json:"last_upgrade_at"`
	// Message says, in words, what failed; empty otherwise.
	Message string `json:"message,omitempty"`
}

// Reader reads the status file.
type Reader struct {
	path string
	now  func() time.Time
}

// New reads path; "" is automatic upgrades not set up.
func New(path string, now func() time.Time) *Reader {
	if now == nil {
		now = time.Now
	}
	return &Reader{path: path, now: now}
}

// Status is the automatic upgrade's state now.
func (r *Reader) Status() Status {
	if r.path == "" {
		return Status{State: StateOff}
	}
	b, err := os.ReadFile(r.path)
	var f file
	if err == nil {
		err = json.Unmarshal(b, &f)
	}
	if err != nil || !f.Enabled {
		return Status{State: StateOff}
	}
	now := r.now()
	s := Status{State: StateOK, LastCheckAt: unix(f.LastCheckAt), LastResult: word(f.LastCheckResult),
		RunningVersion: version(f.RunningVersion), LatestVersion: version(f.LatestVersion)}
	if f.LastUpgrade != nil {
		s.LastUpgradeAt = unix(f.LastUpgrade.At)
	}
	busy := f.State == "upgrading" || f.State == "health-check" || f.State == "rolling-back"
	fl := f.LastFailure
	uncleared := fl != nil && fl.ClearedAt == 0 && (f.LastUpgrade == nil || f.LastUpgrade.At < fl.At)
	switch {
	case f.LastCheckResult == "interrupted" || (busy && f.RunningSince > 0 && now.Sub(time.Unix(f.RunningSince, 0)) > Stuck):
		s.State = StateFailing
		s.Message = "An automatic upgrade stopped part way. Check the install, then run deploy/auto-upgrade/auto-upgrade.sh clear."
	case busy:
		s.State = StateRunning
	case uncleared && !fl.RolledBack:
		s.State = StateFailing
		s.Message = fmt.Sprintf("The automatic upgrade from %s to %s failed (%s), and so did its rollback. Check the install now, then run deploy/auto-upgrade/auto-upgrade.sh clear.",
			version(fl.From), version(fl.To), plain(fl.Reason))
	case uncleared:
		s.State = StateFailing
		s.Message = fmt.Sprintf("The automatic upgrade from %s to %s failed (%s) and was rolled back. %s won't be tried again until you run deploy/auto-upgrade/auto-upgrade.sh retry %s.",
			version(fl.From), version(fl.To), plain(fl.Reason), version(fl.To), version(fl.To))
	}
	return s
}

// Collect writes radioarchive_auto_upgrade_failing, for the "Upgrade
// failed" alert, while automatic upgrades are on.
func (r *Reader) Collect(_ context.Context, w *telemetry.Writer) {
	s := r.Status()
	if s.State == StateOff {
		return
	}
	w.Bool(telemetry.UpgradeFailing, s.State == StateFailing)
	if s.LastCheckAt != nil {
		w.Time(telemetry.UpgradeLastCheck, *s.LastCheckAt)
	}
}

func unix(s int64) *time.Time {
	if s <= 0 {
		return nil
	}
	t := time.Unix(s, 0).UTC()
	return &t
}

// version is a version from the file, or "" if it isn't one.
func version(v string) string {
	if v == "" || len(v) > 32 || strings.Trim(v, "0123456789.") != "" {
		return ""
	}
	return v
}

// word is a fixed word from the file, or "".
func word(v string) string {
	if len(v) > 32 || strings.Trim(v, "abcdefghijklmnopqrstuvwxyz-") != "" {
		return ""
	}
	return v
}

// plain is the script's fixed text, with nothing unprintable or overlong.
func plain(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return -1
	}, s)
	if len(s) > 200 {
		s = s[:200]
	}
	return strings.TrimSpace(s)
}
