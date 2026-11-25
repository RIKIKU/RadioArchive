package server

import (
	"net/http"

	autoupgrade "github.com/RIKIKU/RadioArchive/internal/upgrade"
)

// WithUpgrade serves the automatic upgrade's state (GET
// /api/v1/admin/upgrade; SPEC.md R87, R89; ADR 0044), from the upgrade
// script's status file.
func (a *Admin) WithUpgrade(r *autoupgrade.Reader) *Admin {
	a.upgrade = r
	return a
}

// getUpgrade is GET /api/v1/admin/upgrade.
func (a *Admin) getUpgrade(*http.Request, string) (int, any, error) {
	return http.StatusOK, a.upgrade.Status(), nil
}
