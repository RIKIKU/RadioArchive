package public

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/RIKIKU/RadioArchive/internal/gate"
)

// The zip's list of calls, as its GET URL packs it (SPEC.md R65; ADR 0015).
//
// The packed form is base64url, without padding, of:
//
//	version   1 byte, 1
//	part      uvarint: 0 for a whole basket, else this part's number
//	parts     uvarint: 0 for a whole basket, else how many parts
//	n         uvarint: how many calls
//	calls     n × 16 bytes: each call's ID, its UUID's bytes; first the
//	          loose calls in their order, then the conversations' calls in
//	          the order they first appear
//	m         uvarint: how many conversations
//	m × conversation:
//	  name    uvarint byte length, then UTF-8
//	  note    uvarint byte length, then UTF-8
//	  k       uvarint: how many calls, at least 1
//	  k × uvarint: each call's index in calls, each once
//
// A call no conversation names is loose. Every list has one packed form:
// anything else (a longer varint, calls out of that order, a call twice)
// isn't one, and nor is trailing data.

// ZipRequest is a zip's list of calls (R65), as QUERY and POST take it:
// the loose calls, the conversations with their names, notes and calls in
// order, and, for one part of a basket split in parts (R72), its number
// and how many there are.
type ZipRequest struct {
	Calls         []string          `json:"calls"`
	Conversations []ZipConversation `json:"conversations"`
	Part          int               `json:"part,omitempty"`
	Parts         int               `json:"parts,omitempty"`
}

// ZipConversation is a conversation in a zip's list.
type ZipConversation struct {
	Name  string   `json:"name"`
	Note  string   `json:"note"`
	Calls []string `json:"calls"`
}

// Bounds of a zip's list (ADR 0015). MaxCalls (500) bounds a part's calls
// (R72), as it bounds every call list (R73).
const (
	// MaxZipURL is the longest zip URL (path and query, as the request
	// line has it) the server takes: inside nginx's default 8 KB
	// large_client_header_buffers with room for a path prefix, so a URL
	// that works here works behind a default reverse proxy.
	MaxZipURL = 7680
	// MaxConversationName and MaxConversationNote are in bytes of UTF-8.
	MaxConversationName = 200
	MaxConversationNote = 2000
	// MaxZipParts is the most parts a basket is split into.
	MaxZipParts = 100

	zipFormat = 1
	zipPrefix = "/api/v1/zip/"
)

var (
	errZipInvalid = errors.New("not a zip list")
	errZipTooMany = errors.New("too many calls for one zip")
)

// zipList is a zip's list as the server reads it: call IDs canonical, each
// call once in ids, loose or in conversations but not both.
type zipList struct {
	ids         []string // loose, then the conversations' calls as they first appear
	loose       []string
	convs       []zipConv
	part, parts int
}

type zipConv struct {
	name, note string
	calls      []string
}

// listOf reads a ZipRequest as a zipList. Strings that aren't call IDs
// name no call, as in every call list (ADR 0004), and are left out; so is a
// call named twice in one place, a loose call also in a conversation (it's
// in the conversation), and a conversation left with no calls.
func listOf(req ZipRequest) zipList {
	l := zipList{part: req.Part, parts: req.Parts}
	inConv := map[string]bool{}
	for _, c := range req.Conversations {
		conv := zipConv{name: c.Name, note: c.Note}
		for _, s := range c.Calls {
			if id, ok := gate.ParseCallID(s); ok && !slices.Contains(conv.calls, id) {
				conv.calls = append(conv.calls, id)
				inConv[id] = true
			}
		}
		if len(conv.calls) > 0 {
			l.convs = append(l.convs, conv)
		}
	}
	seen := map[string]bool{}
	for _, s := range req.Calls {
		if id, ok := gate.ParseCallID(s); ok && !inConv[id] && !seen[id] {
			seen[id] = true
			l.loose = append(l.loose, id)
		}
	}
	l.ids = slices.Clone(l.loose)
	for _, c := range l.convs {
		for _, id := range c.calls {
			if !seen[id] {
				seen[id] = true
				l.ids = append(l.ids, id)
			}
		}
	}
	return l
}

