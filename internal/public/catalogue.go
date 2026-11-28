package public

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/gate"
)

// The catalogue, talkgroup search and the fireground lookup (SPEC.md R15,
// R22, R24, R42, R74, R77, R101; ADR 0006). Each reads the gate's
// catalogue, which holds public talkgroups only, and each answer is
// Cache-Control: no-store, so hiding a talkgroup takes effect at the next
// request.

// Paths of the catalogue.
const (
	// CataloguePath is every public talkgroup, with their systems, tags and
	// groups, in one response.
	CataloguePath = "/api/v1/catalogue"
	// SearchPath finds talkgroups by name, label, ID or tag: ?q=, which may
	// be empty.
	SearchPath = "/api/v1/catalogue/search"
	// FiregroundPath finds the talkgroup with a fireground channel number.
	FiregroundPath = "/api/v1/fireground/{number}"
	// FiregroundsPath finds the talkgroups with several fireground channel
	// numbers at once: ?n=508&n=540 (ADR 0057).
	FiregroundsPath = "/api/v1/fireground"
)

// FiregroundURL is the path that looks up a fireground channel number.
func FiregroundURL(number string) string { return "/api/v1/fireground/" + number }

// FiregroundsParam is the several-number lookup's parameter, and
// MaxFiregrounds how many of its values are read: those after are ignored,
// as a fireground link's are (R101).
const (
	FiregroundsParam = "n"
	MaxFiregrounds   = 10
)

// MaxQuery is the longest search query, in bytes.
const MaxQuery = 100

// channelUnavailable is the one answer for a fireground number that names no
// public talkgroup: unknown, hidden, ignored or not a number (R33, R101).
var channelUnavailable = apiError{Code: "channel_unavailable", Message: "This channel isn't available."}

// CatalogueSystem is a system, listed only when there are several (R15).
type CatalogueSystem struct {
	ID    int64  `json:"id"`
	Label string `json:"label"`
	Name  string `json:"name"`
}

// CatalogueTalkgroup is a public talkgroup. Name is what's shown (R24), and
// the label is beside it. LEDColour and Alert are the effective ones (ADR
// 0006), null for none. System is its system's ID, given only when there
// are several systems (R15).
type CatalogueTalkgroup struct {
	System            *int64   `json:"system,omitempty"`
	ID                int32    `json:"id"`
	Label             string   `json:"label"`
	Name              string   `json:"name"`
	Tag               string   `json:"tag"`
	Groups            []string `json:"groups"`
	LEDColour         *string  `json:"led_colour"`
	Alert             *string  `json:"alert"`
	DelayMinutes      int32    `json:"delay_minutes"`
	FiregroundChannel *int32   `json:"fireground_channel"`
	// Quiet: not heard in the quiet window while its system was receiving
	// (R114, ADR 0054). Listeners' lists leave it out; it's here so a
	// client can find it by an exact search, and a listening set naming
	// it keeps it. NotHeardDays is how many whole days it hasn't been
	// heard, given only when it's quiet: at least the window, and the
	// window itself when it's never been heard.
	Quiet        bool `json:"quiet"`
	NotHeardDays int  `json:"not_heard_days,omitempty"`
}

// Catalogue is GET /api/v1/catalogue: every public talkgroup in the
// catalogue's order (system, then sort order, then ID), and the tags and
// groups they name, in their order. TimeZone is the deployment's (TZ), an
// IANA name, which the browser shows times in (SPEC.md section 13; ADR
// 0016). QuietWindowDays is the quiet window (R114).
type Catalogue struct {
	TimeZone        string               `json:"time_zone"`
	QuietWindowDays int                  `json:"quiet_window_days"`
	Systems         []CatalogueSystem    `json:"systems,omitempty"`
	Tags            []string             `json:"tags"`
	Groups          []string             `json:"groups"`
	Talkgroups      []CatalogueTalkgroup `json:"talkgroups"`
}

// SearchResponse is the talkgroups a search finds, best first. Quiet ones
// are left out unless the query is exactly one's number, name or label, or
// the search asks for them with include_quiet=true (R114).
type SearchResponse struct {
	Systems    []CatalogueSystem    `json:"systems,omitempty"`
	Talkgroups []CatalogueTalkgroup `json:"talkgroups"`
}

// IncludeQuiet is the search's parameter that keeps quiet talkgroups in.
const IncludeQuiet = "include_quiet"

// FiregroundResponse is the talkgroup a fireground channel number names.
type FiregroundResponse struct {
	Systems   []CatalogueSystem  `json:"systems,omitempty"`
	Talkgroup CatalogueTalkgroup `json:"talkgroup"`
}

// FiregroundsResponse is the talkgroups several fireground channel numbers
// name: one for each number a public talkgroup has, in the order the
// numbers were asked, each once. A number that names none (unknown, hidden,
// ignored or not a number) is simply not there, so an answer with one is
// byte for byte the answer without it (R33).
type FiregroundsResponse struct {
	Systems    []CatalogueSystem    `json:"systems,omitempty"`
	Talkgroups []CatalogueTalkgroup `json:"talkgroups"`
}

// zoneName is a time zone's IANA name. Go names the zone TZ set "Local", so
// the binary loads TZ by name (cmd/radioarchive); a zone still called Local
// is one no TZ named, which is UTC in the image.
func zoneName(loc *time.Location) string {
	if loc == nil || loc.String() == "Local" || loc.String() == "" {
		return "UTC"
	}
	return loc.String()
}

