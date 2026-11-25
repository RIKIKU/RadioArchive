package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/server"
)

// The audit log (SPEC.md R12, R54, A26; ADR 0029). cmd/radioarchive's
// TestIntegrationAuditHarness drives every state-changing admin route of
// the binary through a real sign-in; these check the store path's "how",
// the read API and what never adds an entry.

// audit reads the audit log with this query.
func (e *env) audit(t *testing.T, query string) server.AuditPage {
	t.Helper()
	return decodeJSON[server.AuditPage](t, e.mustAdmin(t, "GET", "/audit?"+query, nil, 200))
}

func (e *env) auditCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// asModule calls the admin API as the PowerShell module would: a bearer
// token (testAdmin's stand-in for issue #116's), not a session.
func (e *env) asModule(t *testing.T, who, method, path string, body any) int {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, e.srv.URL+server.AdminPath+path, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+who)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func change(e server.AuditEntry, field string) (before, after string) {
	for _, c := range e.Changes {
		if c.Field == field {
			return string(c.Before), string(c.After)
		}
	}
	return "", ""
}

// TestIntegrationA26Audit is A26's server half: renaming 1001 with a
// session, hiding it as the module would and changing an attachment make
// three entries, each with who, how and when.
func TestIntegrationA26Audit(t *testing.T) {
	e := newEnv(t)
	name, _ := e.recorder(t)
	other := e.fixture(t)
	tg := fmt.Sprintf("/systems/%d/talkgroups/1001", e.sys)
	start := time.Now().Add(-time.Second)

	e.mustAdmin(t, "PATCH", tg, map[string]any{"label": "DISP North"}, 200)
	if code := e.asModule(t, "module-admin", "PATCH", tg, map[string]any{"state": "hidden"}); code != 200 {
		t.Fatalf("hiding as the module: %d", code)
	}
	e.mustAdmin(t, "PUT", "/recorders/"+url.PathEscape(name)+"/attachments/ex", map[string]any{"system_id": other}, 200)
	end := time.Now().Add(time.Second)

	page := e.audit(t, "limit=3")
	if len(page.Entries) != 3 || page.Next == nil {
		t.Fatalf("the newest three: %+v", page)
	}
	when := func(x server.AuditEntry) bool { return x.At.After(start) && x.At.Before(end) }
	att, hide, rename := page.Entries[0], page.Entries[1], page.Entries[2]
	if b, a := change(rename, "label"); rename.Kind != "talkgroup" || rename.Thing != fmt.Sprintf("%d/1001", e.sys) ||
		rename.Action != "changed" || b != `"DISP N"` || a != `"DISP North"` || len(rename.Changes) != 1 ||
		rename.Who != (server.AuditWho{ID: "test admin", Name: "Test Admin", Email: "test.admin@example.test"}) ||
		rename.Via != "browser" || !when(rename) || rename.Label != "EX 1001 DISP North" {
		t.Errorf("the rename: %+v", rename)
	}
	if b, a := change(hide, "state"); hide.Kind != "talkgroup" || b != `"public"` || a != `"hidden"` || len(hide.Changes) != 1 ||
		hide.Who.ID != "module-admin" || hide.Who.Name != "Test Admin module-admin" || hide.Via != "module" || !when(hide) {
		t.Errorf("hiding: %+v", hide)
	}
	if b, a := change(att, "system_id"); att.Kind != "attachment" || att.Thing != name+"/ex" || att.Action != "changed" ||
		b != fmt.Sprint(e.sys) || a != fmt.Sprint(other) || att.Via != "browser" || att.Who.ID != "test admin" ||
		!when(att) || att.Label != name+" / ex" {
		t.Errorf("the attachment: %+v", att)
	}
	if b, a := change(att, "system_label"); b != `"EX"` || a != fmt.Sprintf(`"EX-%d"`, other) {
		t.Errorf("the attachment's system label: %s -> %s", b, a)
	}

	// 1001's history: made by the fixture, renamed, hidden.
	hist := e.audit(t, fmt.Sprintf("kind=talkgroup&thing=%d/1001", e.sys))
	if len(hist.Entries) != 3 || hist.Entries[2].Action != "created" || hist.Next != nil {
		t.Errorf("1001's history: %+v", hist)
	}
	if b, a := change(hist.Entries[2], "name"); b != "null" || a != `"Dispatch North"` {
		t.Errorf("1001's creation keeps its fields: name %s -> %s", b, a)
	}
	// By who: only the module's change.
	if mine := e.audit(t, "who=module-admin"); len(mine.Entries) != 1 || mine.Entries[0].ID != hide.ID {
		t.Errorf("the module admin's entries: %+v", mine)
	}
}

