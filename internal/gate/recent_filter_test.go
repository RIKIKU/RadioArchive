package gate

import (
	"slices"
	"testing"
)

// testCatalogue is a catalogue of public talkgroups, by system and ID.
func testCatalogue(tgs ...talkgroupKey) *Catalogue {
	c := &Catalogue{byID: map[talkgroupKey]int{}}
	for i, k := range tgs {
		c.Talkgroups = append(c.Talkgroups, CatalogueTalkgroup{System: System{ID: k.system}, ID: k.talkgroup})
		c.byID[k] = i
	}
	return c
}

// ADR 0049: a set naming every public talkgroup is no filter, whatever else
// it names; one naming some is that filter; one naming none finds nothing.
func TestFilterOf(t *testing.T) {
	cat := testCatalogue(talkgroupKey{1, 1001}, talkgroupKey{1, 1002}, talkgroupKey{2, 1001})
	for _, c := range []struct {
		what    string
		set     *ListeningSet
		ids     []int32
		nothing bool
	}{
		{"no set", nil, nil, false},
		{"every one by ID", &ListeningSet{Any: []int32{1001, 1002}}, nil, false},
		{"every one by system, and more that don't exist", &ListeningSet{InSystem: map[int64][]int32{1: {1001, 1002, 4711}, 2: {1001}, 3: {9}}}, nil, false},
		{"all but one", &ListeningSet{InSystem: map[int64][]int32{1: {1001, 1002}}}, []int32{1001, 1002}, false},
		{"1001 in any system", &ListeningSet{Any: []int32{1001}}, []int32{1001}, false},
		{"none public", &ListeningSet{Any: []int32{4711}}, nil, true},
		{"an empty set", &ListeningSet{}, nil, true},
	} {
		named, ids, nothing := cat.filterOf(c.set)
		slices.Sort(ids)
		if !slices.Equal(ids, c.ids) || nothing != c.nothing || (ids == nil) != (named == nil) {
			t.Errorf("%s: ids %v named %v nothing %v, want ids %v nothing %v", c.what, ids, named, nothing, c.ids, c.nothing)
		}
	}
}
