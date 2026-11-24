package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"net/http"
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
}

func labelOf(id int64, name string, alert, led *string, sort int32) Label {
	return Label{ID: id, Name: name, Alert: alert, LEDColour: led, SortOrder: sort}
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
	g, err := a.st.CreateGroup(r.Context(), p)
	if err != nil {
		return 0, nil, nameTaken(err, "group", in.Name)
	}
	a.log.Info("admin created a group", "admin", who, "group", g.ID)
	return http.StatusCreated, labelOf(g.ID, g.Name, g.Alert, g.LedColour, g.SortOrder), nil
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
	g, err := a.st.UpdateGroup(r.Context(), db.UpdateGroupParams{ID: id, Name: in.Name, SortOrder: in.SortOrder,
		SetAlert: in.Alert.Set, Alert: in.Alert.Value, SetLedColour: in.LEDColour.Set, LedColour: in.LEDColour.Value})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil, refuseAdmin(http.StatusNotFound, "group_not_found", "No group has ID %d.", id)
	}
	if err != nil {
		return 0, nil, nameTaken(err, "group", in.Name)
	}
	a.log.Info("admin changed a group", "admin", who, "group", id)
	return http.StatusOK, labelOf(g.ID, g.Name, g.Alert, g.LedColour, g.SortOrder), nil
}

func (a *Admin) listTags(r *http.Request, _ string) (int, any, error) {
	rows, err := a.st.ListTags(r.Context())
	if err != nil {
		return 0, nil, dbErr("list tags", err)
	}
	out := make([]Label, 0, len(rows))
	for _, t := range rows {
		out = append(out, labelOf(t.ID, t.Name, t.Alert, t.LedColour, t.SortOrder))
	}
	return http.StatusOK, out, nil
}

func (a *Admin) patchTag(r *http.Request, who string) (int, any, error) {
	id, err := pathInt(r, "tag", math.MaxInt64)
	if err != nil {
		return 0, nil, err
	}
	var in labelFields
	if err := decode(r, &in); err != nil {
		return 0, nil, err
	}
	if err := in.check("tag", false); err != nil {
		return 0, nil, err
	}
	t, err := a.st.UpdateTag(r.Context(), db.UpdateTagParams{ID: id, Name: in.Name, SortOrder: in.SortOrder,
		SetAlert: in.Alert.Set, Alert: in.Alert.Value, SetLedColour: in.LEDColour.Set, LedColour: in.LEDColour.Value})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil, refuseAdmin(http.StatusNotFound, "tag_not_found", "No tag has ID %d.", id)
	}
	if err != nil {
		return 0, nil, nameTaken(err, "tag", in.Name)
	}
	a.log.Info("admin changed a tag", "admin", who, "tag", id)
	return http.StatusOK, labelOf(t.ID, t.Name, t.Alert, t.LedColour, t.SortOrder), nil
}
