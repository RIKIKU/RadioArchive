package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/RIKIKU/RadioArchive/internal/public"
	"github.com/RIKIKU/RadioArchive/internal/server"
)

func (e *env) catalogue(t *testing.T) (public.Catalogue, answer) {
	t.Helper()
	a := e.pub(t, "GET", public.CataloguePath, "")
	if a.status != 200 {
		t.Fatalf("GET catalogue: %s", a)
	}
	return decodeJSON[public.Catalogue](t, a.body), a
}

func catalogueIDs(ts []public.CatalogueTalkgroup) []int32 {
	ids := []int32{}
	for _, x := range ts {
		ids = append(ids, x.ID)
	}
	return ids
}

func (e *env) search(t *testing.T, q string) []int32 {
	t.Helper()
	a := e.pub(t, "GET", public.SearchPath+"?q="+url.QueryEscape(q), "")
	if a.status != 200 {
		t.Fatalf("search %q: %s", q, a)
	}
	return catalogueIDs(decodeJSON[public.SearchResponse](t, a.body).Talkgroups)
}

func str(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

// TestIntegrationCatalogue: the catalogue lists public talkgroups only,
// with their groups and tags and the name as R24 shows it, never cached;
// the system only when there are several (R15, R22, R24, R74, R77).
func TestIntegrationCatalogue(t *testing.T) {
	e := newEnv(t)
	sys := e.fixture(t)
	tg := func(id int) string { return fmt.Sprintf("/systems/%d/talkgroups/%d", sys, id) }

	c, a := e.catalogue(t)
	if got := catalogueIDs(c.Talkgroups); !slices.Equal(got, []int32{1001, 1002}) {
		t.Fatalf("catalogue lists %v, want the public 1001 and 1002", got)
	}
	if a.header.Get("Cache-Control") != "no-store" || a.header.Get("Content-Type") != "application/json" {
		t.Errorf("headers %v", a.header)
	}
	// The deployment's time zone, which the browser shows times in (ADR 0016).
	if c.TimeZone != "Australia/Sydney" {
		t.Errorf("time zone %q, want the deployment's Australia/Sydney", c.TimeZone)
	}
	d, f := c.Talkgroups[0], c.Talkgroups[1]
	if d.Name != "Dispatch North" || d.Label != "DISP N" || d.Tag != "dispatch" || !slices.Equal(d.Groups, []string{"North"}) ||
		d.System != nil || d.FiregroundChannel != nil || d.DelayMinutes != 0 || d.LEDColour != nil || d.Alert != nil {
		t.Errorf("1001 = %+v", d)
	}
	if f.Name != "Fireground 41" || f.FiregroundChannel == nil || *f.FiregroundChannel != 41 || len(f.Groups) != 0 {
		t.Errorf("1002 = %+v", f)
	}
	// Only the tags and groups public talkgroups have: not 1003's "admin"
	// or 1004's "other".
	if !slices.Equal(c.Tags, []string{"dispatch", "fireground"}) || !slices.Equal(c.Groups, []string{"North"}) || c.Systems != nil {
		t.Errorf("tags %v, groups %v, systems %v", c.Tags, c.Groups, c.Systems)
	}
	if strings.Contains(string(a.body), "1003") || strings.Contains(string(a.body), "Admin") {
		t.Errorf("the catalogue names 1003: %s", a.body)
	}

	// R24: the name, else the label, else the ID.
	e.mustAdmin(t, "PATCH", tg(1002), map[string]any{"name": ""}, 200)
	e.mustAdmin(t, "PUT", tg(1006), map[string]any{"tag": "dispatch", "delay_minutes": 5}, 201)
	c, _ = e.catalogue(t)
	if len(c.Talkgroups) != 3 || c.Talkgroups[1].Name != "FG 41" || c.Talkgroups[2].Name != "1006" ||
		c.Talkgroups[2].DelayMinutes != 5 {
		t.Errorf("names and delay: %+v", c.Talkgroups)
	}

	// Hiding 1001 takes it out of the next answer, with its group; making
	// it public puts it back.
	e.mustAdmin(t, "PATCH", tg(1001), map[string]any{"state": "hidden"}, 200)
	c, a = e.catalogue(t)
	if got := catalogueIDs(c.Talkgroups); slices.Contains(got, 1001) || len(c.Groups) != 0 ||
		strings.Contains(string(a.body), "Dispatch North") {
		t.Errorf("after hiding 1001: %s", a.body)
	}
	e.mustAdmin(t, "PATCH", tg(1001), map[string]any{"state": "public"}, 200)
	if c, _ = e.catalogue(t); !slices.Contains(catalogueIDs(c.Talkgroups), 1001) {
		t.Errorf("1001 isn't back: %v", catalogueIDs(c.Talkgroups))
	}
	// A change made with no admin API at all (an import, psql) takes effect
	// at the next request too.
	if _, err := e.st.Pool.Exec(context.Background(),
		`UPDATE talkgroups SET state = 'ignored' WHERE system_id = $1 AND talkgroup_id = 1001`, sys); err != nil {
		t.Fatal(err)
	}
	if c, _ = e.catalogue(t); slices.Contains(catalogueIDs(c.Talkgroups), 1001) {
		t.Error("1001, ignored in the database, is still listed")
	}
	e.mustAdmin(t, "PATCH", tg(1001), map[string]any{"state": "public"}, 200)

	// A second system with a public talkgroup: systems are named (R15).
	second := e.fixture(t)
	e.mustAdmin(t, "PATCH", fmt.Sprintf("/systems/%d", second), map[string]any{"sort_order": -1}, 200)
	c, _ = e.catalogue(t)
	if len(c.Systems) != 2 || c.Systems[0].ID != second || c.Systems[1].ID != sys || c.Systems[1].Label != "EX" {
		t.Fatalf("systems %+v", c.Systems)
	}
	if x := c.Talkgroups[0]; x.System == nil || *x.System != second || x.ID != 1001 {
		t.Errorf("first talkgroup %+v, want the second system's 1001", x)
	}
	// A system with only hidden talkgroups isn't named.
	for _, id := range []int{1001, 1002} {
		e.mustAdmin(t, "PATCH", fmt.Sprintf("/systems/%d/talkgroups/%d", second, id), map[string]any{"state": "hidden"}, 200)
	}
	if c, _ = e.catalogue(t); c.Systems != nil || c.Talkgroups[0].System != nil {
		t.Errorf("with one system left: %+v", c)
	}
}

// TestIntegrationCatalogueSettings: a talkgroup with no alert or LED colour
// of its own takes its tag's, then its groups' in their order, then its
// system's; "none" stops there (ADR 0006).
func TestIntegrationCatalogueSettings(t *testing.T) {
	e := newEnv(t)
	sys := e.fixture(t)
	tg := func(id int) string { return fmt.Sprintf("/systems/%d/talkgroups/%d", sys, id) }
	effective := func() map[int32][2]string {
		c, _ := e.catalogue(t)
		out := map[int32][2]string{}
		for _, x := range c.Talkgroups {
			out[x.ID] = [2]string{str(x.Alert), str(x.LEDColour)}
		}
		return out
	}

	e.mustAdmin(t, "PATCH", fmt.Sprintf("/systems/%d", sys), map[string]any{"alert": "beep", "led_colour": "blue"}, 200)
	if got := effective(); got[1001] != [2]string{"beep", "blue"} || got[1002] != [2]string{"beep", "blue"} {
		t.Errorf("from the system: %v", got)
	}
	groups := decodeJSON[[]server.Label](t, e.mustAdmin(t, "GET", "/groups", nil, 200))
	if len(groups) != 1 || groups[0].Name != "North" {
		t.Fatalf("groups %+v", groups)
	}
	e.mustAdmin(t, "PATCH", fmt.Sprintf("/groups/%d", groups[0].ID), map[string]any{"led_colour": "red", "sort_order": 5}, 200)
	south := decodeJSON[server.Label](t, e.mustAdmin(t, "POST", "/groups",
		map[string]any{"name": "South", "led_colour": "green", "alert": "pip"}, 201))
	e.mustAdmin(t, "PATCH", tg(1001), map[string]any{"groups": []string{"North", "South"}}, 200)
	// South sorts first (0 < 5), so its settings win.
	if got := effective(); got[1001] != [2]string{"pip", "green"} {
		t.Errorf("from the groups: %v", got)
	}
	var dispatch server.Label
	for _, x := range decodeJSON[[]server.Label](t, e.mustAdmin(t, "GET", "/tags", nil, 200)) {
		if x.Name == "dispatch" {
			dispatch = x
		}
	}
	e.mustAdmin(t, "PATCH", fmt.Sprintf("/tags/%d", dispatch.ID), map[string]any{"alert": "chime"}, 200)
	if got := effective(); got[1001] != [2]string{"chime", "green"} {
		t.Errorf("tag before groups: %v", got)
	}
	e.mustAdmin(t, "PATCH", tg(1001), map[string]any{"led_colour": "none", "alert": "tone"}, 200)
	if got := effective(); got[1001] != [2]string{"tone", "<nil>"} {
		t.Errorf("its own, and none: %v", got)
	}
	// null clears its own, and it takes them again.
	b := e.mustAdmin(t, "PATCH", tg(1001), map[string]any{"led_colour": nil, "alert": nil}, 200)
	if x := decodeJSON[server.Talkgroup](t, b); x.Alert != nil || x.LEDColour != nil ||
		!slices.Equal(x.Groups, []string{"South", "North"}) {
		t.Errorf("after clearing: %+v", x)
	}
	if got := effective(); got[1001] != [2]string{"chime", "green"} {
		t.Errorf("after clearing: %v", got)
	}
	// Groups: [] takes it out of every group; absent leaves them.
	e.mustAdmin(t, "PATCH", tg(1001), map[string]any{"groups": []string{}}, 200)
	if x := decodeJSON[server.Talkgroup](t, e.mustAdmin(t, "PATCH", tg(1001), map[string]any{"sort_order": 3}, 200)); len(x.Groups) != 0 || x.SortOrder != 3 {
		t.Errorf("no groups: %+v", x)
	}
	if c, _ := e.catalogue(t); !slices.Equal(catalogueIDs(c.Talkgroups), []int32{1002, 1001}) {
		t.Errorf("sort order: %v", catalogueIDs(c.Talkgroups))
	}
	if south.ID == 0 {
		t.Error("no group ID")
	}

	e.wantRefusal(t, "PATCH", tg(1001), map[string]any{"led_colour": "mauve"}, 400, "invalid_led_colour")
	e.wantRefusal(t, "PATCH", tg(1001), map[string]any{"alert": "klaxon"}, 400, "invalid_alert")
	e.wantRefusal(t, "PATCH", tg(1001), map[string]any{"delay_minutes": -1}, 400, "invalid_delay_minutes")
	e.wantRefusal(t, "PATCH", tg(1001), map[string]any{"delay_minutes": 1441}, 400, "invalid_delay_minutes")
	e.wantRefusal(t, "PATCH", tg(1001), map[string]any{"groups": []string{" North"}}, 400, "invalid_group")
	e.wantRefusal(t, "POST", "/groups", map[string]any{"name": "South"}, 409, "group_exists")
	e.wantRefusal(t, "POST", "/groups", map[string]any{"led_colour": "red"}, 400, "name_required")
	e.wantRefusal(t, "PATCH", "/groups/999999", map[string]any{"name": "x"}, 404, "group_not_found")
	e.wantRefusal(t, "PATCH", fmt.Sprintf("/tags/%d", dispatch.ID), map[string]any{"name": "fireground"}, 409, "tag_exists")
	e.wantRefusal(t, "PATCH", fmt.Sprintf("/systems/%d", sys), map[string]any{"led_colour": "plaid"}, 400, "invalid_led_colour")
}

// TestIntegrationSearch: talkgroup search by name, label, ID or tag, an empty
// query included, never finding a hidden or ignored talkgroup (R42, R77).
func TestIntegrationSearch(t *testing.T) {
	e := newEnv(t)
	sys := e.fixture(t)
	for q, want := range map[string][]int32{
		"":               {1001, 1002},
		"   ":            {1001, 1002},
		"north":          {1001},
		"DISP":           {1001},
		"fireground":     {1002}, // its tag, and a word of its name
		"41":             {1002}, // a word of its name
		"1002":           {1002}, // its ID
		"100":            {1001, 1002},
		"dispatch north": {1001},
		"Admin":          {},
		"1003":           {},
		"admin":          {},
		"1004":           {},
		"other":          {},
	} {
		if got := e.search(t, q); !slices.Equal(got, want) {
			t.Errorf("search %q = %v, want %v", q, got, want)
		}
	}
	// An exact match comes first, then a word starting with it, then one
	// containing it.
	e.mustAdmin(t, "PUT", fmt.Sprintf("/systems/%d/talkgroups/990", sys), map[string]any{"tag": "x", "name": "Northern ops"}, 201)
	e.mustAdmin(t, "PUT", fmt.Sprintf("/systems/%d/talkgroups/991", sys), map[string]any{"tag": "x", "name": "Far north"}, 201)
	e.mustAdmin(t, "PUT", fmt.Sprintf("/systems/%d/talkgroups/992", sys), map[string]any{"tag": "x", "name": "Upnorth"}, 201)
	e.mustAdmin(t, "PUT", fmt.Sprintf("/systems/%d/talkgroups/993", sys), map[string]any{"tag": "x", "label": "NORTH"}, 201)
	if got := e.search(t, "north"); !slices.Equal(got, []int32{993, 990, 991, 1001, 992}) {
		t.Errorf("search north = %v", got)
	}
	a := e.pub(t, "GET", public.SearchPath+"?q="+strings.Repeat("x", public.MaxQuery+1), "")
	var body struct{ Error string }
	if a.status != 400 || json.Unmarshal(a.body, &body) != nil || body.Error != "query_too_long" {
		t.Errorf("a long query: %s", a)
	}
}

// TestIntegrationFireground: a fireground channel number answers with its
// talkgroup only if that talkgroup is public. An unknown number, a hidden
// or ignored talkgroup's, and anything that isn't a number get one
// identical answer that names nothing (R33, R100, R101; A15's server side).
func TestIntegrationFireground(t *testing.T) {
	e := newEnv(t)
	sys := e.fixture(t)
	tg := func(id int) string { return fmt.Sprintf("/systems/%d/talkgroups/%d", sys, id) }
	e.mustAdmin(t, "PATCH", tg(1003), map[string]any{"fireground_channel": 43}, 200)
	e.mustAdmin(t, "PATCH", tg(1004), map[string]any{"fireground_channel": "0044"}, 200)

	for _, n := range []string{"041", "41", "0000041"} {
		a := e.pub(t, "GET", public.FiregroundURL(n), "")
		got := decodeJSON[public.FiregroundResponse](t, a.body)
		if a.status != 200 || got.Talkgroup.ID != 1002 || got.Talkgroup.Name != "Fireground 41" ||
			a.header.Get("Cache-Control") != "no-store" {
			t.Errorf("fireground %s: %s", n, a)
		}
	}
	unknown := e.pub(t, "GET", public.FiregroundURL("99"), "")
	if unknown.status != 404 || !strings.Contains(string(unknown.body), `"channel_unavailable"`) ||
		!strings.Contains(string(unknown.body), "This channel isn't available.") {
		t.Fatalf("an unknown number: %s", unknown)
	}
	for _, n := range []string{"43", "043", "44", "-41", "4.1", "41x", "%20", "99999999999999999999", "x"} {
		if a := e.pub(t, "GET", public.FiregroundURL(n), ""); a.String() != unknown.String() {
			t.Errorf("fireground %q answers\n%s\nbut an unknown number answers\n%s", n, a, unknown)
		}
	}
	// Hiding 1002 makes its number unknown at the next request.
	e.mustAdmin(t, "PATCH", tg(1002), map[string]any{"state": "hidden"}, 200)
	if a := e.pub(t, "GET", public.FiregroundURL("41"), ""); a.String() != unknown.String() {
		t.Errorf("41, hidden, answers\n%s", a)
	}
	e.mustAdmin(t, "PATCH", tg(1002), map[string]any{"state": "public"}, 200)
	if a := e.pub(t, "GET", public.FiregroundURL("41"), ""); a.status != 200 {
		t.Errorf("41, public again: %s", a)
	}
}

// TestIntegrationFiregroundUnique: a fireground channel number is unique
// across the deployment, leading zeros not counting; a duplicate is refused
// naming the talkgroup that has it (R100, A25).
func TestIntegrationFiregroundUnique(t *testing.T) {
	e := newEnv(t)
	sys := e.fixture(t)
	other := e.fixture(t)
	tg := func(s int64, id int) string { return fmt.Sprintf("/systems/%d/talkgroups/%d", s, id) }

	for _, try := range []struct {
		method, path string
		body         map[string]any
	}{
		{"PATCH", tg(sys, 1001), map[string]any{"fireground_channel": 41}},
		{"PATCH", tg(sys, 1001), map[string]any{"fireground_channel": "041"}},
		{"PUT", tg(sys, 1005), map[string]any{"tag": "x", "fireground_channel": 41}},
		{"PATCH", tg(other, 1002), map[string]any{"fireground_channel": 41}}, // another system
	} {
		code, b := e.adminReq(t, "test admin", try.method, try.path, try.body)
		var r struct{ Error, Message string }
		if json.Unmarshal(b, &r) != nil || code != 409 || r.Error != "fireground_channel_taken" ||
			!strings.Contains(r.Message, `"Fireground 41"`) {
			t.Errorf("%s %s %v = %d %s, want 409 naming \"Fireground 41\"", try.method, try.path, try.body, code, b)
		}
	}
	if x := decodeJSON[server.Talkgroup](t, e.mustAdmin(t, "GET", tg(sys, 1001), nil, 200)); x.FiregroundChannel != nil {
		t.Errorf("1001 got a number: %+v", x)
	}
	e.wantRefusal(t, "PATCH", tg(sys, 1001), map[string]any{"fireground_channel": -1}, 400, "invalid_body")
	e.wantRefusal(t, "PATCH", tg(sys, 1001), map[string]any{"fireground_channel": "4a"}, 400, "invalid_body")

	// Its own number again is no conflict; once cleared, another may take it.
	e.mustAdmin(t, "PATCH", tg(sys, 1002), map[string]any{"fireground_channel": "41"}, 200)
	x := decodeJSON[server.Talkgroup](t, e.mustAdmin(t, "PATCH", tg(sys, 1002), map[string]any{"fireground_channel": nil}, 200))
	if x.FiregroundChannel != nil {
		t.Errorf("not cleared: %+v", x)
	}
	x = decodeJSON[server.Talkgroup](t, e.mustAdmin(t, "PATCH", tg(other, 1001), map[string]any{"fireground_channel": "041"}, 200))
	if x.FiregroundChannel == nil || *x.FiregroundChannel != 41 {
		t.Errorf("041: %+v", x)
	}
	// A PUT without it gives it up.
	e.mustAdmin(t, "PUT", tg(other, 1001), map[string]any{"tag": "dispatch"}, 200)
	e.mustAdmin(t, "PATCH", tg(sys, 1002), map[string]any{"fireground_channel": 41}, 200)
}

// TestIntegrationCatalogueThousand: about 1,000 talkgroups come back in one
// catalogue response, with no paging (R77).
func TestIntegrationCatalogueThousand(t *testing.T) {
	e := newEnv(t)
	sys := e.fixture(t)
	if _, err := e.st.Pool.Exec(context.Background(), `
		INSERT INTO talkgroups (system_id, talkgroup_id, label, name, tag_id)
		SELECT $1, 20000 + n, 'TG ' || n, 'Talkgroup ' || n, (SELECT id FROM tags WHERE name = 'dispatch')
		FROM generate_series(1, 1000) AS n`, sys); err != nil {
		t.Fatal(err)
	}
	c, a := e.catalogue(t)
	if len(c.Talkgroups) != 1002 {
		t.Fatalf("%d talkgroups in the catalogue, want 1,002", len(c.Talkgroups))
	}
	if got := e.search(t, ""); len(got) != 1002 {
		t.Errorf("an empty search finds %d", len(got))
	}
	t.Logf("1,002 talkgroups: %d bytes", len(a.body))
}
