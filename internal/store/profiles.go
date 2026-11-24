package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/RIKIKU/RadioArchive/internal/store/db"
)

// Signed-in listeners' saved profiles (SPEC.md R110, ADR 0021), as
// internal/account keeps them: one row an account, keyed by the OIDC issuer
// and subject it signs in with. The profile is the account package's JSON,
// checked before it gets here. Errors carry only a SQLSTATE, never a value.

// ListenerProfile is the account's saved profile, nil when it has none.
func (s *Store) ListenerProfile(ctx context.Context, issuer, subject string) ([]byte, error) {
	p, err := s.GetListenerProfile(ctx, db.GetListenerProfileParams{Issuer: issuer, Subject: subject})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the listener's profile: %s", SafeError(err))
	}
	return p, nil
}

// SaveListenerProfile saves the account's profile in place of the one it
// had, if any.
func (s *Store) SaveListenerProfile(ctx context.Context, issuer, subject string, profile []byte) error {
	if err := s.UpsertListenerProfile(ctx, db.UpsertListenerProfileParams{Issuer: issuer, Subject: subject, Profile: profile}); err != nil {
		return fmt.Errorf("save the listener's profile: %s", SafeError(err))
	}
	return nil
}

// ClearListenerProfile deletes the account's profile, if it has one.
func (s *Store) ClearListenerProfile(ctx context.Context, issuer, subject string) error {
	if err := s.DeleteListenerProfile(ctx, db.DeleteListenerProfileParams{Issuer: issuer, Subject: subject}); err != nil {
		return fmt.Errorf("clear the listener's profile: %s", SafeError(err))
	}
	return nil
}
