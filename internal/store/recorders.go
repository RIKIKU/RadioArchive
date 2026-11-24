package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/RIKIKU/RadioArchive/internal/store/db"
)

// KeyChannel is the PostgreSQL notification channel a key change is sent on,
// with the recorder's ID as its payload. The API listens on it and closes
// any live connection whose key is no longer current (SPEC.md R10).
const KeyChannel = "radioarchive_recorder_key"

// keyPrefix marks a RadioArchive client key, so one pasted in the wrong
// place is recognisable.
const keyPrefix = "rak_"

// Errors from the recorder commands.
var (
	ErrRecorderExists   = errors.New("a recorder with that name already exists")
	ErrNoActiveRecorder = errors.New("no active recorder with that name")
	ErrUnknownKey       = errors.New("unknown, rotated or retired client key")
)

// NewClientKey returns a new client key: 32 random bytes, base64url, with a
// prefix. It's shown once and never stored.
func NewClientKey() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // never fails (crypto/rand)
	return keyPrefix + base64.RawURLEncoding.EncodeToString(b)
}

// HashClientKey is what the database keeps of a key. A key is 256 random
// bits, not a password, so one SHA-256 is enough: there's nothing to guess.
func HashClientKey(key string) []byte {
	h := sha256.Sum256([]byte(key))
	return h[:]
}

// ValidRecorderName reports whether name can name a recorder.
func ValidRecorderName(name string) error {
	if strings.TrimSpace(name) != name || name == "" || len(name) > 100 {
		return errors.New("a recorder name is 1-100 characters with no leading or trailing space")
	}
	return nil
}

// AddRecorder creates a recorder and returns its client key.
func (s *Store) AddRecorder(ctx context.Context, name string) (string, error) {
	if err := ValidRecorderName(name); err != nil {
		return "", err
	}
	key := NewClientKey()
	if _, err := s.CreateRecorder(ctx, db.CreateRecorderParams{Name: name, KeyHash: HashClientKey(key)}); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return "", ErrRecorderExists
		}
		return "", fmt.Errorf("add recorder: %s", SafeError(err))
	}
	return key, nil
}

// RotateRecorderKey gives an active recorder a new key and returns it. The
// old key stops at once: uploads check the key on every request, and the
// notification closes the live connection that used it.
func (s *Store) RotateRecorderKey(ctx context.Context, name string) (string, error) {
	key := NewClientKey()
	err := s.inTx(ctx, func(q *db.Queries) error {
		id, err := q.SetRecorderKey(ctx, db.SetRecorderKeyParams{Name: name, KeyHash: HashClientKey(key)})
		if err != nil {
			return err
		}
		return q.NotifyRecorderKeyChanged(ctx, id)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNoActiveRecorder
	}
	if err != nil {
		return "", fmt.Errorf("rotate key: %s", SafeError(err))
	}
	return key, nil
}

// RetireRecorderByName revokes a recorder's key for good. Its row and its
// calls stay (until retention).
func (s *Store) RetireRecorderByName(ctx context.Context, name string) error {
	err := s.inTx(ctx, func(q *db.Queries) error {
		id, err := q.RetireRecorder(ctx, name)
		if err != nil {
			return err
		}
		return q.NotifyRecorderKeyChanged(ctx, id)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNoActiveRecorder
	}
	if err != nil {
		return fmt.Errorf("retire recorder: %s", SafeError(err))
	}
	return nil
}

// Recorder is an authenticated recorder.
type Recorder struct {
	ID      int64
	Name    string
	KeyHash []byte
}

// AuthenticateRecorder finds the active recorder whose key this is.
func (s *Store) AuthenticateRecorder(ctx context.Context, key string) (Recorder, error) {
	if !strings.HasPrefix(key, keyPrefix) {
		return Recorder{}, ErrUnknownKey
	}
	h := HashClientKey(key)
	r, err := s.RecorderByKeyHash(ctx, h)
	if errors.Is(err, pgx.ErrNoRows) {
		return Recorder{}, ErrUnknownKey
	}
	if err != nil {
		return Recorder{}, fmt.Errorf("authenticate: %s", SafeError(err))
	}
	return Recorder{ID: r.ID, Name: r.Name, KeyHash: h}, nil
}

// RecorderInfo is one row of `recorder list`.
type RecorderInfo struct {
	Name         string
	CreatedAt    time.Time
	KeyRotatedAt *time.Time
	RetiredAt    *time.Time
}

// Recorders lists every recorder, retired ones included.
func (s *Store) Recorders(ctx context.Context) ([]RecorderInfo, error) {
	rows, err := s.ListRecorders(ctx)
	if err != nil {
		return nil, fmt.Errorf("list recorders: %s", SafeError(err))
	}
	out := make([]RecorderInfo, 0, len(rows))
	for _, r := range rows {
		ri := RecorderInfo{Name: r.Name, CreatedAt: r.CreatedAt.Time}
		if r.KeyRotatedAt.Valid {
			t := r.KeyRotatedAt.Time
			ri.KeyRotatedAt = &t
		}
		if r.RetiredAt.Valid {
			t := r.RetiredAt.Time
			ri.RetiredAt = &t
		}
		out = append(out, ri)
	}
	return out, nil
}

// InTx runs f in a transaction, committing if it returns nil.
func (s *Store) InTx(ctx context.Context, f func(pgx.Tx, *db.Queries) error) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := f(tx, s.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) inTx(ctx context.Context, f func(*db.Queries) error) error {
	return s.InTx(ctx, func(_ pgx.Tx, q *db.Queries) error { return f(q) })
}
