package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/oidcclient/oidctest"
	"github.com/RIKIKU/RadioArchive/internal/server"
)

// The audit log's harness (SPEC.md R54; ADR 0029), as A10's is for the
// gate: auditProbes holds, for each state-changing admin route, a change
// through it and a refused one. TestIntegrationAuditHarness fails while
// any state-changing admin route the binary serves has no probe, so a new
// admin write joins the harness to exist (issue #118: later slices add
// theirs).
type auditProbe struct {
	// pattern is the route's, as the route table names it.
	pattern string
	// change makes a change through the route; want is the entry it makes.
	change func(w *auditWorld) (status int, body string)
	want   auditWant
	// via is how the change was made: browser unless said (the import's
	// are import, ADR 0030).
	via string
	// refused is a request through the route that changes nothing (a
	// refusal, or a change to what is already so): it adds no entry.
	refused func(w *auditWorld) (status int, body string)
}

type auditWant struct {
	kind, thing, action string
	// field is one field the entry must record, with its JSON before and
	// after.
	field, before, after string
}

// importSetup previews an import of setup and applies it with the
// preview's plan, or with plan when it isn't "": the import's two steps
// (ADR 0030).
func (w *auditWorld) importSetup(setup, plan string) (int, string) {
	w.t.Helper()
	if plan == "" {
		code, b := w.do("POST", "/import", `{"dry_run":true,"setup":`+setup+`}`)
		var rep server.ImportReport
		if code != 200 || json.Unmarshal([]byte(b), &rep) != nil {
			return code, b
		}
		plan = rep.Plan
	}
	return w.do("POST", "/import", `{"plan":"`+plan+`","setup":`+setup+`}`)
}

// importRename is an import renaming 1001.
const importRename = `{"format":"radioarchive-setup","version":1,"systems":[{"id":1,"talkgroups":[{"id":1001,"name":"Dispatch Imported","state":"public"}]}]}`

// auditWorld is the binary, its first admin signed in with a browser, a
// listener, and the recorder Bench (the shell's, R10).
type auditWorld struct {
	t        *testing.T
	base     string
	admin    *http.Client
	listener *http.Client
	group    string // the group the probes create
}

func (w *auditWorld) do(method, path, body string) (int, string) {
	w.t.Helper()
	return call(w.t, w.admin, w.base, method, "/api/v1/admin"+path, body)
}

// tagID is the ID of the tag named name.
func (w *auditWorld) tagID(name string) string {
	w.t.Helper()
	_, b := w.do("GET", "/tags", "")
	var tags []server.Label
	_ = json.Unmarshal([]byte(b), &tags)
	for _, x := range tags {
		if x.Name == name {
			return fmt.Sprint(x.ID)
		}
	}
	w.t.Fatalf("no tag %q in %s", name, b)
	return ""
}

