package store_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/store"
)

// newestEntry is the audit log's newest entry, false for none.
func newestEntry(t *testing.T, st *store.Store) (store.AuditEntry, bool) {
	t.Helper()
	es, err := st.AuditEntries(context.Background(), store.AuditFilter{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(es) == 0 {
		return store.AuditEntry{}, false
	}
	return es[0], true
}

// changesOf is an entry's changes as field: before -> after.
func changesOf(e store.AuditEntry) map[string]string {
	out := map[string]string{}
	for _, c := range e.Changes {
		out[c.Field] = string(c.Before) + " -> " + string(c.After)
	}
	return out
}

// idHash is a made-up session ID's hash, as the cookie's would be.
func idHash(id string) []byte {
	h := sha256.Sum256([]byte(id))
	return h[:]
}

func startSession(t *testing.T, st *store.Store, sub, id string) {
	t.Helper()
	now := time.Now()
	if err := st.StartListenerSession(context.Background(), idHash(id), store.ListenerSession{Issuer: accountsIssuer, Subject: sub,
		Name: "Made Up " + sub, LastUsed: now, Expires: now.Add(time.Hour)}, ""); err != nil {
		t.Fatal(err)
	}
}

func liveSession(t *testing.T, st *store.Store, id string) bool {
	t.Helper()
	se, err := st.ListenerSession(context.Background(), idHash(id), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return se != nil
}

// TestIntegrationDisableAccount (ADR 0033): disabling is the status, every
// session ended and the audit entry, in one transaction, with Authentik
// still to be told; again, it changes nothing and writes nothing.
// Enabling mirrors it and keeps the profile. Never an admin's own
// account; never by an account that isn't an active admin; an unknown
// account is refused. Whether Authentik took it is recorded without an
// entry, and only for the status it was asked about.
func TestIntegrationDisableAccount(t *testing.T) {
	ctx := context.Background()
	st := accountsStore(t)
	signIn(t, st, "1", "owner@example.test", true, "owner@example.test")
	signIn(t, st, "2", "listener@example.test", true, "")
	startSession(t, st, "2", "phone")
	startSession(t, st, "2", "laptop")
	startSession(t, st, "1", "admin")
	if err := st.SaveListenerProfile(ctx, accountsIssuer, "2", []byte(`{"v":1,"talkgroups":[]}`)); err != nil {
		t.Fatal(err)
	}

	a, err := st.SetAccountStatus(ctx, accountsIssuer, by("1"), "2", store.StatusDisabled)
	if err != nil || a.Status != store.StatusDisabled || !a.AuthentikPending {
		t.Fatalf("disable: %+v, %v", a, err)
	}
	if liveSession(t, st, "phone") || liveSession(t, st, "laptop") {
		t.Error("a disabled account's sessions are still live")
	}
	if !liveSession(t, st, "admin") {
		t.Error("disabling ended someone else's session")
	}
	e, ok := newestEntry(t, st)
	if got := changesOf(e); !ok || e.Kind != store.AuditAccount || e.Thing != "2" || e.Action != store.Changed || e.Label != "Made Up 2" ||
		got["status"] != `"active" -> "disabled"` || got["sessions"] != `2 -> 0` || len(got) != 2 || e.By.Subject != "1" {
		t.Errorf("the entry: %+v %v", e, got)
	}
	if again, err := st.SetAccountStatus(ctx, accountsIssuer, by("1"), "2", store.StatusDisabled); err != nil || again.Status != store.StatusDisabled {
		t.Errorf("disable again: %+v, %v", again, err)
	}
	if e2, _ := newestEntry(t, st); e2.ID != e.ID {
		t.Errorf("disabling again wrote %+v", e2)
	}

	// Authentik's answer, for the status it was asked about only.
	if err := st.SetAccountAuthentikPending(ctx, accountsIssuer, by("1"), "2", store.StatusActive, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Account(ctx, accountsIssuer, "2"); !got.AuthentikPending {
		t.Error("an answer about another status cleared it")
	}
	if err := st.SetAccountAuthentikPending(ctx, accountsIssuer, by("1"), "2", store.StatusDisabled, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Account(ctx, accountsIssuer, "2"); got.AuthentikPending {
		t.Error("Authentik's answer wasn't recorded")
	}
	if e2, _ := newestEntry(t, st); e2.ID != e.ID {
		t.Errorf("recording Authentik's answer wrote %+v", e2)
	}

	// A disabled account's sign-in keeps it disabled.
	if a := signIn(t, st, "2", "listener@example.test", true, ""); a.Status != store.StatusDisabled {
		t.Errorf("a disabled account signing in: %+v", a)
	}

	a, err = st.SetAccountStatus(ctx, accountsIssuer, by("1"), "2", store.StatusActive)
	if err != nil || a.Status != store.StatusActive || !a.AuthentikPending {
		t.Fatalf("enable: %+v, %v", a, err)
	}
	e, _ = newestEntry(t, st)
	if got := changesOf(e); got["status"] != `"disabled" -> "active"` || len(got) != 1 {
		t.Errorf("the enabling's entry: %v", got)
	}
	if p, err := st.ListenerProfile(ctx, accountsIssuer, "2"); err != nil || string(p) == "" {
		t.Errorf("the profile after disabling and enabling: %q, %v", p, err)
	}

	for name, c := range map[string]struct {
		by, subject string
		want        error
	}{
		"own account":             {"1", "1", store.ErrOwnAccount},
		"a listener disabling":    {"2", "1", store.ErrNotAdmin},
		"no such account":         {"1", "9", store.ErrNoAccount},
		"a made-up admin, no row": {"8", "2", store.ErrNotAdmin},
	} {
		if _, err := st.SetAccountStatus(ctx, accountsIssuer, by(c.by), c.subject, store.StatusDisabled); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	if e2, _ := newestEntry(t, st); e2.ID != e.ID {
		t.Errorf("a refusal wrote %+v", e2)
	}
	if _, err := st.SetAccountStatus(ctx, accountsIssuer, store.Actor{Subject: "1"}, "2", store.StatusDisabled); !errors.Is(err, store.ErrNoActor) {
		t.Errorf("no how: %v", err)
	}
}

// TestIntegrationDisableAdminsAtOnce: two admins disabling each other at
// once leave exactly one active admin (the last-admin rule, ADR 0025).
func TestIntegrationDisableAdminsAtOnce(t *testing.T) {
	ctx := context.Background()
	for round := range 5 {
		st := accountsStore(t)
		signIn(t, st, "1", "owner@example.test", true, "owner@example.test")
		signIn(t, st, "2", "second@example.test", true, "")
		if _, err := st.SetAccountRole(ctx, accountsIssuer, by("1"), "2", store.RoleAdmin); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		errs := make([]error, 2)
		start := make(chan struct{})
		for i, c := range [][2]string{{"1", "2"}, {"2", "1"}} {
			wg.Go(func() {
				<-start
				_, errs[i] = st.SetAccountStatus(ctx, accountsIssuer, by(c[0]), c[1], store.StatusDisabled)
			})
		}
		close(start)
		wg.Wait()
		if n := activeAdmins(t, st); n != 1 {
			t.Fatalf("round %d: %d active admins left, want 1 (%v)", round, n, errs)
		}
		if (errs[0] == nil) == (errs[1] == nil) {
			t.Errorf("round %d: want one disabling and one refusal: %v", round, errs)
		}
		for _, err := range errs {
			if err != nil && !errors.Is(err, store.ErrNotAdmin) && !errors.Is(err, store.ErrLastAdmin) {
				t.Errorf("round %d: %v", round, err)
			}
		}
	}
}

// TestIntegrationDeleteAccount (ADR 0033): deleting removes the account's
// row, profile and sessions, with an entry keeping what was deleted, in
// one transaction; before runs last in it, and its error rolls everything
// back and comes back as it is. The same refusals as disabling.
func TestIntegrationDeleteAccount(t *testing.T) {
	ctx := context.Background()
	st := accountsStore(t)
	signIn(t, st, "1", "owner@example.test", true, "owner@example.test")
	signIn(t, st, "2", "listener@example.test", true, "")
	startSession(t, st, "2", "phone")
	if err := st.SaveListenerProfile(ctx, accountsIssuer, "2", []byte(`{"v":1,"talkgroups":[]}`)); err != nil {
		t.Fatal(err)
	}
	start, _ := newestEntry(t, st)

	refused := errors.New("Authentik refused")
	var saw store.Account
	if _, err := st.DeleteAccount(ctx, accountsIssuer, by("1"), "2", func(a store.Account) error { saw = a; return refused }); err != refused {
		t.Fatalf("before refusing: %v", err)
	}
	if saw.Subject != "2" || saw.Email != "listener@example.test" {
		t.Errorf("before saw %+v", saw)
	}
	if a, _ := st.Account(ctx, accountsIssuer, "2"); a == nil || !liveSession(t, st, "phone") {
		t.Error("a refused deletion deleted something")
	}
	if p, _ := st.ListenerProfile(ctx, accountsIssuer, "2"); p == nil {
		t.Error("a refused deletion deleted the profile")
	}
	if e, _ := newestEntry(t, st); e.ID != start.ID {
		t.Errorf("a refused deletion wrote %+v", e)
	}

	for name, c := range map[string]struct {
		by, subject string
		want        error
	}{
		"own account":          {"1", "1", store.ErrOwnAccount},
		"a listener deleting":  {"2", "1", store.ErrNotAdmin},
		"no such account":      {"1", "9", store.ErrNoAccount},
		"a made-up admin, row": {"8", "2", store.ErrNotAdmin},
	} {
		if _, err := st.DeleteAccount(ctx, accountsIssuer, by(c.by), c.subject, nil); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}

	was, err := st.DeleteAccount(ctx, accountsIssuer, by("1"), "2", func(store.Account) error { return nil })
	if err != nil || was.Subject != "2" || was.Email != "listener@example.test" {
		t.Fatalf("delete: %+v, %v", was, err)
	}
	if a, _ := st.Account(ctx, accountsIssuer, "2"); a != nil {
		t.Errorf("the row is still there: %+v", a)
	}
	if liveSession(t, st, "phone") {
		t.Error("the session is still live")
	}
	if p, _ := st.ListenerProfile(ctx, accountsIssuer, "2"); p != nil {
		t.Errorf("the profile is still there: %s", p)
	}
	e, _ := newestEntry(t, st)
	got := changesOf(e)
	if e.Kind != store.AuditAccount || e.Thing != "2" || e.Action != store.Deleted || e.Label != "Made Up 2" ||
		got["email"] != `"listener@example.test" -> null` || got["role"] != `"listener" -> null` ||
		got["sessions"] != `1 -> null` || got["saved_profile"] != `true -> null` {
		t.Errorf("the deletion's entry: %+v %v", e, got)
	}
	var fields []string
	for _, c := range e.Changes {
		fields = append(fields, c.Field)
	}
	if b, _ := json.Marshal(fields); string(b) != `["name","email","role","status","sessions","saved_profile"]` {
		t.Errorf("the entry's fields: %s", b)
	}
	// Gone: deleting again is refused; signing in again makes a new listener.
	if _, err := st.DeleteAccount(ctx, accountsIssuer, by("1"), "2", nil); !errors.Is(err, store.ErrNoAccount) {
		t.Errorf("deleting again: %v", err)
	}
	if a := signIn(t, st, "2", "listener@example.test", true, ""); a.Role != store.RoleListener || a.Status != store.StatusActive {
		t.Errorf("signing in after deletion: %+v", a)
	}
}
