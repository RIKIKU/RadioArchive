package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/RIKIKU/RadioArchive/internal/recordings"
	"github.com/RIKIKU/RadioArchive/internal/retention"
	"github.com/RIKIKU/RadioArchive/internal/store"
	"github.com/RIKIKU/RadioArchive/internal/store/db"
)

// AdminPath prefixes the admin API (SPEC.md R51).
const AdminPath = "/api/v1/admin"

// AdminAuthenticator says which admin made a request (R51, R55, R111).
//
// Admin sign-in is Authentik's (OIDC), which arrives with the web admin
// (M4). Until then this repo has no implementation outside _test.go files,
// which no binary contains, and cmd/radioarchive mounts no admin API: there
// is nothing to switch on in production by accident.
type AdminAuthenticator interface {
	// Admin returns the signed-in admin's name, or false if the request
	// isn't from one.
	Admin(r *http.Request) (name string, ok bool)
}

// Admin serves the admin API's systems, attachments, talkgroups, rejected
// calls and recordings (R11, R12, R17, R18, R21, R22, R34), and, given
// the recorder endpoints, the recorders' state and signals (R14).
type Admin struct {
	st   *store.Store
	log  *slog.Logger
	auth AdminAuthenticator
	dir  string
	ret  *retention.Job
	rs   *Recorders
}

// WithRetention serves the retention setting and the recordings volume's
// state (R88) from the retention job too.
func (a *Admin) WithRetention(j *retention.Job) *Admin {
	a.ret = j
	return a
}

// NewAdmin returns the admin API, with the recordings volume at
// recordingsDir. It refuses to exist without an authenticator.
func NewAdmin(st *store.Store, log *slog.Logger, auth AdminAuthenticator, recordingsDir string) (*Admin, error) {
	if auth == nil {
		return nil, errors.New("the admin API needs an admin authenticator")
	}
	return &Admin{st: st, log: log, auth: auth, dir: recordingsDir}, nil
}

// WithAdmin serves the admin API.
func WithAdmin(a *Admin) Option {
	return func(api *API) {
		api.handle(AdminRoute, "GET "+AdminPath+"/calls/{call}/recording", a.signedIn(a.recording))
		for _, r := range []struct {
			pattern string
			h       func(*http.Request, string) (int, any, error)
		}{
			{"GET /systems", a.listSystems},
			{"POST /systems", a.createSystem},
			{"GET /systems/{system}", a.getSystem},
			{"PATCH /systems/{system}", a.patchSystem},
			{"DELETE /systems/{system}", a.deleteSystem},
			{"GET /systems/{system}/talkgroups", a.listTalkgroups},
			{"GET /systems/{system}/talkgroups/{talkgroup}", a.getTalkgroup},
			{"PUT /systems/{system}/talkgroups/{talkgroup}", a.putTalkgroup},
			{"PATCH /systems/{system}/talkgroups/{talkgroup}", a.patchTalkgroup},
			{"GET /systems/{system}/units", a.listUnits},
			{"GET /systems/{system}/units/{unit}", a.getUnit},
			{"PUT /systems/{system}/units/{unit}", a.putUnit},
			{"DELETE /systems/{system}/units/{unit}", a.deleteUnit},
			{"GET /attachments", a.listAttachments},
			{"PUT /recorders/{recorder}/attachments/{short_name}", a.attach},
			{"DELETE /recorders/{recorder}/attachments/{short_name}", a.detach},
			{"GET /rejections", a.rejections},
			{"GET /groups", a.listGroups},
			{"POST /groups", a.createGroup},
			{"PATCH /groups/{group}", a.patchGroup},
			{"GET /tags", a.listTags},
			{"PATCH /tags/{tag}", a.patchTag},
		} {
			method, path, _ := strings.Cut(r.pattern, " ")
			api.handle(AdminRoute, method+" "+AdminPath+path, a.serve(r.h))
		}
		if a.ret != nil {
			api.handle(AdminRoute, "GET "+AdminPath+"/retention", a.serve(a.getRetention))
			api.handle(AdminRoute, "PUT "+AdminPath+"/retention", a.serve(a.putRetention))
		}
		if a.rs != nil {
			api.handle(AdminRoute, "GET "+AdminPath+"/recorders", a.serve(a.listRecorders))
			api.handle(AdminRoute, "GET "+AdminPath+"/recorders/{recorder}", a.serve(a.getRecorder))
			api.handle(AdminRoute, "PATCH "+AdminPath+"/recorders/{recorder}", a.serve(a.patchRecorder))
		}
	}
}

