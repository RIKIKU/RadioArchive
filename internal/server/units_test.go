package server_test

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/public"
	"github.com/RIKIKU/RadioArchive/internal/server"
)

// Units and their aliases (SPEC.md R26, R27, R32; ADR 0019).

// aliasesOf is a call's transmissions as unit=alias (or unit= for none).
func aliasesOf(c public.Call) []string {
	out := make([]string, 0, len(c.Transmissions))
	for _, x := range c.Transmissions {
		a := ""
		if x.Alias != nil {
			a = *x.Alias
		}
		out = append(out, fmt.Sprintf("%d=%s", x.Unit, a))
	}
	return out
}

// TestIntegrationUnitAliases: R26's ranges as the call details and recent
// calls show them, an exact unit beating a range, the narrower range
// winning, an empty alias showing the ID, and the next request seeing a
// change. Recent calls filtered to Engine 1 (5001) list only its calls.
func TestIntegrationUnitAliases(t *testing.T) {
	e := newEnv(t)
	_, key := e.recorder(t)
	t0 := fixtureTime
	a := e.uploadAt(t, key, 1, 1001, nil, t0, 5001)
	b := e.uploadAt(t, key, 2, 1001, nil, t0.Add(time.Minute), 5150)
	c := e.uploadAt(t, key, 3, 1002, nil, t0.Add(2*time.Minute), 5200)

	check := func(why string, want ...string) {
		t.Helper()
		cs := e.publicCalls(t, a, b, c)
		var got []string
		for _, x := range cs {
			got = append(got, aliasesOf(x)...)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: call details' units %q, want %q", why, got, want)
		}
		rs := e.recent(t, around(t0, "order", "oldest")).Calls
		got = got[:0]
		for _, x := range rs {
			got = append(got, aliasesOf(x)...)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: recent calls' units %q, want %q", why, got, want)
		}
	}
	check("the fixture", "5001=Engine 1", "5150=Portables", "5200=")

	units := fmt.Sprintf("/systems/%d/units/", e.sys)
	e.mustAdmin(t, "PUT", units+"5150-5159", map[string]any{"alias": "North portables"}, 201)
	check("a narrower range", "5001=Engine 1", "5150=North portables", "5200=")
	e.mustAdmin(t, "PUT", units+"5150", map[string]any{"alias": ""}, 201)
	check("an exact unit with no alias", "5001=Engine 1", "5150=", "5200=")
	e.mustAdmin(t, "DELETE", units+"5150", nil, 204)
	e.mustAdmin(t, "PUT", units+"5150-5159", map[string]any{"alias": "Portables north"}, 200)
	e.mustAdmin(t, "PUT", units+"5200", map[string]any{"alias": "Tanker 2"}, 201)
	check("changed", "5001=Engine 1", "5150=Portables north", "5200=Tanker 2")

	// The unit filter: the calls with 5001 in them, and no others.
	if got := ids(e.recent(t, around(t0, "unit", "5001")).Calls); !slices.Equal(got, []string{a}) {
		t.Errorf("filtered to 5001: %v, want %v", got, []string{a})
	}
	// A range (ADR 0019): the calls with any radio in it.
	for _, c := range []struct {
		unit string
		want []string
	}{
		{"5100-5199", []string{b}},
		{"5000-5199", []string{a, b}},
		{"5001-5200", []string{a, b, c}},
		{"5151-5199", []string{}},
		{fmt.Sprintf("%d.5000-5199", e.sys), []string{a, b}},
		{fmt.Sprintf("%d.5000-5199", e.sys+1), []string{}},
	} {
		if got := ids(e.recent(t, around(t0, "unit", c.unit, "order", "oldest")).Calls); !slices.Equal(got, c.want) {
			t.Errorf("filtered to %s: %v, want %v", c.unit, got, c.want)
		}
	}

	// Another system's 5001 is no "Engine 1".
	other := e.fixture(t)
	e.mustAdmin(t, "PUT", fmt.Sprintf("/systems/%d/units/5001", other), map[string]any{"alias": "Elsewhere"}, 200)
	check("another system's alias", "5001=Engine 1", "5150=Portables north", "5200=Tanker 2")
}

