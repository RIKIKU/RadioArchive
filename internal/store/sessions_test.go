package store_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/store"
	"github.com/RIKIKU/RadioArchive/internal/store/storetest"
)

// Listeners' sessions (ADR 0022): kept under a hash, live until they
// expire, renewed, ended one at a time with their ID token, ended for a
// whole account, and swept once expired.
func TestIntegrationListenerSessions(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, storetest.FreshURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	const iss = "https://auth.example.test/application/o/radioarchive/"
	now := time.Now().UTC().Truncate(time.Microsecond)
	hash := func(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }
	// Every session's account has a row (ADR 0025), as a sign-in makes it.
	for _, sub := range []string{"42", "43"} {
		if _, _, err := st.SignInAccount(ctx, store.SignIn{Issuer: iss, Subject: sub, At: now}); err != nil {
			t.Fatal(err)
		}
	}
	start := func(h []byte, sub, tok string, expires time.Time) {
		t.Helper()
		if err := st.StartListenerSession(ctx, h, store.ListenerSession{Issuer: iss, Subject: sub, Name: "Made Up",
			Email: "listener@example.test", LastUsed: now, Expires: expires}, tok); err != nil {
			t.Fatal(err)
		}
	}
	start(hash(1), "42", "made-up-id-token", now.Add(time.Hour))
	start(hash(2), "42", "", now.Add(time.Hour))
	start(hash(3), "43", "", now.Add(time.Minute))

	se, err := st.ListenerSession(ctx, hash(1), now)
	if err != nil || se == nil || se.Issuer != iss || se.Subject != "42" || se.Name != "Made Up" ||
		se.Email != "listener@example.test" || !se.LastUsed.Equal(now) || !se.Expires.Equal(now.Add(time.Hour)) ||
		se.Role != store.RoleListener || se.Status != store.StatusActive {
		t.Fatalf("ListenerSession = %+v, %v", se, err)
	}
	// A session whose account has no row isn't one.
	if err := st.StartListenerSession(ctx, hash(8), store.ListenerSession{Issuer: iss, Subject: "44",
		LastUsed: now, Expires: now.Add(time.Hour)}, ""); err != nil {
		t.Fatal(err)
	}
	if se, err := st.ListenerSession(ctx, hash(8), now); se != nil || err != nil {
		t.Errorf("a session with no account: %+v, %v", se, err)
	}
	if _, err := st.EndListenerSession(ctx, hash(8)); err != nil {
		t.Fatal(err)
	}
	if se, err := st.ListenerSession(ctx, hash(9), now); se != nil || err != nil {
		t.Errorf("no such session: %+v, %v", se, err)
	}
	if se, err := st.ListenerSession(ctx, hash(3), now.Add(time.Minute)); se != nil || err != nil {
		t.Errorf("an expired session is live: %+v, %v", se, err)
	}
	if err := st.StartListenerSession(ctx, []byte("short"), store.ListenerSession{Issuer: iss, Subject: "42",
		LastUsed: now, Expires: now}, ""); err == nil {
		t.Error("a session kept under something that isn't a SHA-256")
	}

	later := now.Add(2 * time.Hour)
	if err := st.RenewListenerSession(ctx, hash(1), later, later.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if se, _ := st.ListenerSession(ctx, hash(1), later); se == nil || !se.LastUsed.Equal(later) {
		t.Errorf("renewed: %+v", se)
	}

	if tok, err := st.EndListenerSession(ctx, hash(1)); err != nil || tok != "made-up-id-token" {
		t.Errorf("EndListenerSession = %q, %v", tok, err)
	}
	if tok, err := st.EndListenerSession(ctx, hash(1)); err != nil || tok != "" {
		t.Errorf("ended twice = %q, %v", tok, err)
	}
	start(hash(4), "42", "", now.Add(time.Hour))
	if n, err := st.EndAccountListenerSessions(ctx, iss, "42"); err != nil || n != 2 {
		t.Errorf("EndAccountListenerSessions = %d, %v; want 2", n, err)
	}
	if n, err := st.EndExpiredListenerSessions(ctx, now.Add(time.Minute)); err != nil || n != 1 {
		t.Errorf("EndExpiredListenerSessions = %d, %v; want 1", n, err)
	}
	var left int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM listener_sessions`).Scan(&left); err != nil || left != 0 {
		t.Errorf("%d sessions left, %v", left, err)
	}
}
