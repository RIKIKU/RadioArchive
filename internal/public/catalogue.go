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
)

// FiregroundURL is the path that looks up a fireground channel number.
func FiregroundURL(number string) string { return "/api/v1/fireground/" + number }

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
}

// Catalogue is GET /api/v1/catalogue: every public talkgroup in the
// catalogue's order (system, then sort order, then ID), and the tags and
// groups they name, in their order. TimeZone is the deployment's (TZ), an
// IANA name, which the browser shows times in (SPEC.md section 13; ADR
// 0016).
type Catalogue struct {
	TimeZone   string               `json:"time_zone"`
	Systems    []CatalogueSystem    `json:"systems,omitempty"`
	Tags       []string             `json:"tags"`
	Groups     []string             `json:"groups"`
	Talkgroups []CatalogueTalkgroup `json:"talkgroups"`
}

// SearchResponse is the talkgroups a search finds, best first.
type SearchResponse struct {
	Systems    []CatalogueSystem    `json:"systems,omitempty"`
	Talkgroups []CatalogueTalkgroup `json:"talkgroups"`
}

// FiregroundResponse is the talkgroup a fireground channel number names.
type FiregroundResponse struct {
	Systems   []CatalogueSystem  `json:"systems,omitempty"`
	Talkgroup CatalogueTalkgroup `json:"talkgroup"`
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
	mu   sync.Mutex
	of   *gate.Catalogue
	body []byte
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

func talkgroupOf(c *gate.Catalogue, t gate.CatalogueTalkgroup) CatalogueTalkgroup {
	out := CatalogueTalkgroup{ID: t.ID, Label: t.Label, Name: t.Name, Tag: t.Tag, Groups: t.Groups,
		DelayMinutes: t.DelayMinutes, FiregroundChannel: t.Fireground}
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

func talkgroupsOf(c *gate.Catalogue, ts []gate.CatalogueTalkgroup) []CatalogueTalkgroup {
	out := make([]CatalogueTalkgroup, 0, len(ts))
	for _, t := range ts {
		out = append(out, talkgroupOf(c, t))
	}
	return out
}

func (a *API) catalogue(w http.ResponseWriter, r *http.Request) {
	c, err := a.g.Catalogue(r.Context())
	if err != nil {
		a.failed(w, r, err)
		return
	}
	a.cat.mu.Lock()
	defer a.cat.mu.Unlock()
	if a.cat.of != c {
		b, err := json.Marshal(Catalogue{TimeZone: zoneName(a.loc), Systems: systemsOf(c), Tags: c.Tags, Groups: c.Groups,
			Talkgroups: talkgroupsOf(c, c.Talkgroups)})
		if err != nil {
			a.failed(w, r, err)
			return
		}
		a.cat.of, a.cat.body = c, append(b, '\n')
	}
	writeBody(w, http.StatusOK, a.cat.body)
}

func (a *API) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if len(q) > MaxQuery {
		writeJSON(w, http.StatusBadRequest, apiError{Code: "query_too_long",
			Message: fmt.Sprintf("Search for at most %d characters.", MaxQuery)})
		return
	}
	c, err := a.g.Catalogue(r.Context())
	if err != nil {
		a.failed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, SearchResponse{Systems: systemsOf(c), Talkgroups: talkgroupsOf(c, c.Search(q))})
}

func (a *API) fireground(w http.ResponseWriter, r *http.Request) {
	c, err := a.g.Catalogue(r.Context())
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
	writeJSON(w, http.StatusOK, FiregroundResponse{Systems: systemsOf(c), Talkgroup: talkgroupOf(c, t)})
}