// signedIn lets only an admin's requests through to h.
func (a *Admin) signedIn(h func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		who, ok := a.auth.Admin(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, adminError{Code: "admin_sign_in_required", Message: "Sign in as an admin."})
			return
		}
		h(w, r, who)
	}
}

// recording plays any stored call's recording, hidden and ignored ones too,
// with range requests (R34). It's not audited, and not cached: an admin's
// browser may be shared with listeners.
func (a *Admin) recording(w http.ResponseWriter, r *http.Request, _ string) {
	notFound := func() {
		writeJSON(w, http.StatusNotFound, adminError{Code: "recording_not_found", Message: "No recording is stored for that call."})
	}
	var id pgtype.UUID
	if err := id.Scan(r.PathValue("call")); err != nil {
		notFound()
		return
	}
	row, err := a.st.AdminRecording(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		notFound()
		return
	}
	if err != nil {
		a.log.Error("admin recording failed", "error", store.SafeError(err))
		writeJSON(w, http.StatusInternalServerError, adminError{Code: "internal", Message: "Something went wrong."})
		return
	}
	f, err := recordings.Open(a.dir, row.RecordingPath, row.RecordingAt.Time)
	if errors.Is(err, recordings.ErrMissing) {
		a.log.Warn("a call's recording file is missing", "call", uuidString(id))
		notFound()
		return
	}
	if err != nil {
		a.log.Error("admin recording failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, adminError{Code: "internal", Message: "Something went wrong."})
		return
	}
	defer f.Close()
	f.Serve(w, r, "no-store")
}

// adminError is a machine-readable refusal's body (R78): a stable code and
// a message.
type adminError struct {
	Code    string `json:"error"`
	Message string `json:"message"`
}

// adminRefusal is a handler's refusal: its status and body.
type adminRefusal struct {
	status int
	body   adminError
}

func (e *adminRefusal) Error() string { return e.body.Code + ": " + e.body.Message }

func refuseAdmin(status int, code, format string, a ...any) error {
	return &adminRefusal{status: status, body: adminError{Code: code, Message: fmt.Sprintf(format, a...)}}
}

func (a *Admin) serve(h func(*http.Request, string) (int, any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		who, ok := a.auth.Admin(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, adminError{Code: "admin_sign_in_required", Message: "Sign in as an admin."})
			return
		}
		code, body, err := h(r, who)
		if err != nil {
			var v *adminRefusal
			if errors.As(err, &v) {
				writeJSON(w, v.status, v.body)
				return
			}
			a.log.Error("admin request failed", "method", r.Method, "pattern", r.Pattern, "error", err)
			writeJSON(w, http.StatusInternalServerError, adminError{Code: "internal", Message: "Something went wrong."})
			return
		}
		if body == nil {
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(code)
			return
		}
		writeJSON(w, code, body)
	}
}

// decode reads a JSON body, refusing unknown fields.
func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return refuseAdmin(http.StatusBadRequest, "invalid_body", "The request body isn't valid: %v.", err)
	}
	return nil
}

func pathInt(r *http.Request, name string, max int64) (int64, error) {
	n, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || n < 1 || n > max {
		return 0, refuseAdmin(http.StatusNotFound, name+"_not_found", "No such %s.", name)
	}
	return n, nil
}

func ts(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	u := t.Time.UTC()
	return &u
}

// text checks a label or name: no leading or trailing space, within max
// bytes, and at least min.
func text(field, v string, min, max int) error {
	if strings.TrimSpace(v) != v || len(v) < min || len(v) > max {
		return refuseAdmin(http.StatusBadRequest, "invalid_"+field,
			"%s must be %d-%d characters with no leading or trailing space.", field, min, max)
	}
	return nil
}

func pgCode(err error) (code, constraint string) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code, pgErr.ConstraintName
	}
	return "", ""
}

// Systems (R11).

// System is a system as the admin API shows it.
type System struct {
	ID         int64      `json:"id"`
	Label      string     `json:"label"`
	Name       string     `json:"name"`
	AutoCreate bool       `json:"auto_create"`
	Alert      *string    `json:"alert"`
	LEDColour  *string    `json:"led_colour"`
	SortOrder  int32      `json:"sort_order"`
	CreatedAt  *time.Time `json:"created_at,omitempty"`
	UpdatedAt  *time.Time `json:"updated_at,omitempty"`
}

