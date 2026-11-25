package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/RIKIKU/RadioArchive/internal/server"
)

// The import's update option (issue #117, ADR 0035): what exists takes
// every other field the file gives otherwise, and state never changes.

// importWith sends an import with the update option.
func (e *env) importWith(t *testing.T, setup any, update, dryRun bool, plan *string) (int, server.ImportReport, []byte) {
	t.Helper()
	body := map[string]any{"setup": setup, "dry_run": dryRun, "update": update}
	if plan != nil {
		body["plan"] = *plan
	}
	code, b := e.adminReq(t, "test admin", "POST", "/import", body)
	var rep server.ImportReport
	_ = json.Unmarshal(b, &rep)
	return code, rep, b
}

// applyUpdate previews and applies an import with update.
func (e *env) applyUpdate(t *testing.T, setup any) (preview, applied server.ImportReport) {
	t.Helper()
	code, preview, b := e.importWith(t, setup, true, true, nil)
	if code != 200 || !preview.DryRun || !preview.Update || preview.Applied {
		t.Fatalf("dry run with update = %d %s", code, b)
	}
	code, applied, b = e.importWith(t, setup, true, false, &preview.Plan)
	if code != 200 || !applied.Applied || !applied.Update || applied.Plan != preview.Plan {
		t.Fatalf("apply with update = %d %s", code, b)
	}
	return preview, applied
}

// updateSetup changes every field of what exists that an import can: on
// 1001 (public) everything, and it says hidden; on 1003 (hidden) it says
// public; on 1004 (ignored, unreviewed here) public and reviewed; and the
// system's, a group's and a tag's own fields.
func updateSetup() map[string]any {
	return map[string]any{"format": server.SetupFormat, "version": server.SetupVersion,
		"systems": []map[string]any{{"id": 1, "label": "EXU", "name": "Updated network", "auto_create": true,
			"alert": "tone", "led_colour": "green", "sort_order": 7,
			"talkgroups": []map[string]any{
				{"id": 1001, "label": "DISP N", "name": "Dispatch North", "state": "hidden", "delay_minutes": 5,
					"alert": "beep", "led_colour": "red", "type": "T", "frequency": 851012500, "sort_order": 3},
				{"id": 1003, "state": "public", "alert": "pip"},
				{"id": 1004, "state": "public", "unreviewed": false, "delay_minutes": 2},
			}}},
		"groups": []map[string]any{{"name": "North", "led_colour": "blue", "sort_order": 2}},
		"tags":   []map[string]any{{"name": "dispatch", "alert": "chime"}},
	}
}