// The probes, in the order they run: each finds what the ones before made.
var auditProbes = []auditProbe{
	{pattern: "POST /api/v1/admin/systems",
		change: func(w *auditWorld) (int, string) {
			return w.do("POST", "/systems", `{"id":1,"label":"EX","name":"Example network"}`)
		},
		want: auditWant{"system", "1", "created", "label", `null`, `"EX"`},
		refused: func(w *auditWorld) (int, string) {
			return w.do("POST", "/systems", `{"id":1,"label":"EX again"}`)
		}},
	{pattern: "PATCH /api/v1/admin/systems/{system}",
		change: func(w *auditWorld) (int, string) {
			return w.do("PATCH", "/systems/1", `{"name":"Example Network North"}`)
		},
		want:    auditWant{"system", "1", "changed", "name", `"Example network"`, `"Example Network North"`},
		refused: func(w *auditWorld) (int, string) { return w.do("PATCH", "/systems/999", `{"name":"Nowhere"}`) }},
	{pattern: "PUT /api/v1/admin/systems/{system}/talkgroups/{talkgroup}",
		change: func(w *auditWorld) (int, string) {
			return w.do("PUT", "/systems/1/talkgroups/1001", `{"label":"DISP N","name":"Dispatch North","tag":"dispatch"}`)
		},
		want: auditWant{"talkgroup", "1/1001", "created", "name", `null`, `"Dispatch North"`},
		refused: func(w *auditWorld) (int, string) {
			return w.do("PUT", "/systems/1/talkgroups/1002", `{"label":"No tag"}`)
		}},
	{pattern: "PATCH /api/v1/admin/systems/{system}/talkgroups/{talkgroup}",
		change: func(w *auditWorld) (int, string) {
			return w.do("PATCH", "/systems/1/talkgroups/1001", `{"label":"DISP North","state":"hidden"}`)
		},
		want: auditWant{"talkgroup", "1/1001", "changed", "label", `"DISP N"`, `"DISP North"`},
		refused: func(w *auditWorld) (int, string) {
			return w.do("PATCH", "/systems/1/talkgroups/1001", `{"label":"Secret","state":"secret"}`)
		}},
	{pattern: "POST /api/v1/admin/import",
		change: func(w *auditWorld) (int, string) { return w.importSetup(importRename, "") },
		want:   auditWant{"talkgroup", "1/1001", "changed", "name", `"Dispatch North"`, `"Dispatch Imported"`},
		via:    "import",
		refused: func(w *auditWorld) (int, string) { // a plan that isn't the preview's
			return w.importSetup(strings.Replace(importRename, "Imported", "Imported again", 1), strings.Repeat("0", 64))
		}},
	{pattern: "PUT /api/v1/admin/systems/{system}/units/{unit}",
		change: func(w *auditWorld) (int, string) {
			return w.do("PUT", "/systems/1/units/5001", `{"alias":"Engine One"}`)
		},
		want: auditWant{"unit", "1/5001", "created", "alias", `null`, `"Engine One"`},
		refused: func(w *auditWorld) (int, string) {
			return w.do("PUT", "/systems/1/units/5001", `{"alias":"Engine;One"}`)
		}},
	{pattern: "DELETE /api/v1/admin/systems/{system}/units/{unit}",
		change:  func(w *auditWorld) (int, string) { return w.do("DELETE", "/systems/1/units/5001", "") },
		want:    auditWant{"unit", "1/5001", "deleted", "alias", `"Engine One"`, `null`},
		refused: func(w *auditWorld) (int, string) { return w.do("DELETE", "/systems/1/units/5001", "") }},
	{pattern: "POST /api/v1/admin/groups",
		change: func(w *auditWorld) (int, string) {
			code, b := w.do("POST", "/groups", `{"name":"Northern brigades","alert":"beep"}`)
			var g server.Label
			_ = json.Unmarshal([]byte(b), &g)
			w.group = fmt.Sprint(g.ID)
			return code, b
		},
		want:    auditWant{"group", "", "created", "name", `null`, `"Northern brigades"`},
		refused: func(w *auditWorld) (int, string) { return w.do("POST", "/groups", `{"name":"Northern brigades"}`) }},
	{pattern: "PATCH /api/v1/admin/groups/{group}",
		change: func(w *auditWorld) (int, string) {
			return w.do("PATCH", "/groups/"+w.group, `{"name":"Northern stations"}`)
		},
		want:    auditWant{"group", "", "changed", "name", `"Northern brigades"`, `"Northern stations"`},
		refused: func(w *auditWorld) (int, string) { return w.do("PATCH", "/groups/999999", `{"name":"Nobody"}`) }},
	{pattern: "PATCH /api/v1/admin/tags/{tag}",
		change: func(w *auditWorld) (int, string) {
			return w.do("PATCH", "/tags/"+w.tagID("dispatch"), `{"led_colour":"blue"}`)
		},
		want: auditWant{"tag", "", "changed", "led_colour", `null`, `"blue"`},
		refused: func(w *auditWorld) (int, string) {
			return w.do("PATCH", "/tags/"+w.tagID("dispatch"), `{"led_colour":"plaid"}`)
		}},
	{pattern: "PUT /api/v1/admin/recorders/{recorder}/attachments/{short_name}",
		change: func(w *auditWorld) (int, string) {
			return w.do("PUT", "/recorders/Bench/attachments/ex", `{"system_id":1}`)
		},
		want: auditWant{"attachment", "Bench/ex", "created", "system_label", `null`, `"EX"`},
		refused: func(w *auditWorld) (int, string) {
			return w.do("PUT", "/recorders/Bench/attachments/ex", `{"system_id":999}`)
		}},
	{pattern: "DELETE /api/v1/admin/recorders/{recorder}/attachments/{short_name}",
		change:  func(w *auditWorld) (int, string) { return w.do("DELETE", "/recorders/Bench/attachments/ex", "") },
		want:    auditWant{"attachment", "Bench/ex", "deleted", "system_id", `1`, `null`},
		refused: func(w *auditWorld) (int, string) { return w.do("DELETE", "/recorders/Bench/attachments/ex", "") }},
	{pattern: "PATCH /api/v1/admin/recorders/{recorder}",
		change: func(w *auditWorld) (int, string) {
			return w.do("PATCH", "/recorders/Bench", `{"quiet_after_minutes":30}`)
		},
		want: auditWant{"recorder", "Bench", "changed", "quiet_after_minutes", `null`, `30`},
		refused: func(w *auditWorld) (int, string) {
			return w.do("PATCH", "/recorders/Nobody", `{"quiet_after_minutes":30}`)
		}},
	{pattern: "PUT /api/v1/admin/retention",
		change:  func(w *auditWorld) (int, string) { return w.do("PUT", "/retention", `{"days":60}`) },
		want:    auditWant{"retention", "", "changed", "days", `30`, `60`},
		refused: func(w *auditWorld) (int, string) { return w.do("PUT", "/retention", `{"days":7}`) }}, // unconfirmed
	{pattern: "PUT /api/v1/admin/audit/retention",
		change:  func(w *auditWorld) (int, string) { return w.do("PUT", "/audit/retention", `{"days":365}`) },
		want:    auditWant{"retention", "audit", "changed", "days", `730`, `365`},
		refused: func(w *auditWorld) (int, string) { return w.do("PUT", "/audit/retention", `{"days":29}`) }}, // too short
	{pattern: "PUT /api/v1/admin/quiet",
		change:  func(w *auditWorld) (int, string) { return w.do("PUT", "/quiet", `{"window_days":14}`) },
		want:    auditWant{"retention", "quiet", "changed", "window_days", `7`, `14`},
		refused: func(w *auditWorld) (int, string) { return w.do("PUT", "/quiet", `{"window_days":91}`) }}, // too long
	{pattern: "PATCH /api/v1/admin/accounts/{subject}",
		change:  func(w *auditWorld) (int, string) { return w.do("PATCH", "/accounts/2", `{"role":"admin"}`) },
		want:    auditWant{"account", "2", "changed", "role", `"listener"`, `"admin"`},
		refused: func(w *auditWorld) (int, string) { return w.do("PATCH", "/accounts/1", `{"role":"listener"}`) }},
	{pattern: "DELETE /api/v1/admin/listeners/{subject}/sessions",
		change:  func(w *auditWorld) (int, string) { return w.do("DELETE", "/listeners/2/sessions", "") },
		want:    auditWant{"account", "2", "changed", "sessions", `1`, `0`},
		refused: func(w *auditWorld) (int, string) { return w.do("DELETE", "/listeners/2/sessions", "") }}, // none left
	// Disabling and enabling (ADR 0033): the same route as the role's.
	{pattern: "PATCH /api/v1/admin/accounts/{subject}",
		change:  func(w *auditWorld) (int, string) { return w.do("PATCH", "/accounts/3", `{"status":"disabled"}`) },
		want:    auditWant{"account", "3", "changed", "status", `"active"`, `"disabled"`},
		refused: func(w *auditWorld) (int, string) { return w.do("PATCH", "/accounts/1", `{"status":"disabled"}`) }}, // own account
	{pattern: "PATCH /api/v1/admin/accounts/{subject}",
		change:  func(w *auditWorld) (int, string) { return w.do("PATCH", "/accounts/3", `{"status":"active"}`) },
		want:    auditWant{"account", "3", "changed", "status", `"disabled"`, `"active"`},
		refused: func(w *auditWorld) (int, string) { return w.do("PATCH", "/accounts/3", `{"status":"active"}`) }}, // already so
	{pattern: "DELETE /api/v1/admin/accounts/{subject}",
		change:  func(w *auditWorld) (int, string) { return w.do("DELETE", "/accounts/3", "") },
		want:    auditWant{"account", "3", "deleted", "email", `"third@example.test"`, `null`},
		refused: func(w *auditWorld) (int, string) { return w.do("DELETE", "/accounts/1", "") }}, // own account
	{pattern: "DELETE /api/v1/admin/systems/{system}",
		change: func(w *auditWorld) (int, string) {
			if code, b := w.do("POST", "/systems", `{"id":2,"label":"EX-2","name":"Short-lived network"}`); code != 201 {
				return code, b
			}
			return w.do("DELETE", "/systems/2", "")
		},
		want:    auditWant{"system", "2", "deleted", "name", `"Short-lived network"`, `null`},
		refused: func(w *auditWorld) (int, string) { return w.do("DELETE", "/systems/1", "") }}, // in use
}