func systemOf(r db.GetSystemRow) System {
	return System{ID: r.ID, Label: r.Label, Name: r.Name, AutoCreate: r.AutoCreate,
		Alert: r.Alert, LEDColour: r.LedColour, SortOrder: r.SortOrder,
		CreatedAt: ts(r.CreatedAt), UpdatedAt: ts(r.UpdatedAt)}
}

func (a *Admin) listSystems(r *http.Request, _ string) (int, any, error) {
	rows, err := a.st.ListSystems(r.Context())
	if err != nil {
		return 0, nil, dbErr("list systems", err)
	}
	out := make([]System, 0, len(rows))
	for _, s := range rows {
		out = append(out, systemOf(db.GetSystemRow(s)))
	}
	return http.StatusOK, out, nil
}

func (a *Admin) system(ctx context.Context, id int64) (System, error) {
	s, err := a.st.GetSystem(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return System{}, refuseAdmin(http.StatusNotFound, "system_not_found", "No system has ID %d.", id)
	}
	if err != nil {
		return System{}, dbErr("system", err)
	}
	return systemOf(s), nil
}

func (a *Admin) getSystem(r *http.Request, _ string) (int, any, error) {
	id, err := pathInt(r, "system", math.MaxInt64)
	if err != nil {
		return 0, nil, err
	}
	s, err := a.system(r.Context(), id)
	return http.StatusOK, s, err
}

func systemConflict(err error, id int64, label string) error {
	switch code, constraint := pgCode(err); {
	case code == "23505" && constraint == "systems_pkey":
		return refuseAdmin(http.StatusConflict, "system_exists", "A system with ID %d already exists.", id)
	case code == "23505":
		return refuseAdmin(http.StatusConflict, "label_taken", "Another system is labelled %q.", label)
	}
	return dbErr("system", err)
}

func (a *Admin) createSystem(r *http.Request, who string) (int, any, error) {
	var in System
	if err := decode(r, &in); err != nil {
		return 0, nil, err
	}
	if in.ID < 1 {
		return 0, nil, refuseAdmin(http.StatusBadRequest, "invalid_id", "id must be a whole number from 1.")
	}
	if err := text("label", in.Label, 1, 100); err != nil {
		return 0, nil, err
	}
	if err := text("name", in.Name, 0, 200); err != nil {
		return 0, nil, err
	}
	if err := checkSettings(in.Alert, in.LEDColour); err != nil {
		return 0, nil, err
	}
	s, err := a.st.CreateSystem(r.Context(), db.CreateSystemParams{ID: in.ID, Label: in.Label, Name: in.Name,
		AutoCreate: in.AutoCreate, Alert: in.Alert, LedColour: in.LEDColour, SortOrder: in.SortOrder})
	if err != nil {
		return 0, nil, systemConflict(err, in.ID, in.Label)
	}
	a.log.Info("admin created a system", "admin", who, "system", s.ID, "auto_create", s.AutoCreate)
	return http.StatusCreated, systemOf(db.GetSystemRow(s)), nil
}

func (a *Admin) patchSystem(r *http.Request, who string) (int, any, error) {
	id, err := pathInt(r, "system", math.MaxInt64)
	if err != nil {
		return 0, nil, err
	}
	var in struct {
		Label      *string `json:"label"`
		Name       *string `json:"name"`
		AutoCreate *bool   `json:"auto_create"`
		settingsFields
	}
	if err := decode(r, &in); err != nil {
		return 0, nil, err
	}
	if in.Label != nil {
		if err := text("label", *in.Label, 1, 100); err != nil {
			return 0, nil, err
		}
	}
	if in.Name != nil {
		if err := text("name", *in.Name, 0, 200); err != nil {
			return 0, nil, err
		}
	}
	if err := in.check(); err != nil {
		return 0, nil, err
	}
	s, err := a.st.UpdateSystem(r.Context(), db.UpdateSystemParams{ID: id, Label: in.Label, Name: in.Name,
		AutoCreate: in.AutoCreate, SortOrder: in.SortOrder, SetAlert: in.Alert.Set, Alert: in.Alert.Value,
		SetLedColour: in.LEDColour.Set, LedColour: in.LEDColour.Value})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil, refuseAdmin(http.StatusNotFound, "system_not_found", "No system has ID %d.", id)
	}
	if err != nil {
		label := ""
		if in.Label != nil {
			label = *in.Label
		}
		return 0, nil, systemConflict(err, id, label)
	}
	a.log.Info("admin changed a system", "admin", who, "system", id, "auto_create", s.AutoCreate)
	return http.StatusOK, systemOf(db.GetSystemRow(s)), nil
}

