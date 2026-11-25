package store_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/store"
	"github.com/RIKIKU/RadioArchive/internal/store/storetest"
)

const accountsIssuer = "https://auth.example.test/application/o/radioarchive/"

func accountsStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), storetest.FreshURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	return st
}

func signIn(t *testing.T, st *store.Store, sub, email string, verified bool, initial string) store.Account {
	t.Helper()
	a, _, err := st.SignInAccount(context.Background(), store.SignIn{Issuer: accountsIssuer, Subject: sub, Name: "Made Up " + sub,
		Email: email, EmailVerified: verified, InitialAdmin: initial, At: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func activeAdmins(t *testing.T, st *store.Store) int {
	t.Helper()
	var n int
	if err := st.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM accounts WHERE role = 'admin' AND status = 'active'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestIntegrationFirstAdmin (ADR 0025): while no admin exists, a sign-in
// whose proven email is INITIAL_ADMIN_EMAIL, in another case, becomes
// admin; an unproven one, or another address, is a listener; once an
// admin exists the same address signs in as a listener. Every sign-in
// upserts its row, name and email as signed in.
func TestIntegrationFirstAdmin(t *testing.T) {
	st := accountsStore(t)
	const initial = "Owner@Example.test"

	if a := signIn(t, st, "1", "someone@example.test", true, initial); a.Role != store.RoleListener {
		t.Errorf("another address: %+v", a)
	}
	if a := signIn(t, st, "2", "owner@example.test", false, initial); a.Role != store.RoleListener {
		t.Errorf("the address, not proven: %+v", a)
	}
	if a := signIn(t, st, "2", "owner@example.test", true, ""); a.Role != store.RoleListener {
		t.Errorf("no INITIAL_ADMIN_EMAIL: %+v", a)
	}
	a := signIn(t, st, "2", "OWNER@example.TEST", true, initial)
	if !a.Admin() || a.Email != "OWNER@example.TEST" || a.Name != "Made Up 2" {
		t.Fatalf("the address in another case, proven: %+v", a)
	}
	// Once an admin exists the setting does nothing, for anyone.
	if a := signIn(t, st, "3", "owner@example.test", true, initial); a.Role != store.RoleListener {
		t.Errorf("a second account with the address, once an admin exists: %+v", a)
	}
	// And signing in again keeps the admin an admin, with what it signed in as.
	if a := signIn(t, st, "2", "owner@example.test", true, ""); !a.Admin() || a.Email != "owner@example.test" {
		t.Errorf("the admin signing in again: %+v", a)
	}
	if n := activeAdmins(t, st); n != 1 {
		t.Errorf("%d admins, want 1", n)
	}
	list, err := st.Accounts(context.Background(), accountsIssuer)
	if err != nil || len(list) != 3 || list[0].Subject != "2" {
		t.Errorf("Accounts = %+v, %v; want 3, the admin first", list, err)
	}
}

// TestIntegrationFirstAdminAtOnce: two sign-ins at once, each with the
// address, make exactly one admin.
func TestIntegrationFirstAdminAtOnce(t *testing.T) {
	for round := range 5 {
		st := accountsStore(t)
		var wg sync.WaitGroup
		for i := range 8 {
			wg.Go(func() {
				if _, _, err := st.SignInAccount(context.Background(), store.SignIn{Issuer: accountsIssuer,
					Subject: fmt.Sprint(i + 1), Email: "owner@example.test", EmailVerified: true,
					InitialAdmin: "owner@example.test", At: time.Now()}); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		if n := activeAdmins(t, st); n != 1 {
			t.Fatalf("round %d: %d admins, want exactly 1", round, n)
		}
	}
}

// TestIntegrationLastAdmin (ADR 0025): an admin promotes and demotes other
// accounts; never their own role; the last active admin can't be demoted;
// an account that isn't an admin any more changes nothing.
func TestIntegrationLastAdmin(t *testing.T) {
	ctx := context.Background()
	st := accountsStore(t)
	signIn(t, st, "1", "owner@example.test", true, "owner@example.test")
	signIn(t, st, "2", "second@example.test", true, "owner@example.test")

	if _, err := st.SetAccountRole(ctx, accountsIssuer, "1", "1", store.RoleListener); !errors.Is(err, store.ErrOwnRole) {
		t.Errorf("own role: %v", err)
	}
	if _, err := st.SetAccountRole(ctx, accountsIssuer, "2", "1", store.RoleListener); !errors.Is(err, store.ErrNotAdmin) {
		t.Errorf("a listener demoting the admin: %v", err)
	}
	if _, err := st.SetAccountRole(ctx, accountsIssuer, "1", "9", store.RoleAdmin); !errors.Is(err, store.ErrNoAccount) {
		t.Errorf("no such account: %v", err)
	}
	if a, err := st.SetAccountRole(ctx, accountsIssuer, "1", "2", store.RoleAdmin); err != nil || !a.Admin() {
		t.Fatalf("promote: %+v, %v", a, err)
	}
	if a, err := st.SetAccountRole(ctx, accountsIssuer, "1", "2", store.RoleAdmin); err != nil || !a.Admin() {
		t.Errorf("promote again changes nothing: %+v, %v", a, err)
	}
	if a, err := st.SetAccountRole(ctx, accountsIssuer, "2", "1", store.RoleListener); err != nil || a.Role != store.RoleListener {
		t.Fatalf("demote: %+v, %v", a, err)
	}
	// 1 isn't an admin any more: it changes nothing, and 2 is the last.
	if _, err := st.SetAccountRole(ctx, accountsIssuer, "1", "2", store.RoleListener); !errors.Is(err, store.ErrNotAdmin) {
		t.Errorf("a demoted admin demoting: %v", err)
	}
	// The last active admin: only a disabled admin besides (issue #105
	// sets status; here, by hand).
	signIn(t, st, "3", "third@example.test", true, "")
	if _, err := st.SetAccountRole(ctx, accountsIssuer, "2", "3", store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE accounts SET status = 'disabled' WHERE subject = '3'`); err != nil {
		t.Fatal(err)
	}
	if n := activeAdmins(t, st); n != 1 {
		t.Fatalf("%d active admins, want 1", n)
	}
	if _, err := st.SetAccountRole(ctx, accountsIssuer, "3", "2", store.RoleListener); !errors.Is(err, store.ErrNotAdmin) {
		t.Errorf("a disabled admin demoting: %v", err)
	}
	if _, err := st.SetAccountRole(ctx, accountsIssuer, "2", "3", store.RoleListener); err != nil {
		t.Errorf("demoting a disabled admin, with an active one left: %v", err)
	}
	if n := activeAdmins(t, st); n != 1 {
		t.Errorf("%d active admins, want 1", n)
	}
	// A disabled account's sign-in keeps its role and its last sign-in.
	before, _ := st.Account(ctx, accountsIssuer, "3")
	if a := signIn(t, st, "3", "third@example.test", true, ""); a.Status != store.StatusDisabled || !a.LastSignedIn.Equal(before.LastSignedIn) {
		t.Errorf("a disabled account signing in: %+v, was %+v", a, before)
	}
}

// TestIntegrationLastAdminAtOnce: two admins demoting each other at once
// leave exactly one admin.
func TestIntegrationLastAdminAtOnce(t *testing.T) {
	ctx := context.Background()
	for round := range 5 {
		st := accountsStore(t)
		signIn(t, st, "1", "owner@example.test", true, "owner@example.test")
		signIn(t, st, "2", "second@example.test", true, "")
		if _, err := st.SetAccountRole(ctx, accountsIssuer, "1", "2", store.RoleAdmin); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		errs := make([]error, 2)
		start := make(chan struct{})
		for i, c := range [][2]string{{"1", "2"}, {"2", "1"}} {
			wg.Go(func() {
				<-start
				_, errs[i] = st.SetAccountRole(ctx, accountsIssuer, c[0], c[1], store.RoleListener)
			})
		}
		close(start)
		wg.Wait()
		if n := activeAdmins(t, st); n != 1 {
			t.Fatalf("round %d: %d admins left, want 1 (%v)", round, n, errs)
		}
		if (errs[0] == nil) == (errs[1] == nil) {
			t.Errorf("round %d: want one demotion and one refusal: %v", round, errs)
		}
		for _, err := range errs {
			if err != nil && !errors.Is(err, store.ErrNotAdmin) && !errors.Is(err, store.ErrLastAdmin) {
				t.Errorf("round %d: %v", round, err)
			}
		}
	}
}
