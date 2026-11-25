package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/RIKIKU/RadioArchive/internal/server"
)

// The setup's export and import (SPEC.md R52, R53, R100, A24, A25; issue
// #115, ADR 0030).

// setupDoc is a setup to import: the export's shape, with only the fields
// a test gives.
func setupDoc(systems ...map[string]any) map[string]any {
	if systems == nil {
		systems = []map[string]any{}
	}
	return map[string]any{"format": server.SetupFormat, "version": server.SetupVersion, "systems": systems}
}

// importAs sends an import and answers its status and report.
func (e *env) importAs(t *testing.T, setup any, dryRun bool, plan *string) (int, server.ImportReport, []byte) {
	t.Helper()
	body := map[string]any{"setup": setup, "dry_run": dryRun}
	if plan != nil {
		body["plan"] = *plan
	}
	code, b := e.adminReq(t, "test admin", "POST", "/import", body)
	var rep server.ImportReport
	_ = json.Unmarshal(b, &rep)
	return code, rep, b
}

// preview dry-runs an import and wants it answered.
func (e *env) preview(t *testing.T, setup any) server.ImportReport {
	t.Helper()
	code, rep, b := e.importAs(t, setup, true, nil)
	if code != 200 || !rep.DryRun || rep.Applied || len(rep.Plan) != 64 {
		t.Fatalf("dry run = %d %s", code, b)
	}
	return rep
}

// apply previews and applies an import with the preview's plan.
func (e *env) apply(t *testing.T, setup any) server.ImportReport {
	t.Helper()
	plan := e.preview(t, setup).Plan
	code, rep, b := e.importAs(t, setup, false, &plan)
	if code != 200 || !rep.Applied || rep.Plan != plan {
		t.Fatalf("apply = %d %s", code, b)
	}
	return rep
}

func (e *env) export(t *testing.T) []byte {
	t.Helper()
	return e.mustAdmin(t, "GET", "/setup", nil, 200)
}

func resultOf(rep server.ImportReport, kind, id string) (server.ImportResult, bool) {
	for _, r := range rep.Results {
		if r.Kind == kind && r.ID == id {
			return r, true
		}
	}
	return server.ImportResult{}, false
}

func changedFields(r server.ImportResult) string {
	var out []string
	for _, c := range r.Changes {
		out = append(out, c.Field+"="+string(c.Before)+">"+string(c.After))
	}
	return strings.Join(out, ",")
}

func notChangedFields(r server.ImportResult) string {
	var out []string
	for _, c := range r.NotChanged {
		out = append(out, c.Field+"="+string(c.Before)+">"+string(c.After))
	}
	return strings.Join(out, ",")
}

// a24Setup is A24's file: 1001 renamed, 1005 added, 1003 listed as public,
// 1002 (and 1004) left out, and a placeholder row labelled 1006.
func a24Setup() map[string]any {
	return setupDoc(map[string]any{"id": 1, "label": "EX", "talkgroups": []map[string]any{
		{"id": 1001, "label": "DISP North", "name": "Dispatch Northern", "tag": "dispatch", "groups": []string{"North"}},
		{"id": 1003, "label": "ADMIN", "name": "Admin", "tag": "admin", "state": "public"},
		{"id": 1005, "label": "DISP S", "name": "Dispatch South", "tag": "dispatch"},
		{"id": 1006, "label": "1006", "tag": "other"},
	}})
}

