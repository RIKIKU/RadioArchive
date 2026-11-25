package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/telemetry"
)

// TestStatus (R89; ADR 0041): ok after a good night; failing after a
// failed run, saying why and when the last good one was, or once the last
// good one is more than 36 hours old; pending on a new install for 36
// hours; and off with no status file set.
func TestStatus(t *testing.T) {
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	good := now.Add(-6 * time.Hour).Unix()
	for _, c := range []struct {
		name, json, state, message string
		started                    time.Time
	}{
		{name: "good", state: StateOK,
			json: fmt.Sprintf(`{"since": %d, "last_run_at": %d, "last_run_ok": true, "last_good_at": %d, "last_good_night": "2026-11-26", "nights": 3, "error": ""}`, good-86400, good, good)},
		{name: "failed", state: StateFailing,
			message: "The last backup failed: the dump of radioarchive failed. The last good one was the night of 2026-11-25.",
			json:    fmt.Sprintf(`{"since": 1, "last_run_at": %d, "last_run_ok": false, "last_good_at": %d, "last_good_night": "2026-11-25", "error": "the dump of radioarchive failed"}`, good, good-86400)},
		{name: "failed, never good", state: StateFailing,
			message: "The last backup failed: the check couldn't start its throwaway database. There has been no good backup yet.",
			json:    fmt.Sprintf(`{"since": %d, "last_run_at": %d, "last_run_ok": false, "error": "the check couldn't start its throwaway database"}`, good, good)},
		{name: "overdue", state: StateFailing,
			message: "There has been no good backup for more than 36 hours. The last good one was the night of 2026-11-24.",
			json:    fmt.Sprintf(`{"since": 1, "last_run_at": %d, "last_run_ok": true, "last_good_at": %d, "last_good_night": "2026-11-24"}`, now.Add(-37*time.Hour).Unix(), now.Add(-37*time.Hour).Unix())},
		{name: "new install", state: StatePending,
			json: fmt.Sprintf(`{"since": %d, "last_run_at": 0, "last_run_ok": false}`, now.Add(-2*time.Hour).Unix())},
		{name: "new install, never run", state: StateFailing,
			message: "There has been no good backup for more than 36 hours. There has been no good backup yet.",
			json:    fmt.Sprintf(`{"since": %d, "last_run_at": 0, "last_run_ok": false}`, now.Add(-40*time.Hour).Unix())},
		{name: "no file yet", state: StatePending, started: now.Add(-time.Hour)},
		{name: "no file for 36 hours", state: StateFailing, started: now.Add(-37 * time.Hour),
			message: "There's no backup status: is the backup service running? docker compose ps backup"},
		{name: "unprintable error", state: StateFailing,
			message: "The last backup failed: broken. There has been no good backup yet.",
			json:    fmt.Sprintf(`{"since": %d, "last_run_at": %d, "last_run_ok": false, "error": "bro\u0007ken"}`, good, good)},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "status.json")
			if c.json != "" {
				if err := os.WriteFile(path, []byte(c.json), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			clock := now
			if !c.started.IsZero() {
				clock = c.started
			}
			r := New(path, func() time.Time { return clock })
			clock = now
			s := r.Status()
			if s.State != c.state || s.Message != c.message {
				t.Errorf("got %s %q, want %s %q", s.State, s.Message, c.state, c.message)
			}
		})
	}
	if s := New("", nil).Status(); s.State != StateOff {
		t.Errorf("no file set: %s", s.State)
	}
}

func TestCollect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status.json")
	at := time.Now().Add(-time.Hour).Unix()
	if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"since": 1, "last_run_at": %d, "last_run_ok": true, "last_good_at": %d, "last_good_bytes": 4096, "nights": 2}`, at, at)), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := telemetry.NewRegistry(telemetry.All)
	reg.Add(New(path, nil))
	out := string(reg.Write(context.Background()))
	for _, want := range []string{"radioarchive_backup_failing 0", fmt.Sprintf("radioarchive_backup_last_good_timestamp_seconds %d", at),
		"radioarchive_backup_last_good_bytes 4096", "radioarchive_backup_nights 2"} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
	reg = telemetry.NewRegistry(telemetry.All)
	reg.Add(New("", nil))
	if out := string(reg.Write(context.Background())); strings.Contains(out, "radioarchive_backup_") {
		t.Errorf("backups off, yet:\n%s", out)
	}
}
