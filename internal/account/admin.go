package account

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/RIKIKU/RadioArchive/internal/oidcclient"
	"github.com/RIKIKU/RadioArchive/internal/store"
)

// Admins (SPEC.md R51, R55, R111; ADR 0025). An admin is an account whose
// row in RadioArchive's database has role admin and status active. Roles
// are the radio's own: no Authentik group is asked, so being an admin of
// anything else (PagerArchive included) makes nobody a radio admin, and
// nothing an Authentik token can change grants it.

// How an admin's request was signed in (ADR 0028): the audit log's "how"
// (issue #112). It comes from the credential the request carried, never
// from anything the client says.
const (
	// ViaBrowser: the session cookie of a browser sign-in (ADR 0022).
	ViaBrowser = store.ViaBrowser
	// ViaModule: a bearer access token of the PowerShell module's client.
	ViaModule = store.ViaModule
)

// Admin is the signed-in admin making a request, for the handlers and the
// audit log's "who".
type Admin struct {
	// Issuer and Subject are the account. Logs name it by Subject, never
	// by name or email. Issuer is always the browser client's: a module
	// token's subject is mapped onto the account it signs in as (ADR 0028).
	Issuer, Subject string
	// Name and Email are as Authentik gave them at the account's last
	// browser sign-in.
	Name, Email string
	// Via is how the request was signed in, for the audit log's "how"
	// (ADR 0029): ViaBrowser for a session cookie, ViaModule for the
	// module's bearer token (ADR 0028). It's the authenticator's to say,
	// never the client's.
	Via string
}

// Actor is the admin as the audit log records them.
func (a Admin) Actor() store.Actor {
	return store.Actor{Issuer: a.Issuer, Subject: a.Subject, Name: a.Name, Email: a.Email, Via: a.Via}
}

// Why Service.Admin turns a request away.
var (
	// ErrSignInRequired: no live session (signed out, ended, expired, or
	// the account disabled).
	ErrSignInRequired = errors.New("account: not signed in")
	// ErrNotAdmin: signed in, but the account isn't an admin now.
	ErrNotAdmin = errors.New("account: not an admin")
	// ErrBadToken: a bearer token that isn't a live access token of the
	// module's client (ADR 0028).
	ErrBadToken = errors.New("account: not the module's access token")
	// ErrTwoCredentials: a bearer token and a session cookie at once, so
	// who is acting would be unclear (ADR 0028).
	ErrTwoCredentials = errors.New("account: a bearer token and a session cookie at once")
	// ErrDisabled: a bearer token whose account is disabled.
	ErrDisabled = errors.New("account: disabled")
)

// Admin is the admin making r. A request with Authorization is the
// module's (ADR 0028): its bearer token must be one of the module client's
// access tokens, and its subject an account whose row says admin and
// active, read now. Otherwise it's the session cookie (ADR 0022), then the
// account's row, read now with the session. Nothing is cached, so a
// demotion or a disabling takes effect at the next request. Any other
// error is the database or the issuer failing. With sign-in off, nobody is
// an admin.
func (s *Service) Admin(w http.ResponseWriter, r *http.Request) (Admin, error) {
	if _, ok := r.Header["Authorization"]; ok {
		return s.moduleAdmin(r)
	}
	se, ok, err := s.current(w, r)
	if err != nil {
		return Admin{}, err
	}
	if !ok {
		return Admin{}, ErrSignInRequired
	}
	if !se.Admin {
		return Admin{}, ErrNotAdmin
	}
	return Admin{Issuer: se.Issuer, Subject: se.Subject, Name: se.Name, Email: se.Email, Via: ViaBrowser}, nil
}

// moduleAdmin is the admin a bearer token signs in as. The module has its
// own client and so its own issuer, while accounts are keyed by the
// browser client's issuer (ADR 0025). Both clients are on the same
// Authentik with sub_mode user_id, so the token's subject is the Authentik
// user ID the browser's sign-in recorded: the account is (the browser
// client's issuer, the token's subject). Someone who has never signed in
// with the browser has no row, and so isn't an admin.
func (s *Service) moduleAdmin(r *http.Request) (Admin, error) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		return Admin{}, ErrTwoCredentials
	}
	if !s.On() || s.cfg.Module == nil {
		return Admin{}, ErrBadToken
	}
	raw, ok := bearer(r)
	if !ok {
		return Admin{}, ErrBadToken
	}
	sub, err := s.cfg.Module.Verify(r.Context(), raw)
	if errors.Is(err, oidcclient.ErrBadToken) {
		s.cfg.Log.Info("account: a module token was refused", "reason", strings.TrimPrefix(err.Error(), oidcclient.ErrBadToken.Error()+": "))
		return Admin{}, ErrBadToken
	}
	if err != nil {
		return Admin{}, err
	}
	acc, err := s.cfg.Accounts.Account(r.Context(), s.cfg.OIDC.Issuer, sub)
	if err != nil {
		return Admin{}, err
	}
	switch {
	case acc == nil:
		return Admin{}, ErrNotAdmin
	case acc.Status == store.StatusDisabled:
		return Admin{}, ErrDisabled
	case !acc.Admin():
		return Admin{}, ErrNotAdmin
	}
	return Admin{Issuer: acc.Issuer, Subject: acc.Subject, Name: acc.Name, Email: acc.Email, Via: ViaModule}, nil
}

// bearer is the request's one bearer token.
func bearer(r *http.Request) (string, bool) {
	vs := r.Header.Values("Authorization")
	if len(vs) != 1 {
		return "", false
	}
	scheme, tok, ok := strings.Cut(vs[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || tok == "" || strings.ContainsAny(tok, " \t") {
		return "", false
	}
	return tok, true
}

// Accounts are this deployment's accounts: everyone who has signed in,
// admins first.
func (s *Service) Accounts(ctx context.Context) ([]store.Account, error) {
	if !s.On() {
		return nil, nil
	}
	return s.cfg.Accounts.Accounts(ctx, s.cfg.OIDC.Issuer)
}

// SetRole sets the role of the account with this subject, as the admin by
// changes it, under the last-admin rule (store.SetAccountRole): never
// their own, and never leaving no active admin.
func (s *Service) SetRole(ctx context.Context, by Admin, subject, role string) (store.Account, error) {
	if !s.On() {
		return store.Account{}, store.ErrNotAdmin
	}
	return s.cfg.Accounts.SetAccountRole(ctx, s.cfg.OIDC.Issuer, by.Actor(), subject, role)
}

// ModuleSettings is what the PowerShell module needs from the radio's URL
// alone (ADR 0028): the issuer of its own client, the client's ID, and the
// scopes it asks for. Everything else it reads from the issuer's discovery
// document.
type ModuleSettings struct {
	Issuer   string `json:"issuer"`
	ClientID string `json:"client_id"`
	Scope    string `json:"scope"`
}

// ModuleScope is what the module asks for: offline_access is what gives it
// a refresh token.
const ModuleScope = "openid email profile offline_access"

// modulePath tells the module where to sign in. It's outside the API's
// /api/v1, among the sign-in hops, and answers anyone.
const modulePath = AuthPath + "/module"

func (s *Service) moduleSettings(w http.ResponseWriter, r *http.Request) {
	if !s.On() || s.cfg.Module == nil {
		writeJSON(w, http.StatusNotFound, apiError{Code: "module_sign_in_off", Message: "This radio doesn't offer sign-in for the PowerShell module."})
		return
	}
	writeJSON(w, http.StatusOK, ModuleSettings{Issuer: s.cfg.Module.Issuer, ClientID: s.cfg.Module.ClientID, Scope: ModuleScope})
}
