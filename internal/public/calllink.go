package public

import (
	"net/http"
)

// Call links (SPEC.md R61, R74; ADR 0017).

// CallLinkPath is a call's link: the app's page, which shows the call card
// (R61), with what a link preview may show of the call. It's a page, so it
// lives outside the API, but what it says depends on the call it names, so
// it's this package's, read through the gate, and not the app shell's.
const CallLinkPath = "/calls/{call}"

// CallLinkURL is a call's link's path.
func CallLinkURL(callID string) string { return "/calls/" + callID }

// Page is a public page outside the API: a path the app renders, whose
// preview depends on what it names (ADR 0017). The server sends the app's
// page with Preview's answer; the handler never sees the request's cookies
// or credentials, as no public route does.
type Page struct {
	Pattern string
	// Preview says what link previews may show of what the path names. ok
	// is false when there's nothing public there: the site's generic
	// preview, with one status, whatever the reason.
	Preview func(r *http.Request) (p Preview, ok bool, err error)
}

// Preview is what a link preview shows (R61): a title and a description,
// as text. Never audio, a recording's URL or the call's units.
type Preview struct {
	Title       string
	Description string
}

// PreviewTime is how a call link's preview gives the call's start, on the
// deployment's clock (section 13): "Wed 25 Nov 2026, 14:03:22 AEDT".
const PreviewTime = "Mon 2 Jan 2006, 15:04:05 MST"

// Pages are the public API's pages, for the server to mount.
func (a *API) Pages() []Page { return a.pages }

// callPreview is a call link's preview: the talkgroup's name (R24) and the
// call's start. A call the gate doesn't give (deleted, never stored,
// hidden, ignored, patched with either, within its delay, or not a call
// ID) has none (R33, R61).
func (a *API) callPreview(r *http.Request) (Preview, bool, error) {
	cs, err := a.g.Calls(r.Context(), []string{r.PathValue("call")})
	if err != nil {
		a.log.Error("public request failed", "method", r.Method, "pattern", r.Pattern, "error", err)
		return Preview{}, false, err
	}
	if len(cs) == 0 {
		return Preview{}, false, nil
	}
	c := cs[0]
	return Preview{Title: c.Talkgroup.Name, Description: c.StartedAt.In(a.loc).Format(PreviewTime)}, true, nil
}
