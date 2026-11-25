package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"

	"github.com/RIKIKU/RadioArchive/internal/store"
	"github.com/RIKIKU/RadioArchive/internal/store/db"
)

// The setup's export and import (SPEC.md R11, R21, R22, R26, R52-R54, R89,
// R100, A24, A25; issue #115, ADR 0030).
//
// The export is the whole setup in one versioned JSON document, in a fixed
// order, so the same setup always exports the same bytes. The import takes
// that shape (the module turns trunk-recorder's CSVs into it): it merges on
// system ID, system + talkgroup ID and system + unit; adds what's new with
// every field the file gives, state included; changes only R52's fields of
// a talkgroup that exists, and never its state; and never deletes. A dry
// run answers every row's result and a plan, a fingerprint of them, and
// changes nothing; applying sends the same rows with the plan, and answers
// import_changed, applying nothing, if the results are no longer the
// plan's. Each change is in the audit log, made by the signed-in admin,
// how: import.
//
// With "update": true (issue #117, ADR 0035), what exists takes every
// other field the file gives otherwise too: a talkgroup's delay, alert,
// LED colour, type and frequency, and a system's, group's or tag's own
// fields. State and unreviewed still never change through an import.

// The setup's format: its name and the version this server reads and
// writes.
const (
	SetupFormat  = "radioarchive-setup"
	SetupVersion = 1
)

// The import's limits: the request's body, and its rows (systems,
// talkgroups, units, groups and tags together).
const (
	MaxImportBytes = 8 << 20
	MaxImportRows  = 10_000
)

// Setup is GET /api/v1/admin/setup: the whole setup, as the import takes
// it. Systems, their talkgroups and their units go by ID; groups and tags
// by name. Attachments and recorders aren't in it (R10, R12); hidden
// talkgroups are, because it's the admin's.
type Setup struct {
	Format  string        `json:"format"`
	Version int           `json:"version"`
	Systems []SetupSystem `json:"systems"`
	Groups  []SetupLabel  `json:"groups"`
	Tags    []SetupLabel  `json:"tags"`
}

// SetupSystem is a system in the setup, with its talkgroups and units.
type SetupSystem struct {
	ID         int64            `json:"id"`
	Label      string           `json:"label"`
	Name       string           `json:"name"`
	AutoCreate bool             `json:"auto_create"`
	Alert      *string          `json:"alert"`
	LEDColour  *string          `json:"led_colour"`
	SortOrder  int32            `json:"sort_order"`
	Talkgroups []SetupTalkgroup `json:"talkgroups"`
	Units      []SetupUnit      `json:"units"`
}

// SetupTalkgroup is a talkgroup in the setup: every R22 field it holds.
type SetupTalkgroup struct {
	ID                int32    `json:"id"`
	Label             string   `json:"label"`
	Name              string   `json:"name"`
	Tag               *string  `json:"tag"`
	Groups            []string `json:"groups"`
	State             string   `json:"state"`
	Unreviewed        bool     `json:"unreviewed"`
	Type              string   `json:"type"`
	Frequency         *int64   `json:"frequency"`
	DelayMinutes      int32    `json:"delay_minutes"`
	Alert             *string  `json:"alert"`
	LEDColour         *string  `json:"led_colour"`
	SortOrder         int32    `json:"sort_order"`
	FiregroundChannel *int32   `json:"fireground_channel"`
}

// SetupUnit is a unit in the setup: one radio (from = to) or a range.
type SetupUnit struct {
	From  int64  `json:"from"`
	To    int64  `json:"to"`
	Alias string `json:"alias"`
}

// SetupLabel is a group or a tag in the setup, by name.
type SetupLabel struct {
	Name      string  `json:"name"`
	Alert     *string `json:"alert"`
	LEDColour *string `json:"led_colour"`
	SortOrder int32   `json:"sort_order"`
}

func setupTalkgroup(t Talkgroup) SetupTalkgroup {
	return SetupTalkgroup{ID: t.ID, Label: t.Label, Name: t.Name, Tag: t.Tag, Groups: t.Groups, State: t.State,
		Unreviewed: t.Unreviewed, Type: t.Type, Frequency: t.Frequency, DelayMinutes: t.DelayMinutes,
		Alert: t.Alert, LEDColour: t.LEDColour, SortOrder: t.SortOrder, FiregroundChannel: t.FiregroundChannel}
}

func setupLabel(name string, alert, led *string, sort int32) SetupLabel {
	return SetupLabel{Name: name, Alert: alert, LEDColour: led, SortOrder: sort}
}

func byName(a, b SetupLabel) int { return strings.Compare(a.Name, b.Name) }

// readSetup reads the whole setup in one snapshot.
func (a *Admin) readSetup(ctx context.Context) (Setup, error) {
	tx, err := a.st.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Setup{}, dbErr("export", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)
	systems, err := q.ListSystems(ctx)
	if err != nil {
		return Setup{}, dbErr("export systems", err)
	}
	tgs, err := q.ExportTalkgroups(ctx)
	if err != nil {
		return Setup{}, dbErr("export talkgroups", err)
	}
	units, err := q.ExportUnits(ctx)
	if err != nil {
		return Setup{}, dbErr("export units", err)
	}
	groups, err := q.ListGroups(ctx)
	if err != nil {
		return Setup{}, dbErr("export groups", err)
	}
	tags, err := q.ListTags(ctx)
	if err != nil {
		return Setup{}, dbErr("export tags", err)
	}
	out := Setup{Format: SetupFormat, Version: SetupVersion, Systems: make([]SetupSystem, 0, len(systems)),
		Groups: make([]SetupLabel, 0, len(groups)), Tags: make([]SetupLabel, 0, len(tags))}
	at := map[int64]int{}
	for _, s := range systems {
		at[s.ID] = len(out.Systems)
		out.Systems = append(out.Systems, SetupSystem{ID: s.ID, Label: s.Label, Name: s.Name, AutoCreate: s.AutoCreate,
			Alert: s.Alert, LEDColour: s.LedColour, SortOrder: s.SortOrder,
			Talkgroups: []SetupTalkgroup{}, Units: []SetupUnit{}})
	}
	for _, t := range tgs {
		s := &out.Systems[at[t.SystemID]]
		s.Talkgroups = append(s.Talkgroups, setupTalkgroup(talkgroupOf(db.GetTalkgroupRow(t))))
	}
	for _, u := range units {
		s := &out.Systems[at[u.SystemID]]
		s.Units = append(s.Units, SetupUnit{From: u.FirstID, To: u.LastID, Alias: u.Alias})
	}
	for _, g := range groups {
		out.Groups = append(out.Groups, setupLabel(g.Name, g.Alert, g.LedColour, g.SortOrder))
	}
	for _, t := range tags {
		out.Tags = append(out.Tags, setupLabel(t.Name, t.Alert, t.LedColour, t.SortOrder))
	}
	// By name in bytes, not the database's collation, so the order is the
	// same on every deployment.
	slices.SortFunc(out.Groups, byName)
	slices.SortFunc(out.Tags, byName)
	return out, nil
}