// catalogueBody keeps the last catalogue's encoded answer, so the same
// catalogue isn't encoded again for every request.
type catalogueBody struct {
	mu    sync.Mutex
	of    *gate.Catalogue
	quiet *gate.Quiet
	body  []byte
}

func systemsOf(c *gate.Catalogue) []CatalogueSystem {
	if len(c.Systems) < 2 {
		return nil
	}
	out := make([]CatalogueSystem, 0, len(c.Systems))
	for _, s := range c.Systems {
		out = append(out, CatalogueSystem(s))
	}
	return out
}

func talkgroupOf(c *gate.Catalogue, q *gate.Quiet, t gate.CatalogueTalkgroup) CatalogueTalkgroup {
	out := CatalogueTalkgroup{ID: t.ID, Label: t.Label, Name: t.Name, Tag: t.Tag, Groups: t.Groups,
		DelayMinutes: t.DelayMinutes, FiregroundChannel: t.Fireground}
	out.Quiet, out.NotHeardDays = q.Of(t)
	if len(c.Systems) > 1 {
		id := t.System.ID
		out.System = &id
	}
	if t.LEDColour != "" {
		s := t.LEDColour
		out.LEDColour = &s
	}
	if t.Alert != "" {
		s := t.Alert
		out.Alert = &s
	}
	return out
}

func talkgroupsOf(c *gate.Catalogue, q *gate.Quiet, ts []gate.CatalogueTalkgroup) []CatalogueTalkgroup {
	out := make([]CatalogueTalkgroup, 0, len(ts))
	for _, t := range ts {
		out = append(out, talkgroupOf(c, q, t))
	}
	return out
}

// catalogueAndQuiet reads the catalogue and which of its talkgroups are
// quiet.
func (a *API) catalogueAndQuiet(r *http.Request) (*gate.Catalogue, *gate.Quiet, error) {
	c, err := a.g.Catalogue(r.Context())
	if err != nil {
		return nil, nil, err
	}
	q, err := a.g.Quiet(r.Context())
	if err != nil {
		return nil, nil, err
	}
	return c, q, nil
}

func (a *API) catalogue(w http.ResponseWriter, r *http.Request) {
	c, q, err := a.catalogueAndQuiet(r)
	if err != nil {
		a.failed(w, r, err)
		return
	}
	a.cat.mu.Lock()
	defer a.cat.mu.Unlock()
	if a.cat.of != c || a.cat.quiet != q {
		b, err := json.Marshal(Catalogue{TimeZone: zoneName(a.loc), QuietWindowDays: q.WindowDays, Systems: systemsOf(c),
			Tags: c.Tags, Groups: c.Groups, Talkgroups: talkgroupsOf(c, q, c.Talkgroups)})
		if err != nil {
			a.failed(w, r, err)
			return
		}
		a.cat.of, a.cat.quiet, a.cat.body = c, q, append(b, '\n')
	}
	writeBody(w, http.StatusOK, a.cat.body)
}

func (a *API) search(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if len(query) > MaxQuery {
		writeJSON(w, http.StatusBadRequest, apiError{Code: "query_too_long",
			Message: fmt.Sprintf("Search for at most %d characters.", MaxQuery)})
		return
	}
	include := false
	switch v := r.URL.Query().Get(IncludeQuiet); v {
	case "", "false":
	case "true":
		include = true
	default:
		writeJSON(w, http.StatusBadRequest, apiError{Code: "invalid_" + IncludeQuiet,
			Message: IncludeQuiet + " must be true or false."})
		return
	}
	c, q, err := a.catalogueAndQuiet(r)
	if err != nil {
		a.failed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, SearchResponse{Systems: systemsOf(c),
		Talkgroups: talkgroupsOf(c, q, q.Shown(query, c.Search(query), include))})
}

func (a *API) fireground(w http.ResponseWriter, r *http.Request) {
	c, q, err := a.catalogueAndQuiet(r)
	if err != nil {
		a.failed(w, r, err)
		return
	}
	t, err := c.Fireground(r.PathValue("number"))
	if errors.Is(err, gate.ErrChannelUnavailable) {
		writeJSON(w, http.StatusNotFound, channelUnavailable)
		return
	}
	if err != nil {
		a.failed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, FiregroundResponse{Systems: systemsOf(c), Talkgroup: talkgroupOf(c, q, t)})
}

// firegrounds looks up the first MaxFiregrounds values of ?n=, each read
// as the one-number lookup reads it, in one request, so a link naming 10
// channels costs one of the listener's requests, not 10 (R73). It always
// answers 200, with no talkgroups when none is public.
func (a *API) firegrounds(w http.ResponseWriter, r *http.Request) {
	c, q, err := a.catalogueAndQuiet(r)
	if err != nil {
		a.failed(w, r, err)
		return
	}
	ns := r.URL.Query()[FiregroundsParam]
	if len(ns) > MaxFiregrounds {
		ns = ns[:MaxFiregrounds]
	}
	out := FiregroundsResponse{Systems: systemsOf(c), Talkgroups: []CatalogueTalkgroup{}}
	seen := map[int32]bool{}
	for _, s := range ns {
		n, ok := gate.ParseFireground(s)
		if !ok || seen[n] {
			continue
		}
		seen[n] = true
		t, err := c.Fireground(s)
		if errors.Is(err, gate.ErrChannelUnavailable) {
			continue
		}
		if err != nil {
			a.failed(w, r, err)
			return
		}
		out.Talkgroups = append(out.Talkgroups, talkgroupOf(c, q, t))
	}
	writeJSON(w, http.StatusOK, out)
}
