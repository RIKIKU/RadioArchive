package public

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/gate"
)

// Recent calls and search (SPEC.md R16, R23, R46, R73, R74, R78; ADR
// 0013): GET /api/v1/recent-calls, the public calls that started in a time
// range, newest or oldest first, a page at a time, each in the call
// details' shape. Everything comes from the gate, and every answer is
// Cache-Control: no-store, so hiding a talkgroup takes effect at the next
// request.

// RecentCallsPath is recent calls and search: GET with the search in the
// query string, or QUERY and POST with it in a JSON body (ADR 0016).
const RecentCallsPath = "/api/v1/recent-calls"

// MaxTalkgroups is the most talkgroups one search may name: a listening
// set of the deployment's 1,000, which fits a URL nginx takes (ADR 0013).
const MaxTalkgroups = 1000

// RecentCallsResponse is a page of recent calls. Next is the cursor for
// load more, or null on the last page.
type RecentCallsResponse struct {
	Calls []Call  `json:"calls"`
	Next  *string `json:"next"`
}

// tooSlow is R73's answer for a search that ran out of time.
var tooSlow = apiError{Code: "search_too_slow", Message: "That search took too long. Try a narrower time range."}

func badSearch(code, message string) apiError { return apiError{Code: code, Message: message} }

// Cursor is the load-more cursor for a page that ends at a call: its start
// time and ID, in the order calls are paged in. It names a position, not a
// call: the server never looks the call up.
func Cursor(startedAt time.Time, callID string) string {
	id, ok := gate.ParseCallID(callID)
	if !ok {
		return ""
	}
	raw, _ := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	return cursorOf(gate.Position{StartedAt: startedAt, ID: [16]byte(raw)})
}

func cursorOf(p gate.Position) string {
	var b [24]byte
	binary.BigEndian.PutUint64(b[:8], uint64(p.StartedAt.UnixMicro()))
	copy(b[8:], p.ID[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func parseCursor(s string) (gate.Position, bool) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) != 24 {
		return gate.Position{}, false
	}
	us := int64(binary.BigEndian.Uint64(b[:8]))
	if us < 0 {
		return gate.Position{}, false
	}
	return gate.Position{StartedAt: time.UnixMicro(us).UTC(), ID: [16]byte(b[8:])}, true
}

// parseItem reads `[system "."] id`, as the share link writes a talkgroup
// (ADR 0008).
func parseItem(s string, bits int) (system *int64, id int64, ok bool) {
	sys, rest, dotted := strings.Cut(s, ".")
	if dotted {
		n, err := strconv.ParseInt(sys, 10, 64)
		if err != nil || n <= 0 || strings.HasPrefix(sys, "+") {
			return nil, 0, false
		}
		system, s = &n, rest
	}
	id, err := strconv.ParseInt(s, 10, bits)
	if err != nil || id < 0 || strings.HasPrefix(s, "+") {
		return nil, 0, false
	}
	return system, id, true
}

func parseTalkgroups(vs []string) (*gate.ListeningSet, *apiError) {
	set := gate.ListeningSet{Any: []int32{}, InSystem: map[int64][]int32{}}
	n := 0
	for _, v := range vs {
		if v == "" {
			continue
		}
		for _, item := range strings.Split(v, ",") {
			n++
			if n > MaxTalkgroups {
				e := badSearch("too_many_talkgroups", fmt.Sprintf("Name at most %d talkgroups at once.", MaxTalkgroups))
				return nil, &e
			}
			sys, id, ok := parseItem(item, 32)
			if !ok {
				e := badSearch("invalid_talkgroups",
					"talkgroups is a comma-separated list of talkgroup IDs, each optionally after its system's ID and a dot.")
				return nil, &e
			}
			if sys == nil {
				set.Any = append(set.Any, int32(id))
			} else {
				set.InSystem[*sys] = append(set.InSystem[*sys], int32(id))
			}
		}
	}
	if n == 0 {
		return nil, nil // every public talkgroup
	}
	return &set, nil
}

// readSearch reads a search's query string. Every refusal depends only on
// the request's form, never on what the deployment holds.
func readSearch(q url.Values) (gate.Search, *apiError) {
	var s gate.Search
	one := func(name string) (string, *apiError) {
		vs := q[name]
		if len(vs) > 1 {
			e := badSearch("invalid_"+name, fmt.Sprintf("Give %s once.", name))
			return "", &e
		}
		if len(vs) == 0 {
			return "", nil
		}
		return vs[0], nil
	}
	for name := range q {
		if !searchParam(name) {
			e := badSearch("unknown_parameter", fmt.Sprintf("%q isn't a search parameter.", name))
			return s, &e
		}
	}
	set, e := parseTalkgroups(q["talkgroups"])
	if e != nil {
		return s, e
	}
	s.Talkgroups = set
	for _, p := range []struct {
		name string
		t    *time.Time
	}{{"from", &s.From}, {"to", &s.To}} {
		v, e := one(p.name)
		if e != nil {
			return s, e
		}
		if v == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			e := badSearch("invalid_time", fmt.Sprintf("%s is a date and time in RFC 3339, such as 2026-11-24T12:00:00Z.", p.name))
			return s, &e
		}
		*p.t = t.UTC()
	}
	if v, e := one("unit"); e != nil {
		return s, e
	} else if v != "" {
		sys, id, ok := parseItem(v, 63)
		if !ok {
			e := badSearch("invalid_unit", "unit is a radio ID, optionally after its system's ID and a dot.")
			return s, &e
		}
		s.Unit = &gate.Unit{System: sys, ID: id}
	}
	if v, e := one("order"); e != nil {
		return s, e
	} else {
		switch v {
		case "", "newest":
		case "oldest":
			s.Oldest = true
		default:
			e := badSearch("invalid_order", "order is newest or oldest.")
			return s, &e
		}
	}
	if v, e := one("cursor"); e != nil {
		return s, e
	} else if v != "" {
		p, ok := parseCursor(v)
		if !ok {
			e := badSearch("invalid_cursor", "cursor is the next value of an earlier page.")
			return s, &e
		}
		s.After = &p
	}
	if v, e := one("limit"); e != nil {
		return s, e
	} else if v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > gate.MaxPage {
			e := badSearch("invalid_limit", fmt.Sprintf("limit is from 1 to %d.", gate.MaxPage))
			return s, &e
		}
		s.Limit = n
	}
	return s, nil
}