// exportSetup answers the whole setup, indented, so a saved export reads
// and compares as text. The same setup always gives the same bytes.
func (a *Admin) exportSetup(w http.ResponseWriter, r *http.Request, who string) {
	s, err := a.readSetup(r.Context())
	var b []byte
	if err == nil {
		b, err = json.MarshalIndent(s, "", "  ")
	}
	if err != nil {
		a.log.Error("admin export failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, adminError{Code: "internal", Message: "Something went wrong."})
		return
	}
	talkgroups, units := 0, 0
	for _, x := range s.Systems {
		talkgroups += len(x.Talkgroups)
		units += len(x.Units)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(b, '\n'))
	a.log.Info("admin exported the setup", "admin", who, "systems", len(s.Systems), "talkgroups", talkgroups, "units", units)
}

// The import's request: POST /api/v1/admin/import.

type importRequest struct {
	Setup  *importSetup `json:"setup"`
	DryRun bool         `json:"dry_run"`
	Plan   *string      `json:"plan"`
	// Update makes what exists take every field the file gives otherwise,
	// but a talkgroup's state and unreviewed (ADR 0035).
	Update bool `json:"update"`
}

// importSetup is the setup as the import takes it: the export's shape,
// where a field left out of a row is "not said" (see ADR 0030).
type importSetup struct {
	Format  string         `json:"format"`
	Version int            `json:"version"`
	Systems []importSystem `json:"systems"`
	Groups  []importLabel  `json:"groups"`
	Tags    []importLabel  `json:"tags"`
}

type importSystem struct {
	ID         int64   `json:"id"`
	Label      *string `json:"label"`
	Name       *string `json:"name"`
	AutoCreate *bool   `json:"auto_create"`
	settingsFields
	Talkgroups []importTalkgroup `json:"talkgroups"`
	Units      []importUnit      `json:"units"`
}

type importTalkgroup struct {
	ID         int64 `json:"id"`
	Unreviewed *bool `json:"unreviewed"`
	talkgroupFields
	skip  string             // why the import skips it, if it does
	state *db.TalkgroupState // the state it gives, checked
}

type importUnit struct {
	From  int64   `json:"from"`
	To    *int64  `json:"to"`
	Alias *string `json:"alias"`
}

type importLabel struct {
	Name string `json:"name"`
	settingsFields
}

// Why the import skips a talkgroup's row (R52, section 4).
const (
	SkipPlaceholder  = "placeholder"
	SkipAnalog       = "analog"
	SkipConventional = "conventional"
)

// skipReason is why a row is skipped: its type says analog (trunk-
// recorder's mode A) or conventional, or it's a placeholder, whose label
// is just its talkgroup ID ("1006", or "01006").
func skipReason(t *importTalkgroup) string {
	if t.Type != nil {
		switch strings.ToLower(strings.TrimSpace(*t.Type)) {
		case "a", "analog":
			return SkipAnalog
		case "conventional":
			return SkipConventional
		}
	}
	if t.Label != nil {
		l := strings.TrimSpace(*t.Label)
		if l != "" && !strings.ContainsFunc(l, func(c rune) bool { return c < '0' || c > '9' }) {
			if n, err := strconv.ParseInt(l, 10, 64); err == nil && n == t.ID {
				return SkipPlaceholder
			}
		}
	}
	return ""
}

// atRow prefixes a refusal's message with the row it's about.
func atRow(where string, err error) error {
	var v *adminRefusal
	if errors.As(err, &v) {
		return refuseAdmin(v.status, v.body.Code, "%s: %s", where, v.body.Message)
	}
	return err
}

func duplicate(format string, a ...any) error {
	return refuseAdmin(http.StatusBadRequest, "duplicate_row", format+" is in the file more than once.", a...)
}

// check checks every row before anything is read or written, so a file
// with a bad row changes nothing and says which row.
func (s *importSetup) check() error {
	if s.Format != SetupFormat || s.Version != SetupVersion {
		return refuseAdmin(http.StatusBadRequest, "unsupported_format",
			`An import is a setup with "format": %q and "version": %d, as the export gives.`, SetupFormat, SetupVersion)
	}
	rows := len(s.Systems) + len(s.Groups) + len(s.Tags)
	for _, x := range s.Systems {
		rows += len(x.Talkgroups) + len(x.Units)
	}
	if rows > MaxImportRows {
		return refuseAdmin(http.StatusRequestEntityTooLarge, "too_many_rows",
			"An import is at most %d rows (systems, talkgroups, units, groups and tags together); this one has %d.",
			MaxImportRows, rows)
	}
	systems := map[int64]bool{}
	for i := range s.Systems {
		sys := &s.Systems[i]
		if sys.ID < 1 {
			return refuseAdmin(http.StatusBadRequest, "invalid_id", "A system's id must be a whole number from 1.")
		}
		if systems[sys.ID] {
			return duplicate("System %d", sys.ID)
		}
		systems[sys.ID] = true
		where := fmt.Sprintf("System %d", sys.ID)
		if sys.Label != nil {
			if err := text("label", *sys.Label, 1, 100); err != nil {
				return atRow(where, err)
			}
		}
		if sys.Name != nil {
			if err := text("name", *sys.Name, 0, 200); err != nil {
				return atRow(where, err)
			}
		}
		if err := sys.settingsFields.check(); err != nil {
			return atRow(where, err)
		}
		talkgroups := map[int64]bool{}
		for j := range sys.Talkgroups {
			t := &sys.Talkgroups[j]
			if t.ID < 1 || t.ID > math.MaxInt32 {
				return refuseAdmin(http.StatusBadRequest, "invalid_id",
					"%s: a talkgroup's id must be a whole number from 1 to %d.", where, math.MaxInt32)
			}
			if talkgroups[t.ID] {
				return duplicate("%s, talkgroup %d", where, t.ID)
			}
			talkgroups[t.ID] = true
			if t.skip = skipReason(t); t.skip != "" {
				continue
			}
			st, err := t.talkgroupFields.check()
			if err != nil {
				return atRow(fmt.Sprintf("%s, talkgroup %d", where, t.ID), err)
			}
			t.state = st
		}
		units := map[[2]int64]bool{}
		for j := range sys.Units {
			u := &sys.Units[j]
			to := u.From
			if u.To != nil {
				to = *u.To
			}
			if u.From < 1 || to < u.From {
				return refuseAdmin(http.StatusBadRequest, "invalid_unit",
					"%s: a unit is a radio ID from 1 (from), or a range of them from a lower ID to a higher one (from and to).", where)
			}
			u.To = &to
			uw := fmt.Sprintf("%s, unit %s", where, unitName(u.From, to))
			if units[[2]int64{u.From, to}] {
				return duplicate("%s", uw)
			}
			units[[2]int64{u.From, to}] = true
			if u.Alias == nil {
				return refuseAdmin(http.StatusBadRequest, "invalid_alias", "%s: alias is required: the unit's name, or \"\" for none.", uw)
			}
			if err := text("alias", *u.Alias, 0, MaxUnitAlias); err != nil {
				return atRow(uw, err)
			}
			if strings.ContainsRune(*u.Alias, ';') || strings.ContainsFunc(*u.Alias, unicode.IsControl) {
				return refuseAdmin(http.StatusBadRequest, "invalid_alias", "%s: alias can't hold \";\" or control characters.", uw)
			}
		}
	}
	for _, l := range []struct {
		kind string
		rows []importLabel
	}{{"group", s.Groups}, {"tag", s.Tags}} {
		names := map[string]bool{}
		for _, x := range l.rows {
			if err := text("name", x.Name, 1, 100); err != nil {
				return atRow("A "+l.kind, err)
			}
			if names[x.Name] {
				return duplicate("%s %q", strings.ToUpper(l.kind[:1])+l.kind[1:], x.Name)
			}
			names[x.Name] = true
			if err := x.settingsFields.check(); err != nil {
				return atRow(fmt.Sprintf("%s %q", strings.ToUpper(l.kind[:1])+l.kind[1:], x.Name), err)
			}
		}
	}
	return nil
}

// The import's answer.

// The results a row, or a thing the file leaves out, can have.
const (
	ImportNew       = "new"
	ImportChanged   = "changed"
	ImportUnchanged = "unchanged"
	ImportKept      = "kept"
	ImportSkipped   = "skipped"
)

// ImportCounts counts one kind's results.
type ImportCounts struct {
	New       int `json:"new"`
	Changed   int `json:"changed"`
	Unchanged int `json:"unchanged"`
	Kept      int `json:"kept"`
	Skipped   int `json:"skipped"`
}

func (c *ImportCounts) add(result string) {
	switch result {
	case ImportNew:
		c.New++
	case ImportChanged:
		c.Changed++
	case ImportUnchanged:
		c.Unchanged++
	case ImportKept:
		c.Kept++
	case ImportSkipped:
		c.Skipped++
	}
}

// ImportSummary counts the results of each kind.
type ImportSummary struct {
	Systems    ImportCounts `json:"systems"`
	Talkgroups ImportCounts `json:"talkgroups"`
	Units      ImportCounts `json:"units"`
	Groups     ImportCounts `json:"groups"`
	Tags       ImportCounts `json:"tags"`
}

// ImportResult is one row's result, or a thing's the file leaves out
// (kept).
type ImportResult struct {
	// Kind is system, talkgroup, unit, group or tag.
	Kind string `json:"kind"`
	// System is a talkgroup's or a unit's system.
	System *int64 `json:"system,omitempty"`
	// ID is which one: a system's or talkgroup's ID, a unit's radio ID or
	// range ("5100-5199"), a group's or tag's name.
	ID string `json:"id"`
	// Label is what it goes by, as the audit log names it.
	Label  string `json:"label"`
	Result string `json:"result"`
	// Reason is why a row is skipped: placeholder, analog or conventional.
	Reason string `json:"reason,omitempty"`
	// Changes are a new or changed thing's fields, before and after, as its
	// audit log entry holds them.
	Changes []store.FieldChange `json:"changes,omitempty"`
	// NotChanged are fields of a thing that exists which the file gives
	// otherwise, and which an import never changes (a talkgroup's state,
	// delay, alert and the rest; a system's, group's or tag's settings):
	// before is what it is, after what the file says.
	NotChanged []store.FieldChange `json:"not_changed,omitempty"`
}

// ImportReport is the import's answer: every row's result, and the plan.
type ImportReport struct {
	// Error and Message are set when the import was refused with its
	// results (import_changed).
	Error   string `json:"error,omitempty"`
	Message string `json:"message,omitempty"`
	DryRun  bool   `json:"dry_run"`
	Applied bool   `json:"applied"`
	// Update says whether the import was asked to update what exists.
	Update bool `json:"update"`
	// Plan is the fingerprint of the summary and results: apply with it.
	Plan    string         `json:"plan"`
	Summary ImportSummary  `json:"summary"`
	Results []ImportResult `json:"results"`
}

func (r *ImportReport) add(x ImportResult) {
	switch x.Kind {
	case store.AuditSystem:
		r.Summary.Systems.add(x.Result)
	case store.AuditTalkgroup:
		r.Summary.Talkgroups.add(x.Result)
	case store.AuditUnit:
		r.Summary.Units.add(x.Result)
	case store.AuditGroup:
		r.Summary.Groups.add(x.Result)
	case store.AuditTag:
		r.Summary.Tags.add(x.Result)
	}
	r.Results = append(r.Results, x)
}

// plan fingerprints the update option, the summary and the results:
// SHA-256 of their JSON. A preview without update doesn't apply with it.
func (r *ImportReport) plan() error {
	b, err := json.Marshal(struct {
		Update  bool           `json:"update"`
		Summary ImportSummary  `json:"summary"`
		Results []ImportResult `json:"results"`
	}{r.Update, r.Summary, r.Results})
	if err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	r.Plan = hex.EncodeToString(sum[:])
	return nil
}

// safeErr keeps a database error for errors.As while its text is only its
// SQLSTATE, since logs never hold names (A32).
type safeErr struct {
	what string
	err  error
}

func (e safeErr) Error() string { return e.what + ": " + store.SafeError(e.err) }
func (e safeErr) Unwrap() error { return e.err }

func failed(what string, err error) error {
	if err == nil {
		return nil
	}
	var v *adminRefusal
	if errors.As(err, &v) {
		return err
	}
	return safeErr{what: what, err: err}
}

func raw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// notChanged lists a field the file gives otherwise than it is, which the
// import doesn't change.
func notChanged(out []store.FieldChange, field string, is, file any) []store.FieldChange {
	a, b := raw(is), raw(file)
	if bytes.Equal(a, b) {
		return out
	}
	return append(out, store.FieldChange{Field: field, Before: a, After: b})
}

type tgKey struct {
	sys int64
	id  int32
}

type unitKey struct{ sys, from, to int64 }

// importRun is one import, dry run or applied, in one audited transaction.
type importRun struct {
	ctx     context.Context
	q       *db.Queries
	au      *store.Audit
	in      *importSetup
	systems map[int64]System
	groups  map[string]db.Group
	tags    map[string]db.Tag
	tgs     map[tgKey]Talkgroup
	units   map[unitKey]db.Unit
	// groupsMade and tagsMade are the file's groups and tags it creates.
	groupsMade, tagsMade map[string]bool
	// update: what exists takes every field the file gives (ADR 0035).
	// groupsChanged, tagsChanged and systemsChanged are the changes it made
	// to those that exist.
	update                     bool
	groupsChanged, tagsChanged map[string][]store.FieldChange
	systemsChanged             map[int64][]store.FieldChange
}

func (x *importRun) load() error {
	ctx, q := x.ctx, x.q
	systems, err := q.ListSystems(ctx)
	if err != nil {
		return failed("import systems", err)
	}
	x.systems = map[int64]System{}
	for _, s := range systems {
		x.systems[s.ID] = systemOf(db.GetSystemRow(s))
	}
	groups, err := q.ListGroups(ctx)
	if err != nil {
		return failed("import groups", err)
	}
	x.groups = map[string]db.Group{}
	for _, g := range groups {
		x.groups[g.Name] = g
	}
	tags, err := q.ListTags(ctx)
	if err != nil {
		return failed("import tags", err)
	}
	x.tags = map[string]db.Tag{}
	for _, t := range tags {
		x.tags[t.Name] = t
	}
	tgs, err := q.ExportTalkgroups(ctx)
	if err != nil {
		return failed("import talkgroups", err)
	}
	x.tgs = map[tgKey]Talkgroup{}
	for _, t := range tgs {
		x.tgs[tgKey{t.SystemID, t.TalkgroupID}] = talkgroupOf(db.GetTalkgroupRow(t))
	}
	units, err := q.ExportUnits(ctx)
	if err != nil {
		return failed("import units", err)
	}
	x.units = map[unitKey]db.Unit{}
	for _, u := range units {
		x.units[unitKey{u.SystemID, u.FirstID, u.LastID}] = u
	}
	return nil
}

// sameSet says whether two lists of names hold the same names.
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(x, y)
}