// unitSearch asks unit search for q and wants a 200, uncached.
func (e *env) unitSearch(t *testing.T, q string) []string {
	t.Helper()
	a := e.pub(t, "GET", public.UnitSearchPath+"?q="+url.QueryEscape(q), "")
	if a.status != 200 || a.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("unit search %q: %s", q, a)
	}
	var out []string
	// A radio as 5001=Engine 1, 5200 or "5150 in Portables"; a range as
	// 5100-5199=Portables.
	for _, u := range decodeJSON[public.UnitSearchResponse](t, a.body).Units {
		var s string
		switch {
		case u.ID != nil && u.First == nil && u.Last == nil:
			s = fmt.Sprint(*u.ID)
		case u.ID == nil && u.First != nil && u.Last != nil && u.In == nil:
			s = fmt.Sprintf("%d-%d", *u.First, *u.Last)
		default:
			t.Fatalf("unit search %q: neither a radio nor a range: %s", q, a.body)
		}
		if u.Alias != nil {
			s += "=" + *u.Alias
		}
		if u.In != nil {
			s += " in " + *u.In
		}
		if u.System != nil {
			s = fmt.Sprintf("%d.%s", *u.System, s)
		}
		out = append(out, s)
	}
	return out
}

// TestIntegrationUnitSearch: unit search finds radios heard on public
// calls by alias or ID, best first, a radio only a range names by its ID
// with the range beside it, and range aliases by their alias or their
// radio IDs, never by a number alone; and nothing of a radio heard only on
// a hidden talkgroup or a call within its delay (R23, R32), nor of a range
// whose radios are heard only there.
func TestIntegrationUnitSearch(t *testing.T) {
	e := newEnv(t)
	_, key := e.recorder(t)
	t0 := fixtureTime
	units := fmt.Sprintf("/systems/%d/units/", e.sys)
	e.mustAdmin(t, "PUT", units+"5002", map[string]any{"alias": "Engine 12"}, 201)
	e.mustAdmin(t, "PUT", units+"5003", map[string]any{"alias": "Hidden engine"}, 201)
	// Ranges: one with public 5001 and 5002 in it, one whose only radio
	// heard (5160) is on 1001 patched with hidden 1003, one whose only
	// radio heard (5170) is on delayed 1006, and one never heard.
	e.mustAdmin(t, "PUT", units+"5000-5009", map[string]any{"alias": "Engines"}, 201)
	e.mustAdmin(t, "PUT", units+"5160-5169", map[string]any{"alias": "Covert fleet"}, 201)
	e.mustAdmin(t, "PUT", units+"5170-5179", map[string]any{"alias": "Delayed fleet"}, 201)
	e.mustAdmin(t, "PUT", units+"5900-5999", map[string]any{"alias": "Quiet fleet"}, 201)
	e.mustAdmin(t, "PUT", fmt.Sprintf("/systems/%d/talkgroups/1006", e.sys),
		map[string]any{"label": "DLY 6", "name": "Delayed six", "tag": "dispatch", "delay_minutes": 5}, 201)
	e.uploadAt(t, key, 1, 1001, nil, t0, 5001)
	e.uploadAt(t, key, 2, 1002, nil, t0.Add(time.Minute), 5002)
	e.uploadAt(t, key, 3, 1001, nil, t0.Add(2*time.Minute), 5150)
	e.uploadAt(t, key, 4, 1001, nil, t0.Add(3*time.Minute), 5200)
	// Heard only on hidden 1003, on 1001 patched with 1003, on 1006 within
	// its delay, and on 1003 and 1006 beside a public call.
	e.uploadAt(t, key, 5, 1003, nil, t0.Add(4*time.Minute), 5003)
	e.uploadAt(t, key, 6, 1001, []int32{1003}, t0.Add(5*time.Minute), 5160)
	e.uploadAt(t, key, 7, 1006, nil, t0.Add(6*time.Minute), 5170)
	e.uploadAt(t, key, 8, 1003, nil, t0.Add(7*time.Minute), 5200)
	e.clock.set(t0.Add(10 * time.Minute)) // 1006's call ended 3 min 50 s ago

	for _, c := range []struct {
		q    string
		want []string
	}{
		{"Engine 1", []string{"5001=Engine 1", "5002=Engine 12"}},
		{"engine", []string{"5001=Engine 1", "5002=Engine 12", "5000-5009=Engines"}},
		{"ENGINE 12", []string{"5002=Engine 12"}},
		{"12", []string{"5002=Engine 12"}},
		{"engines", []string{"5000-5009=Engines"}},
		// A range by its alias or its radio IDs: choosing it on purpose.
		{"portables", []string{"5100-5199=Portables"}},
		{"port", []string{"5100-5199=Portables"}},
		{"5100-5199", []string{"5100-5199=Portables"}},
		{"5100-", []string{"5100-5199=Portables"}},
		{"5100 - 51", []string{"5100-5199=Portables"}},
		{"5000-", []string{"5000-5009=Engines"}},
		{"5-", nil},
		{"5100-52", nil},
		{"5101-5199", nil},
		{"fleet", nil},
		// A radio ID never offers a range; one only a range names is
		// offered as its ID, in the range.
		{"5001", []string{"5001=Engine 1"}},
		{"5", []string{"5001=Engine 1", "5002=Engine 12", "5150 in Portables", "5200"}},
		{"51", []string{"5150 in Portables"}},
		{"5150", []string{"5150 in Portables"}},
		{"5200", []string{"5200"}},
		// Heard only where the public may not hear it: not found.
		{"hidden", nil},
		{"5003", nil},
		{"5160", nil},
		{"5170", nil},
		{"516", nil},
		{"covert fleet", nil},
		{"5160-5169", nil},
		{"5160-", nil},
		{"delayed fleet", nil},
		{"5170-", nil},
		{"quiet fleet", nil},
		{"5900-", nil},
		{"", nil},
		{"   ", nil},
		{"05001", nil},
		{"9999999999999999999999", nil},
	} {
		if got := e.unitSearch(t, c.q); !slices.Equal(got, c.want) {
			t.Errorf("unit search %q = %q, want %q", c.q, got, c.want)
		}
	}
	// Once 1006's delay has passed, 5170 is heard on a public call, and its
	// range with it.
	e.clock.set(t0.Add(time.Hour))
	if got := e.unitSearch(t, "5170"); !slices.Equal(got, []string{"5170 in Delayed fleet"}) {
		t.Errorf("after the delay: %q", got)
	}
	if got := e.unitSearch(t, "fleet"); !slices.Equal(got, []string{"5170-5179=Delayed fleet"}) {
		t.Errorf("after the delay, fleet: %q", got)
	}
	// Hiding 1001 takes the units heard only on it (and 1003) out at the
	// next request: 5002 is heard on 1002, 5170 on 1006. A range stays
	// while one of its radios is heard on a public call: Engines by 5002,
	// Portables by 5170.
	e.mustAdmin(t, "PATCH", fmt.Sprintf("/systems/%d/talkgroups/1001", e.sys), map[string]any{"state": "hidden"}, 200)
	if got := e.unitSearch(t, "5"); !slices.Equal(got, []string{"5002=Engine 12", "5170 in Delayed fleet"}) {
		t.Errorf("with 1001 hidden: %q", got)
	}
	for q, want := range map[string][]string{"portables": {"5100-5199=Portables"}, "engines": {"5000-5009=Engines"},
		"5170-": {"5170-5179=Delayed fleet"}, "5150": nil} {
		if got := e.unitSearch(t, q); !slices.Equal(got, want) {
			t.Errorf("with 1001 hidden, %q = %q, want %q", q, got, want)
		}
	}
	// Hiding 1006 too leaves Portables with no radio heard in public: it
	// answers as a range never heard.
	e.mustAdmin(t, "PATCH", fmt.Sprintf("/systems/%d/talkgroups/1006", e.sys), map[string]any{"state": "hidden"}, 200)
	for _, q := range []string{"portables", "5100-5199", "5100-", "fleet"} {
		if got := e.unitSearch(t, q); got != nil {
			t.Errorf("with 1001 and 1006 hidden, %q = %q", q, got)
		}
	}

	if a := e.pub(t, "GET", public.UnitSearchPath+"?q="+strings.Repeat("x", public.MaxQuery+1), ""); a.status != 400 ||
		!strings.Contains(string(a.body), `"query_too_long"`) {
		t.Errorf("a long query: %s", a)
	}
}

