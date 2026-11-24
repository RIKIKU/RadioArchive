package gate

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/RIKIKU/RadioArchive/internal/store"
	"github.com/RIKIKU/RadioArchive/internal/store/db"
)

// The catalogue (SPEC.md R15, R22, R24, R42, R74, R77, R101; ADR 0006): the
// public talkgroups, read only through the public_talkgroups view, whose
// condition is call_state's, as public_calls's is. It's held in memory and
// rebuilt whenever the catalogue's generation number has moved, which the
// gate reads on every request, so hiding a talkgroup takes it out at the
// next request.

// ErrChannelUnavailable is the fireground lookup's only refusal: no
// talkgroup has the number, or the one that has it is hidden or ignored, or
// the number isn't one (R33, R101).
var ErrChannelUnavailable = errors.New("this channel isn't available")

// CatalogueTalkgroup is a public talkgroup as listeners see it.
type CatalogueTalkgroup struct {
	System System
	ID     int32
	Label  string
	// Name is what's shown (R24): the name, else the label, else the ID.
	Name   string
	Tag    string
	Groups []string // in the groups' order
	// LEDColour and Alert are the effective ones (ADR 0006); "" is none.
	LEDColour    string
	Alert        string
	DelayMinutes int32
	Fireground   *int32
}

// Catalogue is every public talkgroup, at one generation. It never changes;
// a newer one replaces it.
type Catalogue struct {
	Generation int64
	// Systems, Tags and Groups are only those its talkgroups name, in their
	// order: a system, tag or group with only hidden talkgroups isn't there.
	Systems    []System
	Tags       []string
	Groups     []string
	Talkgroups []CatalogueTalkgroup

	fireground map[int32]int        // number → index in Talkgroups
	byID       map[talkgroupKey]int // system and talkgroup → index in Talkgroups
	keys       [][]string           // per talkgroup: name, label, tag, ID, lower case
	delayed    bool                 // some talkgroup has a delay
}

type talkgroupKey struct {
	system    int64
	talkgroup int32
}

// talkgroup finds a public talkgroup.
func (c *Catalogue) talkgroup(system int64, talkgroup int32) (CatalogueTalkgroup, bool) {
	i, ok := c.byID[talkgroupKey{system, talkgroup}]
	if !ok {
		return CatalogueTalkgroup{}, false
	}
	return c.Talkgroups[i], true
}

// delayOf is the delay of a call on talkgroup patched with patched (R23):
// the longest in its patch, since the call is heard on each of them. A
// patched ID the system doesn't list has none, as it has no state.
func (c *Catalogue) delayOf(system int64, talkgroup int32, patched []int32) int32 {
	if !c.delayed {
		return 0
	}
	var d int32
	for _, tg := range append([]int32{talkgroup}, patched...) {
		if t, ok := c.talkgroup(system, tg); ok {
			d = max(d, t.DelayMinutes)
		}
	}
	return d
}

// Catalogue returns the catalogue as it is now.
func (g *Gate) Catalogue(ctx context.Context) (*Catalogue, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	// The generation is read before the talkgroups, so a catalogue is never
	// older than the number it's kept under (migration 00007).
	gen, err := g.q.GateCatalogueGeneration(ctx)
	if err != nil {
		return nil, fmt.Errorf("catalogue generation: %s", store.SafeError(err))
	}
	g.catMu.Lock()
	defer g.catMu.Unlock()
	if g.cat != nil && g.cat.Generation == gen {
		return g.cat, nil
	}
	rows, err := g.q.GateCatalogue(ctx)
	if err != nil {
		return nil, fmt.Errorf("catalogue: %s", store.SafeError(err))
	}
	c, err := catalogueOf(gen, rows)
	if err != nil {
		return nil, err
	}
	g.cat = c
	return c, nil
}

type catalogueGroup struct {
	Name      string  `json:"name"`
	SortOrder int32   `json:"sort_order"`
	Alert     *string `json:"alert"`
	LEDColour *string `json:"led_colour"`
}

type ordered struct {
	name string
	sort int32
}

