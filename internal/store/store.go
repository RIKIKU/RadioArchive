// Package store owns the PostgreSQL connection and its migrations.
package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/RIKIKU/RadioArchive/internal/store/db"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store is the database.
type Store struct {
	Pool *pgxpool.Pool
	*db.Queries
}

// Open connects to url and applies any pending migrations (SPEC.md R87:
// goose migrations run at API start).
func Open(ctx context.Context, url string) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("database: %s", SafeError(err))
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database: %s", SafeError(err))
	}
	if err := migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{Pool: pool, Queries: db.New(pool)}, nil
}

func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, sub, goose.WithLogger(goose.NopLogger()))
	if err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("migrations: %s", SafeError(err))
	}
	return nil
}

// Close closes the pool.
func (s *Store) Close() { s.Pool.Close() }

// SafeError describes a database error without its message text, which can
// echo values (P1). PostgreSQL errors are reduced to their SQLSTATE.
func SafeError(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return "SQLSTATE " + pgErr.Code
	}
	var connErr *pgconn.ConnectError
	if errors.As(err, &connErr) {
		return "can't connect to the database"
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err.Error()
	}
	return fmt.Sprintf("%T", err)
}
