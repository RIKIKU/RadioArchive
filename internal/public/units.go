package public

import (
	"fmt"
	"net/http"
)

// Unit search (SPEC.md R26, R27, R32, R46; ADR 0019): the recent calls
// page's unit filter suggests units by alias or radio ID. It finds only
// radios heard on calls the public may have, through the gate, so a radio
// heard only on a hidden talkgroup answers as one never heard. Its answers
// name no talkgroup and no call.

// UnitSearchPath finds units by alias or radio ID: ?q=.
const UnitSearchPath = "/api/v1/units/search"

// FoundUnit is a unit a search found. System is its system's ID, given
// only when there are several systems (R15), as the catalogue gives it.
// Alias is null when it has none (R27).
type FoundUnit struct {
	System *int64  `json:"system,omitempty"`
	ID     int64   `json:"id"`
	Alias  *string `json:"alias"`
}

// UnitSearchResponse is the units a search finds, best first, at most
// gate.MaxUnitsFound.
type UnitSearchResponse struct {
	Units []FoundUnit `json:"units"`
}

func (a *API) unitSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if len(q) > MaxQuery {
		writeJSON(w, http.StatusBadRequest, apiError{Code: "query_too_long",
			Message: fmt.Sprintf("Search for at most %d characters.", MaxQuery)})
		return
	}
	// The catalogue says whether there are several systems.
	c, err := a.g.Catalogue(r.Context())
	if err != nil {
		a.failed(w, r, err)
		return
	}
	us, err := a.g.SearchUnits(r.Context(), q)
	if err != nil {
		a.failed(w, r, err)
		return
	}
	out := UnitSearchResponse{Units: make([]FoundUnit, 0, len(us))}
	for _, u := range us {
		f := FoundUnit{ID: u.ID}
		if len(c.Systems) > 1 {
			s := u.System
			f.System = &s
		}
		if u.Alias != "" {
			s := u.Alias
			f.Alias = &s
		}
		out.Units = append(out.Units, f)
	}
	writeJSON(w, http.StatusOK, out)
}