func (a *Admin) deleteSystem(r *http.Request, who string) (int, any, error) {
	id, err := pathInt(r, "system", math.MaxInt64)
	if err != nil {
		return 0, nil, err
	}
	ctx := r.Context()
	err = a.st.InTx(ctx, func(_ pgx.Tx, q *db.Queries) error {
		s, err := q.GetSystem(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return refuseAdmin(http.StatusNotFound, "system_not_found", "No system has ID %d.", id)
		}
		if err != nil {
			return dbErr("system", err)
		}
		use, err := q.SystemInUse(ctx, id)
		if err != nil {
			return dbErr("system", err)
		}
		if use.Talkgroups || use.Calls {
			return refuseAdmin(http.StatusConflict, "system_in_use",
				"System %s still has talkgroups or calls, so it can't be deleted.", s.Label)
		}
		if _, err := q.DeleteSystem(ctx, id); err != nil {
			if code, _ := pgCode(err); code == "23503" {
				// A call arrived meanwhile.
				return refuseAdmin(http.StatusConflict, "system_in_use",
					"System %s still has talkgroups or calls, so it can't be deleted.", s.Label)
			}
			return dbErr("delete system", err)
		}
		return nil
	})
	if err != nil {
		return 0, nil, err
	}
	a.log.Info("admin deleted a system", "admin", who, "system", id)
	return http.StatusNoContent, nil, nil
}

// Talkgroups (R21-R23, R100).

// Talkgroup is a talkgroup as the admin API shows it. Alert and LEDColour
// are its own: null takes them from its tag, groups or system (ADR 0006).
type Talkgroup struct {
	SystemID          int64      `json:"system_id"`
	ID                int32      `json:"id"`
	Label             string     `json:"label"`
	Name              string     `json:"name"`
	Tag               *string    `json:"tag"`
	Groups            []string   `json:"groups"`
	State             string     `json:"state"`
	Unreviewed        bool       `json:"unreviewed"`
	DelayMinutes      int32      `json:"delay_minutes"`
	Alert             *string    `json:"alert"`
	LEDColour         *string    `json:"led_colour"`
	SortOrder         int32      `json:"sort_order"`
	FiregroundChannel *int32     `json:"fireground_channel"`
	CreatedAt         *time.Time `json:"created_at,omitempty"`
	UpdatedAt         *time.Time `json:"updated_at,omitempty"`
}

func talkgroupOf(t db.GetTalkgroupRow) Talkgroup {
	groups := t.Groups
	if groups == nil {
		groups = []string{}
	}
	return Talkgroup{SystemID: t.SystemID, ID: t.TalkgroupID, Label: t.Label, Name: t.Name, Tag: t.Tag,
		Groups: groups, State: string(t.State), Unreviewed: t.Unreviewed, DelayMinutes: t.DelayMinutes,
		Alert: t.Alert, LEDColour: t.LedColour, SortOrder: t.SortOrder, FiregroundChannel: t.FiregroundChannel,
		CreatedAt: ts(t.CreatedAt), UpdatedAt: ts(t.UpdatedAt)}
}

func (a *Admin) listTalkgroups(r *http.Request, _ string) (int, any, error) {
	id, err := pathInt(r, "system", math.MaxInt64)
	if err != nil {
		return 0, nil, err
	}
	if _, err := a.system(r.Context(), id); err != nil {
		return 0, nil, err
	}
	rows, err := a.st.ListTalkgroups(r.Context(), id)
	if err != nil {
		return 0, nil, dbErr("list talkgroups", err)
	}
	out := make([]Talkgroup, 0, len(rows))
	for _, t := range rows {
		out = append(out, talkgroupOf(db.GetTalkgroupRow(t)))
	}
	return http.StatusOK, out, nil
}

