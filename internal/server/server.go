// Package server is the API's HTTP handler.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
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
// authenticator), public, or the app's. A public route can only come from
// internal/public, whose handlers read through the hidden-talkgroup gate
// (ADR 0004), and lives under APIPath. An app route can only come from the
// web package, the browser app's static files, which carry no deployment
// data, and lives outside APIPath. New refuses any other route.
const (
	HealthRoute RouteKind = iota
	RecorderRoute
	AdminRoute
	PublicRoute
	AppRoute
)

func (k RouteKind) String() string {
	return [...]string{"health", "recorder", "admin", "public", "app"}[k]
}

// Route is one route the API serves.
type Route struct {
	Pattern string
	Kind    RouteKind
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

// apiPath says whether path is the API's rather than the app's.
func apiPath(path string) bool {
	return path == "/healthz" || path == APIPath || under(path, APIPath)
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
			path != RecorderPath && path != AdminPath
	case AppRoute:
		fits = strings.HasPrefix(path, "/") && !apiPath(path)
	}
	if !fits {
		panic(fmt.Sprintf("route %q isn't a %s route", pattern, kind))
	}
	if kind == AppRoute {
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
// gate (R32).
func WithPublic(p *public.API) Option {
	return func(a *API) {
		for _, r := range p.Routes() {
			a.handle(PublicRoute, r.Pattern, r.Handler)
		}
	}
}

// WithApp serves the browser app (SPEC.md section 5): its static files, and
// its one page for every other path outside the API.
func WithApp(app *web.App) Option {
	return func(a *API) {
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
