package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/RIKIKU/RadioArchive/internal/gate"
	"github.com/RIKIKU/RadioArchive/internal/store"
	"github.com/RIKIKU/RadioArchive/internal/store/db"
)

// Groups, tags, and the settings talkgroups take from them (SPEC.md R22;
// ADR 0006).

// optional is a field a PATCH may leave out, set, or clear with null.
type optional[T any] struct {
	Set   bool // given, as a value or null
	Value *T   // nil when null or not given
}

func (o *optional[T]) UnmarshalJSON(b []byte) error {
	o.Set = true
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		o.Value = nil
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	o.Value = &v
	return nil
}

// firegroundNumber is a fireground channel number (R100): a whole number,
// given as a JSON number or a string of digits, where leading zeros don't
// count ("041" is 41).
type firegroundNumber int32

func (n *firegroundNumber) UnmarshalJSON(b []byte) error {
	s := string(bytes.TrimSpace(b))
	if strings.HasPrefix(s, `"`) {
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
	}
	v, ok := gate.ParseFireground(s)
	if !ok {
		return errors.New("fireground_channel must be a whole number")
	}
	*n = firegroundNumber(v)
	return nil
}

// settingsFields are the alert, LED colour and sort order a talkgroup, tag,
// group or system may have of its own.
type settingsFields struct {
	Alert     optional[string] `json:"alert"`
	LEDColour optional[string] `json:"led_colour"`
	SortOrder *int32           `json:"sort_order"`
}

func (f settingsFields) check() error {
	return checkSettings(f.Alert.Value, f.LEDColour.Value)
}

// checkSettings checks an alert's and an LED colour's names, either of which
// may be unset.
func checkSettings(alert, led *string) error {
	if alert != nil && !store.IsAlert(*alert) {
		return refuseAdmin(http.StatusBadRequest, "invalid_alert", "alert must be one of %s, or null.",
			strings.Join(store.Alerts, ", "))
	}
	if led != nil && !store.IsLEDColour(*led) {
		return refuseAdmin(http.StatusBadRequest, "invalid_led_colour", "led_colour must be one of %s, or null.",
			strings.Join(store.LEDColours, ", "))
	}
	return nil
}

// Label is a group or a tag as the admin API shows it: its name and its own
// settings.
type Label struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	Alert     *string `json:"alert"`
	LEDColour *string `json:"led_colour"`
	SortOrder int32   `json:"sort_order"`
	// AlwaysShow is a tag's only: its talkgroups are never quiet (R115;
	// ADR 0054). Absent for a group.
	AlwaysShow *bool `json:"always_show,omitempty"`
}

func labelOf(id int64, name string, alert, led *string, sort int32) Label {
	return Label{ID: id, Name: name, Alert: alert, LEDColour: led, SortOrder: sort}
}

func groupLabel(g db.Group) Label { return labelOf(g.ID, g.Name, g.Alert, g.LedColour, g.SortOrder) }

// tagLabel is a tag, always show included.
func tagLabel(t db.Tag) Label {
	l := labelOf(t.ID, t.Name, t.Alert, t.LedColour, t.SortOrder)
	show := t.AlwaysShow
	l.AlwaysShow = &show
	return l
}

// tagFields are a tag's editable fields: a label's, and always show.
type tagFields struct {
	labelFields
	AlwaysShow *bool `json:"always_show"`
}

// labelFields are a group's or a tag's editable fields.
type labelFields struct {
	Name *string `json:"name"`
	settingsFields
}

func (f labelFields) check(kind string, required bool) error {
	if f.Name == nil && required {
		return refuseAdmin(http.StatusBadRequest, "name_required", "A %s needs a name.", kind)
	}
	if f.Name != nil {
		if err := text("name", *f.Name, 1, 100); err != nil {
			return err
		}
	}
	return f.settingsFields.check()
}

func nameTaken(err error, kind string, name *string) error {
	if code, _ := pgCode(err); code == "23505" && name != nil {
		return refuseAdmin(http.StatusConflict, kind+"_exists", "A %s named %q already exists.", kind, *name)
	}
	return dbErr(kind, err)
}

func (a *Admin) listGroups(r *http.Request, _ string) (int, any, error) {
	rows, err := a.st.ListGroups(r.Context())
	if err != nil {
		return 0, nil, dbErr("list groups", err)
	}
	out := make([]Label, 0, len(rows))
	for _, g := range rows {
		out = append(out, labelOf(g.ID, g.Name, g.Alert, g.LedColour, g.SortOrder))
	}
	return http.StatusOK, out, nil
}

