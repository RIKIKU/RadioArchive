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
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/RIKIKU/RadioArchive/internal/account"
	"github.com/RIKIKU/RadioArchive/internal/recordings"
	"github.com/RIKIKU/RadioArchive/internal/retention"
	"github.com/RIKIKU/RadioArchive/internal/store"
	"github.com/RIKIKU/RadioArchive/internal/store/db"
)

// AdminPath prefixes the admin API (SPEC.md R51).
const AdminPath = "/api/v1/admin"

// AdminAuthenticator says which admin made a request (R51, R55, R111).
//
// The binary's is account.Service (ADR 0025, ADR 0028): the session cookie
// or the PowerShell module's bearer token, never both, then the account's
// row in RadioArchive's database, role admin and status active, read at
// every admin request, nothing cached. No Authentik group is asked.
type AdminAuthenticator interface {
	// Admin returns the signed-in admin. account.ErrSignInRequired is no
	// one signed in (401), account.ErrNotAdmin someone who isn't an admin
	// (403), account.ErrBadToken a bearer token that isn't the module's
	// (401), account.ErrDisabled a disabled account's token (401) and
	// account.ErrTwoCredentials a token and a cookie at once (400); any
	// other error is the check failing (503). It may renew the session's
	// cookie on w.
	Admin(w http.ResponseWriter, r *http.Request) (account.Admin, error)
}

type adminKey struct{}

// AdminFrom is the admin making the request, as the admin API's check
// found them, for a handler that needs more than their ID (the audit
// log's "who").
func AdminFrom(ctx context.Context) (account.Admin, bool) {
	a, ok := ctx.Value(adminKey{}).(account.Admin)
	return a, ok
}

// admin checks the request is an admin's, answering it if not, and returns
// it with the admin in its context. who is the admin's account ID, which
// logs name them by. A state-changing request from another site is
// refused first (Go's
// CrossOriginProtection, on top of the session cookie's SameSite=Lax); a
// request with neither Sec-Fetch-Site nor Origin isn't a browser's, and is
// let through to the check.
func (a *Admin) admin(w http.ResponseWriter, r *http.Request) (*http.Request, string, bool) {
	if err := a.cop.Check(r); err != nil {
		writeJSON(w, http.StatusForbidden, adminError{Code: "other_site", Message: "That request didn't come from this site."})
		return nil, "", false
	}
	adm, err := a.auth.Admin(w, r)
	switch {
	case errors.Is(err, account.ErrSignInRequired):
		writeJSON(w, http.StatusUnauthorized, adminError{Code: "admin_sign_in_required", Message: "Sign in as an admin."})
		return nil, "", false
	case errors.Is(err, account.ErrNotAdmin):
		writeJSON(w, http.StatusForbidden, adminError{Code: "admin_required", Message: "This account isn't an admin."})
		return nil, "", false
	case errors.Is(err, account.ErrBadToken):
		// RFC 6750: the module renews its token once and tries again.
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		writeJSON(w, http.StatusUnauthorized, adminError{Code: "invalid_token", Message: "That sign-in isn't valid now. Sign in again."})
		return nil, "", false
	case errors.Is(err, account.ErrDisabled):
		writeJSON(w, http.StatusUnauthorized, adminError{Code: "account_disabled", Message: "This account is disabled."})
		return nil, "", false
	case errors.Is(err, account.ErrTwoCredentials):
		writeJSON(w, http.StatusBadRequest, adminError{Code: "two_credentials", Message: "Send a session cookie or a bearer token, not both."})
		return nil, "", false
	case err != nil:
		a.log.Warn("admin check failed", "error", err.Error())
		writeJSON(w, http.StatusServiceUnavailable, adminError{Code: "admin_check_unavailable", Message: "Couldn't check your account just now. Try again in a minute."})
		return nil, "", false
	}
	return r.WithContext(context.WithValue(r.Context(), adminKey{}, adm)), adm.Subject, true
}

