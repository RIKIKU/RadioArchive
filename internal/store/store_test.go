package store_test

import (
	"context"
	"testing"

	"github.com/RIKIKU/RadioArchive/internal/store"
	"github.com/RIKIKU/RadioArchive/internal/store/storetest"
)

// TestOpenMigrates opens the database twice: the first run applies the
// migrations, the second finds nothing to do, and goose records version 1.
func TestOpenMigrates(t *testing.T) {
	url := storetest.URL(t)
	ctx := context.Background()
	for i := range 2 {
		st, err := store.Open(ctx, url)
		if err != nil {
			t.Fatalf("open %d: %v", i+1, err)
		}
		var version int64
		if err := st.Pool.QueryRow(ctx, "SELECT max(version_id) FROM goose_db_version").Scan(&version); err != nil {
			st.Close()
			t.Fatalf("goose version: %v", err)
		}
		if version < 1 {
			t.Errorf("goose version = %d, want at least 1", version)
		}
		if ok, err := st.Ping(ctx); err != nil || ok != 1 {
			t.Errorf("Ping = %d, %v; want 1, nil", ok, err)
		}
		st.Close()
	}
}

func TestOpenUnreachable(t *testing.T) {
	// Port 1 on localhost: nothing listens there.
	_, err := store.Open(context.Background(), "postgres://u:p@127.0.0.1:1/db?sslmode=disable&connect_timeout=2")
	if err == nil {
		t.Fatal("Open succeeded against a closed port")
	}
	if got, want := err.Error(), "database: can't connect to the database"; got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
}
