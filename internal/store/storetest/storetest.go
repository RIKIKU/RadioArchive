// Package storetest gives tests the database to run against.
package storetest

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// URL returns RADIOARCHIVE_TEST_DATABASE_URL. Unset, the test is skipped,
// unless RADIOARCHIVE_REQUIRE_DB is set (as in CI), when it fails: CI never
// passes by skipping the database tests.
func URL(t testing.TB) string {
	t.Helper()
	url := os.Getenv("RADIOARCHIVE_TEST_DATABASE_URL")
	if url == "" {
		if os.Getenv("RADIOARCHIVE_REQUIRE_DB") != "" {
			t.Fatal("RADIOARCHIVE_REQUIRE_DB is set but RADIOARCHIVE_TEST_DATABASE_URL is not")
		}
		t.Skip("RADIOARCHIVE_TEST_DATABASE_URL is not set")
	}
	return url
}

// FreshURL is a new, empty database of the test's own, on the server URL
// names, dropped when the test ends. A test needs one when what it checks
// is the whole deployment's (the catalogue, fireground channel numbers,
// which are unique across the deployment), so other tests' rows, in
// packages running at the same time, can't change its answer.
func FreshURL(t testing.TB) string {
	t.Helper()
	base := URL(t)
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("radioarchive_test_%d_%d", time.Now().UnixNano(), rand.Uint32())
	exec := func(sql string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		c, err := pgx.Connect(ctx, base)
		if err != nil {
			return err
		}
		defer c.Close(context.Background())
		_, err = c.Exec(ctx, sql)
		return err
	}
	if err := exec(`CREATE DATABASE "` + name + `"`); err != nil {
		t.Fatalf("create a database for the test: %v", err)
	}
	t.Cleanup(func() {
		if err := exec(`DROP DATABASE IF EXISTS "` + name + `" WITH (FORCE)`); err != nil {
			t.Errorf("drop the test's database: %v", err)
		}
	})
	u.Path = "/" + name
	return u.String()
}