// check refuses a list past the bounds.
func (l zipList) check() error {
	if len(l.ids) > gate.MaxCalls {
		return errZipTooMany
	}
	if len(l.convs) > gate.MaxCalls || l.part < 0 || l.parts < 0 || l.part > l.parts ||
		(l.part == 0) != (l.parts == 0) || l.parts > MaxZipParts {
		return errZipInvalid
	}
	for _, c := range l.convs {
		if len(c.name) > MaxConversationName || len(c.note) > MaxConversationNote ||
			!utf8.ValidString(c.name) || !utf8.ValidString(c.note) {
			return errZipInvalid
		}
	}
	return nil
}

// ZipURL is the GET URL of the zip of req's calls (ADR 0015), packed as
// listOf reads req. It doesn't check the bounds: the server does.
func ZipURL(req ZipRequest) string {
	return zipPrefix + base64.RawURLEncoding.EncodeToString(listOf(req).pack())
}

func (l zipList) pack() []byte {
	b := []byte{zipFormat}
	b = binary.AppendUvarint(b, uint64(l.part))
	b = binary.AppendUvarint(b, uint64(l.parts))
	b = binary.AppendUvarint(b, uint64(len(l.ids)))
	index := make(map[string]int, len(l.ids))
	for i, id := range l.ids {
		raw, _ := hex.DecodeString(strings.ReplaceAll(id, "-", "")) // canonical IDs
		b = append(b, raw...)
		index[id] = i
	}
	b = binary.AppendUvarint(b, uint64(len(l.convs)))
	for _, c := range l.convs {
		b = binary.AppendUvarint(b, uint64(len(c.name)))
		b = append(b, c.name...)
		b = binary.AppendUvarint(b, uint64(len(c.note)))
		b = append(b, c.note...)
		b = binary.AppendUvarint(b, uint64(len(c.calls)))
		for _, id := range c.calls {
			b = binary.AppendUvarint(b, uint64(index[id]))
		}
	}
	return b
}

// unpack reads a packed list: errZipInvalid unless it's the one packed
// form of a list.
func unpack(s string) (zipList, error) {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil || len(raw) == 0 || raw[0] != zipFormat {
		return zipList{}, errZipInvalid
	}
	b := raw[1:]
	uvarint := func(most uint64) (int, bool) {
		v, n := binary.Uvarint(b)
		if n <= 0 || v > most {
			return 0, false
		}
		b = b[n:]
		return int(v), true
	}
	text := func() (string, bool) {
		n, ok := uvarint(uint64(len(b)))
		if !ok || n > len(b) {
			return "", false
		}
		s := string(b[:n])
		b = b[n:]
		return s, true
	}
	var l zipList
	var ok bool
	if l.part, ok = uvarint(MaxZipParts); !ok {
		return zipList{}, errZipInvalid
	}
	if l.parts, ok = uvarint(MaxZipParts); !ok {
		return zipList{}, errZipInvalid
	}
	n, ok := uvarint(uint64(len(b) / 16))
	if !ok {
		return zipList{}, errZipInvalid
	}
	l.ids = make([]string, n)
	for i := range n {
		u := b[:16]
		b = b[16:]
		l.ids[i] = fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
	}
	m, ok := uvarint(uint64(len(b)))
	if !ok {
		return zipList{}, errZipInvalid
	}
	named := make([]bool, n)
	for range m {
		var c zipConv
		var ok1, ok2 bool
		c.name, ok1 = text()
		c.note, ok2 = text()
		k, ok3 := uvarint(uint64(n))
		if !ok1 || !ok2 || !ok3 || k == 0 {
			return zipList{}, errZipInvalid
		}
		for range k {
			i, ok := uvarint(uint64(n - 1))
			if !ok || slices.Contains(c.calls, l.ids[i]) {
				return zipList{}, errZipInvalid
			}
			c.calls = append(c.calls, l.ids[i])
			named[i] = true
		}
		l.convs = append(l.convs, c)
	}
	if len(b) != 0 {
		return zipList{}, errZipInvalid
	}
	req := ZipRequest{Part: l.part, Parts: l.parts}
	for i, id := range l.ids {
		if !named[i] {
			req.Calls = append(req.Calls, id)
		}
	}
	for _, c := range l.convs {
		req.Conversations = append(req.Conversations, ZipConversation{Name: c.name, Note: c.note, Calls: c.calls})
	}
	// One list, one packed form: the calls in their order, each once,
	// and every varint as short as it can be.
	canon := listOf(req)
	if !bytes.Equal(canon.pack(), raw) {
		return zipList{}, errZipInvalid
	}
	return canon, nil
}
