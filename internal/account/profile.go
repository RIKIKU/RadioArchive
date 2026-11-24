package account

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/RIKIKU/RadioArchive/internal/gate"
)

// Attribute is the Authentik user attribute a listener's saved settings
// live in (R110, ADR 0009).
const Attribute = "radioarchive"

// Profile is what a listener's account holds (R41, R48, R64, R110): the
// listening set with its priority marks, the playback settings, the look
// and the download basket with its conversations. Hold and talkgroups
// added for the session are never in it (R41).
//
// The set and the basket are the browser's own formats (ADR 0008, ADR
// 0018), so the browser keeps and sends them as they are.
type Profile struct {
	V          int     `json:"v"`
	Talkgroups []Entry `json:"talkgroups"`
	// Look is harbour or console (R82); Theme is system, light or dark.
	Look  string `json:"look,omitempty"`
	Theme string `json:"theme,omitempty"`
	// Alerts is the playback setting for talkgroups' alert sounds (R22,
	// R48), off by default.
	Alerts bool `json:"alerts"`
	// Basket is the download basket (R64; ADR 0018). A profile saved
	// before M3 has none.
	Basket *Basket `json:"basket,omitempty"`
}

// Basket is the download basket as the browser keeps it (ADR 0018): call
// IDs and the listener's own names and notes, never anything of a call's
// talkgroup. What the basket shows of a call comes from the call details
// each time (P1).
type Basket struct {
	Calls         []BasketCall   `json:"calls"`
	Conversations []Conversation `json:"conversations"`
	// Last is when the whole basket was last downloaded, in Unix
	// milliseconds: Download new since last time takes the calls added
	// after it.
	Last *int64 `json:"last,omitempty"`
}

// BasketCall is a call in the basket and when it was added (Unix ms).
type BasketCall struct {
	ID    string `json:"id"`
	Added int64  `json:"added"`
}

// Conversation is a named group of the basket's calls, in order, with an
// optional note (R64). A call can be in several.
type Conversation struct {
	ID    string       `json:"id"`
	Name  string       `json:"name"`
	Note  string       `json:"note,omitempty"`
	Calls []BasketCall `json:"calls"`
	Last  *int64       `json:"last,omitempty"`
}

// How large a basket the profile holds (ADR 0018): far beyond what a
// listener collects, and small enough that the attribute stays well under
// a megabyte. Names and notes are bounded as a zip's are (ADR 0015).
const (
	MaxBasketCalls        = 1000
	MaxConversations      = 50
	MaxConversationPlaces = 2000 // places in conversations, all together
	maxConversationID     = 32
	maxNameBytes          = 200
	maxNoteBytes          = 2000
)

// Entry is one talkgroup in the set.
type Entry struct {
	System   *int64 `json:"system,omitempty"`
	ID       int32  `json:"id"`
	Priority bool   `json:"priority,omitempty"`
}

// Limits on what a profile may hold, so an account can't be used to store
// anything else in Authentik: a set as big as the deployment, and then
// some.
const (
	maxEntries = 5000
	// The largest set and basket allowed fit with room to spare (ADR 0018).
	maxBody = 1 << 20
)

var (
	looks  = map[string]bool{"": true, "harbour": true, "console": true}
	themes = map[string]bool{"": true, "system": true, "light": true, "dark": true}
)

func (e Entry) key() string {
	if e.System == nil {
		return strconv.Itoa(int(e.ID))
	}
	return strconv.FormatInt(*e.System, 10) + "." + strconv.Itoa(int(e.ID))
}

// parseProfile reads a profile a browser sent, or one kept in Authentik,
// and refuses anything that isn't one.
func parseProfile(b []byte) (Profile, error) {
	var p Profile
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return Profile{}, fmt.Errorf("not a profile: %w", err)
	}
	if dec.More() {
		return Profile{}, errors.New("not a profile: more after it")
	}
	if p.V != 1 {
		return Profile{}, errors.New("not a profile: v must be 1")
	}
	if len(p.Talkgroups) > maxEntries {
		return Profile{}, fmt.Errorf("a profile holds at most %d talkgroups", maxEntries)
	}
	if !looks[p.Look] || !themes[p.Theme] {
		return Profile{}, errors.New("not a profile: unknown look or theme")
	}
	seen := map[string]bool{}
	for _, e := range p.Talkgroups {
		if e.ID <= 0 || (e.System != nil && *e.System <= 0) {
			return Profile{}, errors.New("not a profile: talkgroup and system IDs are above 0")
		}
		if seen[e.key()] {
			return Profile{}, errors.New("not a profile: a talkgroup is in it twice")
		}
		seen[e.key()] = true
	}
	if p.Talkgroups == nil {
		p.Talkgroups = []Entry{}
	}
	if p.Basket != nil {
		if err := p.Basket.check(); err != nil {
			return Profile{}, err
		}
	}
	return p, nil
}

