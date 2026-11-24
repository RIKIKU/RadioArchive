package store_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/RIKIKU/RadioArchive/internal/store"
	"github.com/RIKIKU/RadioArchive/internal/store/storetest"
)

// A listener's profile (R110, ADR 0021): none until saved, replaced by the
// next save, kept apart by issuer and subject, and gone once cleared.
func TestIntegrationListenerProfiles(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, storetest.FreshURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	const iss, other = "https://auth.example.test/application/o/radioarchive/", "https://auth2.example.test/application/o/radioarchive/"
	get := func(issuer, subject string) map[string]any {
		t.Helper()
		b, err := st.ListenerProfile(ctx, issuer, subject)
		if err != nil {
			t.Fatal(err)
		}
		if b == nil {
			return nil
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("not JSON: %s", b)
		}
		return m
	}
	if p := get(iss, "42"); p != nil {
		t.Fatalf("before saving: %v", p)
	}
	if err := st.SaveListenerProfile(ctx, iss, "42", []byte(`{"v":1,"talkgroups":[{"id":1001}]}`)); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveListenerProfile(ctx, iss, "42", []byte(`{"v":1,"talkgroups":[{"id":1002}],"look":"console"}`)); err != nil {
		t.Fatal(err)
	}
	if p := get(iss, "42"); p == nil || p["look"] != "console" {
		t.Fatalf("after saving twice: %v", p)
	}
	// The same subject from another issuer is another account.
	if p := get(other, "42"); p != nil {
		t.Errorf("another issuer's 42: %v", p)
	}
	if p := get(iss, "43"); p != nil {
		t.Errorf("another subject: %v", p)
	}
	var n int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM listener_profiles`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows: %d, %v", n, err)
	}
	// Only a JSON object is a profile.
	if err := st.SaveListenerProfile(ctx, iss, "42", []byte(`[]`)); err == nil {
		t.Error("an array was saved")
	}
	if err := st.ClearListenerProfile(ctx, iss, "42"); err != nil {
		t.Fatal(err)
	}
	if err := st.ClearListenerProfile(ctx, iss, "42"); err != nil {
		t.Fatalf("clearing an account with none: %v", err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM listener_profiles`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows after clearing: %d, %v", n, err)
	}
}