// TestIntegrationAuditHarness (R54; ADR 0029): every state-changing admin
// route the binary serves has a probe; each change made through one, with
// a browser's session, is in the audit log with who (the admin's account,
// name and email), how (browser) and when, and each refused request adds
// nothing. No log line holds a name an entry holds (A32, R90).
func TestIntegrationAuditHarness(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := oidctest.New(t, p4ClientSecret, "1", initialAdmin, "Made Up")
	p.ServeUsers(accountsToken)
	st, logs, srv, api := binaryServer(t, ctx, p.URL, environ{"INITIAL_ADMIN_EMAIL": initialAdmin, "AUTHENTIK_API_TOKEN": accountsToken})
	if _, err := st.AddRecorder(ctx, "Bench"); err != nil {
		t.Fatal(err)
	}
	admin, _ := signInAs(t, p, srv.URL, "1", initialAdmin, true)
	listener, _ := signInAs(t, p, srv.URL, "2", "listener@example.test", true)
	signInAs(t, p, srv.URL, "3", "third@example.test", true) // disabled, enabled and deleted
	if !isAdmin(t, admin, srv.URL) {
		t.Fatal("not an admin")
	}
	w := &auditWorld{t: t, base: srv.URL, admin: admin, listener: listener}

	// Every state-changing admin route has a probe, and every probe a route.
	var writes []string
	for _, r := range api.Routes() {
		if method, _, _ := strings.Cut(r.Pattern, " "); r.Kind == server.AdminRoute && method != "GET" && method != "HEAD" {
			writes = append(writes, r.Pattern)
		}
	}
	var probed []string
	for _, pr := range auditProbes {
		probed = append(probed, pr.pattern)
	}
	for _, r := range writes {
		if !slices.Contains(probed, r) {
			t.Errorf("state-changing admin route %q has no audit probe: every admin change is audited (R54), so add one to auditProbes", r)
		}
	}
	for _, pr := range probed {
		if !slices.Contains(writes, pr) {
			t.Errorf("audit probe for %q, which the binary doesn't serve", pr)
		}
	}

	newest := func() (server.AuditEntry, bool) {
		t.Helper()
		code, b := w.do("GET", "/audit?limit=1", "")
		var page server.AuditPage
		if code != 200 || json.Unmarshal([]byte(b), &page) != nil {
			t.Fatalf("the audit log: %d %s", code, b)
		}
		if len(page.Entries) == 0 {
			return server.AuditEntry{}, false
		}
		return page.Entries[0], true
	}
	for _, pr := range auditProbes {
		start := time.Now().Add(-time.Second)
		code, b := pr.change(w)
		if code >= 300 {
			t.Errorf("%s: the change = %d %s", pr.pattern, code, b)
			continue
		}
		e, ok := newest()
		wt := pr.want
		before, after := "", ""
		for _, c := range e.Changes {
			if c.Field == wt.field {
				before, after = string(c.Before), string(c.After)
			}
		}
		if !ok || e.Kind != wt.kind || (wt.thing != "" && e.Thing != wt.thing) || e.Action != wt.action ||
			before != wt.before || after != wt.after {
			t.Errorf("%s: newest entry %+v; want %s %s %s with %s %s -> %s", pr.pattern, e, wt.kind, wt.thing, wt.action,
				wt.field, wt.before, wt.after)
		}
		via := pr.via
		if via == "" {
			via = "browser"
		}
		if e.Who != (server.AuditWho{ID: "1", Name: "Made Up 1", Email: initialAdmin}) || e.Via != via ||
			e.At.Before(start) || e.At.After(time.Now().Add(time.Second)) {
			t.Errorf("%s: who %+v, how %q, when %s", pr.pattern, e.Who, e.Via, e.At)
		}
		code, b = pr.refused(w)
		if again, _ := newest(); again.ID != e.ID {
			t.Errorf("%s: a request that changed nothing (%d %s) made entry %+v", pr.pattern, code, b, again)
		}
	}

	// Reading adds nothing: every admin read, the audit log's included.
	_, b := w.do("GET", "/audit?limit=200", "")
	var page server.AuditPage
	if err := json.Unmarshal([]byte(b), &page); err != nil {
		t.Fatal(err)
	}
	for _, r := range api.Routes() {
		if method, _ := adminPath(r.Pattern); r.Kind == server.AdminRoute && method == "GET" {
			_, path := adminPath(r.Pattern)
			call(t, admin, srv.URL, method, path, "")
		}
	}
	if _, again := w.do("GET", "/audit?limit=200", ""); again != b {
		t.Errorf("reading the admin API changed the audit log")
	}

	// A32: the names entries hold aren't in any log line.
	plain := regexp.MustCompile(`^[0-9a-z_]+$`)
	var names []string
	add := func(s string) {
		if len(s) >= 4 && !plain.MatchString(s) && s != "Bench" {
			names = append(names, s)
		}
	}
	for _, e := range page.Entries {
		add(e.Label)
		add(e.Who.Name)
		add(e.Who.Email)
		for _, c := range e.Changes {
			for _, raw := range []json.RawMessage{c.Before, c.After} {
				var s string
				if json.Unmarshal(raw, &s) == nil {
					add(s)
				}
			}
		}
	}
	if !slices.Contains(names, "DISP North") || !slices.Contains(names, "Engine One") || !slices.Contains(names, "Made Up 1") {
		t.Fatalf("the check's names miss the fixture's: %q", names)
	}
	for _, n := range names {
		if strings.Contains(logs.String(), n) {
			t.Errorf("a log line holds %q, which the audit log holds", n)
		}
	}
	p4NoAddresses(t, logs)
}
