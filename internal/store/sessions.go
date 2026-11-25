package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/RIKIKU/RadioArchive/internal/store/db"
)

// Signed-in listeners' sessions (ADR 0022), as internal/account keeps them:
// one row a signed-in browser, keyed by the SHA-256 of the random session
// ID its cookie holds. Errors carry only a SQLSTATE, never a value, and
// nothing here logs: the ID token, name and email are never in a log.

// ListenerSession is a live session: the account it signed in as, and the
// name and email the account menu shows.
type ListenerSession struct {
	Issuer, Subject string
	Name, Email     string
	// LastUsed is when it was last renewed, Expires when it ends unless
	// it's used again.
	LastUsed, Expires time.Time
	// Role and Status are its account's, as they are now (ADR 0025): read
	// with the session, never kept with it. StartListenerSession ignores
	// them.
	Role, Status string
}

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

// StartListenerSession keeps a new session under idHash, with the ID token
// Authentik issued ("" for none), live until expires.
func (s *Store) StartListenerSession(ctx context.Context, idHash []byte, se ListenerSession, idToken string) error {
	var tok *string
	if idToken != "" {
		tok = &idToken
	}
	if err := s.InsertListenerSession(ctx, db.InsertListenerSessionParams{IDHash: idHash, Issuer: se.Issuer, Subject: se.Subject,
		Name: se.Name, Email: se.Email, IDToken: tok, UsedAt: ts(se.LastUsed), ExpiresAt: ts(se.Expires)}); err != nil {
		return fmt.Errorf("start the listener's session: %s", SafeError(err))
	}
	return nil
}

// ListenerSession is the session kept under idHash, with its account's
// role and status, nil when there's none live at now: never made, ended,
// expired, or its account has no row.
func (s *Store) ListenerSession(ctx context.Context, idHash []byte, now time.Time) (*ListenerSession, error) {
	r, err := s.GetListenerSession(ctx, db.GetListenerSessionParams{IDHash: idHash, At: ts(now)})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the listener's session: %s", SafeError(err))
	}
	return &ListenerSession{Issuer: r.Issuer, Subject: r.Subject, Name: r.Name, Email: r.Email,
		LastUsed: r.LastUsedAt.Time, Expires: r.ExpiresAt.Time, Role: r.Role, Status: r.Status}, nil
}

// RenewListenerSession marks the session used at now, live until expires.
func (s *Store) RenewListenerSession(ctx context.Context, idHash []byte, now, expires time.Time) error {
	if err := s.TouchListenerSession(ctx, db.TouchListenerSessionParams{IDHash: idHash, UsedAt: ts(now), ExpiresAt: ts(expires)}); err != nil {
		return fmt.Errorf("renew the listener's session: %s", SafeError(err))
	}
	return nil
}

// EndListenerSession deletes the session kept under idHash (signing out),
// returning its ID token for Authentik's end-session endpoint: "" when it
// had none, or had already ended.
func (s *Store) EndListenerSession(ctx context.Context, idHash []byte) (string, error) {
	toks, err := s.DeleteListenerSession(ctx, idHash)
	if err != nil {
		return "", fmt.Errorf("end the listener's session: %s", SafeError(err))
	}
	for _, t := range toks {
		if t != nil {
			return *t, nil
		}
	}
	return "", nil
}

// EndAccountListenerSessions deletes every session of the account (issuer
// and subject), so each browser signed in as it is signed out at its next
// request, as the admin by asks. It returns how many there were. Ending
// any is audited in the same transaction (ADR 0029); ending none changed
// nothing, and isn't.
func (s *Store) EndAccountListenerSessions(ctx context.Context, by Actor, issuer, subject string) (int64, error) {
	var n int64
	err := s.Audited(ctx, by, func(q *db.Queries, au *Audit) error {
		if err := au.Lock(AuditAccount, subject); err != nil {
			return err
		}
		var err error
		if n, err = endSessions(ctx, q, issuer, subject); err != nil || n == 0 {
			return err
		}
		label := ""
		if r, err := q.GetAccount(ctx, db.GetAccountParams{Issuer: issuer, Subject: subject}); err == nil {
			label = account(r).label()
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		return au.Record(Change{Kind: AuditAccount, Thing: subject, Label: label,
			Before: map[string]int64{"sessions": n}, After: map[string]int64{"sessions": 0}})
	})
	if errors.Is(err, ErrNoActor) {
		return 0, err
	}
	if err != nil {
		return 0, fmt.Errorf("end the account's sessions: %s", SafeError(err))
	}
	return n, nil
}

// endSessions deletes every session of the account in q's transaction,
// returning how many there were: EndAccountListenerSessions's change, and
// part of disabling and deleting an account (ADR 0033).
func endSessions(ctx context.Context, q *db.Queries, issuer, subject string) (int64, error) {
	return q.DeleteAccountListenerSessions(ctx, db.DeleteAccountListenerSessionsParams{Issuer: issuer, Subject: subject})
}

// EndExpiredListenerSessions deletes the sessions that ended by now,
// returning how many.
func (s *Store) EndExpiredListenerSessions(ctx context.Context, now time.Time) (int64, error) {
	n, err := s.DeleteExpiredListenerSessions(ctx, ts(now))
	if err != nil {
		return 0, fmt.Errorf("delete expired listener sessions: %s", SafeError(err))
	}
	return n, nil
}