// writes says whether a row changes the talkgroup t that exists: R52's
// fields (label, name, tag, groups, fireground channel number, and sort
// order when given), and with update its delay, alert, LED colour, type
// and frequency too. Never its state or unreviewed.
func (r *importTalkgroup) writes(t Talkgroup, update bool) bool {
	fg := r.fireground()
	switch {
	case r.Label != nil && *r.Label != t.Label, r.Name != nil && *r.Name != t.Name,
		r.Tag != nil && (t.Tag == nil || *r.Tag != *t.Tag),
		r.Groups != nil && !sameSet(*r.Groups, t.Groups),
		r.SortOrder != nil && *r.SortOrder != t.SortOrder,
		r.FiregroundChannel.Set && !equalPtr(fg, t.FiregroundChannel):
		return true
	case !update:
		return false
	case r.DelayMinutes != nil && *r.DelayMinutes != t.DelayMinutes,
		r.Alert.Set && !equalPtr(r.Alert.Value, t.Alert),
		r.LEDColour.Set && !equalPtr(r.LEDColour.Value, t.LEDColour),
		r.Type != nil && *r.Type != t.Type,
		r.Frequency.Set && !equalPtr(r.Frequency.Value, t.Frequency):
		return true
	}
	return false
}

// writes says whether, with update, a system's row changes the system s
// that exists.
func (r *importSystem) writes(s System) bool {
	return (r.Label != nil && *r.Label != s.Label) || (r.Name != nil && *r.Name != s.Name) ||
		(r.AutoCreate != nil && *r.AutoCreate != s.AutoCreate) || r.settingsFields.differ(s.Alert, s.LEDColour, s.SortOrder)
}

