// Package server is the API's HTTP handler.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/account"
	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
	"github.com/RIKIKU/RadioArchive/internal/limits"
	"github.com/RIKIKU/RadioArchive/internal/public"
	"github.com/RIKIKU/RadioArchive/web"
)

// Pinger reaches the database: store.Store's sqlc query Ping.
type Pinger interface {
	Ping(ctx context.Context) (int32, error)
}

// APIPath prefixes every API route: the recorder client's, the admin API's
// and the public API's. The app shell never answers under it.
const APIPath = "/api"

// RecorderPath prefixes the recorder client's endpoints (ADR 0002).
const RecorderPath = "/api/v1/recorder"

// RouteKind is who a route is for, and so what stands in front of it.
type RouteKind int

// Route kinds. Every route the API serves is one of these, by its path:
// the health check, the recorder client's (under RecorderPath, behind its
// client key), the admin API's (under AdminPath, behind the admin
// authenticator), public, the listener's account (under account.AuthPath
// and account.AccountPath), or the app's. A public route can only come from
// internal/public, whose handlers read through the hidden-talkgroup gate
// (ADR 0004), and lives under APIPath; it never sees the request's cookies
// or credentials, so signing in can't change its answer (P3, ADR 0009). An
// account route can only come from internal/account. An app route can only
// come from the web package, the browser app's static files, which carry no
// deployment data, and lives outside APIPath and the sign-in hops. A page
// route is a public page outside the API whose preview depends on what its
// path names, a call link (ADR 0017): it lives where app routes do, can
// only come from internal/public's pages, reads through the gate, and its
// handler sees no cookie or credential either; the server writes the app's
// page around what it says, in the asker's look. New refuses any other
// route.
const (
	HealthRoute RouteKind = iota
	RecorderRoute
	AdminRoute
	PublicRoute
	AppRoute
	AccountRoute
	PageRoute
)

func (k RouteKind) String() string {
	return [...]string{"health", "recorder", "admin", "public", "app", "account", "page"}[k]
}

// Route is one route the API serves.
type Route struct {
	Pattern string
	Kind    RouteKind
	// Limit is the heavy-use limit in front of a public route or page
	// (ADR 0036); other routes have none.
	Limit limits.Kind
}

// API is the API's handler, with the table of what it serves.
//
// The app's routes are on a mux of their own, reached only by paths outside
// APIPath and /healthz. So the app shell, a catch-all because the browser's
// router owns every other path, can never answer for an API path, and an
// API path that isn't mounted still answers 404 or 405.
type API struct {
	mux    *http.ServeMux
	app    *http.ServeMux
	routes []Route
	shell  *web.App // writes the public API's pages (WithApp)
	// limits stand in front of the public routes and pages (WithLimits),
	// and limitAuth says which requests are signed-in admins', which
	// they let through.
	limits    *limits.Limits
	limitAuth AdminAuthenticator
}

// Option adds to the API's handler.
type Option func(*API)

// ServeHTTP serves r. Every response says not to index it (R74).
func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Robots-Tag", "noindex")
	if apiPath(r.URL.Path) {
		a.mux.ServeHTTP(w, r)
		return
	}
	a.app.ServeHTTP(w, r)
}

// apiPath says whether path is the API's rather than the app's: the sign-in
// hops are the API's too.
func apiPath(path string) bool {
	return path == "/healthz" || path == APIPath || under(path, APIPath) ||
		path == account.AuthPath || under(path, account.AuthPath)
}

// Routes are every route the API serves, and their kinds. The A10 harness
// exercises every public one.
func (a *API) Routes() []Route { return a.routes }

func under(path, prefix string) bool { return strings.HasPrefix(path, prefix+"/") }

// handle mounts a route of a kind, after checking its path is where that
// kind lives. A mistake here is the programmer's, found by any test that
// builds the API, so it panics.
func (a *API) handle(kind RouteKind, pattern string, h http.Handler) {
	_, path, ok := strings.Cut(pattern, " ")
	if !ok {
		panic(fmt.Sprintf("route %q has no method", pattern))
	}
	var fits bool
	switch kind {
	case HealthRoute:
		fits = path == "/healthz"
	case RecorderRoute:
		fits = under(path, RecorderPath)
	case AdminRoute:
		fits = under(path, AdminPath)
	case PublicRoute:
		fits = under(path, APIPath) && !under(path, RecorderPath) && !under(path, AdminPath) &&
			!under(path, account.AccountPath) &&
			path != RecorderPath && path != AdminPath && path != account.AccountPath
	case AccountRoute:
		fits = under(path, account.AuthPath) || path == account.AccountPath || under(path, account.AccountPath)
	case AppRoute:
		fits = strings.HasPrefix(path, "/") && !apiPath(path)
	case PageRoute:
		fits = strings.HasPrefix(path, "/") && path != "/" && !apiPath(path)
	}
	if !fits {
		panic(fmt.Sprintf("route %q isn't a %s route", pattern, kind))
	}
	if kind == AppRoute || kind == PageRoute {
		a.app.Handle(pattern, h)
	} else {
		a.mux.Handle(pattern, h)
	}
	a.routes = append(a.routes, Route{Pattern: pattern, Kind: kind})
}

