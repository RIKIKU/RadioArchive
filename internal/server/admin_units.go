package server

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"

	"github.com/RIKIKU/RadioArchive/internal/store/db"
)

// Units (SPEC.md R26, R27; ADR 0019): a radio ID, or a range of them, with
// an alias in its system. Set through the admin API only until the web
// admin's table (M4). A unit's path names it: `5001` is one radio, and
// `5100-5199` the range from 5100 to 5199 inclusive. An exact unit beats a
// range that covers it, and the narrower of two ranges wins.

// Unit is a unit as the admin API shows it. From and To are the same for
// one radio.
type Unit struct {
	SystemID  int64      `json:"system_id"`
	From      int64      `json:"from"`
	To        int64      `json:"to"`
	Alias     string     `json:"alias"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

func unitOf(u db.Unit) Unit {
	return Unit{SystemID: u.SystemID, From: u.FirstID, To: u.LastID, Alias: u.Alias,
		CreatedAt: ts(u.CreatedAt), UpdatedAt: ts(u.UpdatedAt)}
}

// MaxUnitAlias is the longest alias, in bytes.
const MaxUnitAlias = 100

// unitPath reads a unit's system and its radio ID or range from the path.
func (a *Admin) unitPath(r *http.Request) (sys, from, to int64, err error) {
	sys, err = pathInt(r, "system", math.MaxInt64)
	if err != nil {
		return 0, 0, 0, err
	}
	notFound := refuseAdmin(http.StatusNotFound, "unit_not_found",
		"A unit is a radio ID, such as 5001, or a range of them, such as 5100-5199.")
	s := r.PathValue("unit")
	first, last, isRange := strings.Cut(s, "-")
	if !isRange {
		last = first
	}
	from, err1 := strconv.ParseInt(first, 10, 64)
	to, err2 := strconv.ParseInt(last, 10, 64)
	if err1 != nil || err2 != nil || from < 1 || to < from || (isRange && to == from) ||
		strconv.FormatInt(from, 10) != first || strconv.FormatInt(to, 10) != last {
		return 0, 0, 0, notFound
	}
	if _, err := a.system(r.Context(), sys); err != nil {
		return 0, 0, 0, err
	}
	return sys, from, to, nil
}

func (a *Admin) listUnits(r *http.Request, _ string) (int, any, error) {
	sys, err := pathInt(r, "system", math.MaxInt64)
	if err != nil {
		return 0, nil, err
	}
	if _, err := a.system(r.Context(), sys); err != nil {
		return 0, nil, err
	}
	rows, err := a.st.ListUnits(r.Context(), sys)
	if err != nil {
		return 0, nil, dbErr("list units", err)
	}
	out := make([]Unit, 0, len(rows))
	for _, u := range rows {
		out = append(out, unitOf(u))
	}
	return http.StatusOK, out, nil
}

func (a *Admin) getUnit(r *http.Request, _ string) (int, any, error) {
	sys, from, to, err := a.unitPath(r)
	if err != nil {
		return 0, nil, err
	}
	u, err := a.st.GetUnit(r.Context(), db.GetUnitParams{SystemID: sys, FirstID: from, LastID: to})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil, refuseAdmin(http.StatusNotFound, "unit_not_found", "System %d has no unit %s.", sys, r.PathValue("unit"))
	}
	if err != nil {
		return 0, nil, dbErr("unit", err)
	}
	return http.StatusOK, unitOf(u), nil
}

// putUnit adds or changes a unit: {"alias": "Engine 1"}. An empty alias is
// a unit with none, which listeners see by its ID (R27), even within a
// range that has one.
func (a *Admin) putUnit(r *http.Request, who string) (int, any, error) {
	sys, from, to, err := a.unitPath(r)
	if err != nil {
		return 0, nil, err
	}
	var in struct {
		Alias *string `json:"alias"`
	}
	if err := decode(r, &in); err != nil {
		return 0, nil, err
	}
	if in.Alias == nil {
		return 0, nil, refuseAdmin(http.StatusBadRequest, "invalid_alias", "alias is required: the unit's name, or \"\" for none.")
	}
	if err := text("alias", *in.Alias, 0, MaxUnitAlias); err != nil {
		return 0, nil, err
	}
	if strings.ContainsRune(*in.Alias, ';') || strings.ContainsFunc(*in.Alias, unicode.IsControl) {
		return 0, nil, refuseAdmin(http.StatusBadRequest, "invalid_alias", "alias can't hold \";\" or control characters.")
	}
	ctx := r.Context()
	created, err := a.st.PutUnit(ctx, db.PutUnitParams{SystemID: sys, FirstID: from, LastID: to, Alias: *in.Alias})
	if err != nil {
		return 0, nil, dbErr("put unit", err)
	}
	u, err := a.st.GetUnit(ctx, db.GetUnitParams{SystemID: sys, FirstID: from, LastID: to})
	if err != nil {
		return 0, nil, dbErr("unit", err)
	}
	// Never the alias: no unit name is logged (A32).
	a.log.Info("admin set a unit", "admin", who, "system", sys, "from", from, "to", to, "created", created)
	if created {
		return http.StatusCreated, unitOf(u), nil
	}
	return http.StatusOK, unitOf(u), nil
}

func (a *Admin) deleteUnit(r *http.Request, who string) (int, any, error) {
	sys, from, to, err := a.unitPath(r)
	if err != nil {
		return 0, nil, err
	}
	n, err := a.st.DeleteUnit(r.Context(), db.DeleteUnitParams{SystemID: sys, FirstID: from, LastID: to})
	if err != nil {
		return 0, nil, dbErr("delete unit", err)
	}
	if n == 0 {
		return 0, nil, refuseAdmin(http.StatusNotFound, "unit_not_found", "System %d has no unit %s.", sys, r.PathValue("unit"))
	}
	a.log.Info("admin deleted a unit", "admin", who, "system", sys, "from", from, "to", to)
	return http.StatusNoContent, nil, nil
}