// differ says whether settings the file gives differ from what a system,
// group or tag has.
func (f settingsFields) differ(alert, led *string, sort int32) bool {
	return (f.Alert.Set && !equalPtr(f.Alert.Value, alert)) || (f.LEDColour.Set && !equalPtr(f.LEDColour.Value, led)) ||
		(f.SortOrder != nil && *f.SortOrder != sort)
}

func equalPtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// talkgroupName is a talkgroup as a refusal names it: its name, else its
// label, else its ID (R24).
func talkgroupName(id int32, label, name string) string {
	if name != "" {
		return name
	}
	if label != "" {
		return label
	}
	return strconv.Itoa(int(id))
}

func (x *importRun) systemLabel(id int64) string {
	if s, ok := x.systems[id]; ok {
		return s.Label
	}
	for _, s := range x.in.Systems {
		if s.ID == id && s.Label != nil {
			return *s.Label
		}
	}
	return strconv.FormatInt(id, 10)
}

// firegroundCheck refuses a setup that would give one fireground channel
// number to two talkgroups (R100, A25): within the file, or against a
// talkgroup the file doesn't change. It names them all.
func (x *importRun) firegroundCheck() error {
	type holder struct {
		key  tgKey
		name string
	}
	final := map[tgKey]*int32{}
	names := map[tgKey]string{}
	for k, t := range x.tgs {
		final[k] = t.FiregroundChannel
		names[k] = talkgroupName(t.ID, t.Label, t.Name)
	}
	for _, s := range x.in.Systems {
		for i := range s.Talkgroups {
			r := &s.Talkgroups[i]
			if r.skip != "" {
				continue
			}
			k := tgKey{s.ID, int32(r.ID)}
			t, exists := x.tgs[k]
			if !exists || r.FiregroundChannel.Set {
				final[k] = r.fireground()
			}
			label, name := t.Label, t.Name
			if r.Label != nil {
				label = *r.Label
			}
			if r.Name != nil {
				name = *r.Name
			}
			names[k] = talkgroupName(k.id, label, name)
		}
	}
	by := map[int32][]holder{}
	for k, n := range final {
		if n != nil {
			by[*n] = append(by[*n], holder{k, names[k]})
		}
	}
	var numbers []int32
	for n, hs := range by {
		if len(hs) > 1 {
			numbers = append(numbers, n)
		}
	}
	if len(numbers) == 0 {
		return nil
	}
	slices.Sort(numbers)
	var parts []string
	for _, n := range numbers {
		hs := by[n]
		sort.Slice(hs, func(i, j int) bool {
			return hs[i].key.sys < hs[j].key.sys || (hs[i].key.sys == hs[j].key.sys && hs[i].key.id < hs[j].key.id)
		})
		var who []string
		for _, h := range hs {
			who = append(who, fmt.Sprintf("talkgroup %d %q in system %s", h.key.id, h.name, x.systemLabel(h.key.sys)))
		}
		parts = append(parts, fmt.Sprintf("Fireground channel %d would be %s", n, strings.Join(who, " and ")))
	}
	return refuseAdmin(http.StatusConflict, "fireground_channel_taken",
		"%s. A number is one talkgroup's (R100), so nothing was imported.", strings.Join(parts, "; "))
}

// lock takes the audit log's lock on every thing that exists and the
// import writes, so an admin changing one of them at once records what
// the import left (ADR 0029), then reads them again. Things it creates
// aren't locked: one created meanwhile makes the insert fail, which is
// import_changed.
func (x *importRun) lock() error {
	type thing struct{ kind, key string }
	var things []thing
	if x.update {
		for _, l := range x.in.Groups {
			if g, ok := x.groups[l.Name]; ok && l.differ(g.Alert, g.LedColour, g.SortOrder) {
				things = append(things, thing{store.AuditGroup, strconv.FormatInt(g.ID, 10)})
			}
		}
		for _, l := range x.in.Tags {
			if t, ok := x.tags[l.Name]; ok && l.differ(t.Alert, t.LedColour, t.SortOrder) {
				things = append(things, thing{store.AuditTag, strconv.FormatInt(t.ID, 10)})
			}
		}
	}
	for i := range x.in.Systems {
		s := &x.in.Systems[i]
		if sys, ok := x.systems[s.ID]; ok && x.update && s.writes(sys) {
			things = append(things, thing{store.AuditSystem, systemThing(s.ID)})
		}
		for i := range s.Talkgroups {
			r := &s.Talkgroups[i]
			k := tgKey{s.ID, int32(r.ID)}
			if t, ok := x.tgs[k]; ok && r.skip == "" && r.writes(t, x.update) {
				things = append(things, thing{store.AuditTalkgroup, talkgroupThing(k.sys, k.id)})
			}
		}
		for _, u := range s.Units {
			if was, ok := x.units[unitKey{s.ID, u.From, *u.To}]; ok && was.Alias != *u.Alias {
				things = append(things, thing{store.AuditUnit, unitThing(s.ID, u.From, *u.To)})
			}
		}
	}
	if len(things) == 0 {
		return nil
	}
	slices.SortFunc(things, func(a, b thing) int {
		if c := strings.Compare(a.kind, b.kind); c != 0 {
			return c
		}
		return strings.Compare(a.key, b.key)
	})
	for _, t := range things {
		if err := x.au.Lock(t.kind, t.key); err != nil {
			return failed("import lock", err)
		}
	}
	return x.load()
}