func talkgroupPath(r *http.Request) (int64, int32, error) {
	sys, err := pathInt(r, "system", math.MaxInt64)
	if err != nil {
		return 0, 0, err
	}
	tg, err := pathInt(r, "talkgroup", math.MaxInt32)
	if err != nil {
		return 0, 0, err
	}
	return sys, int32(tg), nil
}

func (a *Admin) talkgroup(ctx context.Context, sys int64, tg int32) (Talkgroup, error) {
	t, err := a.st.GetTalkgroup(ctx, db.GetTalkgroupParams{SystemID: sys, TalkgroupID: tg})
	if errors.Is(err, pgx.ErrNoRows) {
		return Talkgroup{}, refuseAdmin(http.StatusNotFound, "talkgroup_not_found", "System %d has no talkgroup %d.", sys, tg)
	}
	if err != nil {
		return Talkgroup{}, dbErr("talkgroup", err)
	}
	return talkgroupOf(t), nil
}

func (a *Admin) getTalkgroup(r *http.Request, _ string) (int, any, error) {
	sys, tg, err := talkgroupPath(r)
	if err != nil {
		return 0, nil, err
	}
	t, err := a.talkgroup(r.Context(), sys, tg)
	return http.StatusOK, t, err
}

func parseState(s string) (db.TalkgroupState, error) {
	switch st := db.TalkgroupState(s); st {
	case store.Public, store.Hidden, store.Ignored:
		return st, nil
	}
	return "", refuseAdmin(http.StatusBadRequest, "invalid_state", "state must be public, hidden or ignored.")
}

// maxGroups is the most groups one talkgroup may be in.
const maxGroups = 50

// talkgroupFields are a talkgroup's editable fields. In a PATCH, absent is
// "unchanged"; null clears alert, LED colour and fireground channel number,
// and groups: [] takes the talkgroup out of every group. In a PUT, absent is
// the default: no groups, no delay, sort order 0, none of its own alert or
// LED colour, and no fireground channel number.
type talkgroupFields struct {
	Label             *string                    `json:"label"`
	Name              *string                    `json:"name"`
	Tag               *string                    `json:"tag"`
	State             *string                    `json:"state"`
	Groups            *[]string                  `json:"groups"`
	DelayMinutes      *int32                     `json:"delay_minutes"`
	FiregroundChannel optional[firegroundNumber] `json:"fireground_channel"`
	settingsFields
}

func (f *talkgroupFields) check() (*db.TalkgroupState, error) {
	if f.Label != nil {
		if err := text("label", *f.Label, 0, 100); err != nil {
			return nil, err
		}
	}
	if f.Name != nil {
		if err := text("name", *f.Name, 0, 200); err != nil {
			return nil, err
		}
	}
	if f.Tag != nil {
		if err := text("tag", *f.Tag, 1, 100); err != nil {
			return nil, err
		}
	}
	if f.Groups != nil {
		if len(*f.Groups) > maxGroups {
			return nil, refuseAdmin(http.StatusBadRequest, "too_many_groups", "A talkgroup is in at most %d groups.", maxGroups)
		}
		var gs []string
		for _, g := range *f.Groups {
			if err := text("group", g, 1, 100); err != nil {
				return nil, err
			}
			if !slices.Contains(gs, g) {
				gs = append(gs, g)
			}
		}
		*f.Groups = gs
	}
	if f.DelayMinutes != nil && (*f.DelayMinutes < 0 || *f.DelayMinutes > store.MaxDelayMinutes) {
		return nil, refuseAdmin(http.StatusBadRequest, "invalid_delay_minutes",
			"delay_minutes must be a whole number of minutes from 0 to %d.", store.MaxDelayMinutes)
	}
	if err := f.settingsFields.check(); err != nil {
		return nil, err
	}
	if f.State == nil {
		return nil, nil
	}
	st, err := parseState(*f.State)
	return &st, err
}

// fireground is the fireground channel number the edit gives, if any.
func (f *talkgroupFields) fireground() *int32 {
	if f.FiregroundChannel.Value == nil {
		return nil
	}
	n := int32(*f.FiregroundChannel.Value)
	return &n
}

