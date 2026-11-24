package gate

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/store"
	"github.com/RIKIKU/RadioArchive/internal/store/db"
)

// Units and their aliases (SPEC.md R26, R27, R32; ADR 0019). The aliases
// are held with the catalogue, at its generation (migration 00016 moves it
// on every change to a unit), so a call the gate gives names its units as
// they are at the next request. Nothing here says which units exist: the
// gate names only the units of calls it gives, and unit search finds only
// units heard on a call the public may have, so a radio heard only on a
// hidden talkgroup is found by neither its alias nor its ID.

// unitDef is one unit row: a radio ID (first == last) or a range.
type unitDef struct {
	system      int64
	first, last int64
	alias       string
	lower       string // alias, lower case
}

type unitKey struct {
	system int64
	unit   int64
}

// unitBook is every unit's alias, for naming the units of public calls.
type unitBook struct {
	exact  map[unitKey]string
	ranges map[int64][]unitDef // per system, narrowest first, then lowest
	defs   []unitDef           // all, in the table's order, for unit search
}

func unitBookOf(rows []db.GateUnitsRow) unitBook {
	b := unitBook{exact: map[unitKey]string{}, ranges: map[int64][]unitDef{}, defs: make([]unitDef, 0, len(rows))}
	for _, r := range rows {
		d := unitDef{system: r.SystemID, first: r.FirstID, last: r.LastID, alias: r.Alias, lower: strings.ToLower(r.Alias)}
		b.defs = append(b.defs, d)
		if d.first == d.last {
			b.exact[unitKey{d.system, d.first}] = d.alias
		} else {
			b.ranges[d.system] = append(b.ranges[d.system], d)
		}
	}
	for _, rs := range b.ranges {
		slices.SortFunc(rs, func(a, b unitDef) int {
			return cmp.Or(cmp.Compare(a.last-a.first, b.last-b.first), cmp.Compare(a.first, b.first))
		})
	}
	return b
}

// UnitAlias is a radio's alias in its system (R26), or "" when it has
// none: its own, else that of the narrowest range covering it, else none.
// An exact unit with an empty alias has none, whatever range covers it.
func (c *Catalogue) UnitAlias(system, unit int64) string {
	if a, ok := c.units.exact[unitKey{system, unit}]; ok {
		return a
	}
	for _, r := range c.units.ranges[system] {
		if r.first <= unit && unit <= r.last {
			return r.alias
		}
	}
	return ""
}

// nameUnits gives a public call's transmissions their units' aliases.
func (c *Catalogue) nameUnits(system int64, call *Call) {
	for i := range call.Transmissions {
		call.Transmissions[i].Alias = c.UnitAlias(system, call.Transmissions[i].Unit)
	}
}

// FoundUnit is a unit unit search found: a radio heard on a public call.
type FoundUnit struct {
	System int64
	ID     int64
	Alias  string // "" when it has none
}

// Bounds of unit search (ADR 0019).
const (
	// MaxUnitsFound is the most units one search answers.
	MaxUnitsFound = 20
	// unitCandidates is the most radio IDs one search checks.
	unitCandidates = 60
	// unitScan is how many of a radio's transmissions are read to find its
	// public calls, and unitCalls how many of those are checked.
	unitScan  = 100
	unitCalls = 10
)

// unitTier is how well a radio matches a search, as the catalogue's search
// tiers talkgroups (ADR 0006): 0 its alias or ID is the query; 1 its alias,
// or a word of it, or its ID starts with it; 2 its alias contains it; 3 no
// match. digits says the query is a number, which only then matches IDs.
func unitTier(q string, digits bool, id int64, lower string) int {
	sid := strconv.FormatInt(id, 10)
	switch {
	case lower == q || (digits && sid == q):
		return 0
	case lower != "" && (strings.HasPrefix(lower, q) || strings.Contains(lower, " "+q)),
		digits && strings.HasPrefix(sid, q):
		return 1
	case lower != "" && strings.Contains(lower, q):
		return 2
	}
	return 3
}

// idSpans are the spans of radio IDs whose decimal form starts with q, a
// number with no leading zero: q itself, then q0-q9, q00-q99, and so on, as
// far as a bigint goes.
func idSpans(q string) [][2]int64 {
	p, err := strconv.ParseInt(q, 10, 64)
	if err != nil || p <= 0 || q[0] == '0' {
		return nil
	}
	var out [][2]int64
	for scale := int64(1); ; scale *= 10 {
		lo := p * scale
		if lo/scale != p { // overflowed
			break
		}
		hi := lo + (scale - 1)
		if hi < lo {
			hi = math.MaxInt64
		}
		out = append(out, [2]int64{lo, hi})
		if scale > math.MaxInt64/10 {
			break
		}
	}
	return out
}

