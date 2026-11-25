package upgrade

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

// TestStatus (R87, R89; ADR 0044): off until switched on; ok after an
// upgrade or none; running while upgrading; failing after a rolled-back
// failure (and after a failed rollback, or a run stopped part way) until a
// later upgrade or clear.
func TestStatus(t *testing.T) {
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	at := now.Add(-time.Hour).Unix()
	fail := func(rolledBack bool, cleared int64) string {
		return fmt.Sprintf(`"last_failure": {"at": %d, "from": "0.1.1", "to": "0.1.2", "step": "health", "reason": "the API didn't answer as 0.1.2 within 600 s (it runs 0.1.1)", "rolled_back": %v, "cleared_at": %d}`,
			at, rolledBack, cleared)
	}
	for _, c := range []struct {
		name, json, state, message string
	}{
		{"off", `{"enabled": false}`, StateOff, ""},
		{"on, never upgraded", `{"enabled": true, "state": "idle", "last_check_result": "up-to-date", "last_check_at": 1}`, StateOK, ""},
		{"upgraded", fmt.Sprintf(`{"enabled": true, "state": "idle", "last_upgrade": {"at": %d, "from": "0.1.0", "to": "0.1.1"}}`, at), StateOK, ""},
		{"running", fmt.Sprintf(`{"enabled": true, "state": "health-check", "running_since": %d}`, now.Add(-10*time.Minute).Unix()), StateRunning, ""},
		{"stuck", fmt.Sprintf(`{"enabled": true, "state": "upgrading", "running_since": %d}`, now.Add(-3*time.Hour).Unix()), StateFailing,
			"An automatic upgrade stopped part way. Check the install, then run deploy/auto-upgrade/auto-upgrade.sh clear."},
		{"interrupted", `{"enabled": true, "state": "upgrading", "last_check_result": "interrupted"}`, StateFailing,
			"An automatic upgrade stopped part way. Check the install, then run deploy/auto-upgrade/auto-upgrade.sh clear."},
		{"rolled back", `{"enabled": true, "state": "idle", "last_check_result": "rolled-back", ` + fail(true, 0) + `}`, StateFailing,
			"The automatic upgrade from 0.1.1 to 0.1.2 failed (the API didn't answer as 0.1.2 within 600 s (it runs 0.1.1)) and was rolled back. 0.1.2 won't be tried again until you run deploy/auto-upgrade/auto-upgrade.sh retry 0.1.2."},
		{"rollback failed", `{"enabled": true, "state": "idle", ` + fail(false, 0) + `}`, StateFailing,
			"The automatic upgrade from 0.1.1 to 0.1.2 failed (the API didn't answer as 0.1.2 within 600 s (it runs 0.1.1)), and so did its rollback. Check the install now, then run deploy/auto-upgrade/auto-upgrade.sh clear."},
		{"cleared", `{"enabled": true, "state": "idle", ` + fail(true, at+60) + `}`, StateOK, ""},
		{"upgraded since", fmt.Sprintf(`{"enabled": true, "state": "idle", "last_upgrade": {"at": %d, "from": "0.1.1", "to": "0.1.3"}, `, at+600) + fail(true, 0) + `}`, StateOK, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "status.json")
			if err := os.WriteFile(path, []byte(c.json), 0o644); err != nil {
				t.Fatal(err)
			}
			s := New(path, func() time.Time { return now }).Status()
			if s.State != c.state || s.Message != c.message {
				t.Errorf("got %s %q, want %s %q", s.State, s.Message, c.state, c.message)
			}
		})
	}
	if s := New(filepath.Join(t.TempDir(), "none.json"), nil).Status(); s.State != StateOff {
		t.Errorf("no file: %s", s.State)
	}
}

func TestCollect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status.json")
	if err := os.WriteFile(path, []byte(`{"enabled": true, "state": "idle", "last_check_result": "interrupted", "last_check_at": 1764000000}`), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := telemetry.NewRegistry(telemetry.All)
	reg.Add(New(path, nil))
	out := string(reg.Write(context.Background()))
	for _, want := range []string{"radioarchive_auto_upgrade_failing 1", "radioarchive_auto_upgrade_last_check_timestamp_seconds 1764000000"} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
}