// audited makes an admin change in one transaction with its audit log
// entries (store.Audited, ADR 0029), as the admin the request's check
// found, and how they signed in. Every state-changing admin handler writes
// through it (or through a store method that does), so a change can't
// commit without its entry.
func (a *Admin) audited(r *http.Request, f func(q *db.Queries, au *store.Audit) error) error {
	by, _ := AdminFrom(r.Context())
	return a.st.Audited(r.Context(), by.Actor(), f)
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
	ls   ListenerSessions
	now  func() time.Time
	accs AccountRoles
	cop  *http.CrossOriginProtection
}

// WithClock gives the admin API its clock, for the talkgroup table's 30
// days (ADR 0026). Without one it's time.Now.
func (a *Admin) WithClock(now func() time.Time) *Admin {
	a.now = now
	return a
}

// AccountRoles lists accounts, sets their roles (account.Service, ADR
// 0025), disables, enables and deletes them (ADR 0033).
type AccountRoles interface {
	Accounts(ctx context.Context) ([]store.Account, error)
	SetRole(ctx context.Context, by account.Admin, subject, role string) (store.Account, error)
	// SetStatus disables or enables an account: the radio's change, then
	// Authentik's, whose failure (account.ErrAuthentik…) comes back with
	// the account as the radio changed it.
	SetStatus(ctx context.Context, by account.Admin, subject, status string) (store.Account, error)
	// Delete deletes an account, its profile and sessions, and in the
	// standalone deployment its Authentik user; if Authentik fails,
	// nothing is deleted.
	Delete(ctx context.Context, by account.Admin, subject string) (store.Account, error)
}

// WithAccounts serves the accounts: admins promote and demote other
// accounts (R55), disable, enable and delete them (ADR 0033).
func (a *Admin) WithAccounts(acc AccountRoles) *Admin {
	a.accs = acc
	return a
}

// ListenerSessions ends signed-in listeners' sessions (account.Service,
// ADR 0022).
type ListenerSessions interface {
	// EndAccountSessions ends every session of the listener with this
	// subject, as the admin by asks (audited), returning how many there
	// were.
	EndAccountSessions(ctx context.Context, by account.Admin, subject string) (int64, error)
}

// WithListenerSessions serves ending a listener's sessions: the first half
// of disabling a listener (M4), before they are disabled in Authentik.
func (a *Admin) WithListenerSessions(l ListenerSessions) *Admin {
	a.ls = l
	return a
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
	return &Admin{st: st, log: log, auth: auth, dir: recordingsDir, now: time.Now, cop: http.NewCrossOriginProtection()}, nil
}