// SearchUnits finds units heard on public calls (R27, R32; ADR 0019) by
// alias or radio ID, ignoring case: those named exactly first, then those
// whose alias (or a word of it) or ID starts with q, then those whose alias
// contains it; each tier by ID for a number, else by alias. At most
// MaxUnitsFound. A radio heard only on hidden or ignored talkgroups, or
// only on calls still within their delay (R23), isn't found, whatever its
// alias, so the answer is the one a radio never heard gets. An empty query
// finds nothing.
func (g *Gate) SearchUnits(ctx context.Context, query string) ([]FoundUnit, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return []FoundUnit{}, nil
	}
	cat, err := g.Catalogue(ctx)
	if err != nil {
		return nil, err
	}
	digits := strings.Trim(q, "0123456789") == ""

	// Where to look: the units whose alias matches, best first, with the
	// IDs that start with a number after those it names exactly.
	type span struct {
		tier     int
		from, to int64
	}
	var spans []span
	for _, d := range cat.units.defs {
		if d.lower == "" {
			continue
		}
		if t := unitTier(q, false, 0, d.lower); t < 3 {
			spans = append(spans, span{t, d.first, d.last})
		}
	}
	if digits {
		for _, s := range idSpans(q) {
			spans = append(spans, span{0, s[0], s[1]})
		}
	}
	slices.SortStableFunc(spans, func(a, b span) int { return cmp.Compare(a.tier, b.tier) })

	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	seen := map[int64]bool{}
	var candidates []int64
	for _, s := range spans {
		left := unitCandidates - len(candidates)
		if left <= 0 {
			break
		}
		ids, err := g.q.GateUnitsIn(ctx, db.GateUnitsInParams{FirstID: s.from, LastID: s.to, RowLimit: int32(left)})
		if err != nil {
			return nil, fmt.Errorf("unit search: %s", store.SafeError(err))
		}
		for _, id := range ids {
			if id > 0 && !seen[id] && len(candidates) < unitCandidates {
				seen[id] = true
				candidates = append(candidates, id)
			}
		}
	}
	if len(candidates) == 0 {
		return []FoundUnit{}, nil
	}
	rows, err := g.q.GateUnitsHeard(ctx, db.GateUnitsHeardParams{Units: candidates, ScanLimit: unitScan, PerUnit: unitCalls})
	if err != nil {
		return nil, fmt.Errorf("unit search: %s", store.SafeError(err))
	}

	// A radio is found in a system when one of its calls there is one the
	// call details would give: its talkgroup in the catalogue and no delay
	// still holding it (Gate.held).
	type found struct {
		FoundUnit
		tier  int
		lower string
	}
	got := map[unitKey]bool{}
	var out []found
	for _, r := range rows {
		k := unitKey{r.SystemID, r.Unit}
		if got[k] {
			continue
		}
		if _, ok := cat.talkgroup(r.SystemID, r.Talkgroup); !ok {
			continue
		}
		var ended *time.Time
		if r.EndedAt.Valid {
			t := r.EndedAt.Time
			ended = &t
		}
		if g.delayHolds(cat.delayOf(r.SystemID, r.Talkgroup, r.PatchedTalkgroups), ended) {
			continue
		}
		alias := cat.UnitAlias(r.SystemID, r.Unit)
		lower := strings.ToLower(alias)
		tier := unitTier(q, digits, r.Unit, lower)
		if tier == 3 {
			continue
		}
		got[k] = true
		out = append(out, found{FoundUnit{System: r.SystemID, ID: r.Unit, Alias: alias}, tier, lower})
	}
	order := make(map[int64]int, len(cat.Systems))
	for i, s := range cat.Systems {
		order[s.ID] = i
	}
	slices.SortFunc(out, func(a, b found) int {
		if c := cmp.Compare(a.tier, b.tier); c != 0 {
			return c
		}
		if !digits {
			if c := cmp.Compare(a.lower, b.lower); c != 0 {
				return c
			}
		}
		return cmp.Or(cmp.Compare(a.ID, b.ID), cmp.Compare(order[a.System], order[b.System]))
	})
	res := make([]FoundUnit, 0, min(len(out), MaxUnitsFound))
	for _, f := range out {
		if len(res) == MaxUnitsFound {
			break
		}
		res = append(res, f.FoundUnit)
	}
	return res, nil
}
