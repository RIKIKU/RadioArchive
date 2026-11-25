package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/RIKIKU/RadioArchive/internal/store"
	"github.com/RIKIKU/RadioArchive/internal/store/db"
)

// auditCount is how many entries the audit log holds.
func auditCount(t *testing.T, st *store.Store) int {
	t.Helper()
	var n int
	if err := st.Pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// setting is the settings row's value, "" for none.
func setting(t *testing.T, st *store.Store, key string) string {
	t.Helper()
	v, err := st.GetSetting(context.Background(), key)
	if errors.Is(err, pgx.ErrNoRows) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestIntegrationAudited (ADR 0029): a change and its entry commit
// together or not at all. A change that fails leaves no entry; an entry
// whose write fails leaves no change; a change with no admin, or no "how",
// never runs. The log is append-only.
func TestIntegrationAudited(t *testing.T) {
	st := accountsStore(t)
	ctx := context.Background()
	module := store.Actor{Issuer: accountsIssuer, Subject: "1", Name: "Made Up 1", Email: "one@example.test", Via: store.ViaModule}
	set := func(by store.Actor, v string, kind string, fail error) error {
		return st.Audited(ctx, by, func(q *db.Queries, au *store.Audit) error {
			if err := au.Lock(kind, "k"); err != nil {
				return err
			}
			if err := q.SetSetting(ctx, db.SetSettingParams{Key: "audit_test", Value: v}); err != nil {
				return err
			}
			if err := au.Record(store.Change{Kind: kind, Thing: "k", Label: "Test setting",
				Before: map[string]string{"value": "was"}, After: map[string]string{"value": v}}); err != nil {
				return err
			}
			return fail
		})
	}

	// Committed: the change and its entry, with who and how.
	if err := set(module, "one", store.AuditRetention, nil); err != nil {
		t.Fatal(err)
	}
	if setting(t, st, "audit_test") != "one" || auditCount(t, st) != 1 {
		t.Fatalf("committed: setting %q, %d entries", setting(t, st, "audit_test"), auditCount(t, st))
	}
	es, err := st.AuditEntries(ctx, store.AuditFilter{Limit: 10})
	if err != nil || len(es) != 1 {
		t.Fatalf("entries %+v, %v", es, err)
	}
	e := es[0]
	if e.By != module || e.Kind != store.AuditRetention || e.Thing != "k" || e.Label != "Test setting" || e.Action != store.Changed ||
		len(e.Changes) != 1 || e.Changes[0].Field != "value" || string(e.Changes[0].Before) != `"was"` || string(e.Changes[0].After) != `"one"` ||
		time.Since(e.At) > time.Minute || time.Since(e.At) < -time.Minute {
		t.Errorf("entry %+v", e)
	}

	// The change fails after recording: neither stays.
	refused := errors.New("refused")
	if err := set(module, "two", store.AuditRetention, refused); !errors.Is(err, refused) {
		t.Fatalf("failed change: %v", err)
	}
	if setting(t, st, "audit_test") != "one" || auditCount(t, st) != 1 {
		t.Errorf("after a failed change: setting %q, %d entries", setting(t, st, "audit_test"), auditCount(t, st))
	}
	// The entry can't be written: the change doesn't stay.
	if err := set(module, "three", "no such kind", nil); err == nil {
		t.Fatal("an entry the table refuses was written")
	}
	if setting(t, st, "audit_test") != "one" || auditCount(t, st) != 1 {
		t.Errorf("after a failed entry: setting %q, %d entries", setting(t, st, "audit_test"), auditCount(t, st))
	}
	// No admin, or no way in: nothing runs.
	for _, by := range []store.Actor{{Via: store.ViaBrowser}, {Subject: "1"}, {Subject: "1", Via: "the client says so"}} {
		ran := false
		err := st.Audited(ctx, by, func(*db.Queries, *store.Audit) error { ran = true; return nil })
		if !errors.Is(err, store.ErrNoActor) || ran {
			t.Errorf("actor %+v: %v, ran %v", by, err, ran)
		}
	}

	// Append-only: nothing rewrites or removes an entry.
	for _, q := range []string{`UPDATE audit_log SET label = 'x'`, `DELETE FROM audit_log`, `TRUNCATE audit_log`} {
		if _, err := st.Pool.Exec(ctx, q); err == nil {
			t.Errorf("%s succeeded", q)
		}
	}
	if auditCount(t, st) != 1 {
		t.Errorf("%d entries after attempts to change them", auditCount(t, st))
	}
	// The prune's way in isn't anyone's: the nightly run's "how" and the
	// audit log's own kind come only from the prune.
	nightly := store.Actor{Subject: store.SystemSubject, Name: "System", Via: store.ViaNightly}
	if err := st.Audited(ctx, nightly, func(*db.Queries, *store.Audit) error { return nil }); !errors.Is(err, store.ErrNoActor) {
		t.Errorf("an audited change as the nightly run: %v", err)
	}
}

// TestIntegrationAuditPrune (owner, 2026-11-25; ADR 0032): the nightly
// prune deletes the entries past the audit log's period, and only those,
// and writes one entry saying so; with none past it, it writes nothing.
// Turning the prune's switch on by hand still deletes nothing the period
// keeps. The period's one reading takes 90 to 3,650 days, else 730.
func TestIntegrationAuditPrune(t *testing.T) {
	st := accountsStore(t)
	ctx := context.Background()
	for _, c := range []struct {
		value string
		want  int32
	}{{"", 730}, {"90", 90}, {"3650", 3650}, {"89", 730}, {"3651", 730}, {"abc", 730}, {"-5", 730}, {"99999999", 730}} {
		if c.value != "" {
			if err := st.SetSetting(ctx, db.SetSettingParams{Key: "audit_retention_days", Value: c.value}); err != nil {
				t.Fatal(err)
			}
		}
		if got, err := st.AuditRetentionDays(ctx); err != nil || got != c.want {
			t.Errorf("setting %q: %d days, %v; want %d", c.value, got, err, c.want)
		}
	}
	if err := st.SetSetting(ctx, db.SetSettingParams{Key: "audit_retention_days", Value: "365"}); err != nil {
		t.Fatal(err)
	}
	add := func(days int) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, `
INSERT INTO audit_log (at, issuer, subject, admin_name, via, kind, thing, label, action, changes)
VALUES (now() - make_interval(days => $1), 'i', 'old', 'Old Admin', 'browser', 'unit', '1/5001', 'Engine', 'changed', '[]')`, days); err != nil {
			t.Fatal(err)
		}
	}
	add(400)
	add(366)
	add(300)
	add(0)

	// The switch by hand: still nothing the period keeps.
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('radioarchive.audit_prune', 'on', true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM audit_log`); err == nil {
		t.Error("with the switch on by hand, DELETE took entries the period keeps")
	}
	_ = tx.Rollback(ctx)
	if n := auditCount(t, st); n != 4 {
		t.Fatalf("%d entries", n)
	}

	p, err := st.PruneAuditLog(ctx)
	if err != nil || p.Deleted != 2 || p.Days != 365 || time.Since(p.Cutoff) < 364*24*time.Hour || time.Since(p.Cutoff) > 366*24*time.Hour {
		t.Fatalf("prune: %+v, %v", p, err)
	}
	es, err := st.AuditEntries(ctx, store.AuditFilter{Limit: 10})
	if err != nil || len(es) != 3 {
		t.Fatalf("after the prune: %+v, %v", es, err)
	}
	e := es[0]
	if e.By != (store.Actor{Subject: store.SystemSubject, Name: "System", Via: store.ViaNightly}) || e.Kind != store.AuditAudit ||
		e.Action != store.Deleted || e.Label != "Audit log" || len(e.Changes) != 3 ||
		e.Changes[0].Field != "entries" || string(e.Changes[0].Before) != "2" || string(e.Changes[0].After) != "null" ||
		e.Changes[1].Field != "before" || e.Changes[2].Field != "days" || string(e.Changes[2].Before) != "365" {
		t.Errorf("the prune's entry: %+v", e)
	}
	var before time.Time
	if err := json.Unmarshal(e.Changes[1].Before, &before); err != nil || before.Sub(p.Cutoff).Abs() > time.Second {
		t.Errorf("the prune's cut-off %s, %v; want %s", e.Changes[1].Before, err, p.Cutoff)
	}
	// Nothing more past it: nothing deleted, no entry.
	if p, err := st.PruneAuditLog(ctx); err != nil || p.Deleted != 0 {
		t.Fatalf("again: %+v, %v", p, err)
	}
	if n := auditCount(t, st); n != 3 {
		t.Errorf("%d entries after a prune with nothing to do", n)
	}
}

// TestIntegrationAuditEntries: newest first, paged with a cursor, filtered
// by who, kind, thing and time.
func TestIntegrationAuditEntries(t *testing.T) {
	st := accountsStore(t)
	ctx := context.Background()
	record := func(by string, kind, thing string) {
		t.Helper()
		err := st.Audited(ctx, store.Actor{Subject: by, Via: store.ViaBrowser}, func(_ *db.Queries, au *store.Audit) error {
			return au.Record(store.Change{Kind: kind, Thing: thing, After: map[string]string{"name": thing}})
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now().Add(-time.Second)
	record("1", store.AuditSystem, "1")
	record("2", store.AuditTalkgroup, "1/1001")
	record("1", store.AuditTalkgroup, "1/1001")
	record("2", store.AuditTalkgroup, "1/1002")
	record("1", store.AuditUnit, "1/5001")

	things := func(f store.AuditFilter) []string {
		t.Helper()
		if f.Limit == 0 {
			f.Limit = 100
		}
		es, err := st.AuditEntries(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range es {
			out = append(out, e.By.Subject+":"+e.Thing)
		}
		return out
	}
	eq := func(name string, got []string, want ...string) {
		t.Helper()
		if len(got) != len(want) {
			t.Errorf("%s: %q, want %q", name, got, want)
			return
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%s: %q, want %q", name, got, want)
				return
			}
		}
	}
	eq("all, newest first", things(store.AuditFilter{}), "1:1/5001", "2:1/1002", "1:1/1001", "2:1/1001", "1:1")
	eq("who", things(store.AuditFilter{Subject: "2"}), "2:1/1002", "2:1/1001")
	eq("kind", things(store.AuditFilter{Kind: store.AuditTalkgroup}), "2:1/1002", "1:1/1001", "2:1/1001")
	eq("thing", things(store.AuditFilter{Kind: store.AuditTalkgroup, Thing: "1/1001"}), "1:1/1001", "2:1/1001")
	eq("since now", things(store.AuditFilter{Since: time.Now().Add(time.Second)}))
	eq("until the start", things(store.AuditFilter{Until: start}))
	eq("since the start", things(store.AuditFilter{Since: start, Kind: store.AuditUnit}), "1:1/5001")

	// Pages of two, each after the last one's last entry.
	var pages []string
	f := store.AuditFilter{Limit: 2}
	for range 5 {
		es, err := st.AuditEntries(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		if len(es) == 0 {
			break
		}
		for _, e := range es {
			pages = append(pages, e.By.Subject+":"+e.Thing)
		}
		last := es[len(es)-1]
		f.AfterAt, f.AfterID = last.At, last.ID
	}
	eq("paged", pages, "1:1/5001", "2:1/1002", "1:1/1001", "2:1/1001", "1:1")
}

// TestIntegrationRoleChangesAudited: a role change is recorded with the
// account's name as it was; a refused one records nothing.
func TestIntegrationRoleChangesAudited(t *testing.T) {
	st := accountsStore(t)
	ctx := context.Background()
	signIn(t, st, "1", "owner@example.test", true, "owner@example.test")
	signIn(t, st, "2", "two@example.test", true, "")
	if _, err := st.SetAccountRole(ctx, accountsIssuer, by("1"), "2", store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][2]string{{"1", "1"}, {"1", "9"}, {"3", "2"}} {
		if _, err := st.SetAccountRole(ctx, accountsIssuer, by(c[0]), c[1], store.RoleListener); err == nil {
			t.Errorf("%s changing %s: no refusal", c[0], c[1])
		}
	}
	// The same role again changes nothing, and records nothing.
	if _, err := st.SetAccountRole(ctx, accountsIssuer, by("1"), "2", store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	es, err := st.AuditEntries(ctx, store.AuditFilter{Limit: 10})
	if err != nil || len(es) != 1 {
		t.Fatalf("entries %+v, %v; want one", es, err)
	}
	e := es[0]
	if e.By.Subject != "1" || e.By.Via != store.ViaBrowser || e.Kind != store.AuditAccount || e.Thing != "2" ||
		e.Label != "Made Up 2" || e.Action != store.Changed || len(e.Changes) != 1 ||
		string(e.Changes[0].Before) != `"listener"` || string(e.Changes[0].After) != `"admin"` {
		t.Errorf("entry %+v", e)
	}
}
