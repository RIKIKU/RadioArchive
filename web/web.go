// Package web is the browser app (SPEC.md section 5, ADR 0001): SvelteKit
// (adapter-static) builds it into web/build, which is embedded in the
// server binary and served for every path the API doesn't own.
//
// The app shell is static files and carries no deployment data: no
// talkgroup, system or call. So this package may not import the store, the
// gate, the database or any other part of this repo (imports_test.go
// enforces it), and its routes are the server's app routes
// (server.AppRoute), apart from internal/public's, which alone serve data
// (ADR 0004). The A10 harness probes them too.
package web

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:build
var files embed.FS

// Cookies the app saves the listener's look and theme in (R82, R48), read
// here so the page is sent already in them.
const (
	LookCookie  = "look"
	ThemeCookie = "theme"
)

// Looks and themes the page may be sent in. Harbour, the default, is the
// page as built (no data-look); a theme that isn't set follows the system.
var (
	looks  = map[string]bool{"harbour": true, "console": true}
	themes = map[string]bool{"light": true, "dark": true}
)

// RobotsTxt is /robots.txt (R74).
const RobotsTxt = "User-agent: *\nDisallow: /\n"

// Route is one of the app's routes.
type Route struct {
	Pattern string
	Handler http.Handler
}

// App is the browser app.
type App struct {
	routes []Route
}

// Routes are the app's routes, for the server to mount.
func (a *App) Routes() []Route { return a.routes }

// New returns the app built into this binary. If it wasn't built (a Go-only
// build), every page says so.
func New() *App {
	root, _ := fs.Sub(files, "build")
	return Serve(root)
}

// Serve returns the app built into root.
func Serve(root fs.FS) *App {
	return &App{routes: []Route{
		{"GET /robots.txt", http.HandlerFunc(robots)},
		{"GET /", shell(root)},
	}}
}

func robots(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write([]byte(RobotsTxt))
}

// shell serves the build's files, and index.html, the app's one page, for
// every other path, so the browser's router can handle it. The page is sent
// in the listener's look and theme (WithLook), so its first paint is
// already theirs and the default never flashes first (R82).
func shell(root fs.FS) http.Handler {
	index, err := fs.ReadFile(root, "index.html")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "The browser app wasn't built into this binary (cd web && npm run build).", http.StatusServiceUnavailable)
		})
	}
	fileServer := http.FileServerFS(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" && p != "index.html" {
			if st, err := fs.Stat(root, p); err == nil && !st.IsDir() {
				if strings.HasPrefix(p, "_app/immutable/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-cache")
				}
				w.Header().Set("Content-Security-Policy", csp)
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		look, theme := "", ""
		if c, err := r.Cookie(LookCookie); err == nil {
			look = c.Value
		}
		if c, err := r.Cookie(ThemeCookie); err == nil {
			theme = c.Value
		}
		// The page depends on the asker's cookies, so no cache may keep one
		// listener's for another.
		w.Header().Set("Cache-Control", "no-cache, private")
		w.Header().Set("Vary", "Cookie")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", csp)
		_, _ = w.Write(WithLook(index, look, theme))
	})
}

// WithLook is the page with the look and theme on its root element:
// data-look for Console (Harbour is the page as built) and data-theme for
// light or dark (none follows the system). Anything but a known look or
// theme is left out, so nothing else can ever be written into the page.
func WithLook(page []byte, look, theme string) []byte {
	var attrs string
	if looks[look] && look != "harbour" {
		attrs += ` data-look="` + look + `"`
	}
	if themes[theme] {
		attrs += ` data-theme="` + theme + `"`
	}
	i := bytes.Index(page, []byte("<html"))
	if attrs == "" || i < 0 {
		return page
	}
	i += len("<html")
	out := make([]byte, 0, len(page)+len(attrs))
	out = append(out, page[:i]...)
	out = append(out, attrs...)
	return append(out, page[i:]...)
}

// csp allows only this origin. SvelteKit's static bootstrap script is
// inline.
const csp = "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"
