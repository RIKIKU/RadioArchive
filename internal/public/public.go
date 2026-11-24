// Package public is the public API: every endpoint a listener, a link
// preview or the phone app reaches without signing in (SPEC.md R32).
//
// Its handlers see the deployment only through the gate (internal/gate).
// This package may not import the store, the database driver or the file
// system (imports_test.go enforces it), and its routes are the only public
// ones the server mounts (internal/server). A new public surface (recent
// calls, the live feed, zips) is added here, on the gate, and to
// the A10 harness (internal/server/a10_test.go), which fails while any public
// route is missing from it.
package public

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/RIKIKU/RadioArchive/internal/gate"
)

// Paths of the public API.
const (
	// CallsPath takes a list of call IDs (R67): QUERY, and POST while
	// browsers' support for QUERY is unsettled.
	CallsPath = "/api/v1/calls"
	// RecordingPath is a call's recording, with range requests (R20).
	RecordingPath = "/api/v1/calls/{call}/recording"
)

// Route is one public endpoint.
type Route struct {
	Pattern string
	Handler http.Handler
}

// API is the public API.
type API struct {
	g      *gate.Gate
	log    *slog.Logger
	routes []Route
	cat    catalogueBody
}

// New returns the public API, reading through g.
func New(g *gate.Gate, log *slog.Logger) *API {
	a := &API{g: g, log: log}
	a.handle("QUERY "+CallsPath, a.calls)
	a.handle("POST "+CallsPath, a.calls)
	a.handle("GET "+RecordingPath, a.recording)
	a.handle("GET "+CataloguePath, a.catalogue)
	a.handle("GET "+SearchPath, a.search)
	a.handle("GET "+FiregroundPath, a.fireground)
	return a
}

func (a *API) handle(pattern string, h http.HandlerFunc) {
	a.routes = append(a.routes, Route{Pattern: pattern, Handler: h})
}

// Routes are the public API's endpoints, for the server to mount.
func (a *API) Routes() []Route { return a.routes }

// apiError is a refusal's body (R78): a stable code and a message.
type apiError struct {
	Code    string `json:"error"`
	Message string `json:"message"`
}

// unavailable is the one answer for a call the public may not have, and for
// one that never existed (R33, R61): the same status, headers and body.
var unavailable = apiError{Code: "call_unavailable", Message: "This call isn't available."}

func writeJSON(w http.ResponseWriter, code int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		code, b = http.StatusInternalServerError, []byte(`{"error":"internal","message":"Something went wrong."}`)
	}
	writeBody(w, code, append(b, '\n'))
}

// writeBody answers with an encoded JSON body, never cached (R74).
func writeBody(w http.ResponseWriter, code int, b []byte) {
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Length", fmt.Sprint(len(b)))
	w.WriteHeader(code)
	_, _ = w.Write(b)
}

func (a *API) failed(w http.ResponseWriter, r *http.Request, err error) {
	a.log.Error("public request failed", "method", r.Method, "pattern", r.Pattern, "error", err)
	writeJSON(w, http.StatusInternalServerError, apiError{Code: "internal", Message: "Something went wrong."})
}

// Call details (R61, R67).

// System is a call's system.
type System struct {
	ID    int64  `json:"id"`
	Label string `json:"label"`
	Name  string `json:"name"`
}

// Talkgroup is a call's talkgroup. Name is what's shown (R24).
type Talkgroup struct {
	ID    int32  `json:"id"`
	Label string `json:"label"`
	Name  string `json:"name"`
	Tag   string `json:"tag"`
}

// Transmission is one unit's key-up, from the call's start.
type Transmission struct {
	Unit       int64  `json:"unit"`
	OffsetMS   int32  `json:"offset_ms"`
	DurationMS *int32 `json:"duration_ms"`
}

// Recording is where a call's recording plays from.
type Recording struct {
	URL  string `json:"url"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

// Call is a call's details.
type Call struct {
	ID                string         `json:"id"`
	System            System         `json:"system"`
	Talkgroup         Talkgroup      `json:"talkgroup"`
	PatchedTalkgroups []int32        `json:"patched_talkgroups"`
	StartedAt         string         `json:"started_at"`
	EndedAt           *string        `json:"ended_at"`
	DurationMS        *int64         `json:"duration_ms"`
	InProgress        bool           `json:"in_progress"`
	Transmissions     []Transmission `json:"transmissions"`
	Recording         *Recording     `json:"recording"`
}

// CallsRequest is the body of a call list request.
type CallsRequest struct {
	IDs []string `json:"ids"`
}

// CallsResponse lists the available calls among those asked for, in the
// order asked. Unavailable ones are left out (R64, R66).
type CallsResponse struct {
	Calls []Call `json:"calls"`
}

const timeFormat = "2006-01-02T15:04:05.000Z07:00"

func (a *API) calls(w http.ResponseWriter, r *http.Request) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	var in CallsRequest
	if err := dec.Decode(&in); err != nil || dec.Decode(&struct{}{}) != io.EOF {
		writeJSON(w, http.StatusBadRequest, apiError{Code: "invalid_body",
			Message: `The body must be {"ids": [call IDs]}.`})
		return
	}
	if len(in.IDs) > gate.MaxCalls {
		writeJSON(w, http.StatusBadRequest, apiError{Code: "too_many_calls",
			Message: fmt.Sprintf("Ask for at most %d calls at once.", gate.MaxCalls)})
		return
	}
	cs, err := a.g.Calls(r.Context(), in.IDs)
	if err != nil {
		a.failed(w, r, err)
		return
	}
	out := CallsResponse{Calls: make([]Call, 0, len(cs))}
	for _, c := range cs {
		out.Calls = append(out.Calls, callOf(c))
	}
	writeJSON(w, http.StatusOK, out)
}

func callOf(c gate.Call) Call {
	out := Call{
		ID:                c.ID,
		System:            System(c.System),
		Talkgroup:         Talkgroup(c.Talkgroup),
		PatchedTalkgroups: c.Patched,
		StartedAt:         c.StartedAt.Format(timeFormat),
		InProgress:        c.EndedAt == nil,
		Transmissions:     make([]Transmission, 0, len(c.Transmissions)),
	}
	if c.EndedAt != nil {
		s := c.EndedAt.Format(timeFormat)
		d := c.EndedAt.Sub(c.StartedAt).Milliseconds()
		out.EndedAt, out.DurationMS = &s, &d
	}
	for _, t := range c.Transmissions {
		out.Transmissions = append(out.Transmissions, Transmission(t))
	}
	if c.RecordingSize != nil {
		out.Recording = &Recording{URL: RecordingURL(c.ID), Type: "audio/ogg", Size: *c.RecordingSize}
	}
	return out
}

// RecordingURL is the path a call's recording plays from.
func RecordingURL(callID string) string {
	return "/api/v1/calls/" + callID + "/recording"
}

// Recording playback (R20, R74).

func (a *API) recording(w http.ResponseWriter, r *http.Request) {
	rec, err := a.g.Recording(r.Context(), r.PathValue("call"))
	if errors.Is(err, gate.ErrUnavailable) {
		writeJSON(w, http.StatusNotFound, unavailable)
		return
	}
	if err != nil {
		a.failed(w, r, err)
		return
	}
	defer rec.Close()
	rec.Serve(w, r)
}