// WithAdmin serves the admin API.
func WithAdmin(a *Admin) Option {
	return func(api *API) {
		api.handle(AdminRoute, "GET "+AdminPath+"/calls/{call}/recording", a.signedIn(a.recording))
		// The setup's export and import (ADR 0030).
		api.handle(AdminRoute, "GET "+AdminPath+"/setup", a.signedIn(a.exportSetup))
		for _, r := range []struct {
			pattern string
			h       func(*http.Request, string) (int, any, error)
		}{
			{"GET /me", a.me},
			{"GET /systems", a.listSystems},
			{"POST /systems", a.createSystem},
			{"GET /systems/{system}", a.getSystem},
			{"PATCH /systems/{system}", a.patchSystem},
			{"DELETE /systems/{system}", a.deleteSystem},
			{"GET /systems/{system}/talkgroups", a.listTalkgroups},
			{"GET /systems/{system}/talkgroups/{talkgroup}", a.getTalkgroup},
			{"GET /systems/{system}/talkgroups/{talkgroup}/calls", a.talkgroupCalls},
			{"GET /talkgroups", a.talkgroupTable},
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
			{"GET /overlaps", a.overlaps},
			{"GET /groups", a.listGroups},
			{"POST /groups", a.createGroup},
			{"PATCH /groups/{group}", a.patchGroup},
			{"GET /tags", a.listTags},
			{"PATCH /tags/{tag}", a.patchTag},
			{"GET /audit", a.auditLog},
			{"POST /import", a.importSetup},
			{"GET /audit/admins", a.auditAdmins},
		} {
			method, path, _ := strings.Cut(r.pattern, " ")
			api.handle(AdminRoute, method+" "+AdminPath+path, a.serve(r.h))
		}
		if a.ret != nil {
			api.handle(AdminRoute, "GET "+AdminPath+"/retention", a.serve(a.getRetention))
			api.handle(AdminRoute, "PUT "+AdminPath+"/retention", a.serve(a.putRetention))
			api.handle(AdminRoute, "GET "+AuditPath+"/retention", a.serve(a.getAuditRetention))
			api.handle(AdminRoute, "PUT "+AuditPath+"/retention", a.serve(a.putAuditRetention))
		}
		if a.rs != nil {
			api.handle(AdminRoute, "GET "+AdminPath+"/recorders", a.serve(a.listRecorders))
			api.handle(AdminRoute, "GET "+AdminPath+"/recorders/{recorder}", a.serve(a.getRecorder))
			api.handle(AdminRoute, "PATCH "+AdminPath+"/recorders/{recorder}", a.serve(a.patchRecorder))
		}
		if a.ls != nil {
			api.handle(AdminRoute, "DELETE "+AdminPath+"/listeners/{subject}/sessions", a.serve(a.endListenerSessions))
		}
		if a.accs != nil {
			api.handle(AdminRoute, "GET "+AdminPath+"/accounts", a.serve(a.listAccounts))
			api.handle(AdminRoute, "PATCH "+AdminPath+"/accounts/{subject}", a.serve(a.patchAccount))
			api.handle(AdminRoute, "DELETE "+AdminPath+"/accounts/{subject}", a.serve(a.deleteAccount))
		}
	}
}

// EndedSessions is DELETE /api/v1/admin/listeners/{subject}/sessions: how
// many of the listener's sessions were ended.
type EndedSessions struct {
	Ended int64 `json:"ended"`
}

// endListenerSessions signs a listener out everywhere at once: each of
// their browsers is signed out at its next request (ADR 0022). The subject
// is the listener's Authentik user ID. The log names the admin and the
// count, not the listener.
func (a *Admin) endListenerSessions(r *http.Request, who string) (int, any, error) {
	subject := r.PathValue("subject")
	if subject == "" || len(subject) > 255 {
		return 0, nil, refuseAdmin(http.StatusBadRequest, "bad_subject", "Name the listener by their Authentik user ID.")
	}
	by, _ := AdminFrom(r.Context())
	n, err := a.ls.EndAccountSessions(r.Context(), by, subject)
	if err != nil {
		return 0, nil, err
	}
	a.log.Info("admin ended a listener's sessions", "admin", who, "ended", n)
	return http.StatusOK, EndedSessions{Ended: n}, nil
}

