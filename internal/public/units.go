package public

import (
	"fmt"
	"net/http"
)

// Unit search (SPEC.md R26, R27, R32, R46; ADR 0019): the recent calls
// page's unit filter suggests radios by alias or radio ID, and range
// aliases by alias or by their radio IDs. It finds only radios heard on
// calls the public may have, and ranges with one of those in them, through
// the gate, so a radio or a range heard only on a hidden talkgroup answers
// as one never heard. Its answers name no talkgroup and no call.

// UnitSearchPath finds units by alias or radio ID: ?q=.
const UnitSearchPath = "/api/v1/units/search"

// FoundUnit is a unit a search found: a radio (ID) or a range alias (First
// and Last, both included). System is its system's ID, given only when
// there are several systems (R15), as the catalogue gives it. Alias is a
// radio's own alias, or the range's; null when a radio has none of its
// own. In is given for a radio with no unit of its own that a range alias
// covers: the alias its calls show it by.
type FoundUnit struct {
	System *int64  `json:"system,omitempty"`
	ID     *int64  `json:"id,omitempty"`
	First  *int64  `json:"first,omitempty"`
	Last   *int64  `json:"last,omitempty"`
	Alias  *string `json:"alias"`
	In     *string `json:"in,omitempty"`
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
	ptr := func(v int64) *int64 { return &v }
	str := func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}
	out := UnitSearchResponse{Units: make([]FoundUnit, 0, len(us))}
	for _, u := range us {
		f := FoundUnit{Alias: str(u.Alias), In: str(u.In)}
		if u.IsRange() {
			f.First, f.Last = ptr(u.First), ptr(u.Last)
		} else {
			f.ID = ptr(u.ID)
		}
		if len(c.Systems) > 1 {
			f.System = ptr(u.System)
		}
		out.Units = append(out.Units, f)
	}
	writeJSON(w, http.StatusOK, out)
}