// systemLabelCheck refuses, with update, a setup that would give two
// systems one label, before anything is written: systems' labels are
// unique, and a unique index failing mid-import would read as
// import_changed.
func (x *importRun) systemLabelCheck() error {
	final := map[int64]string{}
	for id, s := range x.systems {
		final[id] = s.Label
	}
	for _, s := range x.in.Systems {
		_, exists := x.systems[s.ID]
		if s.Label != nil && (!exists || x.update) {
			final[s.ID] = *s.Label
		}
	}
	by := map[string][]int64{}
	for id, l := range final {
		by[l] = append(by[l], id)
	}
	var labels []string
	for l, ids := range by {
		if len(ids) > 1 {
			labels = append(labels, l)
		}
	}
	if len(labels) == 0 {
		return nil
	}
	slices.Sort(labels)
	ids := by[labels[0]]
	slices.Sort(ids)
	return refuseAdmin(http.StatusConflict, "label_taken",
		"Systems %d and %d would both be labelled %q, so nothing was imported.", ids[0], ids[1], labels[0])
}

// updateExisting, with update, changes the systems, groups and tags that
// exist where the file gives their fields otherwise, recording each.
func (x *importRun) updateExisting(systems []importSystem, made map[int64]bool) error {
	x.groupsChanged, x.tagsChanged, x.systemsChanged =
		map[string][]store.FieldChange{}, map[string][]store.FieldChange{}, map[int64][]store.FieldChange{}
	if !x.update {
		return nil
	}
	ctx, q := x.ctx, x.q
	for _, l := range x.in.Tags {
		was, ok := x.tags[l.Name]
		if !ok || x.tagsMade[l.Name] || !l.differ(was.Alert, was.LedColour, was.SortOrder) {
			continue
		}
		t, err := q.UpdateTag(ctx, db.UpdateTagParams{ID: was.ID, SortOrder: l.SortOrder, SetAlert: l.Alert.Set,
			Alert: l.Alert.Value, SetLedColour: l.LEDColour.Set, LedColour: l.LEDColour.Value})
		if err != nil {
			return failed("import tag", err)
		}
		before, after := labelOf(was.ID, was.Name, was.Alert, was.LedColour, was.SortOrder),
			labelOf(t.ID, t.Name, t.Alert, t.LedColour, t.SortOrder)
		ch, err := store.Changes(before, after)
		if err != nil {
			return err
		}
		if err := x.au.Record(store.Change{Kind: store.AuditTag, Thing: strconv.FormatInt(t.ID, 10), Label: t.Name,
			Before: before, After: after}); err != nil {
			return failed("import audit", err)
		}
		x.tagsChanged[l.Name] = ch
		x.tags[l.Name] = t
	}
	for _, l := range x.in.Groups {
		was, ok := x.groups[l.Name]
		if !ok || x.groupsMade[l.Name] || !l.differ(was.Alert, was.LedColour, was.SortOrder) {
			continue
		}
		g, err := q.UpdateGroup(ctx, db.UpdateGroupParams{ID: was.ID, SortOrder: l.SortOrder, SetAlert: l.Alert.Set,
			Alert: l.Alert.Value, SetLedColour: l.LEDColour.Set, LedColour: l.LEDColour.Value})
		if err != nil {
			return failed("import group", err)
		}
		before, after := labelOf(was.ID, was.Name, was.Alert, was.LedColour, was.SortOrder),
			labelOf(g.ID, g.Name, g.Alert, g.LedColour, g.SortOrder)
		ch, err := store.Changes(before, after)
		if err != nil {
			return err
		}
		if err := x.au.Record(store.Change{Kind: store.AuditGroup, Thing: strconv.FormatInt(g.ID, 10), Label: g.Name,
			Before: before, After: after}); err != nil {
			return failed("import audit", err)
		}
		x.groupsChanged[l.Name] = ch
		x.groups[l.Name] = g
	}
	for i := range systems {
		s := &systems[i]
		was, ok := x.systems[s.ID]
		if !ok || made[s.ID] || !s.writes(was) {
			continue
		}
		row, err := q.UpdateSystem(ctx, db.UpdateSystemParams{ID: s.ID, Label: s.Label, Name: s.Name,
			AutoCreate: s.AutoCreate, SortOrder: s.SortOrder, SetAlert: s.Alert.Set, Alert: s.Alert.Value,
			SetLedColour: s.LEDColour.Set, LedColour: s.LEDColour.Value})
		if err != nil {
			return failed("import system", err)
		}
		sys := systemOf(db.GetSystemRow(row))
		ch, err := store.Changes(was.audited(), sys.audited())
		if err != nil {
			return err
		}
		if err := x.au.Record(store.Change{Kind: store.AuditSystem, Thing: systemThing(s.ID), Label: sys.Label,
			Before: was.audited(), After: sys.audited()}); err != nil {
			return failed("import audit", err)
		}
		x.systemsChanged[s.ID] = ch
		x.systems[s.ID] = sys
	}
	return nil
}