// signedIn lets only an admin's requests through to h.
func (a *Admin) signedIn(h func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r, who, ok := a.admin(w, r)
		if !ok {
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
		r, who, ok := a.admin(w, r)
		if !ok {
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
	ctx := r.Context()
	var out System
	err := a.audited(r, func(q *db.Queries, au *store.Audit) error {
		s, err := q.CreateSystem(ctx, db.CreateSystemParams{ID: in.ID, Label: in.Label, Name: in.Name,
			AutoCreate: in.AutoCreate, Alert: in.Alert, LedColour: in.LEDColour, SortOrder: in.SortOrder})
		if err != nil {
			return systemConflict(err, in.ID, in.Label)
		}
		out = systemOf(db.GetSystemRow(s))
		return au.Record(store.Change{Kind: store.AuditSystem, Thing: systemThing(out.ID), Label: out.Label,
			After: out.audited()})
	})
	if err != nil {
		return 0, nil, err
	}
	a.log.Info("admin created a system", "admin", who, "system", out.ID, "auto_create", out.AutoCreate)
	return http.StatusCreated, out, nil
}

func systemThing(id int64) string { return strconv.FormatInt(id, 10) }

// audited is the system as the audit log records it: without its times.
func (s System) audited() System {
	s.CreatedAt, s.UpdatedAt = nil, nil
	return s
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
	ctx := r.Context()
	var out System
	err = a.audited(r, func(q *db.Queries, au *store.Audit) error {
		if err := au.Lock(store.AuditSystem, systemThing(id)); err != nil {
			return dbErr("system", err)
		}
		was, err := q.GetSystem(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return refuseAdmin(http.StatusNotFound, "system_not_found", "No system has ID %d.", id)
		}
		if err != nil {
			return dbErr("system", err)
		}
		s, err := q.UpdateSystem(ctx, db.UpdateSystemParams{ID: id, Label: in.Label, Name: in.Name,
			AutoCreate: in.AutoCreate, SortOrder: in.SortOrder, SetAlert: in.Alert.Set, Alert: in.Alert.Value,
			SetLedColour: in.LEDColour.Set, LedColour: in.LEDColour.Value})
		if errors.Is(err, pgx.ErrNoRows) {
			return refuseAdmin(http.StatusNotFound, "system_not_found", "No system has ID %d.", id)
		}
		if err != nil {
			label := ""
			if in.Label != nil {
				label = *in.Label
			}
			return systemConflict(err, id, label)
		}
		out = systemOf(db.GetSystemRow(s))
		return au.Record(store.Change{Kind: store.AuditSystem, Thing: systemThing(id), Label: out.Label,
			Before: systemOf(was).audited(), After: out.audited()})
	})
	if err != nil {
		return 0, nil, err
	}
	a.log.Info("admin changed a system", "admin", who, "system", id, "auto_create", out.AutoCreate)
	return http.StatusOK, out, nil
}

func (a *Admin) deleteSystem(r *http.Request, who string) (int, any, error) {
	id, err := pathInt(r, "system", math.MaxInt64)
	if err != nil {
		return 0, nil, err
	}
	ctx := r.Context()
	err = a.audited(r, func(q *db.Queries, au *store.Audit) error {
		if err := au.Lock(store.AuditSystem, systemThing(id)); err != nil {
			return dbErr("system", err)
		}
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
		return au.Record(store.Change{Kind: store.AuditSystem, Thing: systemThing(id), Label: s.Label,
			Before: systemOf(s).audited()})
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
	SystemID          int64    `json:"system_id"`
	ID                int32    `json:"id"`
	Label             string   `json:"label"`
	Name              string   `json:"name"`
	Tag               *string  `json:"tag"`
	Groups            []string `json:"groups"`
	State             string   `json:"state"`
	Unreviewed        bool     `json:"unreviewed"`
	DelayMinutes      int32    `json:"delay_minutes"`
	Alert             *string  `json:"alert"`
	LEDColour         *string  `json:"led_colour"`
	SortOrder         int32    `json:"sort_order"`
	FiregroundChannel *int32   `json:"fireground_channel"`
	// Type is what the setup says the talkgroup is (trunk-recorder's mode:
	// D, T, E, ...), "" for nothing said; Frequency is in hertz (R22). Kept
	// and shown, never a listener's (migration 00024, ADR 0030).
	Type      string     `json:"type"`
	Frequency *int64     `json:"frequency"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

func talkgroupOf(t db.GetTalkgroupRow) Talkgroup {
	groups := t.Groups
	if groups == nil {
		groups = []string{}
	}
	return Talkgroup{SystemID: t.SystemID, ID: t.TalkgroupID, Label: t.Label, Name: t.Name, Tag: t.Tag,
		Groups: groups, State: string(t.State), Unreviewed: t.Unreviewed, DelayMinutes: t.DelayMinutes,
		Alert: t.Alert, LEDColour: t.LedColour, SortOrder: t.SortOrder, FiregroundChannel: t.FiregroundChannel,
		Type: t.Type, Frequency: t.Frequency, CreatedAt: ts(t.CreatedAt), UpdatedAt: ts(t.UpdatedAt)}
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
// "unchanged"; null clears alert, LED colour, fireground channel number and
// frequency, and groups: [] takes the talkgroup out of every group. In a
// PUT, absent is the default: no groups, no delay, sort order 0, none of
// its own alert or LED colour, no fireground channel number, no type and
// no frequency.
type talkgroupFields struct {
	Label             *string                    `json:"label"`
	Name              *string                    `json:"name"`
	Tag               *string                    `json:"tag"`
	State             *string                    `json:"state"`
	Groups            *[]string                  `json:"groups"`
	DelayMinutes      *int32                     `json:"delay_minutes"`
	FiregroundChannel optional[firegroundNumber] `json:"fireground_channel"`
	Type              *string                    `json:"type"`
	Frequency         optional[int64]            `json:"frequency"`
	settingsFields
}

// MaxTalkgroupType is the longest type, in bytes; MaxFrequency the highest
// frequency, in hertz (100 GHz).
const (
	MaxTalkgroupType = 32
	MaxFrequency     = 100_000_000_000
)

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
	if f.Type != nil {
		if err := text("type", *f.Type, 0, MaxTalkgroupType); err != nil {
			return nil, err
		}
		if strings.ContainsFunc(*f.Type, unicode.IsControl) {
			return nil, refuseAdmin(http.StatusBadRequest, "invalid_type", "type can't hold control characters.")
		}
	}
	if v := f.Frequency.Value; v != nil && (*v < 1 || *v > MaxFrequency) {
		return nil, refuseAdmin(http.StatusBadRequest, "invalid_frequency",
			"frequency must be a whole number of hertz from 1 to %d, or null.", int64(MaxFrequency))
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
	var t Talkgroup
	err = a.audited(r, func(q *db.Queries, au *store.Audit) error {
		system, err := q.GetSystem(ctx, sys)
		if errors.Is(err, pgx.ErrNoRows) {
			return refuseAdmin(http.StatusNotFound, "system_not_found", "No system has ID %d.", sys)
		} else if err != nil {
			return dbErr("system", err)
		}
		thing := talkgroupThing(sys, tg)
		if err := au.Lock(store.AuditTalkgroup, thing); err != nil {
			return dbErr("talkgroup", err)
		}
		var before any
		if was, err := q.GetTalkgroup(ctx, db.GetTalkgroupParams{SystemID: sys, TalkgroupID: tg}); err == nil {
			before = talkgroupOf(was).audited()
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return dbErr("talkgroup", err)
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
			if before == nil {
				code = http.StatusCreated
			}
			p := db.PutTalkgroupParams{SystemID: sys, TalkgroupID: tg, TagID: tagID, State: store.Public,
				Alert: in.Alert.Value, LedColour: in.LEDColour.Value, FiregroundChannel: fg,
				Frequency: in.Frequency.Value}
			if in.Type != nil {
				p.Type = *in.Type
			}
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
				SetFiregroundChannel: in.FiregroundChannel.Set, FiregroundChannel: fg,
				Type: in.Type, SetFrequency: in.Frequency.Set, Frequency: in.Frequency.Value})
			if err != nil {
				return talkgroupErr(err)
			}
			if n == 0 {
				return refuseAdmin(http.StatusNotFound, "talkgroup_not_found", "System %d has no talkgroup %d.", sys, tg)
			}
		}
		if in.Groups != nil {
			if err := setGroups(ctx, q, sys, tg, *in.Groups); err != nil {
				return err
			}
		}
		now, err := q.GetTalkgroup(ctx, db.GetTalkgroupParams{SystemID: sys, TalkgroupID: tg})
		if err != nil {
			return dbErr("talkgroup", err)
		}
		t = talkgroupOf(now)
		label := fmt.Sprintf("%s %d", system.Label, tg)
		if t.Label != "" {
			label += " " + t.Label
		}
		return au.Record(store.Change{Kind: store.AuditTalkgroup, Thing: thing, Label: label,
			Before: before, After: t.audited()})
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
	a.log.Info("admin changed a talkgroup", "admin", who, "system", sys, "talkgroup", tg, "state", t.State)
	return code, t, nil
}

func talkgroupThing(sys int64, tg int32) string { return fmt.Sprintf("%d/%d", sys, tg) }

// audited is the talkgroup as the audit log records it: without its times.
func (t Talkgroup) audited() Talkgroup {
	t.CreatedAt, t.UpdatedAt = nil, nil
	return t
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
	ctx := r.Context()
	name := r.PathValue("recorder")
	var out Attachment
	err = a.audited(r, func(q *db.Queries, au *store.Audit) error {
		s, err := q.GetSystem(ctx, in.SystemID)
		if errors.Is(err, pgx.ErrNoRows) {
			return refuseAdmin(http.StatusNotFound, "system_not_found", "No system has ID %d.", in.SystemID)
		}
		if err != nil {
			return dbErr("system", err)
		}
		if err := au.Lock(store.AuditAttachment, attachmentThing(name, short)); err != nil {
			return dbErr("attach", err)
		}
		before, err := attachmentNow(ctx, q, rec, name, short)
		if err != nil {
			return err
		}
		at, err := q.Attach(ctx, db.AttachParams{RecorderID: rec, ShortName: short, SystemID: s.ID})
		if err != nil {
			return dbErr("attach", err)
		}
		out = Attachment{Recorder: name, ShortName: short, SystemID: s.ID, SystemLabel: s.Label, AttachedAt: ts(at)}
		after := out
		after.AttachedAt = nil
		return au.Record(store.Change{Kind: store.AuditAttachment, Thing: attachmentThing(name, short),
			Label: name + " / " + short, Before: before, After: after})
	})
	if err != nil {
		return 0, nil, err
	}
	a.log.Info("admin attached a recorder's system", "admin", who, "recorder", name,
		"short_name", short, "system", out.SystemID)
	return http.StatusOK, out, nil
}

// attachmentThing is an attachment's key in the audit log: the recorder's
// name and the shortName.
func attachmentThing(recorder, short string) string { return recorder + "/" + short }

// attachmentNow is the pair's attachment as the audit log records it, or
// nil (untyped) when it isn't attached.
func attachmentNow(ctx context.Context, q *db.Queries, rec int64, name, short string) (any, error) {
	x, err := q.GetAttachment(ctx, db.GetAttachmentParams{RecorderID: rec, ShortName: short})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, dbErr("attachment", err)
	}
	return Attachment{Recorder: name, ShortName: short, SystemID: x.SystemID, SystemLabel: x.SystemLabel}, nil
}

func (a *Admin) detach(r *http.Request, who string) (int, any, error) {
	rec, short, err := a.pair(r)
	if err != nil {
		return 0, nil, err
	}
	ctx := r.Context()
	name := r.PathValue("recorder")
	err = a.audited(r, func(q *db.Queries, au *store.Audit) error {
		if err := au.Lock(store.AuditAttachment, attachmentThing(name, short)); err != nil {
			return dbErr("detach", err)
		}
		before, err := attachmentNow(ctx, q, rec, name, short)
		if err != nil {
			return err
		}
		n, err := q.Detach(ctx, db.DetachParams{RecorderID: rec, ShortName: short})
		if err != nil {
			return dbErr("detach", err)
		}
		if n == 0 || before == nil {
			return refuseAdmin(http.StatusNotFound, "attachment_not_found", "%s / %s isn't attached.", name, short)
		}
		return au.Record(store.Change{Kind: store.AuditAttachment, Thing: attachmentThing(name, short),
			Label: name + " / " + short, Before: before})
	})
	if err != nil {
		return 0, nil, err
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
