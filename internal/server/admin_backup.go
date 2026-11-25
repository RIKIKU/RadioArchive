package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/RIKIKU/RadioArchive/internal/backup"
)

// Backups for admins (SPEC.md R89; ADR 0041): their state, shown as an
// alert on every admin page when failing, and the setup export written
// beside each night's dumps.

// WithBackup serves the backup's state (GET /api/v1/admin/backup), from
// the backup service's status file.
func (a *Admin) WithBackup(r *backup.Reader) *Admin {
	a.backup = r
	return a
}

// getBackup is GET /api/v1/admin/backup.
func (a *Admin) getBackup(*http.Request, string) (int, any, error) {
	return http.StatusOK, a.backup.Status(), nil
}

// SetupExportFile is the file the setup export is written to, in its
// folder, for the backup service to copy beside each night's dumps.
const SetupExportFile = "setup.json"

// WriteSetupExport writes the whole setup (R53), as the admin export
// answers it, to dir/setup.json, replacing it whole: a file the backup
// service copies is always a complete export. It's written as the API's
// user, readable by the backup service. It returns the export, and writes
// nothing when it's the same as last (the export written before, if any)
// and the file is still there.
func (a *Admin) WriteSetupExport(ctx context.Context, dir string, last []byte) ([]byte, error) {
	s, err := a.readSetup(ctx)
	if err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	b = append(b, '\n')
	if _, err := os.Stat(filepath.Join(dir, SetupExportFile)); err == nil && bytes.Equal(b, last) {
		return b, nil
	}
	f, err := os.CreateTemp(dir, ".setup-*.json")
	if err != nil {
		return nil, fmt.Errorf("the setup export's folder: %w", err)
	}
	_, werr := f.Write(b)
	cerr := f.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(f.Name(), 0o644)
	}
	if werr == nil {
		werr = os.Rename(f.Name(), filepath.Join(dir, SetupExportFile))
	}
	if werr != nil {
		_ = os.Remove(f.Name())
		return nil, fmt.Errorf("writing the setup export: %w", werr)
	}
	return b, nil
}
