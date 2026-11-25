// Package backup reads the nightly backup's state for admins and the
// metrics (SPEC.md R89, ADR 0041). The backup service
// (deploy/backup/backup.sh) writes it to a status file; the API only reads
// it, from a volume mounted read-only. In the combined deployment it's
// PagerArchive's file for this database (databases/radioarchive.json,
// #128), which has the same fields.
package backup

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

// Overdue is how old the last good backup may be before backups count as
// failing (R89, as PagerArchive's R86a).
const Overdue = 36 * time.Hour

// file is the status file's fields that are read. Times are Unix seconds.
type file struct {
	Since         int64  `json:"since"`
	LastRunAt     int64  `json:"last_run_at"`
	LastRunOK     bool   `json:"last_run_ok"`
	RunningSince  int64  `json:"running_since"`
	LastGoodAt    int64  `json:"last_good_at"`
	LastGoodNight string `json:"last_good_night"`
	LastGoodBytes int64  `json:"last_good_bytes"`
	Nights        int    `json:"nights"`
	Error         string `json:"error"`
}

// States.
const (
	StateOK      = "ok"
	StateFailing = "failing"
	StatePending = "pending" // set up, with no good backup yet, and not yet overdue
	StateOff     = "off"     // BACKUP_STATUS_FILE isn't set: this API has no backups to show
)

// Status is the backup's state as admins see it.
type Status struct {
	State         string     `json:"state"`
	LastRunAt     *time.Time `json:"last_run_at"`
	LastRunOK     bool       `json:"last_run_ok"`
	Running       bool       `json:"running"`
	LastGoodAt    *time.Time `json:"last_good_at"`
	LastGoodNight string     `json:"last_good_night,omitempty"`
	LastGoodBytes int64      `json:"last_good_bytes"`
	Nights        int        `json:"nights"`
	// Message says, in words, why backups are failing; empty otherwise.
	Message string `json:"message,omitempty"`
}

// Reader reads the status file.
type Reader struct {
	path    string
	started time.Time
	now     func() time.Time
}

// New reads path, the status file; "" is backups not set up. now is the
// clock (nil: time.Now).
func New(path string, now func() time.Time) *Reader {
	if now == nil {
		now = time.Now
	}
	return &Reader{path: path, started: now(), now: now}
}

// Status is the backup's state now.
func (r *Reader) Status() Status {
	if r.path == "" {
		return Status{State: StateOff}
	}
	now := r.now()
	b, err := os.ReadFile(r.path)
	var f file
	if err == nil {
		err = json.Unmarshal(b, &f)
	}
	if err != nil {
		// No state yet: a new install, until the backup service has started.
		// It's failing once it's been 36 hours with nothing.
		if now.Sub(r.started) < Overdue {
			return Status{State: StatePending}
		}
		return Status{State: StateFailing, Message: "There's no backup status: is the backup service running? docker compose ps backup"}
	}
	s := Status{LastRunOK: f.LastRunOK, Running: f.RunningSince > 0, LastGoodBytes: f.LastGoodBytes,
		Nights: f.Nights, LastGoodNight: plain(f.LastGoodNight), LastRunAt: unix(f.LastRunAt), LastGoodAt: unix(f.LastGoodAt)}
	since := time.Unix(f.Since, 0)
	if f.Since == 0 {
		since = r.started
	}
	last := "There has been no good backup yet."
	if s.LastGoodAt != nil {
		last = fmt.Sprintf("The last good one was the night of %s.", s.LastGoodNight)
	}
	switch {
	case s.LastRunAt != nil && !f.LastRunOK:
		s.State = StateFailing
		why := plain(f.Error)
		if why == "" {
			why = "no reason was recorded"
		}
		s.Message = fmt.Sprintf("The last backup failed: %s. %s", why, last)
	case s.LastGoodAt != nil && now.Sub(*s.LastGoodAt) > Overdue:
		s.State = StateFailing
		s.Message = "There has been no good backup for more than 36 hours. " + last
	case s.LastGoodAt == nil && now.Sub(since) > Overdue:
		s.State = StateFailing
		s.Message = "There has been no good backup for more than 36 hours. " + last
	case s.LastGoodAt == nil:
		s.State = StatePending
	default:
		s.State = StateOK
	}
	return s
}

// Collect writes the backup's metrics: failing (for the "Backup failing"
// alert, R91), and the last good backup's time and size and the nights
// kept, for the dashboard.
func (r *Reader) Collect(_ context.Context, w *telemetry.Writer) {
	s := r.Status()
	if s.State == StateOff {
		return
	}
	w.Bool(telemetry.BackupFailing, s.State == StateFailing)
	if s.LastGoodAt != nil {
		w.Time(telemetry.BackupLastGood, *s.LastGoodAt)
	}
	w.Set(telemetry.BackupBytes, float64(s.LastGoodBytes))
	w.Set(telemetry.BackupNights, float64(s.Nights))
}

func unix(s int64) *time.Time {
	if s <= 0 {
		return nil
	}
	t := time.Unix(s, 0).UTC()
	return &t
}

// plain is text from the status file as it may be shown: the script writes
// fixed text only, but nothing unprintable or overlong is passed on.
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