// writeTalkgroup applies an admin's add or edit in one transaction. Either
// marks the talkgroup reviewed.
func (a *Admin) writeTalkgroup(r *http.Request, who string, full bool) (int, any, error) {
	sys, tg, err := talkgroupPath(r)
	if err != nil {
		return 0, nil, err
	}
	var in talkgroupFields
	if err := decode(r, &in); err != nil {
		return 0, nil, err
	}
	state, err := in.check()
	if err != nil {
		return 0, nil, err
	}
	if full && in.Tag == nil {
		return 0, nil, refuseAdmin(http.StatusBadRequest, "tag_required", "A talkgroup needs a tag.")
	}
	fg := in.fireground()
	ctx := r.Context()
	code := http.StatusOK
	err = a.st.InTx(ctx, func(_ pgx.Tx, q *db.Queries) error {
		if _, err := q.GetSystem(ctx, sys); errors.Is(err, pgx.ErrNoRows) {
			return refuseAdmin(http.StatusNotFound, "system_not_found", "No system has ID %d.", sys)
		} else if err != nil {
			return dbErr("system", err)
		}
		if fg != nil {
			// R100: one talkgroup per number, across the deployment.
			if err := firegroundTaken(ctx, q, *fg, sys, tg); err != nil {
				return err
			}
		}
		var tagID *int64
		if in.Tag != nil {
			id, err := q.EnsureTag(ctx, *in.Tag)
			if err != nil {
				return dbErr("tag", err)
			}
			tagID = &id
		}
		if full {
			_, err := q.GetTalkgroup(ctx, db.GetTalkgroupParams{SystemID: sys, TalkgroupID: tg})
			if errors.Is(err, pgx.ErrNoRows) {
				code = http.StatusCreated
			} else if err != nil {
				return dbErr("talkgroup", err)
			}
			p := db.PutTalkgroupParams{SystemID: sys, TalkgroupID: tg, TagID: tagID, State: store.Public,
				Alert: in.Alert.Value, LedColour: in.LEDColour.Value, FiregroundChannel: fg}
			if in.Label != nil {
				p.Label = *in.Label
			}
			if in.Name != nil {
				p.Name = *in.Name
			}
			if state != nil {
				p.State = *state
			}
			if in.DelayMinutes != nil {
				p.DelayMinutes = *in.DelayMinutes
			}
			if in.SortOrder != nil {
				p.SortOrder = *in.SortOrder
			}
			if err := talkgroupErr(q.PutTalkgroup(ctx, p)); err != nil {
				return err
			}
			if in.Groups == nil {
				in.Groups = &[]string{}
			}
		} else {
			n, err := q.PatchTalkgroup(ctx, db.PatchTalkgroupParams{SystemID: sys, TalkgroupID: tg,
				Label: in.Label, Name: in.Name, TagID: tagID, State: state,
				DelayMinutes: in.DelayMinutes, SortOrder: in.SortOrder,
				SetAlert: in.Alert.Set, Alert: in.Alert.Value,
				SetLedColour: in.LEDColour.Set, LedColour: in.LEDColour.Value,
				SetFiregroundChannel: in.FiregroundChannel.Set, FiregroundChannel: fg})
			if err != nil {
				return talkgroupErr(err)
			}
			if n == 0 {
				return refuseAdmin(http.StatusNotFound, "talkgroup_not_found", "System %d has no talkgroup %d.", sys, tg)
			}
		}
		if in.Groups != nil {
			return setGroups(ctx, q, sys, tg, *in.Groups)
		}
		return nil
	})
	if code, constraint := pgCode(err); code == "23505" && constraint == "talkgroups_fireground_channel" && fg != nil {
		// Another admin gave the number away meanwhile.
		if taken := firegroundTaken(ctx, a.st.Queries, *fg, sys, tg); taken != nil {
			err = taken
		}
	}
	if err != nil {
		return 0, nil, err
	}
	t, err := a.talkgroup(ctx, sys, tg)
	if err != nil {
		return 0, nil, err
	}
	a.log.Info("admin changed a talkgroup", "admin", who, "system", sys, "talkgroup", tg, "state", t.State)
	return code, t, nil
}

