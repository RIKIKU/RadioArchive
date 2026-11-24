package public

import (
	"encoding/base64"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func testID(i int) string { return fmt.Sprintf("%08x-0000-4000-8000-%012x", i, i) }

// A list packs and unpacks to itself, in its one packed form.
func TestZipPackRoundTrip(t *testing.T) {
	req := ZipRequest{
		Calls: []string{testID(1), "not an ID", testID(2), testID(1), testID(3)},
		Conversations: []ZipConversation{
			{Name: "Structure fire", Note: "Nörd, 2 alarms", Calls: []string{testID(4), testID(3), testID(4)}},
			{Name: "", Calls: []string{"1003"}},
			{Name: "Second", Calls: []string{testID(5), testID(4)}},
		},
		Part: 2, Parts: 3,
	}
	want := zipList{
		ids:   []string{testID(1), testID(2), testID(4), testID(3), testID(5)},
		loose: []string{testID(1), testID(2)},
		convs: []zipConv{
			{name: "Structure fire", note: "Nörd, 2 alarms", calls: []string{testID(4), testID(3)}},
			{name: "Second", calls: []string{testID(5), testID(4)}},
		},
		part: 2, parts: 3,
	}
	if got := listOf(req); !reflect.DeepEqual(got, want) {
		t.Fatalf("listOf:\n%+v\nwant\n%+v", got, want)
	}
	url := ZipURL(req)
	got, err := unpack(strings.TrimPrefix(url, zipPrefix))
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("unpacked %v:\n%+v\nwant\n%+v", err, got, want)
	}
	// The example in ADR 0015.
	ex := ZipURL(ZipRequest{Calls: []string{"0f8e2c1a-6b3d-4e5f-9a7b-1c2d3e4f5a6b"},
		Conversations: []ZipConversation{{Name: "Structure fire", Calls: []string{"6f1d2c3b-4a59-4e8f-9d7c-0b1a2c3d4e5f"}}}})
	if ex != "/api/v1/zip/AQAAAg-OLBprPU5fmnscLT5PWmtvHSw7SllOj518CxosPU5fAQ5TdHJ1Y3R1cmUgZmlyZQABAQ" {
		t.Errorf("the ADR's example packs as %s", ex)
	}
}

// Anything but a list's one packed form is refused.
func TestZipUnpackRefuses(t *testing.T) {
	enc := base64.RawURLEncoding.EncodeToString
	id := make([]byte, 16)
	valid := append(append([]byte{1, 0, 0, 1}, id...), 0)
	if _, err := unpack(enc(valid)); err != nil {
		t.Fatalf("a valid list: %v", err)
	}
	for why, s := range map[string]string{
		"empty":                    "",
		"not base64url":            "AQ+A",
		"padded":                   enc(valid) + "=",
		"another version":          enc(append([]byte{2}, valid[1:]...)),
		"trailing data":            enc(append(valid, 0)),
		"truncated":                enc(valid[:10]),
		"a long varint":            enc(append(append([]byte{1, 0, 0, 0x81, 0}, id...), 0)),
		"more calls than bytes":    enc([]byte{1, 0, 0, 5}),
		"a call twice":             enc(append(append(append([]byte{1, 0, 0, 2}, id...), id...), 0)),
		"an index out of range":    enc(append(append([]byte{1, 0, 0, 1}, id...), 1, 0, 0, 1, 1)),
		"an empty conversation":    enc(append(append([]byte{1, 0, 0, 1}, id...), 1, 0, 0, 0)),
		"a call twice in one":      enc(append(append([]byte{1, 0, 0, 1}, id...), 1, 0, 0, 2, 0, 0)),
		"a name past the end":      enc(append(append([]byte{1, 0, 0, 1}, id...), 1, 9, 'a')),
		"conversation calls first": enc(conversationFirst()),
	} {
		if _, err := unpack(s); err == nil {
			t.Errorf("%s: accepted", why)
		}
	}
}

// A conversation's call before a loose one in the table: not the packed
// form, where loose calls come first.
func conversationFirst() []byte {
	b := []byte{1, 0, 0, 2}
	b = append(b, make([]byte, 16)...)
	one := make([]byte, 16)
	one[15] = 1
	b = append(b, one...)
	return append(b, 1, 0, 0, 1, 0) // a conversation of call 0; call 1 loose
}

func TestZipListBounds(t *testing.T) {
	ids := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = testID(i)
		}
		return out
	}
	for why, c := range map[string]struct {
		req  ZipRequest
		want error
	}{
		"500 calls":          {ZipRequest{Calls: ids(500)}, nil},
		"501 calls":          {ZipRequest{Calls: ids(501)}, errZipTooMany},
		"a part":             {ZipRequest{Calls: ids(1), Part: 1, Parts: 1}, nil},
		"part 0 of 2":        {ZipRequest{Calls: ids(1), Part: 0, Parts: 2}, errZipInvalid},
		"part 3 of 2":        {ZipRequest{Calls: ids(1), Part: 3, Parts: 2}, errZipInvalid},
		"too many parts":     {ZipRequest{Calls: ids(1), Part: 1, Parts: MaxZipParts + 1}, errZipInvalid},
		"a name at its most": {ZipRequest{Conversations: []ZipConversation{{Name: strings.Repeat("n", MaxConversationName), Calls: ids(1)}}}, nil},
		"a long name":        {ZipRequest{Conversations: []ZipConversation{{Name: strings.Repeat("n", MaxConversationName+1), Calls: ids(1)}}}, errZipInvalid},
		"a long note":        {ZipRequest{Conversations: []ZipConversation{{Note: strings.Repeat("n", MaxConversationNote+1), Calls: ids(1)}}}, errZipInvalid},
	} {
		if got := listOf(c.req).check(); got != c.want {
			t.Errorf("%s: %v, want %v", why, got, c.want)
		}
	}
}

// The URL budget against the most calls a part may hold (R72): 500 calls
// don't fit in MaxZipURL, whatever the packing of a 128-bit ID, so the
// browser splits at the budget first. These are the numbers ADR 0015
// gives.
func TestZipURLSizes(t *testing.T) {
	ids := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = testID(i)
		}
		return out
	}
	if n := len(ZipURL(ZipRequest{Calls: ids(500)})); n != 10687 {
		t.Errorf("500 loose calls: %d characters", n)
	}
	most := 0
	for n := 1; len(ZipURL(ZipRequest{Calls: ids(n)})) <= MaxZipURL; n++ {
		most = n
	}
	inConv := 0
	for n := 1; len(ZipURL(ZipRequest{Conversations: []ZipConversation{{Name: "Structure fire", Calls: ids(n)}}})) <= MaxZipURL; n++ {
		inConv = n
	}
	if most != 359 || inConv != 325 {
		t.Errorf("at most %d loose calls, %d in one conversation, fit %d characters", most, inConv, MaxZipURL)
	}
}