// run makes the import, recording each change, and answers its results.
func (x *importRun) run() (ImportReport, error) {
	ctx, q := x.ctx, x.q
	rep := ImportReport{Results: []ImportResult{}, Update: x.update}
	if err := q.LockImport(ctx); err != nil {
		return rep, failed("import lock", err)
	}
	if err := x.load(); err != nil {
		return rep, err
	}
	if err := x.lock(); err != nil {
		return rep, err
	}
	if err := x.firegroundCheck(); err != nil {
		return rep, err
	}
	if err := x.systemLabelCheck(); err != nil {
		return rep, err
	}
	// A new talkgroup needs a tag, unless it's unreviewed (R22).
	for _, s := range x.in.Systems {
		for i := range s.Talkgroups {
			r := &s.Talkgroups[i]
			if _, ok := x.tgs[tgKey{s.ID, int32(r.ID)}]; !ok && r.skip == "" && r.Tag == nil &&
				(r.Unreviewed == nil || !*r.Unreviewed) {
				return rep, refuseAdmin(http.StatusBadRequest, "tag_required",
					"System %d, talkgroup %d: a new talkgroup needs a tag, unless it's unreviewed.", s.ID, r.ID)
			}
		}
		if _, ok := x.systems[s.ID]; !ok && s.Label == nil {
			return rep, refuseAdmin(http.StatusBadRequest, "label_required",
				"System %d: a new system needs a label.", s.ID)
		}
	}

	// What the file names first, so its talkgroups find them with their
	// settings: its new tags, groups and systems.
	x.tagsMade, x.groupsMade = map[string]bool{}, map[string]bool{}
	for _, l := range x.in.Tags {
		if _, ok := x.tags[l.Name]; ok {
			continue
		}
		p := db.CreateTagParams{Name: l.Name, Alert: l.Alert.Value, LedColour: l.LEDColour.Value}
		if l.SortOrder != nil {
			p.SortOrder = *l.SortOrder
		}
		t, err := q.CreateTag(ctx, p)
		if err != nil {
			return rep, failed("import tag", err)
		}
		x.tagsMade[l.Name] = true
		if err := x.au.Record(store.Change{Kind: store.AuditTag, Thing: strconv.FormatInt(t.ID, 10), Label: t.Name,
			After: labelOf(t.ID, t.Name, t.Alert, t.LedColour, t.SortOrder)}); err != nil {
			return rep, failed("import audit", err)
		}
		x.tags[l.Name] = t
	}
	for _, l := range x.in.Groups {
		if _, ok := x.groups[l.Name]; ok {
			continue
		}
		p := db.CreateGroupParams{Name: l.Name, Alert: l.Alert.Value, LedColour: l.LEDColour.Value}
		if l.SortOrder != nil {
			p.SortOrder = *l.SortOrder
		}
		g, err := q.CreateGroup(ctx, p)
		if err != nil {
			return rep, failed("import group", err)
		}
		x.groupsMade[l.Name] = true
		if err := x.au.Record(store.Change{Kind: store.AuditGroup, Thing: strconv.FormatInt(g.ID, 10), Label: g.Name,
			After: labelOf(g.ID, g.Name, g.Alert, g.LedColour, g.SortOrder)}); err != nil {
			return rep, failed("import audit", err)
		}
		x.groups[l.Name] = g
	}
	systems := slices.Clone(x.in.Systems)
	slices.SortFunc(systems, func(a, b importSystem) int { return cmpInt(a.ID, b.ID) })
	made := map[int64]bool{}
	for _, s := range systems {
		if _, ok := x.systems[s.ID]; ok {
			continue
		}
		p := db.CreateSystemParams{ID: s.ID, Label: *s.Label, Alert: s.Alert.Value, LedColour: s.LEDColour.Value}
		if s.Name != nil {
			p.Name = *s.Name
		}
		if s.AutoCreate != nil {
			p.AutoCreate = *s.AutoCreate
		}
		if s.SortOrder != nil {
			p.SortOrder = *s.SortOrder
		}
		row, err := q.CreateSystem(ctx, p)
		if err != nil {
			return rep, failed("import system", err)
		}
		sys := systemOf(db.GetSystemRow(row))
		if err := x.au.Record(store.Change{Kind: store.AuditSystem, Thing: systemThing(s.ID), Label: sys.Label,
			After: sys.audited()}); err != nil {
			return rep, failed("import audit", err)
		}
		x.systems[s.ID] = sys
		made[s.ID] = true
	}
	if err := x.updateExisting(systems, made); err != nil {
		return rep, err
	}

	// Free the fireground channel numbers that move, so two talkgroups can
	// swap theirs in one import.
	for _, s := range systems {
		for i := range s.Talkgroups {
			r := &s.Talkgroups[i]
			k := tgKey{s.ID, int32(r.ID)}
			if t, ok := x.tgs[k]; ok && r.skip == "" && r.FiregroundChannel.Set && t.FiregroundChannel != nil &&
				!equalPtr(r.fireground(), t.FiregroundChannel) {
				if err := q.ClearFireground(ctx, db.ClearFiregroundParams{SystemID: k.sys, TalkgroupID: k.id}); err != nil {
					return rep, failed("import fireground", err)
				}
			}
		}
	}

	// Every system the deployment has, by ID: the file's with their rows,
	// the rest kept.
	var ids []int64
	for id := range x.systems {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	file := map[int64]*importSystem{}
	for i := range systems {
		file[systems[i].ID] = &systems[i]
	}
	for _, id := range ids {
		sys := x.systems[id]
		s, ok := file[id]
		res := ImportResult{Kind: store.AuditSystem, ID: systemThing(id), Label: sys.Label}
		switch {
		case !ok:
			res.Result = ImportKept
			rep.add(res)
			continue
		case made[id]:
			res.Result = ImportNew
			ch, err := store.Changes(nil, sys.audited())
			if err != nil {
				return rep, err
			}
			res.Changes = ch
		case x.update:
			res.Result = ImportUnchanged
			if ch, ok := x.systemsChanged[id]; ok {
				res.Result, res.Changes = ImportChanged, ch
			}
		default:
			res.Result = ImportUnchanged
			var nc []store.FieldChange
			if s.Label != nil {
				nc = notChanged(nc, "label", sys.Label, *s.Label)
			}
			if s.Name != nil {
				nc = notChanged(nc, "name", sys.Name, *s.Name)
			}
			if s.AutoCreate != nil {
				nc = notChanged(nc, "auto_create", sys.AutoCreate, *s.AutoCreate)
			}
			nc = x.settingsNotChanged(nc, s.settingsFields, sys.Alert, sys.LEDColour, sys.SortOrder)
			res.NotChanged = nc
		}
		rep.add(res)
		if err := x.talkgroups(&rep, s); err != nil {
			return rep, err
		}
		if err := x.unitRows(&rep, s); err != nil {
			return rep, err
		}
	}
	x.labels(&rep, store.AuditGroup, x.in.Groups, x.groupsMade, x.groupsChanged, func(name string) SetupLabel {
		g := x.groups[name]
		return setupLabel(g.Name, g.Alert, g.LedColour, g.SortOrder)
	}, x.groupNames())
	x.labels(&rep, store.AuditTag, x.in.Tags, x.tagsMade, x.tagsChanged, func(name string) SetupLabel {
		t := x.tags[name]
		return setupLabel(t.Name, t.Alert, t.LedColour, t.SortOrder)
	}, x.tagNames())
	return rep, rep.plan()
}

func cmpInt[T int64 | int32](a, b T) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// settingsNotChanged lists the alert, LED colour and sort order a file
// gives a system, group or tag that exists, where they differ: the import
// doesn't change them.
func (x *importRun) settingsNotChanged(nc []store.FieldChange, f settingsFields, alert, led *string, sort int32) []store.FieldChange {
	if f.Alert.Set {
		nc = notChanged(nc, "alert", alert, f.Alert.Value)
	}
	if f.LEDColour.Set {
		nc = notChanged(nc, "led_colour", led, f.LEDColour.Value)
	}
	if f.SortOrder != nil {
		nc = notChanged(nc, "sort_order", sort, *f.SortOrder)
	}
	return nc
}

func (x *importRun) talkgroupLabel(sys int64, id int32, label string) string {
	out := fmt.Sprintf("%s %d", x.systemLabel(sys), id)
	if label != "" {
		out += " " + label
	}
	return out
}

// talkgroups imports a system's talkgroups, in ID order, and keeps those
// the file leaves out.
func (x *importRun) talkgroups(rep *ImportReport, s *importSystem) error {
	ctx, q := x.ctx, x.q
	rows := slices.Clone(s.Talkgroups)
	slices.SortFunc(rows, func(a, b importTalkgroup) int { return cmpInt(a.ID, b.ID) })
	inFile := map[int32]bool{}
	sysID := s.ID
	for i := range rows {
		r := &rows[i]
		k := tgKey{sysID, int32(r.ID)}
		inFile[k.id] = true
		res := ImportResult{Kind: store.AuditTalkgroup, System: &sysID, ID: strconv.FormatInt(r.ID, 10)}
		before, exists := x.tgs[k]
		if r.skip != "" {
			label := ""
			if r.Label != nil {
				label = *r.Label
			}
			res.Label, res.Result, res.Reason = x.talkgroupLabel(sysID, k.id, label), ImportSkipped, r.skip
			rep.add(res)
			continue
		}
		var tagID *int64
		if r.Tag != nil && (!exists || before.Tag == nil || *before.Tag != *r.Tag) {
			id, err := q.EnsureTag(ctx, *r.Tag)
			if err != nil {
				return failed("import tag", err)
			}
			tagID = &id
		}
		switch {
		case exists && !r.writes(before, x.update):
			res.Label, res.Result = x.talkgroupLabel(sysID, k.id, before.Label), ImportUnchanged
		case exists:
			p := db.ImportUpdateTalkgroupParams{SystemID: k.sys, TalkgroupID: k.id, Label: before.Label,
				Name: before.Name, TagID: tagID, SortOrder: before.SortOrder, FiregroundChannel: before.FiregroundChannel,
				DelayMinutes: before.DelayMinutes, Alert: before.Alert, LedColour: before.LEDColour, Type: before.Type,
				Frequency: before.Frequency}
			if x.update {
				if r.DelayMinutes != nil {
					p.DelayMinutes = *r.DelayMinutes
				}
				if r.Alert.Set {
					p.Alert = r.Alert.Value
				}
				if r.LEDColour.Set {
					p.LedColour = r.LEDColour.Value
				}
				if r.Type != nil {
					p.Type = *r.Type
				}
				if r.Frequency.Set {
					p.Frequency = r.Frequency.Value
				}
			}
			if r.Label != nil {
				p.Label = *r.Label
			}
			if r.Name != nil {
				p.Name = *r.Name
			}
			if r.SortOrder != nil {
				p.SortOrder = *r.SortOrder
			}
			if r.FiregroundChannel.Set {
				p.FiregroundChannel = r.fireground()
			}
			if err := q.ImportUpdateTalkgroup(ctx, p); err != nil {
				return failed("import talkgroup", err)
			}
			if r.Groups != nil && !sameSet(*r.Groups, before.Groups) {
				if err := setGroups(ctx, q, k.sys, k.id, *r.Groups); err != nil {
					return err
				}
			}
			res.Result = ImportChanged
		default:
			p := db.ImportTalkgroupParams{SystemID: k.sys, TalkgroupID: k.id, TagID: tagID, State: store.Public,
				Alert: r.Alert.Value, LedColour: r.LEDColour.Value, FiregroundChannel: r.fireground(),
				Frequency: r.Frequency.Value}
			if r.Label != nil {
				p.Label = *r.Label
			}
			if r.Name != nil {
				p.Name = *r.Name
			}
			if r.state != nil {
				p.State = *r.state
			}
			if r.Unreviewed != nil {
				p.Unreviewed = *r.Unreviewed
			}
			if r.DelayMinutes != nil {
				p.DelayMinutes = *r.DelayMinutes
			}
			if r.SortOrder != nil {
				p.SortOrder = *r.SortOrder
			}
			if r.Type != nil {
				p.Type = *r.Type
			}
			if err := q.ImportTalkgroup(ctx, p); err != nil {
				return failed("import talkgroup", err)
			}
			if r.Groups != nil && len(*r.Groups) > 0 {
				if err := setGroups(ctx, q, k.sys, k.id, *r.Groups); err != nil {
					return err
				}
			}
			res.Result = ImportNew
		}
		if res.Result != ImportUnchanged {
			now, err := q.GetTalkgroup(ctx, db.GetTalkgroupParams{SystemID: k.sys, TalkgroupID: k.id})
			if err != nil {
				return failed("import talkgroup", err)
			}
			after := talkgroupOf(now).audited()
			var was any
			if exists {
				was = before.audited()
			}
			ch, err := store.Changes(was, after)
			if err != nil {
				return err
			}
			res.Label, res.Changes = x.talkgroupLabel(sysID, k.id, after.Label), ch
			if err := x.au.Record(store.Change{Kind: store.AuditTalkgroup, Thing: talkgroupThing(k.sys, k.id),
				Label: res.Label, Before: was, After: after}); err != nil {
				return failed("import audit", err)
			}
		}
		if exists {
			// What the file says otherwise, which an import never changes on
			// a talkgroup that exists (owner, 2026-11-25): state above all.
			var nc []store.FieldChange
			if r.State != nil {
				nc = notChanged(nc, "state", before.State, *r.State)
			}
			if r.Unreviewed != nil {
				nc = notChanged(nc, "unreviewed", before.Unreviewed, *r.Unreviewed)
			}
			// With update, the rest were taken above.
			if x.update {
				res.NotChanged = nc
				rep.add(res)
				continue
			}
			if r.Type != nil {
				nc = notChanged(nc, "type", before.Type, *r.Type)
			}
			if r.Frequency.Set {
				nc = notChanged(nc, "frequency", before.Frequency, r.Frequency.Value)
			}
			if r.DelayMinutes != nil {
				nc = notChanged(nc, "delay_minutes", before.DelayMinutes, *r.DelayMinutes)
			}
			if r.Alert.Set {
				nc = notChanged(nc, "alert", before.Alert, r.Alert.Value)
			}
			if r.LEDColour.Set {
				nc = notChanged(nc, "led_colour", before.LEDColour, r.LEDColour.Value)
			}
			res.NotChanged = nc
		}
		rep.add(res)
	}
	var kept []Talkgroup
	for k, t := range x.tgs {
		if k.sys == sysID && !inFile[k.id] {
			kept = append(kept, t)
		}
	}
	slices.SortFunc(kept, func(a, b Talkgroup) int { return cmpInt(a.ID, b.ID) })
	for _, t := range kept {
		rep.add(ImportResult{Kind: store.AuditTalkgroup, System: &sysID, ID: strconv.Itoa(int(t.ID)),
			Label: x.talkgroupLabel(sysID, t.ID, t.Label), Result: ImportKept})
	}
	return nil
}

// unitRows imports a system's units, by radio ID, and keeps those the file
// leaves out.
func (x *importRun) unitRows(rep *ImportReport, s *importSystem) error {
	ctx, q := x.ctx, x.q
	rows := slices.Clone(s.Units)
	slices.SortFunc(rows, func(a, b importUnit) int {
		if c := cmpInt(a.From, b.From); c != 0 {
			return c
		}
		return cmpInt(*a.To, *b.To)
	})
	sysID := s.ID
	inFile := map[unitKey]bool{}
	label := func(from, to int64) string { return x.systemLabel(sysID) + " " + unitName(from, to) }
	for _, u := range rows {
		k := unitKey{sysID, u.From, *u.To}
		inFile[k] = true
		res := ImportResult{Kind: store.AuditUnit, System: &sysID, ID: unitName(k.from, k.to), Label: label(k.from, k.to)}
		was, exists := x.units[k]
		switch {
		case exists && was.Alias == *u.Alias:
			res.Result = ImportUnchanged
			rep.add(res)
			continue
		case exists:
			if _, err := q.PutUnit(ctx, db.PutUnitParams{SystemID: k.sys, FirstID: k.from, LastID: k.to, Alias: *u.Alias}); err != nil {
				return failed("import unit", err)
			}
			res.Result = ImportChanged
		default:
			if err := q.ImportUnit(ctx, db.ImportUnitParams{SystemID: k.sys, FirstID: k.from, LastID: k.to, Alias: *u.Alias}); err != nil {
				return failed("import unit", err)
			}
			res.Result = ImportNew
		}
		now, err := q.GetUnit(ctx, db.GetUnitParams{SystemID: k.sys, FirstID: k.from, LastID: k.to})
		if err != nil {
			return failed("import unit", err)
		}
		var before any
		if exists {
			before = unitOf(was).audited()
		}
		after := unitOf(now).audited()
		if res.Changes, err = store.Changes(before, after); err != nil {
			return err
		}
		if err := x.au.Record(store.Change{Kind: store.AuditUnit, Thing: unitThing(k.sys, k.from, k.to),
			Label: res.Label, Before: before, After: after}); err != nil {
			return failed("import audit", err)
		}
		rep.add(res)
	}
	var kept []unitKey
	for k := range x.units {
		if k.sys == sysID && !inFile[k] {
			kept = append(kept, k)
		}
	}
	slices.SortFunc(kept, func(a, b unitKey) int {
		if c := cmpInt(a.from, b.from); c != 0 {
			return c
		}
		return cmpInt(a.to, b.to)
	})
	for _, k := range kept {
		rep.add(ImportResult{Kind: store.AuditUnit, System: &sysID, ID: unitName(k.from, k.to),
			Label: label(k.from, k.to), Result: ImportKept})
	}
	return nil
}

func (x *importRun) groupNames() []string {
	var out []string
	for n := range x.groups {
		out = append(out, n)
	}
	return out
}

func (x *importRun) tagNames() []string {
	var out []string
	for n := range x.tags {
		out = append(out, n)
	}
	return out
}

// labels reports the file's groups or tags (new; changed, with update;
// else unchanged, since an import without update doesn't change one that
// exists) and keeps the rest, by name.
func (x *importRun) labels(rep *ImportReport, kind string, rows []importLabel, made map[string]bool,
	changed map[string][]store.FieldChange, get func(string) SetupLabel, all []string) {
	rows = slices.Clone(rows)
	slices.SortFunc(rows, func(a, b importLabel) int { return strings.Compare(a.Name, b.Name) })
	inFile := map[string]bool{}
	for _, l := range rows {
		inFile[l.Name] = true
		now := get(l.Name)
		res := ImportResult{Kind: kind, ID: l.Name, Label: l.Name}
		if made[l.Name] {
			res.Result = ImportNew
			res.Changes, _ = store.Changes(nil, now)
		} else if x.update {
			res.Result = ImportUnchanged
			if ch, ok := changed[l.Name]; ok {
				res.Result, res.Changes = ImportChanged, ch
			}
		} else {
			res.Result = ImportUnchanged
			res.NotChanged = x.settingsNotChanged(nil, l.settingsFields, now.Alert, now.LEDColour, now.SortOrder)
		}
		rep.add(res)
	}
	// Kept: in the deployment before the import and not in the file. all
	// holds those and the file's new ones; a group or tag made by naming it
	// on a talkgroup isn't in it, and is in that talkgroup's changes.
	slices.Sort(all)
	for _, n := range all {
		if !inFile[n] && !made[n] {
			rep.add(ImportResult{Kind: kind, ID: n, Label: n, Result: ImportKept})
		}
	}
}

// errDryRun rolls back a dry run, and errImportChanged an import whose
// results aren't the plan's, with their entries.
var (
	errDryRun        = errors.New("import: a dry run")
	errImportChanged = errors.New("import: changed since the plan")
)

// importSetup is POST /api/v1/admin/import: {"setup": {...}, "dry_run":
// true} answers the results and the plan, changing nothing; {"setup":
// {...}, "plan": "..."} applies it, if the results are still the plan's.
func (a *Admin) importSetup(r *http.Request, who string) (int, any, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxImportBytes+1))
	if err != nil {
		return 0, nil, refuseAdmin(http.StatusBadRequest, "invalid_body", "The request body couldn't be read.")
	}
	if len(body) > MaxImportBytes {
		return 0, nil, refuseAdmin(http.StatusRequestEntityTooLarge, "body_too_large",
			"An import is at most %d MB; this one is larger.", MaxImportBytes>>20)
	}
	var req importRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return 0, nil, refuseAdmin(http.StatusBadRequest, "invalid_body", "The request body isn't valid: %v.", err)
	}
	if req.Setup == nil {
		return 0, nil, refuseAdmin(http.StatusBadRequest, "setup_required", `Send the setup to import as "setup".`)
	}
	if !req.DryRun && req.Plan == nil {
		return 0, nil, refuseAdmin(http.StatusBadRequest, "plan_required",
			`Preview first: send the setup with "dry_run": true, then again with the plan it answers.`)
	}
	if err := req.Setup.check(); err != nil {
		return 0, nil, err
	}
	rep, err := a.runImport(r, req.Setup, req.Update, req.DryRun, req.Plan)
	if code, _ := pgCode(err); code == "23505" || errors.Is(err, errImportChanged) {
		// Something changed since the plan, or while importing: nothing is
		// applied, and the answer is the results as they are now.
		if !errors.Is(err, errImportChanged) {
			if rep, err = a.runImport(r, req.Setup, req.Update, true, nil); !errors.Is(err, errDryRun) {
				return 0, nil, err
			}
		}
		rep.Error, rep.Message = "import_changed",
			"The setup has changed since the preview, so nothing was imported. These are the results now: preview them, then import again."
		a.log.Info("admin's import changed since its preview", "admin", who)
		return http.StatusConflict, rep, nil
	}
	if errors.Is(err, errDryRun) {
		err = nil
	}
	if err != nil {
		return 0, nil, err
	}
	s := rep.Summary
	a.log.Info("admin imported the setup", "admin", who, "dry_run", rep.DryRun, "applied", rep.Applied, "update", rep.Update,
		"talkgroups_new", s.Talkgroups.New, "talkgroups_changed", s.Talkgroups.Changed,
		"talkgroups_skipped", s.Talkgroups.Skipped, "units_new", s.Units.New, "units_changed", s.Units.Changed,
		"systems_new", s.Systems.New)
	return http.StatusOK, rep, nil
}

// runImport runs the import in one audited transaction, as the signed-in
// admin, how: import. A dry run, or a plan that isn't its results, rolls
// it back with its entries (errDryRun, errImportChanged).
func (a *Admin) runImport(r *http.Request, setup *importSetup, update, dryRun bool, plan *string) (ImportReport, error) {
	by, _ := AdminFrom(r.Context())
	actor := by.Actor()
	actor.Via = store.ViaImport
	var rep ImportReport
	err := a.st.Audited(r.Context(), actor, func(q *db.Queries, au *store.Audit) error {
		var err error
		x := &importRun{ctx: r.Context(), q: q, au: au, in: setup, update: update}
		if rep, err = x.run(); err != nil {
			return err
		}
		rep.DryRun = dryRun
		switch {
		case dryRun:
			return errDryRun
		case rep.Plan != *plan:
			return errImportChanged
		}
		rep.Applied = true
		return nil
	})
	return rep, err
}
