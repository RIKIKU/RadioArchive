package server

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/RIKIKU/RadioArchive/internal/limits"
	"github.com/RIKIKU/RadioArchive/internal/public"
)

// Heavy-use limits (SPEC.md R70-R73; ADR 0036). They stand in front of the
// public API's routes and pages only: the recorder client's, the admin
// API's, the account's, the app's files and the health check are never
// limited. Each public route says which limit it's under
// (public.Route.Limit); the server counts and refuses.

// WithLimits puts l in front of every public route and page. A request it
// would refuse is let through when auth says a signed-in admin made it, by
// browser session or the module's token (R70); auth is asked only then,
// so other requests cost no read. Without WithLimits nothing is limited,
// as the tests that aren't about limits run.
func WithLimits(l *limits.Limits, auth AdminAuthenticator) Option {
	return func(a *API) {
		a.limits, a.limitAuth = l, auth
	}
}

// kindOf is the limits package's kind for a public route's limit. A route
// without one is the programmer's mistake, found by any test that builds
// the API, so it panics.
func kindOf(pattern string, l public.Limit) limits.Kind {
	switch l {
	case public.LimitRequests:
		return limits.Requests
	case public.LimitPlayback:
		return limits.Playback
	case public.LimitLive:
		return limits.Live
	case public.LimitZip:
		return limits.Zip
	}
	panic(fmt.Sprintf("public route %q has no limit", pattern))
}

// limited is h behind the limit of kind k. The limits are read at each
// request, so the options can come in any order.
func (a *API) limited(k limits.Kind, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l := a.limits
		if l == nil {
			h.ServeHTTP(w, r)
			return
		}
		switch k {
		case limits.Requests, limits.Playback:
			if f, ok := l.Take(k, l.Client(r)); !ok && !a.byAdmin(w, r) {
				f.Write(w)
				return
			}
		case limits.Live:
			// A GET that isn't an upgrade is the browser asking why its
			// socket was refused, since its WebSocket can't read the
			// answer (ADR 0037): the same refusal, or else the handler's
			// 426 Upgrade Required. It opens and counts nothing.
			if !upgrade(r) {
				if f, ok := l.LiveCheck(l.Client(r)); !ok && !a.byAdmin(w, r) {
					f.Write(w)
					return
				}
				break
			}
			release, f, ok := l.Live(l.Client(r))
			switch {
			case ok:
				// The socket counts until the handler returns, when it
				// has closed.
				defer release()
			case !a.byAdmin(w, r):
				f.Write(w)
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}

// upgrade says whether r asks to become a WebSocket.
func upgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// byAdmin says whether a signed-in admin made r (R70): only a request with
// a cookie or an Authorization header can be one. A failed check isn't an
// admin.
func (a *API) byAdmin(w http.ResponseWriter, r *http.Request) bool {
	if a.limitAuth == nil {
		return false
	}
	if _, ok := r.Header["Authorization"]; !ok && len(r.Cookies()) == 0 {
		return false
	}
	_, err := a.limitAuth.Admin(w, r)
	return err == nil
}
