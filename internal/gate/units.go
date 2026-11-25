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
// hidden talkgroup is found by neither its alias nor its ID, and a range
// whose radios are heard only there isn't found at all.

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

// FoundUnit is what unit search found: a radio heard on a public call, or
// a range alias one of whose radios is (ADR 0019).
type FoundUnit struct {
	System int64
	// ID is a radio's; 0 for a range.
	ID int64
	// First and Last are a range's radio IDs, both included; 0 for a radio.
	First, Last int64
	// Alias is a radio's own alias (from a unit of its own, never a
	// range's), or a range's; "" when a radio has none of its own.
	Alias string
	// In is, for a radio with no unit of its own, the alias its calls name
	// it by: the narrowest range covering it (R26). "" otherwise.
	In string
}

// IsRange says whether f is a range alias rather than a radio.
func (f FoundUnit) IsRange() bool { return f.First != f.Last }

// Bounds of unit search (ADR 0019).
const (
	// MaxUnitsFound is the most units one search answers.
	MaxUnitsFound = 20
	// unitCandidates is the most radio IDs one search checks, and
	// rangeCandidates the most of them one range alias may take.
	unitCandidates  = 60
	rangeCandidates = 20
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

// spanTyped is the query as a range's radio IDs, "5100-5199" or the start
// of it ("5100-", "5100-51"), spaces taken out; "" when it isn't one.
func spanTyped(q string) string {
	t := strings.ReplaceAll(q, " ", "")
	lo, hi, ok := strings.Cut(t, "-")
	if !ok || lo == "" || strings.Trim(lo, "0123456789") != "" || strings.Trim(hi, "0123456789") != "" {
		return ""
	}
	return t
}

// rangeTier is how well a range alias matches a search: by its alias
// (unitTier's tiers), or, when the query is a range's radio IDs, by its
// own (0 the same, 1 they start with it). A number alone never matches a
// range: typing a radio ID offers the radio, not a range (ADR 0019).
func rangeTier(q string, digits bool, span string, d unitDef) int {
	switch {
	case span != "":
		text := fmt.Sprintf("%d-%d", d.first, d.last)
		if text == span {
			return 0
		}
		if strings.HasPrefix(text, span) {
			return 1
		}
		return 3
	case digits:
		return 3
	}
	return unitTier(q, false, 0, d.lower)
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

// SearchUnits finds units heard on public calls (R27, R32; ADR 0019), as
// PagerArchive's selection field offers capcodes and range aliases:
//
//   - A radio by its own alias or its radio ID, ignoring case. One with no
//     unit of its own is found by its ID only, with In naming the range
//     its calls show it by ("5150 in Portables").
//   - A range alias by its alias, or by its radio IDs or the start of them
//     ("5100-5199", "5100-"): choosing a range on purpose. A number alone
//     never finds a range.
//
// Those named exactly come first, then those whose alias (or a word of it)
// or ID starts with q, then those whose alias contains it; each tier by ID
// for a number, else by alias. At most MaxUnitsFound. A radio heard only on
// hidden or ignored talkgroups, or only on calls still within their delay
// (R23), isn't found, whatever its alias, so the answer is the one a radio
// never heard gets; and a range is found only when one of its radios is
// heard on a call the public may have, so a range heard only there answers
// as one never heard. An empty query finds nothing.
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
	span := ""
	if !digits {
		span = spanTyped(q)
	}

	// Where to look, best first: the radios whose own alias matches, the
	// IDs that start with a number, and the ranges that match, each a span
	// of radio IDs to read the heard ones of.
	type look struct {
		tier     int
		from, to int64
		rng      int // index in ranges, or -1 for radios
	}
	var looks []look
	var ranges []unitDef
	var rangeTiers []int
	for _, d := range cat.units.defs {
		if d.lower == "" {
			continue
		}
		if d.first == d.last {
			if t := unitTier(q, false, 0, d.lower); t < 3 {
				looks = append(looks, look{t, d.first, d.last, -1})
			}
			continue
		}
		if t := rangeTier(q, digits, span, d); t < 3 {
			looks = append(looks, look{t, d.first, d.last, len(ranges)})
			ranges = append(ranges, d)
			rangeTiers = append(rangeTiers, t)
		}
	}
	if digits {
		for _, s := range idSpans(q) {
			looks = append(looks, look{0, s[0], s[1], -1})
		}
	}
	slices.SortStableFunc(looks, func(a, b look) int { return cmp.Compare(a.tier, b.tier) })

	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	seen := map[int64]bool{}
	var candidates []int64
	inRange := make([][]int64, len(ranges)) // the heard radio IDs read for each range
	for _, l := range looks {
		left := unitCandidates - len(candidates)
		if left <= 0 {
			break
		}
		if l.rng >= 0 {
			left = min(left, rangeCandidates)
		}
		ids, err := g.q.GateUnitsIn(ctx, db.GateUnitsInParams{FirstID: l.from, LastID: l.to, RowLimit: int32(left)})
		if err != nil {
			return nil, fmt.Errorf("unit search: %s", store.SafeError(err))
		}
		for _, id := range ids {
			if id <= 0 {
				continue
			}
			if l.rng >= 0 {
				inRange[l.rng] = append(inRange[l.rng], id)
			}
			if !seen[id] && len(candidates) < unitCandidates {
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

	// A radio is heard in a system when one of its calls there is one the
	// call details would give: its talkgroup in the catalogue and no delay
	// still holding it (Gate.held).
	heard := map[unitKey]bool{}
	var order []unitKey
	for _, r := range rows {
		k := unitKey{r.SystemID, r.Unit}
		if heard[k] {
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
		heard[k] = true
		order = append(order, k)
	}

	type found struct {
		FoundUnit
		tier  int
		lower string
	}
	var out []found
	// Each radio is matched again on its own alias in its own system, so
	// neither a range's alias nor another system's names it.
	for _, k := range order {
		own, has := cat.units.exact[k]
		f := FoundUnit{System: k.system, ID: k.unit, First: k.unit, Last: k.unit, Alias: own}
		if !has {
			f.In = cat.UnitAlias(k.system, k.unit)
		}
		lower := strings.ToLower(own)
		if tier := unitTier(q, digits, k.unit, lower); tier < 3 {
			out = append(out, found{f, tier, lower})
		}
	}
	// A range is found when one of its radios read is heard in its system.
	for i, d := range ranges {
		for _, id := range inRange[i] {
			if heard[unitKey{d.system, id}] {
				out = append(out, found{FoundUnit{System: d.system, First: d.first, Last: d.last, Alias: d.alias}, rangeTiers[i], d.lower})
				break
			}
		}
	}
	sysOrder := make(map[int64]int, len(cat.Systems))
	for i, s := range cat.Systems {
		sysOrder[s.ID] = i
	}
	isRange := func(f found) int {
		if f.IsRange() {
			return 1
		}
		return 0
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
		return cmp.Or(cmp.Compare(a.First, b.First), cmp.Compare(isRange(a), isRange(b)), cmp.Compare(a.Last, b.Last),
			cmp.Compare(sysOrder[a.System], sysOrder[b.System]))
	})
	res := make([]FoundUnit, 0, min(len(out), MaxUnitsFound))
	for _, f := range out {
		if len(res) == MaxUnitsFound {
			break
		}
		if !f.IsRange() {
			f.First, f.Last = 0, 0
		}
		res = append(res, f.FoundUnit)
	}
	return res, nil
}