// setGroups puts a talkgroup in exactly these groups, creating any that
// don't exist yet, as a tag is.
func setGroups(ctx context.Context, q *db.Queries, sys int64, tg int32, names []string) error {
	ids := make([]int64, 0, len(names))
	for _, n := range names {
		id, err := q.EnsureGroup(ctx, n)
		if err != nil {
			return dbErr("group", err)
		}
		ids = append(ids, id)
	}
	if err := q.ClearTalkgroupGroups(ctx, db.ClearTalkgroupGroupsParams{SystemID: sys, TalkgroupID: tg}); err != nil {
		return dbErr("talkgroup groups", err)
	}
	if err := q.AddTalkgroupGroups(ctx, db.AddTalkgroupGroupsParams{SystemID: sys, TalkgroupID: tg, GroupIds: ids}); err != nil {
		return dbErr("talkgroup groups", err)
	}
	return nil
}

// firegroundTaken refuses a fireground channel number another talkgroup
// has, naming that talkgroup (R100): "Fireground 41".
func firegroundTaken(ctx context.Context, q *db.Queries, n int32, sys int64, tg int32) error {
	h, err := q.FiregroundHolder(ctx, db.FiregroundHolderParams{Channel: n, SystemID: sys, TalkgroupID: tg})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return dbErr("fireground channel", err)
	}
	name := h.Name
	if name == "" {
		name = h.Label
	}
	if name == "" {
		name = strconv.Itoa(int(h.TalkgroupID))
	}
	return refuseAdmin(http.StatusConflict, "fireground_channel_taken",
		"Fireground channel %d is already talkgroup %d %q in system %s.", n, h.TalkgroupID, name, h.SystemLabel)
}

// talkgroupErr explains the table's check that a reviewed talkgroup has a
// tag.
func talkgroupErr(err error) error {
	if code, constraint := pgCode(err); code == "23514" && constraint == "talkgroups_check" {
		return refuseAdmin(http.StatusBadRequest, "tag_required", "A talkgroup needs a tag once it's reviewed.")
	}
	if code, constraint := pgCode(err); code == "23505" && constraint == "talkgroups_fireground_channel" {
		return err // writeTalkgroup names the talkgroup that has it
	}
	if err != nil {
		return dbErr("talkgroup", err)
	}
	return nil
}

func (a *Admin) putTalkgroup(r *http.Request, who string) (int, any, error) {
	return a.writeTalkgroup(r, who, true)
}

func (a *Admin) patchTalkgroup(r *http.Request, who string) (int, any, error) {
	return a.writeTalkgroup(r, who, false)
}

// Attachments (R12).

// Attachment is a recorder + shortName pair attached to a system.
type Attachment struct {
	Recorder    string     `json:"recorder"`
	ShortName   string     `json:"short_name"`
	SystemID    int64      `json:"system_id"`
	SystemLabel string     `json:"system_label"`
	AttachedAt  *time.Time `json:"attached_at,omitempty"`
}

func (a *Admin) listAttachments(r *http.Request, _ string) (int, any, error) {
	rows, err := a.st.ListAttachments(r.Context())
	if err != nil {
		return 0, nil, dbErr("list attachments", err)
	}
	out := make([]Attachment, 0, len(rows))
	for _, x := range rows {
		out = append(out, Attachment{Recorder: x.Recorder, ShortName: x.ShortName, SystemID: x.SystemID,
			SystemLabel: x.SystemLabel, AttachedAt: ts(x.AttachedAt)})
	}
	return http.StatusOK, out, nil
}

// pair reads the path's recorder (active ones only) and shortName.
func (a *Admin) pair(r *http.Request) (int64, string, error) {
	name, short := r.PathValue("recorder"), r.PathValue("short_name")
	if len(short) < 1 || len(short) > 64 {
		return 0, "", refuseAdmin(http.StatusBadRequest, "invalid_short_name", "A shortName is 1-64 bytes.")
	}
	rec, err := a.st.RecorderByName(r.Context(), name)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && rec.RetiredAt.Valid) {
		return 0, "", refuseAdmin(http.StatusNotFound, "recorder_not_found", "No active recorder is named %q.", name)
	}
	if err != nil {
		return 0, "", dbErr("recorder", err)
	}
	return rec.ID, short, nil
}