func TestIntegrationImportUpdate(t *testing.T) {
	e := newEnv(t)
	sys := e.fixture(t)
	if _, err := e.st.Pool.Exec(context.Background(),
		`UPDATE talkgroups SET unreviewed = true WHERE system_id = $1 AND talkgroup_id = 1004`, sys); err != nil {
		t.Fatal(err)
	}
	tg := func(id int) server.Talkgroup {
		return decodeJSON[server.Talkgroup](t, e.mustAdmin(t, "GET", fmt.Sprintf("/systems/1/talkgroups/%d", id), nil, 200))
	}

	// Without update: R52's fields only, the rest listed as not changed.
	plain := e.preview(t, updateSetup())
	if plain.Update {
		t.Error("a preview without update says update")
	}
	if r, _ := resultOf(plain, "talkgroup", "1001"); r.Result != "changed" || changedFields(r) != `sort_order=0>3` {
		t.Errorf("1001 without update: %+v", r)
	}
	if r, _ := resultOf(plain, "system", "1"); r.Result != "unchanged" || len(r.NotChanged) != 6 {
		t.Errorf("system 1 without update: %+v", r)
	}
	// A plan made without update doesn't apply with it: the plan covers it.
	before, entries := e.export(t), e.auditCount(t)
	code, rep, b := e.importWith(t, updateSetup(), true, false, &plain.Plan)
	if code != 409 || rep.Error != "import_changed" || !rep.Update {
		t.Fatalf("an update applied with a plain plan = %d %s", code, b)
	}
	if !bytes.Equal(e.export(t), before) || e.auditCount(t) != entries {
		t.Fatal("import_changed applied something")
	}

	// With update: every field, but never state or unreviewed.
	preview, applied := e.applyUpdate(t, updateSetup())
	r1001, _ := resultOf(preview, "talkgroup", "1001")
	if r1001.Result != "changed" || notChangedFields(r1001) != `state="public">"hidden"` {
		t.Errorf("1001 with update: %+v", r1001)
	}
	for _, f := range []string{`delay_minutes=0>5`, `alert=null>"beep"`, `led_colour=null>"red"`, `type="">"T"`,
		`frequency=null>851012500`, `sort_order=0>3`} {
		if !strings.Contains(changedFields(r1001), f) {
			t.Errorf("1001's changes %s lack %s", changedFields(r1001), f)
		}
	}
	if strings.Contains(changedFields(r1001), "state") {
		t.Errorf("1001's state changed: %s", changedFields(r1001))
	}
	if r, _ := resultOf(preview, "talkgroup", "1003"); r.Result != "changed" || changedFields(r) != `alert=null>"pip"` ||
		notChangedFields(r) != `state="hidden">"public"` {
		t.Errorf("1003 with update: %+v", r)
	}
	if r, _ := resultOf(preview, "talkgroup", "1004"); r.Result != "changed" || changedFields(r) != `delay_minutes=0>2` ||
		notChangedFields(r) != `state="ignored">"public",unreviewed=true>false` {
		t.Errorf("1004 with update: %+v", r)
	}
	if r, _ := resultOf(preview, "system", "1"); r.Result != "changed" || len(r.NotChanged) != 0 ||
		changedFields(r) != `label="EX">"EXU",name="Example network">"Updated network",auto_create=false>true,alert=null>"tone",led_colour=null>"green",sort_order=0>7` {
		t.Errorf("system 1 with update: %+v", r)
	}
	if r, _ := resultOf(preview, "group", "North"); r.Result != "changed" || changedFields(r) != `led_colour=null>"blue",sort_order=0>2` {
		t.Errorf("group North with update: %+v", r)
	}
	if r, _ := resultOf(preview, "tag", "dispatch"); r.Result != "changed" || changedFields(r) != `alert=null>"chime"` {
		t.Errorf("tag dispatch with update: %+v", r)
	}
	if applied.Summary != preview.Summary {
		t.Errorf("applied %+v, previewed %+v", applied.Summary, preview.Summary)
	}

	// State never changes through an import, update or not.
	if x := tg(1001); x.State != "public" || x.DelayMinutes != 5 || x.Alert == nil || *x.Alert != "beep" ||
		x.LEDColour == nil || *x.LEDColour != "red" || x.Type != "T" || x.Frequency == nil || *x.Frequency != 851012500 {
		t.Errorf("1001 after: %+v", x)
	}
	if x := tg(1003); x.State != "hidden" || x.Alert == nil || *x.Alert != "pip" {
		t.Errorf("1003 after: %+v: hidden stays hidden", x)
	}
	if x := tg(1004); x.State != "ignored" || !x.Unreviewed || x.DelayMinutes != 2 {
		t.Errorf("1004 after: %+v: ignored and unreviewed stay so", x)
	}
	if s := decodeJSON[server.System](t, e.mustAdmin(t, "GET", "/systems/1", nil, 200)); s.Label != "EXU" || !s.AutoCreate ||
		s.SortOrder != 7 {
		t.Errorf("system 1 after: %+v", s)
	}

	// Audited, as import: 3 talkgroups, the system, the group and the tag.
	if n := e.auditCount(t) - entries; n != 6 {
		t.Errorf("%d entries, want 6", n)
	}
	page := e.audit(t, "limit=6")
	kinds := map[string]int{}
	for _, x := range page.Entries {
		kinds[x.Kind]++
		if x.Via != "import" || x.Action != "changed" || x.Who.ID != "test admin" {
			t.Errorf("entry %+v: want a change by the test admin, how import", x)
		}
	}
	if kinds["talkgroup"] != 3 || kinds["system"] != 1 || kinds["group"] != 1 || kinds["tag"] != 1 {
		t.Errorf("entries by kind: %v", kinds)
	}

	// The same file again changes nothing.
	entries = e.auditCount(t)
	_, again := e.applyUpdate(t, updateSetup())
	for _, c := range []server.ImportCounts{again.Summary.Systems, again.Summary.Talkgroups, again.Summary.Groups, again.Summary.Tags} {
		if c.New != 0 || c.Changed != 0 {
			t.Errorf("again: %+v", again.Summary)
		}
	}
	if e.auditCount(t) != entries {
		t.Error("an update that changes nothing wrote entries")
	}

	// A label another system has is refused before anything is written.
	e.mustAdmin(t, "POST", "/systems", map[string]any{"id": 2, "label": "Other"}, 201)
	before, entries = e.export(t), e.auditCount(t)
	clash := setupDoc(map[string]any{"id": 2, "label": "EXU"})
	for _, dry := range []bool{true, false} {
		plan := strings.Repeat("0", 64)
		code, _, b := e.importWith(t, clash, true, dry, &plan)
		if code != 409 || !strings.Contains(string(b), `"label_taken"`) || !strings.Contains(string(b), "Systems 1 and 2") {
			t.Errorf("a taken label (dry run %v) = %d %s", dry, code, b)
		}
	}
	// Without update the file's label for a system that exists isn't
	// taken, so it doesn't clash.
	if r, _ := resultOf(e.preview(t, clash), "system", "2"); r.Result != "unchanged" {
		t.Errorf("system 2 without update: %+v", r)
	}
	if !bytes.Equal(e.export(t), before) || e.auditCount(t) != entries {
		t.Error("a refused import changed something")
	}
}
