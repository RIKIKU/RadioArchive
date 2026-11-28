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
	"html"
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
	routes   []Route
	index    []byte   // the app's one page; nil when the app wasn't built
	combined Combined // beside PagerArchive (combined.go); zero standalone
	// assetLinks is /.well-known/assetlinks.json (applinks.go); nil answers 404.
	assetLinks []byte
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
	a := &App{}
	if index, err := fs.ReadFile(root, "index.html"); err == nil {
		a.index = index
	}
	a.routes = []Route{
		{"GET /robots.txt", http.HandlerFunc(robots)},
		{"GET " + AssetLinksPath, http.HandlerFunc(a.assetLinksFile)},
		{"GET /", a.shell(root)},
	}
	return a
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
func (a *App) shell(root fs.FS) http.Handler {
	fileServer := http.FileServerFS(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.index == nil {
			notBuilt(w)
			return
		}
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
		// The page depends on the asker's cookies, so no cache may keep one
		// listener's for another.
		a.write(w, r, http.StatusOK, "no-cache, private", a.index)
	})
}

func notBuilt(w http.ResponseWriter) {
	http.Error(w, "The browser app wasn't built into this binary (cd web && npm run build).", http.StatusServiceUnavailable)
}

// write sends page with status, in the asker's look and theme. Beside
// PagerArchive, the look is the shared cookie's, and the page carries the
// app switcher's and the shared look's settings (combined.go).
func (a *App) write(w http.ResponseWriter, r *http.Request, status int, cache string, page []byte) {
	look, theme := "", ""
	shared := a.combined.sharedHere(r)
	if shared {
		look, theme = lookFromCookies(r), themeFromCookies(r)
	} else {
		if c, err := r.Cookie(LookCookie); err == nil {
			look = c.Value
		}
		if c, err := r.Cookie(ThemeCookie); err == nil {
			theme = c.Value
		}
	}
	h := w.Header()
	h.Set("Cache-Control", cache)
	h.Set("Vary", "Cookie")
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", csp)
	if shared {
		a.combined.moveLook(w, look)
		a.combined.moveTheme(w, theme)
	}
	w.WriteHeader(status)
	page = WithLook(a.combined.withSwitcher(page), look, theme)
	if shared {
		page = a.combined.withShared(page)
	}
	_, _ = w.Write(page)
}

// Preview is what a page tells link previews about what it names (R61,
// R74): a title and a description, as text. The zero Preview is none: the
// app's own page, which is the site's generic preview.
type Preview struct {
	Title       string
	Description string
}

// Page sends the app's page with status and preview, in the asker's look
// and theme, for a path that names something: a call link (ADR 0017). The
// server calls it for the public API's pages, which decide what may be
// shown (internal/public); this package only writes it. A zero preview
// sends the app's own page, byte for byte. A page built from what the
// deployment holds is never kept by a cache, so hiding a talkgroup takes
// effect at the next request (R74).
func (a *App) Page(w http.ResponseWriter, r *http.Request, status int, p Preview) {
	if a.index == nil {
		notBuilt(w)
		return
	}
	a.write(w, r, status, "no-store", WithPreview(a.index, p))
}

// WithPreview is the page with p's preview tags at the end of its head:
// Open Graph's and the description, which link previews read. The text is
// escaped, so a talkgroup's name can never be markup. A zero preview, or a
// page with no head, is the page as it is.
func WithPreview(page []byte, p Preview) []byte {
	if p == (Preview{}) {
		return page
	}
	i := bytes.Index(page, []byte("</head>"))
	if i < 0 {
		return page
	}
	t, d := html.EscapeString(p.Title), html.EscapeString(p.Description)
	tags := `<meta property="og:type" content="website" />` +
		`<meta property="og:site_name" content="RadioArchive" />` +
		`<meta property="og:title" content="` + t + `" />` +
		`<meta property="og:description" content="` + d + `" />` +
		`<meta name="description" content="` + d + `" />` +
		`<meta name="twitter:card" content="summary" />`
	out := make([]byte, 0, len(page)+len(tags))
	out = append(out, page[:i]...)
	out = append(out, tags...)
	return append(out, page[i:]...)
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
// inline. 'wasm-unsafe-eval' lets the page compile WebAssembly, and nothing
// else (no eval): the WASM Opus decoder for browsers without WebCodecs'
// Opus, which is in the app's own script (R39, ADR 0011).
const csp = "default-src 'self'; script-src 'self' 'unsafe-inline' 'wasm-unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"