func (a *API) recentCalls(w http.ResponseWriter, r *http.Request) {
	a.serveRecent(w, r, r.URL.Query())
}

// recentCallsBody is the same search with its parameters in a JSON body
// (QUERY, and POST while browsers' support for QUERY is unsettled): for a
// list of talkgroups too long for a URL behind a default nginx (ADR 0016).
// The body is read into the query string's form and answered by the same
// code, so every rule, refusal and answer is the GET's.
func (a *API) recentCallsBody(w http.ResponseWriter, r *http.Request) {
	q, bad := readSearchBody(w, r)
	if bad != nil {
		writeJSON(w, http.StatusBadRequest, bad)
		return
	}
	a.serveRecent(w, r, q)
}

// MaxSearchBody is the most a search's JSON body may hold: 1,000
// talkgroups, each with its system, and the other parameters, with room.
const MaxSearchBody = 64 << 10

var invalidSearchBody = apiError{Code: "invalid_body",
	Message: `The body is a JSON object of the search's parameters: {"talkgroups": ["1001", "1.1002"], "from", "to", "unit", "order", "cursor", "limit"}.`}

// readSearchBody reads a search's JSON body into the query string's form:
// talkgroups is an array of `[system "."] id` strings, limit a number, and
// the rest strings. A null is a parameter not given.
func readSearchBody(w http.ResponseWriter, r *http.Request) (url.Values, *apiError) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxSearchBody))
	var in map[string]json.RawMessage
	if err := dec.Decode(&in); err != nil || in == nil || dec.Decode(&struct{}{}) != io.EOF {
		return nil, &invalidSearchBody
	}
	q := url.Values{}
	for name, raw := range in {
		if !searchParam(name) {
			e := badSearch("unknown_parameter", fmt.Sprintf("%q isn't a search parameter.", name))
			return nil, &e
		}
		if string(raw) == "null" {
			continue
		}
		switch name {
		case "talkgroups":
			var items []string
			if err := json.Unmarshal(raw, &items); err != nil {
				return nil, &invalidSearchBody
			}
			for _, item := range items {
				if item == "" || strings.Contains(item, ",") {
					e := badSearch("invalid_talkgroups",
						"talkgroups is a list of talkgroup IDs, each optionally after its system's ID and a dot.")
					return nil, &e
				}
			}
			if len(items) > 0 {
				q.Set(name, strings.Join(items, ","))
			}
		case "limit":
			// A number, not a string holding one (which json.Number takes).
			var n json.Number
			if err := json.Unmarshal(raw, &n); err != nil || raw[0] == '"' {
				return nil, &invalidSearchBody
			}
			q.Set(name, n.String())
		default:
			var v string
			if err := json.Unmarshal(raw, &v); err != nil {
				return nil, &invalidSearchBody
			}
			q.Set(name, v)
		}
	}
	return q, nil
}

func searchParam(name string) bool {
	switch name {
	case "talkgroups", "from", "to", "unit", "order", "cursor", "limit":
		return true
	}
	return false
}

// serveRecent answers a search read from a query string, or from a body in
// that form.
func (a *API) serveRecent(w http.ResponseWriter, r *http.Request, q url.Values) {
	s, bad := readSearch(q)
	if bad != nil {
		writeJSON(w, http.StatusBadRequest, bad)
		return
	}
	from, to := a.g.Range(s)
	switch {
	case to.Sub(from) > gate.MaxRange:
		writeJSON(w, http.StatusBadRequest, badSearch("range_too_long",
			"Search at most 31 days at once. Try a narrower time range."))
		return
	case !to.After(from):
		writeJSON(w, http.StatusBadRequest, badSearch("invalid_range", "to must be after from."))
		return
	}
	page, err := a.g.RecentCalls(r.Context(), s)
	if errors.Is(err, gate.ErrTooSlow) {
		writeJSON(w, http.StatusServiceUnavailable, tooSlow)
		return
	}
	if err != nil {
		a.failed(w, r, err)
		return
	}
	del, err := a.g.Deletion(r.Context())
	if err != nil {
		a.failed(w, r, err)
		return
	}
	out := RecentCallsResponse{Calls: make([]Call, 0, len(page.Calls))}
	for _, c := range page.Calls {
		out.Calls = append(out.Calls, callOf(c, del))
	}
	if page.Next != nil {
		next := cursorOf(*page.Next)
		out.Next = &next
	}
	writeJSON(w, http.StatusOK, out)
}