// TestIntegrationImportA24 is A24's server half: the preview shows 1
// changed (label and name), 1 new, 1003 unchanged in state, 1002 kept and
// 1006 skipped as a placeholder, and nothing changes until it's applied
// with the plan.
func TestIntegrationImportA24(t *testing.T) {
	e := newEnv(t)
	sys := e.fixture(t)
	before := e.export(t)
	entries := e.auditCount(t)

	rep := e.preview(t, a24Setup())
	if got := rep.Summary.Talkgroups; got != (server.ImportCounts{New: 1, Changed: 1, Unchanged: 1, Kept: 2, Skipped: 1}) {
		t.Errorf("talkgroups %+v", got)
	}
	if got := rep.Summary.Systems; got != (server.ImportCounts{Unchanged: 1}) {
		t.Errorf("systems %+v", got)
	}
	if got := rep.Summary.Units; got != (server.ImportCounts{Kept: 2}) {
		t.Errorf("units %+v (the file has none: both kept)", got)
	}
	r1001, _ := resultOf(rep, "talkgroup", "1001")
	if r1001.Result != "changed" || changedFields(r1001) != `label="DISP N">"DISP North",name="Dispatch North">"Dispatch Northern"` ||
		r1001.Label != "EX 1001 DISP North" || r1001.System == nil || *r1001.System != sys {
		t.Errorf("1001: %+v", r1001)
	}
	r1003, _ := resultOf(rep, "talkgroup", "1003")
	if r1003.Result != "unchanged" || len(r1003.Changes) != 0 || notChangedFields(r1003) != `state="hidden">"public"` {
		t.Errorf("1003 must stay hidden, with the file's state shown as not changed: %+v", r1003)
	}
	r1005, _ := resultOf(rep, "talkgroup", "1005")
	if r1005.Result != "new" || !strings.Contains(changedFields(r1005), `state=null>"public"`) ||
		!strings.Contains(changedFields(r1005), `name=null>"Dispatch South"`) {
		t.Errorf("1005: %+v", r1005)
	}
	if r, _ := resultOf(rep, "talkgroup", "1002"); r.Result != "kept" || r.Label != "EX 1002 FG 41" {
		t.Errorf("1002: %+v", r)
	}
	if r, _ := resultOf(rep, "talkgroup", "1006"); r.Result != "skipped" || r.Reason != "placeholder" {
		t.Errorf("1006: %+v", r)
	}

	// Nothing has changed: not the setup, not the audit log.
	if !bytes.Equal(e.export(t), before) || e.auditCount(t) != entries {
		t.Fatal("a dry run changed something")
	}
	e.wantRefusal(t, "GET", "/systems/1/talkgroups/1005", nil, 404, "talkgroup_not_found")
	// Applying needs the plan.
	if code, _, b := e.importAs(t, a24Setup(), false, nil); code != 400 || !strings.Contains(string(b), "plan_required") {
		t.Errorf("an apply without a plan = %d %s", code, b)
	}

	catalogue := func() string {
		resp, err := http.Get(e.srv.URL + "/api/v1/catalogue")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	if c := catalogue(); strings.Contains(c, "Northern") || strings.Contains(c, "Dispatch South") {
		t.Errorf("the catalogue has a dry run's changes: %s", c)
	}

	// Applied with the plan: the same results, and only them.
	applied := e.apply(t, a24Setup())
	// The catalogue's generation moved at the commit (ADR 0006): the next
	// request has the changes, and still nothing of hidden 1003.
	if c := catalogue(); !strings.Contains(c, "Dispatch Northern") || !strings.Contains(c, "Dispatch South") ||
		strings.Contains(c, `"Admin"`) || strings.Contains(c, "ADMIN") {
		t.Errorf("the catalogue after the import: %s", c)
	}
	if applied.Summary != rep.Summary {
		t.Errorf("applied %+v, previewed %+v", applied.Summary, rep.Summary)
	}
	tg := func(id int) server.Talkgroup {
		return decodeJSON[server.Talkgroup](t, e.mustAdmin(t, "GET", fmt.Sprintf("/systems/1/talkgroups/%d", id), nil, 200))
	}
	if x := tg(1001); x.Label != "DISP North" || x.Name != "Dispatch Northern" {
		t.Errorf("1001 after: %+v", x)
	}
	if x := tg(1003); x.State != "hidden" {
		t.Errorf("1003 after: %+v: an import never changes a talkgroup's state", x)
	}
	if x := tg(1005); x.State != "public" || x.Tag == nil || *x.Tag != "dispatch" || x.Unreviewed {
		t.Errorf("1005 after: %+v", x)
	}
	if x := tg(1002); x.FiregroundChannel == nil || *x.FiregroundChannel != 41 {
		t.Errorf("1002 after: %+v (kept)", x)
	}
	e.wantRefusal(t, "GET", "/systems/1/talkgroups/1006", nil, 404, "talkgroup_not_found")

	// In the audit log: two entries, who, how (import), and each change.
	if n := e.auditCount(t) - entries; n != 2 {
		t.Errorf("%d entries, want 2 (1001 changed, 1005 created)", n)
	}
	page := e.audit(t, "limit=2")
	for _, x := range page.Entries {
		if x.Via != "import" || x.Who != (server.AuditWho{ID: "test admin", Name: "Test Admin", Email: "test.admin@example.test"}) {
			t.Errorf("entry %+v: want who the test admin, how import", x)
		}
	}
	if x := page.Entries[1]; x.Thing != "1/1001" || x.Action != "changed" || len(x.Changes) != 2 {
		t.Errorf("1001's entry: %+v", x)
	}
	if x := page.Entries[0]; x.Thing != "1/1005" || x.Action != "created" {
		t.Errorf("1005's entry: %+v", x)
	}

	// The same file again changes nothing, and writes nothing.
	entries = e.auditCount(t)
	again := e.apply(t, a24Setup())
	if s := again.Summary.Talkgroups; s.New != 0 || s.Changed != 0 || s.Unchanged != 3 || s.Skipped != 1 {
		t.Errorf("again: %+v", s)
	}
	if e.auditCount(t) != entries {
		t.Error("an import that changes nothing wrote entries")
	}
}

// TestIntegrationImportChanged: a change between the dry run and the apply
// answers import_changed with the results now, and applies nothing.
func TestIntegrationImportChanged(t *testing.T) {
	e := newEnv(t)
	e.fixture(t)
	plan := e.preview(t, a24Setup()).Plan
	e.mustAdmin(t, "PATCH", "/systems/1/talkgroups/1001", map[string]any{"name": "Dispatch Northern"}, 200)
	entries := e.auditCount(t)
	before := e.export(t)
	code, rep, b := e.importAs(t, a24Setup(), false, &plan)
	if code != 409 || rep.Error != "import_changed" || rep.Message == "" || rep.Applied || rep.Plan == plan {
		t.Fatalf("apply after a change = %d %s", code, b)
	}
	if r, _ := resultOf(rep, "talkgroup", "1001"); changedFields(r) != `label="DISP N">"DISP North"` {
		t.Errorf("the results now: 1001 %+v", r)
	}
	if !bytes.Equal(e.export(t), before) || e.auditCount(t) != entries {
		t.Error("import_changed applied something")
	}
	// With the new plan, it applies.
	code, _, b = e.importAs(t, a24Setup(), false, &rep.Plan)
	if code != 200 {
		t.Errorf("apply with the new plan = %d %s", code, b)
	}
}

// TestIntegrationImportSkips: placeholder, analog and conventional rows are
// skipped and reported (R52, section 4), whether or not the talkgroup
// exists, and change nothing.
func TestIntegrationImportSkips(t *testing.T) {
	e := newEnv(t)
	e.fixture(t)
	rep := e.apply(t, setupDoc(map[string]any{"id": 1, "talkgroups": []map[string]any{
		{"id": 2001, "label": "ANLG", "name": "Analog ops", "tag": "ops", "type": "A"},
		{"id": 2002, "label": "CONV", "name": "Simplex", "tag": "ops", "type": "Conventional", "frequency": 151625000},
		{"id": 2003, "label": "02003", "tag": "ops"},
		{"id": 1001, "label": "1001", "name": "Renamed by a placeholder", "tag": "dispatch"},
		{"id": 2004, "label": "TDMA", "name": "Phase two", "tag": "ops", "type": "T", "frequency": 851012500},
	}}))
	want := map[string]string{"2001": "analog", "2002": "conventional", "2003": "placeholder", "1001": "placeholder"}
	for id, reason := range want {
		if r, _ := resultOf(rep, "talkgroup", id); r.Result != "skipped" || r.Reason != reason {
			t.Errorf("%s: %+v, want skipped as %s", id, r, reason)
		}
	}
	if s := rep.Summary.Talkgroups; s.Skipped != 4 || s.New != 1 {
		t.Errorf("%+v", s)
	}
	x := decodeJSON[server.Talkgroup](t, e.mustAdmin(t, "GET", "/systems/1/talkgroups/2004", nil, 200))
	if x.Type != "T" || x.Frequency == nil || *x.Frequency != 851012500 {
		t.Errorf("a P25 phase 2 row is imported with its type and frequency: %+v", x)
	}
	if x := decodeJSON[server.Talkgroup](t, e.mustAdmin(t, "GET", "/systems/1/talkgroups/1001", nil, 200)); x.Name != "Dispatch North" {
		t.Errorf("a placeholder row changed 1001: %+v", x)
	}
	e.wantRefusal(t, "GET", "/systems/1/talkgroups/2001", nil, 404, "talkgroup_not_found")
}

// TestIntegrationImportFireground (R100, A25's rule): a file giving two
// talkgroups one number, or giving 1001 the 41 that 1002 keeps, stops the
// import, names the talkgroups and changes nothing; moving numbers between
// talkgroups in one file works.
func TestIntegrationImportFireground(t *testing.T) {
	e := newEnv(t)
	e.fixture(t)
	before, entries := e.export(t), e.auditCount(t)
	for name, c := range map[string]struct {
		rows  []map[string]any
		names []string
	}{
		"twice in the file": {[]map[string]any{
			{"id": 3001, "label": "FG 60A", "name": "Fireground 60 A", "tag": "fireground", "fireground_channel": 60},
			{"id": 3002, "label": "FG 60B", "name": "Fireground 60 B", "tag": "fireground", "fireground_channel": "060"},
		}, []string{`talkgroup 3001 "Fireground 60 A" in system EX`, `talkgroup 3002 "Fireground 60 B" in system EX`}},
		"one 1002 keeps": {[]map[string]any{
			{"id": 1001, "label": "DISP N", "fireground_channel": 41},
		}, []string{`talkgroup 1001 "Dispatch North" in system EX`, `talkgroup 1002 "Fireground 41" in system EX`}},
	} {
		for _, dry := range []bool{true, false} {
			plan := strings.Repeat("0", 64)
			code, _, b := e.importAs(t, setupDoc(map[string]any{"id": 1, "talkgroups": c.rows}), dry, &plan)
			var r struct{ Error, Message string }
			_ = json.Unmarshal(b, &r)
			if code != 409 || r.Error != "fireground_channel_taken" {
				t.Errorf("%s (dry run %v) = %d %s", name, dry, code, b)
			}
			for _, n := range c.names {
				if !strings.Contains(r.Message, n) {
					t.Errorf("%s: %q doesn't name %s", name, r.Message, n)
				}
			}
		}
	}
	if !bytes.Equal(e.export(t), before) || e.auditCount(t) != entries {
		t.Error("a refused import changed something")
	}

	// 1001 takes 41 while 1002 moves to 42: one file, one transaction.
	e.apply(t, setupDoc(map[string]any{"id": 1, "talkgroups": []map[string]any{
		{"id": 1001, "fireground_channel": 41}, {"id": 1002, "fireground_channel": 42},
	}}))
	for id, n := range map[int]int32{1001: 41, 1002: 42} {
		x := decodeJSON[server.Talkgroup](t, e.mustAdmin(t, "GET", fmt.Sprintf("/systems/1/talkgroups/%d", id), nil, 200))
		if x.FiregroundChannel == nil || *x.FiregroundChannel != n {
			t.Errorf("%d: %+v, want fireground channel %d", id, x, n)
		}
	}
	// null takes a number away.
	e.apply(t, setupDoc(map[string]any{"id": 1, "talkgroups": []map[string]any{{"id": 1001, "fireground_channel": nil}}}))
	if x := decodeJSON[server.Talkgroup](t, e.mustAdmin(t, "GET", "/systems/1/talkgroups/1001", nil, 200)); x.FiregroundChannel != nil {
		t.Errorf("1001: %+v", x)
	}
}

// TestIntegrationImportRoundTrip is A24's round trip: the fixture's export,
// with every field set somewhere, imported into an empty deployment,
// exports identically, byte for byte. Hidden 1003 comes back hidden.
func TestIntegrationImportRoundTrip(t *testing.T) {
	e := newEnv(t)
	sys := e.fixture(t)
	ctx := context.Background()
	// Every R22 field, and the groups', tags' and systems' settings.
	e.mustAdmin(t, "PATCH", fmt.Sprintf("/systems/%d", sys), map[string]any{"auto_create": true, "alert": "chime",
		"led_colour": "green", "sort_order": 3}, 200)
	e.mustAdmin(t, "PATCH", fmt.Sprintf("/systems/%d/talkgroups/1001", sys), map[string]any{"delay_minutes": 5,
		"alert": "beep", "led_colour": "red", "sort_order": 2, "type": "D", "frequency": 851012500,
		"groups": []string{"North", "east", "Zulu"}}, 200)
	e.mustAdmin(t, "POST", "/groups", map[string]any{"name": "Unused group", "alert": "pip", "sort_order": 9}, 201)
	g := decodeJSON[[]server.Label](t, e.mustAdmin(t, "GET", "/groups", nil, 200))
	for _, x := range g {
		if x.Name == "east" {
			e.mustAdmin(t, "PATCH", fmt.Sprintf("/groups/%d", x.ID), map[string]any{"led_colour": "cyan", "sort_order": -1}, 200)
		}
	}
	tags := decodeJSON[[]server.Label](t, e.mustAdmin(t, "GET", "/tags", nil, 200))
	for _, x := range tags {
		if x.Name == "fireground" {
			e.mustAdmin(t, "PATCH", fmt.Sprintf("/tags/%d", x.ID), map[string]any{"alert": "none", "led_colour": "orange", "sort_order": 4}, 200)
		}
	}
	// An auto-created talkgroup: unreviewed, with no tag.
	if _, err := e.st.Pool.Exec(ctx, `INSERT INTO talkgroups (system_id, talkgroup_id, unreviewed) VALUES ($1, 4711, true)`, sys); err != nil {
		t.Fatal(err)
	}
	first := e.export(t)
	var s server.Setup
	if err := json.Unmarshal(first, &s); err != nil || s.Format != "radioarchive-setup" || s.Version != 1 ||
		len(s.Systems) != 1 || len(s.Systems[0].Talkgroups) != 5 || len(s.Systems[0].Units) != 2 {
		t.Fatalf("the export: %v %s", err, first)
	}
	if !bytes.Equal(e.export(t), first) {
		t.Fatal("two exports of one setup differ")
	}

	empty := newEnv(t)
	var doc any
	_ = json.Unmarshal(first, &doc)
	rep := empty.apply(t, doc)
	if s := rep.Summary; s.Talkgroups.New != 5 || s.Units.New != 2 || s.Systems.New != 1 || s.Groups.New != 4 ||
		s.Tags.New != 4 {
		t.Errorf("into an empty deployment: %+v", s)
	}
	if second := empty.export(t); !bytes.Equal(second, first) {
		t.Errorf("the second export differs:\n%s\nfirst:\n%s", second, first)
	}
	x := decodeJSON[server.Talkgroup](t, empty.mustAdmin(t, "GET", "/systems/1/talkgroups/1003", nil, 200))
	if x.State != "hidden" {
		t.Errorf("1003 came back %s", x.State)
	}
	// Into the deployment it came from, nothing changes.
	again := e.apply(t, doc)
	for _, c := range []server.ImportCounts{again.Summary.Systems, again.Summary.Talkgroups, again.Summary.Units,
		again.Summary.Groups, again.Summary.Tags} {
		if c.New != 0 || c.Changed != 0 || c.Kept != 0 || c.Skipped != 0 {
			t.Errorf("re-importing its own export: %+v", again.Summary)
		}
	}
}

// TestIntegrationImportMerge: a new system is created (R11); new units are
// added and aliases changed, units left out kept; groups and tags by name,
// created on first use; settings of what exists left as they are.
func TestIntegrationImportMerge(t *testing.T) {
	e := newEnv(t)
	e.fixture(t)
	rep := e.apply(t, map[string]any{"format": server.SetupFormat, "version": server.SetupVersion,
		"systems": []map[string]any{
			{"id": 1, "label": "Renamed", "alert": "tone", "units": []map[string]any{
				{"from": 5001, "alias": "Engine One"}, {"from": 5002, "to": 5002, "alias": "Engine 2"},
				{"from": 5100, "to": 5199, "alias": "Portables"}}},
			{"id": 7, "label": "EX-7", "name": "Seventh network", "talkgroups": []map[string]any{
				{"id": 1001, "label": "OPS", "name": "Operations", "tag": "ops", "groups": []string{"Seven", "North"}}}},
		},
		"groups": []map[string]any{{"name": "Seven", "led_colour": "purple"}, {"name": "North", "led_colour": "blue"}},
	})
	if r, _ := resultOf(rep, "system", "1"); r.Result != "unchanged" ||
		notChangedFields(r) != `label="EX">"Renamed",alert=null>"tone"` {
		t.Errorf("system 1: %+v", r)
	}
	if r, _ := resultOf(rep, "system", "7"); r.Result != "new" {
		t.Errorf("system 7: %+v", r)
	}
	if s := rep.Summary.Units; s != (server.ImportCounts{New: 1, Changed: 1, Unchanged: 1}) {
		t.Errorf("units %+v", s)
	}
	if r, _ := resultOf(rep, "group", "North"); r.Result != "unchanged" || notChangedFields(r) != `led_colour=null>"blue"` {
		t.Errorf("group North: %+v", r)
	}
	if r, _ := resultOf(rep, "group", "Seven"); r.Result != "new" {
		t.Errorf("group Seven: %+v", r)
	}
	if r, _ := resultOf(rep, "tag", "dispatch"); r.Result != "kept" {
		t.Errorf("tag dispatch: %+v", r)
	}
	sys1 := decodeJSON[server.System](t, e.mustAdmin(t, "GET", "/systems/1", nil, 200))
	if sys1.Label != "EX" || sys1.Alert != nil {
		t.Errorf("system 1 was changed: %+v", sys1)
	}
	tg := decodeJSON[server.Talkgroup](t, e.mustAdmin(t, "GET", "/systems/7/talkgroups/1001", nil, 200))
	if tg.Name != "Operations" || len(tg.Groups) != 2 || tg.Tag == nil || *tg.Tag != "ops" {
		t.Errorf("system 7's 1001: %+v", tg)
	}
	u := decodeJSON[server.Unit](t, e.mustAdmin(t, "GET", "/systems/1/units/5001", nil, 200))
	if u.Alias != "Engine One" {
		t.Errorf("5001: %+v", u)
	}
	for _, g := range decodeJSON[[]server.Label](t, e.mustAdmin(t, "GET", "/groups", nil, 200)) {
		if (g.Name == "Seven") != (g.LEDColour != nil && *g.LEDColour == "purple") {
			t.Errorf("group %+v: the new group has the file's settings, North keeps its own", g)
		}
	}
}

// TestIntegrationImportRefusals: what the import refuses, changing
// nothing.
func TestIntegrationImportRefusals(t *testing.T) {
	e := newEnv(t)
	e.fixture(t)
	before, entries := e.export(t), e.auditCount(t)
	sys := func(rows ...map[string]any) map[string]any {
		return setupDoc(map[string]any{"id": 1, "talkgroups": rows})
	}
	many := make([]map[string]any, server.MaxImportRows+1)
	for i := range many {
		many[i] = map[string]any{"from": i + 1, "alias": ""}
	}
	for _, c := range []struct {
		name   string
		setup  any
		status int
		code   string
	}{
		{"no format", map[string]any{"systems": []any{}}, 400, "unsupported_format"},
		{"another version", map[string]any{"format": server.SetupFormat, "version": 2, "systems": []any{}}, 400, "unsupported_format"},
		{"a field it doesn't know", setupDoc(map[string]any{"id": 1, "colour": "red"}), 400, "invalid_body"},
		{"a talkgroup twice", sys(map[string]any{"id": 1001}, map[string]any{"id": 1001}), 400, "duplicate_row"},
		{"a bad state", sys(map[string]any{"id": 1001, "state": "secret"}), 400, "invalid_state"},
		{"a bad colour", sys(map[string]any{"id": 1001, "led_colour": "plaid"}), 400, "invalid_led_colour"},
		{"a new talkgroup without a tag", sys(map[string]any{"id": 1007, "label": "X"}), 400, "tag_required"},
		{"a new system without a label", setupDoc(map[string]any{"id": 9}), 400, "label_required"},
		{"a unit's alias with ;", setupDoc(map[string]any{"id": 1, "units": []map[string]any{{"from": 5001, "alias": "a;b"}}}), 400, "invalid_alias"},
		{"too many rows", setupDoc(map[string]any{"id": 1, "units": many}), 413, "too_many_rows"},
	} {
		code, _, b := e.importAs(t, c.setup, true, nil)
		var r struct{ Error, Message string }
		_ = json.Unmarshal(b, &r)
		if code != c.status || r.Error != c.code || r.Message == "" {
			t.Errorf("%s = %d %s, want %d %s", c.name, code, b, c.status, c.code)
		}
	}
	e.wantRefusal(t, "POST", "/import", map[string]any{"dry_run": true}, 400, "setup_required")
	big := map[string]any{"setup": setupDoc(), "dry_run": true, "pad": strings.Repeat("x", server.MaxImportBytes)}
	e.wantRefusal(t, "POST", "/import", big, 413, "body_too_large")
	if !bytes.Equal(e.export(t), before) || e.auditCount(t) != entries {
		t.Error("a refused import changed something")
	}
	// Admins only.
	for _, p := range []string{"GET /setup", "POST /import"} {
		method, path, _ := strings.Cut(p, " ")
		if code, _ := e.adminReq(t, "", method, path, map[string]any{}); code != 401 {
			t.Errorf("%s without sign-in = %d", p, code)
		}
	}
}
