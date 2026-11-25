package account

import (
	"context"
	"errors"
	"net/http"

	"github.com/RIKIKU/RadioArchive/internal/store"
)

// Admins (SPEC.md R51, R55, R111; ADR 0025). An admin is an account whose
// row in RadioArchive's database has role admin and status active. Roles
// are the radio's own: no Authentik group is asked, so being an admin of
// anything else (PagerArchive included) makes nobody a radio admin, and
// nothing an Authentik token can change grants it.

// Admin is the signed-in admin making a request, for the handlers and the
// audit log's "who".
type Admin struct {
	// Issuer and Subject are the account. Logs name it by Subject, never
	// by name or email.
	Issuer, Subject string
	// Name and Email are as Authentik gave them at the session's sign-in.
	Name, Email string
	// Via is how the request was signed in, for the audit log's "how"
	// (ADR 0029): store.ViaBrowser for a session cookie. It's the
	// authenticator's to say, never the client's.
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
)

// Admin is the admin making r: the session cookie (ADR 0022), then the
// account's row, role admin and status active, read now with the session,
// nothing cached, so a demotion or a disabling takes effect at the next
// request. Any other error is the database failing. With sign-in off,
// nobody is an admin.
func (s *Service) Admin(w http.ResponseWriter, r *http.Request) (Admin, error) {
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
	return Admin{Issuer: se.Issuer, Subject: se.Subject, Name: se.Name, Email: se.Email, Via: store.ViaBrowser}, nil
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