// TestIntegrationAuditReadAPI: paged by its cursor, filtered by time, and
// refusing what it can't read.
func TestIntegrationAuditReadAPI(t *testing.T) {
	e := newEnv(t)
	e.fixture(t) // 1 system, 4 talkgroups, 2 units
	all := e.audit(t, "")
	if len(all.Entries) != 7 || all.Next != nil {
		t.Fatalf("all: %d entries, next %v", len(all.Entries), all.Next)
	}
	var paged []int64
	q := "limit=3"
	for range 5 {
		p := e.audit(t, q)
		for _, x := range p.Entries {
			paged = append(paged, x.ID)
		}
		if p.Next == nil {
			break
		}
		q = "limit=3&cursor=" + url.QueryEscape(*p.Next)
	}
	if len(paged) != 7 {
		t.Fatalf("paged %v", paged)
	}
	for i := range paged {
		if paged[i] != all.Entries[i].ID {
			t.Errorf("paged %v, want newest first as %+v", paged, all.Entries)
			break
		}
	}
	future := url.QueryEscape(time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	if p := e.audit(t, "since="+future); len(p.Entries) != 0 {
		t.Errorf("since an hour from now: %d", len(p.Entries))
	}
	if p := e.audit(t, "until="+future+"&kind=unit"); len(p.Entries) != 2 {
		t.Errorf("units until an hour from now: %d", len(p.Entries))
	}
	for _, c := range []struct{ query, code string }{
		{"kind=calls", "invalid_kind"}, {"since=yesterday", "invalid_since"}, {"until=1", "invalid_until"},
		{"limit=0", "invalid_limit"}, {"limit=201", "invalid_limit"}, {"cursor=abc", "invalid_cursor"},
		{"order=random", "invalid_order"},
	} {
		e.wantRefusal(t, "GET", "/audit?"+c.query, nil, 400, c.code)
	}
	if code, _ := e.adminReq(t, "", "GET", "/audit", nil); code != 401 {
		t.Errorf("signed out: %d", code)
	}
}

// TestIntegrationAuditNotForReads (R34, R54): playing a recording, hidden
// or not, reading the talkgroup table, every other admin read, and the
// audit log itself add nothing. So does a refused change.
func TestIntegrationAuditNotForReads(t *testing.T) {
	e := newEnv(t)
	_, key := e.recorder(t)
	hidden, audio := e.storeCall(t, key, 1, 1003, nil)
	n := e.auditCount(t)
	if code, body := e.adminReq(t, "test admin", "GET", "/calls/"+hidden+"/recording", nil); code != 200 || !bytes.Equal(body, audio) {
		t.Fatalf("playback: %d", code)
	}
	for _, p := range []string{"/systems", fmt.Sprintf("/systems/%d", e.sys), fmt.Sprintf("/systems/%d/talkgroups", e.sys),
		fmt.Sprintf("/systems/%d/talkgroups/1003", e.sys), fmt.Sprintf("/systems/%d/talkgroups/1003/calls", e.sys),
		"/talkgroups", fmt.Sprintf("/systems/%d/units", e.sys), fmt.Sprintf("/systems/%d/units/5001", e.sys),
		"/attachments", "/rejections", "/groups", "/tags", "/retention", "/recorders", "/audit", "/audit?kind=talkgroup", "/setup",
		"/audit?order=oldest", "/audit/admins", "/audit/retention"} {
		e.mustAdmin(t, "GET", p, nil, 200)
	}
	e.wantRefusal(t, "PATCH", fmt.Sprintf("/systems/%d/talkgroups/1001", e.sys), map[string]any{"state": "secret"}, 400, "invalid_state")
	e.wantRefusal(t, "DELETE", fmt.Sprintf("/systems/%d", e.sys), nil, 409, "system_in_use")
	// A change to what it already is changes nothing.
	e.mustAdmin(t, "PATCH", fmt.Sprintf("/systems/%d/talkgroups/1001", e.sys), map[string]any{"state": "public"}, 200)
	if got := e.auditCount(t); got != n {
		t.Errorf("%d entries after reads and refusals, was %d", got, n)
	}
}

// TestIntegrationAuditOldestFirstAndWho (ADR 0032): order=oldest pages the
// other way with its own cursor, and /audit/admins lists everyone with an
// entry, by their latest name.
func TestIntegrationAuditOldestFirstAndWho(t *testing.T) {
	e := newEnv(t)
	sys := e.fixture(t)
	tg := fmt.Sprintf("/systems/%d/talkgroups/1001", sys)
	if code := e.asModule(t, "module-admin", "PATCH", tg, map[string]any{"state": "hidden"}); code != 200 {
		t.Fatalf("hiding as the module: %d", code)
	}
	newest := e.audit(t, "limit=200")
	var paged []int64
	q := "order=oldest&limit=3"
	for range 5 {
		p := e.audit(t, q)
		for _, x := range p.Entries {
			paged = append(paged, x.ID)
		}
		if p.Next == nil {
			break
		}
		q = "order=oldest&limit=3&cursor=" + url.QueryEscape(*p.Next)
	}
	if len(paged) != len(newest.Entries) {
		t.Fatalf("oldest first %v, newest first %d", paged, len(newest.Entries))
	}
	for i, id := range paged {
		if want := newest.Entries[len(newest.Entries)-1-i].ID; id != want {
			t.Errorf("oldest first %v: entry %d is %d, want %d", paged, i, id, want)
			break
		}
	}
	who := decodeJSON[[]server.AuditWho](t, e.mustAdmin(t, "GET", "/audit/admins", nil, 200))
	if len(who) != 2 || who[0] != (server.AuditWho{ID: "test admin", Name: "Test Admin", Email: "test.admin@example.test"}) ||
		who[1].ID != "module-admin" || who[1].Name != "Test Admin module-admin" {
		t.Errorf("who: %+v", who)
	}
	if n := e.auditCount(t); n != len(newest.Entries) {
		t.Errorf("reading added entries: %d, was %d", n, len(newest.Entries))
	}
}

// backdate adds an entry of n days ago, as an admin's change long ago, for
// the audit log's retention.
func (e *env) backdate(t *testing.T, days int, label string) {
	t.Helper()
	if _, err := e.st.Pool.Exec(context.Background(), `
INSERT INTO audit_log (at, issuer, subject, admin_name, admin_email, via, kind, thing, label, action, changes)
VALUES (now() - make_interval(days => $1), 'https://issuer.example.test', 'old admin', 'Old Admin', 'old@example.test',
        'browser', 'talkgroup', '1/1001', $2, 'changed', '[{"field":"label","before":"A","after":"B"}]')`, days, label); err != nil {
		t.Fatal(err)
	}
}

// TestIntegrationAuditRetention (owner, 2026-11-25; ADR 0032): the audit
// log's own period, 730 days by default, is a setting changed like any
// other, audited; shortening it asks first, naming how many entries go,
// only when some would; the nightly run deletes what's past it and says so
// in one entry of the system's; and recordings' retention never touches
// it (R54).
func TestIntegrationAuditRetention(t *testing.T) {
	e := newEnv(t)
	_, key := e.recorder(t)
	ctx := context.Background()
	got := decodeJSON[server.AuditRetention](t, e.mustAdmin(t, "GET", "/audit/retention", nil, 200))
	if got.Days != 730 || got.DefaultDays != 730 || got.MinDays != 90 || got.MaxDays != 3650 || got.NextRun.IsZero() {
		t.Errorf("the default: %+v", got)
	}
	e.backdate(t, 800, "Eight hundred days ago")
	e.backdate(t, 400, "Four hundred days ago")
	e.backdate(t, 100, "A hundred days ago")

	// Recordings' retention, however short, and the nightly run, however
	// far on by the job's clock: the entries the audit period keeps stay.
	e.era(t)
	e.storeAt(t, key, 1, 1001, eraNow.Add(-31*day))
	e.mustAdmin(t, "PUT", "/retention", map[string]any{"days": 7, "confirm": true}, 200)
	res, err := e.job.RunNightly(ctx)
	if err != nil || res.Calls != 1 || res.AuditEntries != 1 {
		t.Fatalf("nightly: %+v, %v", res, err)
	}
	all := e.audit(t, "limit=200")
	prune := all.Entries[0]
	if b, _ := change(prune, "entries"); prune.Kind != "audit" || prune.Action != "deleted" || prune.Via != "nightly" ||
		prune.Who != (server.AuditWho{ID: "system", Name: "System"}) || b != "1" || prune.Label != "Audit log" {
		t.Errorf("the prune's entry: %+v", prune)
	}
	if b, _ := change(prune, "days"); b != "730" {
		t.Errorf("the prune's period: %s", b)
	}
	for _, x := range all.Entries {
		if x.Label == "Eight hundred days ago" {
			t.Errorf("an entry past 730 days was kept: %+v", x)
		}
	}
	e.clock.set(time.Now().AddDate(10, 0, 0))
	before := e.mustAdmin(t, "GET", "/audit?limit=200", nil, 200)
	if res, err := e.job.RunNightly(ctx); err != nil || res.AuditEntries != 0 {
		t.Fatalf("nightly ten years on by the job's clock: %+v, %v", res, err)
	}
	if after := e.mustAdmin(t, "GET", "/audit?limit=200", nil, 200); !bytes.Equal(before, after) {
		t.Errorf("a nightly run with nothing past the audit period changed the log:\n%s\nwas\n%s", after, before)
	}

	// Out of range.
	for _, d := range []int{89, 3651, 0} {
		e.wantRefusal(t, "PUT", "/audit/retention", map[string]any{"days": d}, 400, "invalid_days")
	}
	// Shorter, deleting none: saved at once, audited.
	n := e.auditCount(t)
	if got := decodeJSON[server.AuditRetention](t, e.mustAdmin(t, "PUT", "/audit/retention", map[string]any{"days": 500}, 200)); got.Days != 500 {
		t.Errorf("500 days: %+v", got)
	}
	set := e.audit(t, "limit=1").Entries[0]
	if b, a := change(set, "days"); set.Kind != "retention" || set.Thing != "audit" || set.Label != "Audit log retention" ||
		b != "730" || a != "500" || set.Via != "browser" || e.auditCount(t) != n+1 {
		t.Errorf("the change's entry: %+v", set)
	}
	// Shorter, deleting some: asked first, with how many; nothing saved.
	b := e.mustAdmin(t, "PUT", "/audit/retention", map[string]any{"days": 90}, 409)
	ask := decodeJSON[server.AuditRetentionConfirm](t, b)
	if ask.Code != "confirm_shorter_audit_retention" || ask.Entries != 2 || ask.Days != 90 ||
		ask.Message != "Shortening the audit log's retention from 500 to 90 days deletes 2 entries at the next nightly run. Confirm?" {
		t.Errorf("asking: %s", b)
	}
	if got := decodeJSON[server.AuditRetention](t, e.mustAdmin(t, "GET", "/audit/retention", nil, 200)); got.Days != 500 || e.auditCount(t) != n+1 {
		t.Errorf("unconfirmed, it changed: %+v", got)
	}
	e.mustAdmin(t, "PUT", "/audit/retention", map[string]any{"days": 90, "confirm": true}, 200)
	if res, err := e.job.RunNightly(ctx); err != nil || res.AuditEntries != 2 {
		t.Fatalf("nightly at 90 days: %+v, %v", res, err)
	}
	if b, _ := change(e.audit(t, "limit=1").Entries[0], "entries"); b != "2" {
		t.Errorf("the second prune's entry: %s", b)
	}
	// Longer: saved, and nothing comes back.
	e.mustAdmin(t, "PUT", "/audit/retention", map[string]any{"days": 3650}, 200)
	if old := e.audit(t, "who="+url.QueryEscape("old admin")); len(old.Entries) != 0 {
		t.Errorf("pruned entries are back: %+v", old.Entries)
	}
	// Still no DELETE but the prune's, nor any UPDATE.
	if _, err := e.st.Pool.Exec(ctx, `DELETE FROM audit_log`); err == nil {
		t.Error("DELETE outside the prune went through")
	}
	if _, err := e.st.Pool.Exec(ctx, `UPDATE audit_log SET label = 'rewritten'`); err == nil {
		t.Error("UPDATE went through")
	}
}
