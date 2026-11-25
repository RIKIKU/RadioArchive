package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/RIKIKU/RadioArchive/internal/store/db"
)

// Accounts (ADR 0025): everyone who has finished a sign-in to the radio,
// one row each, keyed by the OIDC issuer and subject they sign in with,
// with their role and status. An admin is an active account whose role is
// admin; roles and status are the radio's own, never Authentik's. Errors
// carry only a SQLSTATE, never a value, and nothing here logs: a name or
// email is never in a log.

// Roles and statuses.
const (
	RoleListener = "listener"
	RoleAdmin    = "admin"

	StatusActive   = "active"
	StatusDisabled = "disabled"
)

// Account is one account's row.
type Account struct {
	Issuer, Subject string
	// Name and Email are as Authentik gave them at the last sign-in.
	Name, Email   string
	Role, Status  string
	FirstSignedIn time.Time
	LastSignedIn  time.Time
}

// Admin says whether the account is an admin now: role admin, active.
func (a Account) Admin() bool { return a.Role == RoleAdmin && a.Status == StatusActive }

func account(r db.Account) Account {
	return Account{Issuer: r.Issuer, Subject: r.Subject, Name: r.Name, Email: r.Email, Role: r.Role, Status: r.Status,
		FirstSignedIn: r.FirstSignedInAt.Time, LastSignedIn: r.LastSignedInAt.Time}
}

// SignIn is a finished sign-in, as SignInAccount records it.
type SignIn struct {
	Issuer, Subject, Name, Email string
	// EmailVerified is whether Authentik has proven the address (the ID
	// token's email_verified). Only a proven address is made the first
	// admin.
	EmailVerified bool
	// InitialAdmin is INITIAL_ADMIN_EMAIL: "" for none.
	InitialAdmin string
	At           time.Time
}

// SignInAccount records a finished sign-in: the account's row is made, or
// brought up to date with the name and email it signed in with. While this
// issuer has no active admin, a sign-in whose proven email is
// InitialAdmin, ignoring case, makes the account admin, in the same
// transaction as the check. Two such sign-ins at once wait for each other,
// so they make one admin. Once any admin exists, InitialAdmin does
// nothing. A disabled account keeps its role and its last sign-in: its
// sign-in is refused (the caller reads its status). promoted says this
// sign-in made the account the first admin.
func (s *Store) SignInAccount(ctx context.Context, in SignIn) (a Account, promoted bool, err error) {
	var out Account
	promote := false
	err = s.inTx(ctx, func(q *db.Queries) error {
		if in.EmailVerified && in.InitialAdmin != "" && in.Email != "" &&
			strings.EqualFold(strings.TrimSpace(in.Email), strings.TrimSpace(in.InitialAdmin)) {
			if err := q.LockFirstAdmin(ctx); err != nil {
				return err
			}
			admins, err := q.LockActiveAdmins(ctx, in.Issuer)
			if err != nil {
				return err
			}
			promote = len(admins) == 0
		}
		r, err := q.UpsertSignedInAccount(ctx, db.UpsertSignedInAccountParams{Issuer: in.Issuer, Subject: in.Subject,
			Name: in.Name, Email: in.Email, Promote: promote, At: ts(in.At)})
		if err != nil {
			return err
		}
		out = account(r)
		return nil
	})
	if err != nil {
		return Account{}, false, fmt.Errorf("record the sign-in: %s", SafeError(err))
	}
	return out, promote && out.Admin(), nil
}

// Account is the account's row, nil for none.
func (s *Store) Account(ctx context.Context, issuer, subject string) (*Account, error) {
	r, err := s.GetAccount(ctx, db.GetAccountParams{Issuer: issuer, Subject: subject})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the account: %s", SafeError(err))
	}
	a := account(r)
	return &a, nil
}

// Accounts are the issuer's accounts, admins first, then by email.
func (s *Store) Accounts(ctx context.Context, issuer string) ([]Account, error) {
	rs, err := s.ListAccounts(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("list the accounts: %s", SafeError(err))
	}
	out := make([]Account, 0, len(rs))
	for _, r := range rs {
		out = append(out, account(r))
	}
	return out, nil
}

// Refusals of a role change.
var (
	// ErrNotAdmin: the account making the change isn't an active admin
	// (any more).
	ErrNotAdmin = errors.New("store: not an active admin")
	// ErrNoAccount: no account has that subject.
	ErrNoAccount = errors.New("store: no such account")
	// ErrOwnRole: an admin never changes their own role.
	ErrOwnRole = errors.New("store: an admin can't change their own role")
	// ErrLastAdmin: the change would leave no active admin.
	ErrLastAdmin = errors.New("store: the last active admin")
)

// SetAccountRole sets the role of the issuer's account subject, as the
// admin by changes it. In one transaction, with the active admins locked
// (SELECT … FOR UPDATE), it checks that by is still an active admin, that
// by isn't changing their own role, and that demoting subject leaves an
// active admin: so two admins demoting each other at once leave one. It
// returns the account as it is now.
func (s *Store) SetAccountRole(ctx context.Context, issuer, by, subject, role string) (Account, error) {
	if role != RoleListener && role != RoleAdmin {
		return Account{}, fmt.Errorf("store: no role %q", role)
	}
	if by == subject {
		return Account{}, ErrOwnRole
	}
	var out Account
	err := s.inTx(ctx, func(q *db.Queries) error {
		admins, err := q.LockActiveAdmins(ctx, issuer)
		if err != nil {
			return err
		}
		if !slices.Contains(admins, by) {
			return ErrNotAdmin
		}
		r, err := q.GetAccountForUpdate(ctx, db.GetAccountForUpdateParams{Issuer: issuer, Subject: subject})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoAccount
		}
		if err != nil {
			return err
		}
		cur := account(r)
		if cur.Role == role {
			out = cur
			return nil
		}
		if cur.Admin() && len(admins) <= 1 {
			return ErrLastAdmin
		}
		if r, err = q.UpdateAccountRole(ctx, db.UpdateAccountRoleParams{Issuer: issuer, Subject: subject, Role: role}); err != nil {
			return err
		}
		out = account(r)
		return nil
	})
	switch {
	case errors.Is(err, ErrNotAdmin), errors.Is(err, ErrNoAccount), errors.Is(err, ErrLastAdmin):
		return Account{}, err
	case err != nil:
		return Account{}, fmt.Errorf("change the account's role: %s", SafeError(err))
	}
	return out, nil
}