func (a *Admin) attach(r *http.Request, who string) (int, any, error) {
	rec, short, err := a.pair(r)
	if err != nil {
		return 0, nil, err
	}
	var in struct {
		SystemID int64 `json:"system_id"`
	}
	if err := decode(r, &in); err != nil {
		return 0, nil, err
	}
	s, err := a.system(r.Context(), in.SystemID)
	if err != nil {
		return 0, nil, err
	}
	at, err := a.st.Attach(r.Context(), db.AttachParams{RecorderID: rec, ShortName: short, SystemID: s.ID})
	if err != nil {
		return 0, nil, dbErr("attach", err)
	}
	a.log.Info("admin attached a recorder's system", "admin", who, "recorder", r.PathValue("recorder"),
		"short_name", short, "system", s.ID)
	return http.StatusOK, Attachment{Recorder: r.PathValue("recorder"), ShortName: short, SystemID: s.ID,
		SystemLabel: s.Label, AttachedAt: ts(at)}, nil
}

func (a *Admin) detach(r *http.Request, who string) (int, any, error) {
	rec, short, err := a.pair(r)
	if err != nil {
		return 0, nil, err
	}
	n, err := a.st.Detach(r.Context(), db.DetachParams{RecorderID: rec, ShortName: short})
	if err != nil {
		return 0, nil, dbErr("detach", err)
	}
	if n == 0 {
		return 0, nil, refuseAdmin(http.StatusNotFound, "attachment_not_found", "%s / %s isn't attached.", r.PathValue("recorder"), short)
	}
	a.log.Info("admin detached a recorder's system", "admin", who, "recorder", r.PathValue("recorder"), "short_name", short)
	return http.StatusNoContent, nil, nil
}

// Rejected calls (R12, R17, R18), with the wording the Recorders page and the
// talkgroup admin show.

// UnattachedRejection counts one recorder + shortName pair's calls refused
// for not being attached to a system.
type UnattachedRejection struct {
	Recorder  string     `json:"recorder"`
	ShortName string     `json:"short_name"`
	Calls     int64      `json:"calls"`
	LastAt    *time.Time `json:"last_at"`
	Message   string     `json:"message"`
}

// TalkgroupRejection counts one talkgroup's calls refused for being unknown
// or ignored.
type TalkgroupRejection struct {
	SystemID  int64      `json:"system_id"`
	Talkgroup int32      `json:"talkgroup"`
	Reason    string     `json:"reason"`
	Calls     int64      `json:"calls"`
	LastAt    *time.Time `json:"last_at"`
	Message   string     `json:"message"`
}

// Rejections is GET /api/v1/admin/rejections.
type Rejections struct {
	Unattached []UnattachedRejection `json:"unattached"`
	Talkgroups []TalkgroupRejection  `json:"talkgroups"`
}

func callsRejected(n int64) string {
	if n == 1 {
		return "1 call rejected"
	}
	return strconv.FormatInt(n, 10) + " calls rejected"
}

func (a *Admin) rejections(r *http.Request, _ string) (int, any, error) {
	ctx := r.Context()
	var sys *int64
	if v := r.URL.Query().Get("system"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return 0, nil, refuseAdmin(http.StatusBadRequest, "invalid_system", "system must be a system ID.")
		}
		sys = &id
	}
	out := Rejections{Unattached: []UnattachedRejection{}, Talkgroups: []TalkgroupRejection{}}
	if sys == nil {
		pairs, err := a.st.UnattachedRejections(ctx)
		if err != nil {
			return 0, nil, dbErr("rejections", err)
		}
		for _, p := range pairs {
			out.Unattached = append(out.Unattached, UnattachedRejection{Recorder: p.Recorder, ShortName: p.ShortName,
				Calls: p.Calls, LastAt: ts(p.LastAt),
				Message: fmt.Sprintf("%s / %s: %s, not attached to a system", p.Recorder, p.ShortName, callsRejected(p.Calls))})
		}
	}
	tgs, err := a.st.TalkgroupRejections(ctx, sys)
	if err != nil {
		return 0, nil, dbErr("rejections", err)
	}
	for _, t := range tgs {
		why := "not in the list"
		if t.Reason == store.IgnoredTalkgroup {
			why = "ignored"
		}
		out.Talkgroups = append(out.Talkgroups, TalkgroupRejection{SystemID: t.SystemID, Talkgroup: t.Talkgroup,
			Reason: string(t.Reason), Calls: t.Calls, LastAt: ts(t.LastAt),
			Message: fmt.Sprintf("TG %d: %s, %s", t.Talkgroup, callsRejected(t.Calls), why)})
	}
	return http.StatusOK, out, nil
}

func dbErr(what string, err error) error {
	return fmt.Errorf("%s: %s", what, store.SafeError(err))
}