// check refuses a basket that isn't one, or is larger than a profile holds.
func (b *Basket) check() error {
	if len(b.Calls) > MaxBasketCalls {
		return fmt.Errorf("a basket holds at most %d calls", MaxBasketCalls)
	}
	if len(b.Conversations) > MaxConversations {
		return fmt.Errorf("a basket holds at most %d conversations", MaxConversations)
	}
	bad := errors.New("not a profile: not a basket")
	in := map[string]bool{}
	for _, c := range b.Calls {
		id, ok := gate.ParseCallID(c.ID)
		if !ok || id != c.ID || c.Added < 0 || in[id] {
			return bad
		}
		in[id] = true
	}
	if b.Last != nil && *b.Last < 0 {
		return bad
	}
	places := 0
	ids := map[string]bool{}
	for _, cv := range b.Conversations {
		if cv.ID == "" || len(cv.ID) > maxConversationID || ids[cv.ID] || strings.Trim(cv.ID, idChars) != "" {
			return bad
		}
		ids[cv.ID] = true
		if strings.TrimSpace(cv.Name) == "" || len(cv.Name) > maxNameBytes || len(cv.Note) > maxNoteBytes ||
			!utf8.ValidString(cv.Name) || !utf8.ValidString(cv.Note) || (cv.Last != nil && *cv.Last < 0) {
			return bad
		}
		seen := map[string]bool{}
		for _, c := range cv.Calls {
			if !in[c.ID] || seen[c.ID] || c.Added < 0 {
				return bad
			}
			seen[c.ID] = true
		}
		places += len(cv.Calls)
	}
	if places > MaxConversationPlaces {
		return fmt.Errorf("a basket's conversations hold at most %d calls together", MaxConversationPlaces)
	}
	if b.Calls == nil {
		b.Calls = []BasketCall{}
	}
	if b.Conversations == nil {
		b.Conversations = []Conversation{}
	}
	for i := range b.Conversations {
		if b.Conversations[i].Calls == nil {
			b.Conversations[i].Calls = []BasketCall{}
		}
	}
	return nil
}

// idChars are the characters a conversation's ID may have.
const idChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"

// publicIn is which entries name a public talkgroup now, by the same rule
// as the browser's (web/src/lib/listening/set.ts, find): an entry with no
// system names the talkgroup with its ID; one with a system names that
// system's, or, while there's one system, the talkgroup with its ID.
func publicIn(c *gate.Catalogue) func(Entry) bool {
	byKey := map[string]bool{}
	byID := map[int32]bool{}
	for _, t := range c.Talkgroups {
		byKey[strconv.FormatInt(t.System.ID, 10)+"."+strconv.Itoa(int(t.ID))] = true
		byID[t.ID] = true
	}
	single := len(c.Systems) <= 1
	return func(e Entry) bool {
		if e.System == nil {
			return byID[e.ID]
		}
		return byKey[e.key()] || (single && byID[e.ID])
	}
}

// shown is the profile as the browser may see it: only talkgroups that are
// public now (P1, R33). One hidden since it was saved stays in the account
// (kept), unseen, in case it's made public again.
func shown(p Profile, public func(Entry) bool) Profile {
	out := p
	out.Talkgroups = []Entry{}
	for _, e := range p.Talkgroups {
		if public(e) {
			out.Talkgroups = append(out.Talkgroups, e)
		}
	}
	return out
}

// kept is the profile to save when a browser sends next: the browser's,
// plus what the account held that the browser couldn't have seen (not
// public now), so saving never loses a talkgroup that's hidden for a while.
// The basket is the browser's whole: its calls are only IDs, which the
// browser had anyway. A browser that sends none (a page from before M3)
// leaves the account's as it was.
func kept(next Profile, before *Profile, public func(Entry) bool) Profile {
	if before == nil {
		return next
	}
	out := next
	if out.Basket == nil {
		out.Basket = before.Basket
	}
	out.Talkgroups = append([]Entry{}, next.Talkgroups...)
	in := map[string]bool{}
	for _, e := range next.Talkgroups {
		in[e.key()] = true
	}
	for _, e := range before.Talkgroups {
		if !public(e) && !in[e.key()] && len(out.Talkgroups) < maxEntries {
			out.Talkgroups = append(out.Talkgroups, e)
		}
	}
	return out
}
