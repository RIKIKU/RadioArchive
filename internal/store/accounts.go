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
	// AuthentikPending says the account's status hasn't reached Authentik
	// yet: it was disabled or enabled here, and Authentik couldn't be
	// reached or refused (ADR 0033).
	AuthentikPending bool
}

// Admin says whether the account is an admin now: role admin, active.
func (a Account) Admin() bool { return a.Role == RoleAdmin && a.Status == StatusActive }

// label is the name the audit log knows the account by: its name, or its
// email if it has none.
func (a Account) label() string {
	if a.Name != "" {
		return a.Name
	}
	return a.Email
}

func account(r db.Account) Account {
	return Account{Issuer: r.Issuer, Subject: r.Subject, Name: r.Name, Email: r.Email, Role: r.Role, Status: r.Status,
		FirstSignedIn: r.FirstSignedInAt.Time, LastSignedIn: r.LastSignedInAt.Time, AuthentikPending: r.AuthentikPending}
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
// active admin: so two admins demoting each other at once leave one. The
// change is audited in the same transaction (ADR 0029). It returns the
// account as it is now.
func (s *Store) SetAccountRole(ctx context.Context, issuer string, by Actor, subject, role string) (Account, error) {
	if role != RoleListener && role != RoleAdmin {
		return Account{}, fmt.Errorf("store: no role %q", role)
	}
	if by.Subject == subject {
		return Account{}, ErrOwnRole
	}
	var out Account
	err := s.Audited(ctx, by, func(q *db.Queries, au *Audit) error {
		admins, err := q.LockActiveAdmins(ctx, issuer)
		if err != nil {
			return err
		}
		if !slices.Contains(admins, by.Subject) {
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
		return au.Record(Change{Kind: AuditAccount, Thing: subject, Label: out.label(),
			Before: map[string]string{"role": cur.Role}, After: map[string]string{"role": out.Role}})
	})
	switch {
	case errors.Is(err, ErrNoActor), errors.Is(err, ErrNotAdmin), errors.Is(err, ErrNoAccount), errors.Is(err, ErrLastAdmin):
		return Account{}, err
	case err != nil:
		return Account{}, fmt.Errorf("change the account's role: %s", SafeError(err))
	}
	return out, nil
}

// ErrOwnAccount: an admin never disables, enables or deletes their own
// account (ADR 0033). Another admin can.
var ErrOwnAccount = errors.New("store: an admin can't disable, enable or delete their own account")

// statusChange is an account's status, and its sessions, as the audit log
// records disabling and enabling it (ADR 0029, ADR 0033).
type statusChange struct {
	Status   string `json:"status"`
	Sessions int64  `json:"sessions"`
}

// deletedAccount is an account as the audit log records its deletion:
// everything the admin API showed of it, and what went with it.
type deletedAccount struct {
	Name         string `json:"name"`
	Email        string `json:"email"`
	Role         string `json:"role"`
	Status       string `json:"status"`
	Sessions     int64  `json:"sessions"`
	SavedProfile bool   `json:"saved_profile"`
}

// lockTarget starts every change an admin makes to another account's
// status or row, as SetAccountRole does its role: the issuer's active
// admins locked, by among them, the account's audit lock taken (so ending
// its sessions at the same moment waits), and its row locked. It returns
// the active admins and the account as it is now.
func lockTarget(ctx context.Context, q *db.Queries, au *Audit, issuer string, by Actor, subject string) ([]string, Account, error) {
	admins, err := q.LockActiveAdmins(ctx, issuer)
	if err != nil {
		return nil, Account{}, err
	}
	if !slices.Contains(admins, by.Subject) {
		return nil, Account{}, ErrNotAdmin
	}
	if err := au.Lock(AuditAccount, subject); err != nil {
		return nil, Account{}, err
	}
	r, err := q.GetAccountForUpdate(ctx, db.GetAccountForUpdateParams{Issuer: issuer, Subject: subject})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, Account{}, ErrNoAccount
	}
	if err != nil {
		return nil, Account{}, err
	}
	return admins, account(r), nil
}

// SetAccountStatus disables or enables the issuer's account subject, as
// the admin by asks: the radio's half of disabling (ADR 0033), which
// internal/account follows with Authentik's. In one transaction, with the
// active admins locked: by must still be an active admin, never on their
// own account, and disabling the last active admin is refused. Disabling
// ends every session of the account (endSessions, as
// EndAccountListenerSessions does), so each browser signed in as it is
// signed out at its next request, and its next sign-in is refused. The
// change and its audit entry commit together, with Authentik marked as
// still to be told (AuthentikPending). An account already so changes
// nothing and writes no entry. It returns the account as it is now.
func (s *Store) SetAccountStatus(ctx context.Context, issuer string, by Actor, subject, status string) (Account, error) {
	if status != StatusActive && status != StatusDisabled {
		return Account{}, fmt.Errorf("store: no status %q", status)
	}
	if by.Subject == subject {
		return Account{}, ErrOwnAccount
	}
	var out Account
	err := s.Audited(ctx, by, func(q *db.Queries, au *Audit) error {
		admins, cur, err := lockTarget(ctx, q, au, issuer, by, subject)
		if err != nil {
			return err
		}
		if cur.Status == status {
			out = cur
			return nil
		}
		if status == StatusDisabled && cur.Admin() && len(admins) <= 1 {
			return ErrLastAdmin
		}
		r, err := q.UpdateAccountStatus(ctx, db.UpdateAccountStatusParams{Issuer: issuer, Subject: subject, Status: status})
		if err != nil {
			return err
		}
		out = account(r)
		var n int64
		if status == StatusDisabled {
			if n, err = endSessions(ctx, q, issuer, subject); err != nil {
				return err
			}
		}
		return au.Record(Change{Kind: AuditAccount, Thing: subject, Label: out.label(),
			Before: statusChange{Status: cur.Status, Sessions: n}, After: statusChange{Status: out.Status}})
	})
	switch {
	case errors.Is(err, ErrNoActor), errors.Is(err, ErrNotAdmin), errors.Is(err, ErrNoAccount), errors.Is(err, ErrLastAdmin):
		return Account{}, err
	case err != nil:
		return Account{}, fmt.Errorf("change the account's status: %s", SafeError(err))
	}
	return out, nil
}

// SetAccountAuthentikPending records whether Authentik has yet to take the
// account's status: false once it has, true while it couldn't be reached
// or refused (ADR 0033). It changes nothing if the status isn't status any
// more, since the later change set its own. It's the record of Authentik's
// answer, not an admin's change of the radio's data, so it runs as by's
// audited transaction and writes no entry.
func (s *Store) SetAccountAuthentikPending(ctx context.Context, issuer string, by Actor, subject, status string, pending bool) error {
	err := s.Audited(ctx, by, func(q *db.Queries, _ *Audit) error {
		_, err := q.SetAccountAuthentikPending(ctx, db.SetAccountAuthentikPendingParams{Issuer: issuer, Subject: subject,
			Status: status, Pending: pending})
		return err
	})
	if errors.Is(err, ErrNoActor) {
		return err
	}
	if err != nil {
		return fmt.Errorf("record Authentik's answer: %s", SafeError(err))
	}
	return nil
}

// DeleteAccount deletes the issuer's account subject, as the admin by
// asks (ADR 0033; owner, 2026-11-25): its row, its saved profile and its
// sessions, in one transaction with its audit entry, which keeps what was
// deleted. The rules are disabling's: by must still be an active admin,
// never on their own account, and the last active admin can't be deleted.
// before, if not nil, runs last in the transaction with the account as it
// was (deleting its Authentik user). Its error rolls everything back and
// is returned as it is, so a deletion Authentik refused changes nothing
// and can be repeated. It returns the account as it was.
func (s *Store) DeleteAccount(ctx context.Context, issuer string, by Actor, subject string, before func(Account) error) (Account, error) {
	if by.Subject == subject {
		return Account{}, ErrOwnAccount
	}
	var out Account
	var beforeErr error
	err := s.Audited(ctx, by, func(q *db.Queries, au *Audit) error {
		admins, cur, err := lockTarget(ctx, q, au, issuer, by, subject)
		if err != nil {
			return err
		}
		if cur.Admin() && len(admins) <= 1 {
			return ErrLastAdmin
		}
		n, err := endSessions(ctx, q, issuer, subject)
		if err != nil {
			return err
		}
		p, err := q.DeleteAccountListenerProfile(ctx, db.DeleteAccountListenerProfileParams{Issuer: issuer, Subject: subject})
		if err != nil {
			return err
		}
		if _, err := q.DeleteAccount(ctx, db.DeleteAccountParams{Issuer: issuer, Subject: subject}); err != nil {
			return err
		}
		if err := au.Record(Change{Kind: AuditAccount, Thing: subject, Label: cur.label(),
			Before: deletedAccount{Name: cur.Name, Email: cur.Email, Role: cur.Role, Status: cur.Status, Sessions: n, SavedProfile: p > 0}}); err != nil {
			return err
		}
		out = cur
		if before != nil {
			beforeErr = before(cur)
		}
		return beforeErr
	})
	switch {
	case beforeErr != nil:
		return Account{}, beforeErr
	case errors.Is(err, ErrNoActor), errors.Is(err, ErrNotAdmin), errors.Is(err, ErrNoAccount), errors.Is(err, ErrLastAdmin):
		return Account{}, err
	case err != nil:
		return Account{}, fmt.Errorf("delete the account: %s", SafeError(err))
	}
	return out, nil
}
