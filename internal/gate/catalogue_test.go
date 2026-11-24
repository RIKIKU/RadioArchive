package gate

import (
	"slices"
	"testing"

	"github.com/RIKIKU/RadioArchive/internal/store/db"
)

func s(v string) *string { return &v }

func TestParseFireground(t *testing.T) {
	for in, want := range map[string]int32{"41": 41, "041": 41, "0000041": 41, "0": 0, "000": 0, "2147483647": 1<<31 - 1} {
		if got, ok := ParseFireground(in); !ok || got != want {
			t.Errorf("%q = %d %v, want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "-1", "+1", "4.1", "41 ", " 41", "4a", "2147483648", "00000000000000041", "٤١"} {
		if got, ok := ParseFireground(in); ok {
			t.Errorf("%q = %d, want refused", in, got)
		}
	}
}

// ADR 0006: the talkgroup's own, its tag's, its groups' in order, its
// system's; "none" stops there.
func TestEffective(t *testing.T) {
	for _, c := range []struct {
		from []*string
		want string
	}{
		{nil, ""},
		{[]*string{nil, nil, nil}, ""},
		{[]*string{s("red"), s("blue")}, "red"},
		{[]*string{nil, s("blue"), s("green")}, "blue"},
		{[]*string{nil, s("none"), s("green")}, ""},
		{[]*string{nil, nil, s("green")}, "green"},
	} {
		if got := effective(c.from); got != c.want {
			t.Errorf("effective(%v) = %q, want %q", c.from, got, c.want)
		}
	}
}

func TestCatalogueOf(t *testing.T) {
	rows := []db.GateCatalogueRow{
		{SystemID: 1, SystemLabel: "EX", SystemLedColour: s("blue"), TalkgroupID: 1001, Label: "DISP N",
			Name: "Dispatch North", Tag: s("dispatch"), TagSortOrder: new(int32),
			Groups: []byte(`[{"name":"South","sort_order":0,"alert":"pip","led_colour":null},{"name":"North","sort_order":5,"alert":null,"led_colour":"red"}]`)},
		{SystemID: 1, SystemLabel: "EX", TalkgroupID: 1002, Label: "FG 41", Tag: s("fireground"),
			TagSortOrder: new(int32), FiregroundChannel: func() *int32 { n := int32(41); return &n }(), Groups: []byte(`[]`)},
		{SystemID: 1, SystemLabel: "EX", TalkgroupID: 1006, Groups: []byte(`[]`)},
	}
	c, err := catalogueOf(7, rows)
	if err != nil {
		t.Fatal(err)
	}
	if c.Generation != 7 || len(c.Systems) != 1 || !slices.Equal(c.Tags, []string{"dispatch", "fireground"}) ||
		!slices.Equal(c.Groups, []string{"South", "North"}) {
		t.Errorf("catalogue %+v", c)
	}
	d := c.Talkgroups[0]
	if d.Alert != "pip" || d.LEDColour != "red" || !slices.Equal(d.Groups, []string{"South", "North"}) {
		t.Errorf("1001 %+v", d)
	}
	if c.Talkgroups[1].Name != "FG 41" || c.Talkgroups[1].LEDColour != "" || c.Talkgroups[2].Name != "1006" {
		t.Errorf("names %+v", c.Talkgroups)
	}
	if got, err := c.Fireground("041"); err != nil || got.ID != 1002 {
		t.Errorf("fireground 041 = %+v %v", got, err)
	}
	for _, n := range []string{"99", "x", ""} {
		if _, err := c.Fireground(n); err != ErrChannelUnavailable {
			t.Errorf("fireground %q: %v", n, err)
		}
	}
	ids := func(ts []CatalogueTalkgroup) []int32 {
		var out []int32
		for _, x := range ts {
			out = append(out, x.ID)
		}
		return out
	}
	for q, want := range map[string][]int32{"": {1001, 1002, 1006}, "FG": {1002}, "1006": {1006},
		"north": {1001}, "Dispatch": {1001}, "zzz": nil} {
		if got := ids(c.Search(q)); !slices.Equal(got, want) {
			t.Errorf("search %q = %v, want %v", q, got, want)
		}
	}
}