func catalogueOf(gen int64, rows []db.GateCatalogueRow) (*Catalogue, error) {
	c := &Catalogue{Generation: gen, Systems: []System{}, Tags: []string{}, Groups: []string{},
		Talkgroups: make([]CatalogueTalkgroup, 0, len(rows)), fireground: map[int32]int{},
		byID: make(map[talkgroupKey]int, len(rows)), keys: make([][]string, 0, len(rows))}
	seenSystem := map[int64]bool{}
	tags, groups := map[string]int32{}, map[string]int32{}
	for _, r := range rows {
		var gs []catalogueGroup
		if err := json.Unmarshal(r.Groups, &gs); err != nil {
			return nil, fmt.Errorf("catalogue groups: %w", err)
		}
		sys := System{ID: r.SystemID, Label: r.SystemLabel, Name: r.SystemName}
		if !seenSystem[sys.ID] {
			seenSystem[sys.ID] = true
			c.Systems = append(c.Systems, sys)
		}
		t := CatalogueTalkgroup{System: sys, ID: r.TalkgroupID, Label: r.Label,
			Name: displayName(r.TalkgroupID, r.Name, r.Label), Tag: deref(r.Tag),
			Groups: make([]string, 0, len(gs)), DelayMinutes: r.DelayMinutes, Fireground: r.FiregroundChannel}
		if r.Tag != nil {
			tags[*r.Tag] = deref32(r.TagSortOrder)
		}
		alerts := []*string{r.Alert, r.TagAlert}
		leds := []*string{r.LedColour, r.TagLedColour}
		for _, g := range gs {
			t.Groups = append(t.Groups, g.Name)
			groups[g.Name] = g.SortOrder
			alerts, leds = append(alerts, g.Alert), append(leds, g.LEDColour)
		}
		t.Alert = effective(append(alerts, r.SystemAlert))
		t.LEDColour = effective(append(leds, r.SystemLedColour))
		if t.Fireground != nil {
			c.fireground[*t.Fireground] = len(c.Talkgroups)
		}
		c.byID[talkgroupKey{sys.ID, t.ID}] = len(c.Talkgroups)
		c.delayed = c.delayed || t.DelayMinutes > 0
		c.Talkgroups = append(c.Talkgroups, t)
		c.keys = append(c.keys, []string{strings.ToLower(t.Name), strings.ToLower(t.Label),
			strings.ToLower(t.Tag), strconv.Itoa(int(t.ID))})
	}
	c.Tags, c.Groups = inOrder(tags), inOrder(groups)
	return c, nil
}

// effective is the first setting given, in the order ADR 0006 takes them:
// the talkgroup's own, its tag's, its groups', its system's. "none" stops
// there with none.
func effective(from []*string) string {
	for _, s := range from {
		if s == nil {
			continue
		}
		if *s == store.NoSetting {
			return ""
		}
		return *s
	}
	return ""
}

func inOrder(m map[string]int32) []string {
	xs := make([]ordered, 0, len(m))
	for n, s := range m {
		xs = append(xs, ordered{n, s})
	}
	slices.SortFunc(xs, func(a, b ordered) int { return cmp.Or(cmp.Compare(a.sort, b.sort), cmp.Compare(a.name, b.name)) })
	out := make([]string, len(xs))
	for i, o := range xs {
		out[i] = o.name
	}
	return out
}

func deref32(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

// Search finds talkgroups by name, label, ID or tag (R42, R77), ignoring
// case: those it names exactly first, then those with a field or word
// starting with q, then those containing it, each in the catalogue's order.
// An empty query finds every talkgroup.
func (c *Catalogue) Search(q string) []CatalogueTalkgroup {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return c.Talkgroups
	}
	var tiers [4][]CatalogueTalkgroup // the last is no match
	for i, keys := range c.keys {
		tier := 3
		for _, k := range keys {
			switch {
			case k == q:
				tier = 0
			case strings.HasPrefix(k, q) || strings.Contains(k, " "+q):
				tier = min(tier, 1)
			case strings.Contains(k, q):
				tier = min(tier, 2)
			}
		}
		tiers[tier] = append(tiers[tier], c.Talkgroups[i])
	}
	return slices.Concat(tiers[0], tiers[1], tiers[2])
}

// MaxFiregroundDigits is the longest fireground channel number the lookup
// reads, leading zeros included.
const MaxFiregroundDigits = 16

// ParseFireground reads a fireground channel number (R100): decimal digits
// only, leading zeros not counting.
func ParseFireground(s string) (int32, bool) {
	if s == "" || len(s) > MaxFiregroundDigits {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	s = strings.TrimLeft(s, "0")
	if s == "" {
		return 0, true
	}
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return 0, false
	}
	return int32(n), true
}

// Fireground finds the public talkgroup with a fireground channel number
// (R101), or answers ErrChannelUnavailable, whatever the reason.
func (c *Catalogue) Fireground(number string) (CatalogueTalkgroup, error) {
	n, ok := ParseFireground(number)
	if !ok {
		return CatalogueTalkgroup{}, ErrChannelUnavailable
	}
	i, ok := c.fireground[n]
	if !ok {
		return CatalogueTalkgroup{}, ErrChannelUnavailable
	}
	return c.Talkgroups[i], nil
}