// TestIntegrationUnitSearchSystems: with several systems, each unit found
// names its system, and the same radio ID in two systems is two units.
func TestIntegrationUnitSearchSystems(t *testing.T) {
	e := newEnv(t)
	_, key := e.recorder(t)
	first := e.sys
	e.uploadAt(t, key, 1, 1001, nil, fixtureTime, 5001)
	_, key2 := e.recorder(t)
	second := e.sys
	e.mustAdmin(t, "PUT", fmt.Sprintf("/systems/%d/units/5001", second), map[string]any{"alias": "Engine 1 south"}, 200)
	e.uploadAt(t, key2, 2, 1001, nil, fixtureTime.Add(time.Minute), 5001)
	got := e.unitSearch(t, "5001")
	want := []string{fmt.Sprintf("%d.5001=Engine 1", first), fmt.Sprintf("%d.5001=Engine 1 south", second)}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("unit search 5001 = %q, want %q", got, want)
	}
}

// TestIntegrationAdminUnits: the admin API's units (R26, R51): adding,
// changing, listing, deleting, and what it refuses.
func TestIntegrationAdminUnits(t *testing.T) {
	e := newEnv(t)
	e.recorder(t)
	units := fmt.Sprintf("/systems/%d/units", e.sys)
	list := decodeJSON[[]server.Unit](t, e.mustAdmin(t, "GET", units, nil, 200))
	if len(list) != 2 || list[0].From != 5001 || list[0].To != 5001 || list[0].Alias != "Engine 1" ||
		list[1].From != 5100 || list[1].To != 5199 || list[1].Alias != "Portables" {
		t.Fatalf("the fixture's units: %+v", list)
	}
	got := decodeJSON[server.Unit](t, e.mustAdmin(t, "GET", units+"/5100-5199", nil, 200))
	if got.Alias != "Portables" || got.SystemID != e.sys || got.CreatedAt == nil {
		t.Errorf("GET 5100-5199: %+v", got)
	}
	got = decodeJSON[server.Unit](t, e.mustAdmin(t, "PUT", units+"/5001", map[string]any{"alias": "Engine one"}, 200))
	if got.Alias != "Engine one" {
		t.Errorf("PUT 5001: %+v", got)
	}
	for _, c := range []struct {
		path string
		body any
		code int
		err  string
	}{
		{"/5200-5100", map[string]any{"alias": "x"}, 404, "unit_not_found"},
		{"/5100-5100", map[string]any{"alias": "x"}, 404, "unit_not_found"},
		{"/0", map[string]any{"alias": "x"}, 404, "unit_not_found"},
		{"/05001", map[string]any{"alias": "x"}, 404, "unit_not_found"},
		{"/x", map[string]any{"alias": "x"}, 404, "unit_not_found"},
		{"/5001", map[string]any{}, 400, "invalid_alias"},
		{"/5001", map[string]any{"alias": "A;B"}, 400, "invalid_alias"},
		{"/5001", map[string]any{"alias": " padded"}, 400, "invalid_alias"},
		{"/5001", map[string]any{"alias": "tab\there"}, 400, "invalid_alias"},
		{"/5001", map[string]any{"alias": strings.Repeat("x", 101)}, 400, "invalid_alias"},
		{"/5001", map[string]any{"alias": "x", "state": "hidden"}, 400, "invalid_body"},
	} {
		code, b := e.adminReq(t, "test admin", "PUT", units+c.path, c.body)
		if code != c.code || !strings.Contains(string(b), `"`+c.err+`"`) {
			t.Errorf("PUT %s %v = %d %s, want %d %s", c.path, c.body, code, b, c.code, c.err)
		}
	}
	if code, b := e.adminReq(t, "test admin", "PUT", "/systems/999999/units/5001", map[string]any{"alias": "x"}); code != 404 ||
		!strings.Contains(string(b), "system_not_found") {
		t.Errorf("no such system: %d %s", code, b)
	}
	e.mustAdmin(t, "DELETE", units+"/5100-5199", nil, 204)
	e.mustAdmin(t, "DELETE", units+"/5100-5199", nil, 404)
	e.mustAdmin(t, "GET", units+"/5100-5199", nil, 404)
	if code, _ := e.adminReq(t, "", "GET", units, nil); code != 401 {
		t.Errorf("not an admin: %d", code)
	}
}
