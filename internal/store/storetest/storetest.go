// Package storetest gives tests the database to run against.
package storetest

import (
	"os"
	"testing"
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
