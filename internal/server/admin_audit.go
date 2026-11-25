package server

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/store"
)

// The audit log's read API (SPEC.md R54; ADR 0029): every admin change,
// newest first, paged by time with a cursor, filtered by who, kind, thing
// and time range. Reading it isn't audited, and adds nothing.

// AuditPath is the audit log under the admin API.
const AuditPath = AdminPath + "/audit"

// Audit page sizes.
const (
	DefaultAuditPage = 50
	MaxAuditPage     = 200
)

// AuditWho is an entry's admin: their account's ID, and their name and
// email as they were at the change.
type AuditWho struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// AuditEntry is one entry. Via is how the change was made: browser,
// module or import. Changes are the fields changed, in the admin API's
// order, with each one's value before and after (null before for a
// creation, null after for a deletion).
type AuditEntry struct {
	ID      int64               `json:"id"`
	At      time.Time           `json:"at"`
	Who     AuditWho            `json:"who"`
	Via     string              `json:"via"`
	Kind    string              `json:"kind"`
	Thing   string              `json:"thing"`
	Label   string              `json:"label"`
	Action  string              `json:"action"`
	Changes []store.FieldChange `json:"changes"`
}

// AuditPage is GET /api/v1/admin/audit: a page of entries, and the cursor
// for the next (null on the last).
type AuditPage struct {
	Entries []AuditEntry `json:"entries"`
	Next    *string      `json:"next"`
}

func auditCursor(e store.AuditEntry) string {
	return strconv.FormatInt(e.At.UnixMicro(), 10) + "_" + strconv.FormatInt(e.ID, 10)
}

func parseAuditCursor(s string) (time.Time, int64, bool) {
	a, b, ok := strings.Cut(s, "_")
	us, err1 := strconv.ParseInt(a, 10, 64)
	id, err2 := strconv.ParseInt(b, 10, 64)
	if !ok || err1 != nil || err2 != nil || id < 1 {
		return time.Time{}, 0, false
	}
	return time.UnixMicro(us), id, true
}

func (a *Admin) auditLog(r *http.Request, _ string) (int, any, error) {
	v := r.URL.Query()
	f := store.AuditFilter{Subject: v.Get("who"), Kind: v.Get("kind"), Thing: v.Get("thing"), Limit: DefaultAuditPage}
	if f.Kind != "" && !slices.Contains(store.AuditKinds, f.Kind) {
		return 0, nil, refuseAdmin(http.StatusBadRequest, "invalid_kind", "kind must be one of %s.", strings.Join(store.AuditKinds, ", "))
	}
	for _, p := range []struct {
		name string
		to   *time.Time
	}{{"since", &f.Since}, {"until", &f.Until}} {
		if s := v.Get(p.name); s != "" {
			t, err := time.Parse(time.RFC3339Nano, s)
			if err != nil {
				return 0, nil, refuseAdmin(http.StatusBadRequest, "invalid_"+p.name, "%s must be a time such as 2026-11-25T09:00:00Z.", p.name)
			}
			*p.to = t
		}
	}
	if s := v.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > MaxAuditPage {
			return 0, nil, refuseAdmin(http.StatusBadRequest, "invalid_limit", "limit must be a whole number from 1 to %d.", MaxAuditPage)
		}
		f.Limit = n
	}
	if s := v.Get("cursor"); s != "" {
		at, id, ok := parseAuditCursor(s)
		if !ok {
			return 0, nil, refuseAdmin(http.StatusBadRequest, "invalid_cursor", "That cursor isn't one this API gave.")
		}
		f.AfterAt, f.AfterID = at, id
	}
	// One more than the page, to know whether there's another.
	want := f.Limit
	f.Limit++
	rows, err := a.st.AuditEntries(r.Context(), f)
	if err != nil {
		return 0, nil, err
	}
	out := AuditPage{Entries: make([]AuditEntry, 0, min(len(rows), want))}
	for i, e := range rows {
		if i == want {
			c := auditCursor(rows[i-1])
			out.Next = &c
			break
		}
		changes := e.Changes
		if changes == nil {
			changes = []store.FieldChange{}
		}
		out.Entries = append(out.Entries, AuditEntry{ID: e.ID, At: e.At.UTC(),
			Who: AuditWho{ID: e.By.Subject, Name: e.By.Name, Email: e.By.Email}, Via: e.By.Via,
			Kind: e.Kind, Thing: e.Thing, Label: e.Label, Action: e.Action, Changes: changes})
	}
	return http.StatusOK, out, nil
}
