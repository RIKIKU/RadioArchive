package account

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/RIKIKU/RadioArchive/internal/gate"
)

// Attribute is the Authentik user attribute a listener's saved settings
// live in (R110, ADR 0009).
const Attribute = "radioarchive"

// Profile is what a listener's account holds (R41, R48, R110): the
// listening set with its priority marks, the playback settings and the
// look. The download basket joins it in M3. Hold and talkgroups added for
// the session are never in it (R41).
//
// The set is the browser's own format (ADR 0008), so the browser keeps and
// sends it as it is.
type Profile struct {
	V          int     `json:"v"`
	Talkgroups []Entry `json:"talkgroups"`
	// Look is harbour or console (R82); Theme is system, light or dark.
	Look  string `json:"look,omitempty"`
	Theme string `json:"theme,omitempty"`
	// Alerts is the playback setting for talkgroups' alert sounds (R22,
	// R48), off by default.
	Alerts bool `json:"alerts"`
}

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
	maxBody    = 256 << 10
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
	return p, nil
}

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
func kept(next Profile, before *Profile, public func(Entry) bool) Profile {
	if before == nil {
		return next
	}
	out := next
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