// WithRecorders serves the recorder client's endpoints (ADR 0002): the live
// WebSocket and the recording upload.
func WithRecorders(rs *Recorders) Option {
	return func(a *API) {
		a.handle(RecorderRoute, "GET "+cp.LivePath, http.HandlerFunc(rs.serveLive))
		a.handle(RecorderRoute, "POST "+cp.RecordingsPath, http.HandlerFunc(rs.serveUpload))
	}
}

// WithPublic serves the public API, every route of which reads through the
// gate (R32), and sees no cookie or credential (anonymous). Each is behind
// its heavy-use limit, once WithLimits is given.
func WithPublic(p *public.API) Option {
	return func(a *API) {
		for _, r := range p.Routes() {
			k := kindOf(r.Pattern, r.Limit)
			a.handle(PublicRoute, r.Pattern, a.limited(k, anonymous(r.Handler)))
			a.routes[len(a.routes)-1].Limit = k
		}
		// A page reads through the gate as a request does: it's under the
		// request limit (R73).
		for _, pg := range p.Pages() {
			a.handle(PageRoute, pg.Pattern, a.limited(limits.Requests, a.page(pg)))
			a.routes[len(a.routes)-1].Limit = limits.Requests
		}
	}
}

// page serves one of the public API's pages (ADR 0017): the app's page,
// with what pg says link previews may show of what its path names. pg sees
// the request without cookies or credentials, as a public route does; only
// the look and theme the page is written in come from the asker's cookies,
// through the app, which carries no deployment data. Nothing public there,
// for whatever reason, is the app's own page, 404.
func (a *API) page(pg public.Page) http.Handler {
	anon := func(r *http.Request) *http.Request {
		r = r.Clone(r.Context())
		r.Header.Del("Cookie")
		r.Header.Del("Authorization")
		return r
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.shell == nil {
			http.NotFound(w, r)
			return
		}
		p, ok, err := pg.Preview(anon(r))
		switch {
		case err != nil:
			a.shell.Page(w, r, http.StatusInternalServerError, web.Preview{})
		case !ok:
			a.shell.Page(w, r, http.StatusNotFound, web.Preview{})
		default:
			a.shell.Page(w, r, http.StatusOK, web.Preview{Title: p.Title, Description: p.Description})
		}
	})
}

// anonymous hands h the request without its cookies or credentials, so a
// public route answers a signed-in listener exactly as anyone else (P3,
// R111): it can't tell them apart (ADR 0009).
func anonymous(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.Clone(r.Context())
		r.Header.Del("Cookie")
		r.Header.Del("Authorization")
		h.ServeHTTP(w, r)
	})
}

// WithAccount serves listeners' sign-in and their account (ADR 0009). None
// of its routes is the admin API's, and its session is no admin
// authenticator: listener sign-in opens nothing (R111).
func WithAccount(acc *account.Service) Option {
	return func(a *API) {
		for _, r := range acc.Routes() {
			a.handle(AccountRoute, r.Pattern, r.Handler)
		}
	}
}

// WithApp serves the browser app (SPEC.md section 5): its static files, and
// its one page for every other path outside the API.
func WithApp(app *web.App) Option {
	return func(a *API) {
		a.shell = app
		for _, r := range app.Routes() {
			a.handle(AppRoute, r.Pattern, r.Handler)
		}
	}
}

// New returns the API's handler.
func New(db Pinger, log *slog.Logger, opts ...Option) *API {
	a := &API{mux: http.NewServeMux(), app: http.NewServeMux()}
	a.handle(HealthRoute, "GET /healthz", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if _, err := db.Ping(ctx); err != nil {
			log.Warn("health check: database unreachable", "error", err)
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("database unreachable\n"))
			return
		}
		_, _ = w.Write([]byte("ok\n"))
	}))
	for _, o := range opts {
		o(a)
	}
	return a
}