func (a *Admin) createGroup(r *http.Request, who string) (int, any, error) {
	var in labelFields
	if err := decode(r, &in); err != nil {
		return 0, nil, err
	}
	if err := in.check("group", true); err != nil {
		return 0, nil, err
	}
	p := db.CreateGroupParams{Name: *in.Name, Alert: in.Alert.Value, LedColour: in.LEDColour.Value}
	if in.SortOrder != nil {
		p.SortOrder = *in.SortOrder
	}
	var out Label
	err := a.audited(r, func(q *db.Queries, au *store.Audit) error {
		g, err := q.CreateGroup(r.Context(), p)
		if err != nil {
			return nameTaken(err, "group", in.Name)
		}
		out = labelOf(g.ID, g.Name, g.Alert, g.LedColour, g.SortOrder)
		return au.Record(store.Change{Kind: store.AuditGroup, Thing: strconv.FormatInt(g.ID, 10), Label: g.Name, After: out})
	})
	if err != nil {
		return 0, nil, err
	}
	a.log.Info("admin created a group", "admin", who, "group", out.ID)
	return http.StatusCreated, out, nil
}

// patchLabel changes a group or a tag (kind), as get reads it and update
// writes it, audited.
func (a *Admin) patchLabel(r *http.Request, kind string, id int64, get func(*db.Queries) (Label, error),
	update func(*db.Queries) (Label, error), name *string) (Label, error) {
	var out Label
	thing := strconv.FormatInt(id, 10)
	notFound := refuseAdmin(http.StatusNotFound, kind+"_not_found", "No %s has ID %d.", kind, id)
	err := a.audited(r, func(q *db.Queries, au *store.Audit) error {
		if err := au.Lock(kind, thing); err != nil {
			return dbErr(kind, err)
		}
		was, err := get(q)
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound
		}
		if err != nil {
			return dbErr(kind, err)
		}
		out, err = update(q)
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound
		}
		if err != nil {
			return nameTaken(err, kind, name)
		}
		return au.Record(store.Change{Kind: kind, Thing: thing, Label: out.Name, Before: was, After: out})
	})
	return out, err
}

func (a *Admin) patchGroup(r *http.Request, who string) (int, any, error) {
	id, err := pathInt(r, "group", math.MaxInt64)
	if err != nil {
		return 0, nil, err
	}
	var in labelFields
	if err := decode(r, &in); err != nil {
		return 0, nil, err
	}
	if err := in.check("group", false); err != nil {
		return 0, nil, err
	}
	ctx := r.Context()
	out, err := a.patchLabel(r, store.AuditGroup, id,
		func(q *db.Queries) (Label, error) {
			g, err := q.GetGroup(ctx, id)
			return groupLabel(g), err
		},
		func(q *db.Queries) (Label, error) {
			g, err := q.UpdateGroup(ctx, db.UpdateGroupParams{ID: id, Name: in.Name, SortOrder: in.SortOrder,
				SetAlert: in.Alert.Set, Alert: in.Alert.Value, SetLedColour: in.LEDColour.Set, LedColour: in.LEDColour.Value})
			return groupLabel(g), err
		}, in.Name)
	if err != nil {
		return 0, nil, err
	}
	a.log.Info("admin changed a group", "admin", who, "group", id)
	return http.StatusOK, out, nil
}

func (a *Admin) listTags(r *http.Request, _ string) (int, any, error) {
	rows, err := a.st.ListTags(r.Context())
	if err != nil {
		return 0, nil, dbErr("list tags", err)
	}
	out := make([]Label, 0, len(rows))
	for _, t := range rows {
		out = append(out, tagLabel(t))
	}
	return http.StatusOK, out, nil
}

func (a *Admin) patchTag(r *http.Request, who string) (int, any, error) {
	id, err := pathInt(r, "tag", math.MaxInt64)
	if err != nil {
		return 0, nil, err
	}
	var in tagFields
	if err := decode(r, &in); err != nil {
		return 0, nil, err
	}
	if err := in.check("tag", false); err != nil {
		return 0, nil, err
	}
	ctx := r.Context()
	out, err := a.patchLabel(r, store.AuditTag, id,
		func(q *db.Queries) (Label, error) {
			t, err := q.GetTag(ctx, id)
			return tagLabel(t), err
		},
		func(q *db.Queries) (Label, error) {
			t, err := q.UpdateTag(ctx, db.UpdateTagParams{ID: id, Name: in.Name, SortOrder: in.SortOrder,
				SetAlert: in.Alert.Set, Alert: in.Alert.Value, SetLedColour: in.LEDColour.Set, LedColour: in.LEDColour.Value,
				AlwaysShow: in.AlwaysShow})
			return tagLabel(t), err
		}, in.Name)
	if err != nil {
		return 0, nil, err
	}
	a.log.Info("admin changed a tag", "admin", who, "tag", id)
	return http.StatusOK, out, nil
}
