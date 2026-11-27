package web

// Beside PagerArchive (SPEC.md R102, R104; ADR 0054): the app switcher's
// settings, and the look cookie the two apps share on their parent domain.

import (
	"bytes"
	"html"
	"net"
	"net/http"
	"strings"
	"time"
)

// Combined is how the app runs beside PagerArchive. The zero Combined is
// standalone: no switcher, and the look cookie is this host's own.
type Combined struct {
	// PagerArchiveName and PagerArchiveURL are the app switcher's (R102):
	// the other app's name and address. No URL, no switcher.
	PagerArchiveName string
	PagerArchiveURL  string
	// LookDomain is LOOK_COOKIE_DOMAIN (R104): the parent domain the apps
	// share one look cookie on, lower case with no leading dot; "" keeps the
	// look cookie this host's own.
	LookDomain string
	// Secure marks the shared look cookie Secure: on unless APP_URL is
	// plain HTTP (a test).
	Secure bool
}

// Combined sets how the app runs beside PagerArchive, and returns it.
func (a *App) Combined(c Combined) *App {
	a.combined = c
	return a
}

// lookCookieAge is how long the shared look cookie lasts: a year, as
// ArchiveUx's lookCookie (look.js) writes it.
const lookCookieAge = 365 * 24 * time.Hour

// sharedHere reports whether the look is shared for this request: the
// domain is set, and the page is asked for on it or under it. On any other
// host (an address of the server's own, say) the browser would refuse the
// domain's cookie, so the look stays that host's own there.
func (c Combined) sharedHere(r *http.Request) bool {
	if c.LookDomain == "" {
		return false
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return host == c.LookDomain || strings.HasSuffix(host, "."+c.LookDomain)
}

// lookFromCookies is the look in the request's cookies, or "" when the
// cookie is absent, empty or not a known look: ArchiveUx's lookFromCookies
// (look.js), as PagerArchive's server reads it. The first cookie of that
// name counts, its value is the look's name as it is (double quotes around
// it dropped), and anything but a known look is none.
func lookFromCookies(r *http.Request) string {
	for _, line := range r.Header.Values("Cookie") {
		for _, part := range strings.Split(line, ";") {
			name, v, ok := strings.Cut(part, "=")
			if !ok || strings.TrimSpace(name) != LookCookie {
				continue
			}
			v = strings.TrimSpace(v)
			if len(v) >= 2 && strings.HasPrefix(v, `"`) && strings.HasSuffix(v, `"`) {
				v = v[1 : len(v)-1]
			}
			if looks[v] {
				return v
			}
			return ""
		}
	}
	return ""
}

// themeFromCookies is the theme in the request's cookies, light or dark, or
// "" to follow the system: ArchiveUx's themeFromCookies (look.js, 0.7.0).
// Unlike the look's, an empty or unknown `theme` cookie is passed over for
// a later one of the same name.
func themeFromCookies(r *http.Request) string {
	for _, line := range r.Header.Values("Cookie") {
		for _, part := range strings.Split(line, ";") {
			name, v, ok := strings.Cut(part, "=")
			if !ok || strings.TrimSpace(name) != ThemeCookie {
				continue
			}
			v = strings.TrimSpace(v)
			if len(v) >= 2 && strings.HasPrefix(v, `"`) && strings.HasSuffix(v, `"`) {
				v = v[1 : len(v)-1]
			}
			if themes[v] {
				return v
			}
		}
	}
	return ""
}

// moveTheme is moveLook's for the theme (ArchiveUx 0.7.0): the page sent in
// a light or dark theme ends this host's own `theme` cookie and writes the
// theme to the shared one. Following the system, nothing is written.
func (c Combined) moveTheme(w http.ResponseWriter, theme string) {
	if !themes[theme] {
		return
	}
	c.move(w, ThemeCookie, theme)
}

// moveLook keeps the look in the shared cookie only. RadioArchive standing
// alone keeps its look in a `look` cookie of this host's own, which has the
// shared one's name: kept beside it, it could be the first of the two the
// browser sends, and hide the look chosen in PagerArchive. So the page that
// was sent in look ends this host's own cookie and writes look to the
// shared one, as ArchiveUx's lookCookie does. The look a browser chose here
// alone is carried into the shared cookie on its first page this way.
// Nothing is written without a look.
func (c Combined) moveLook(w http.ResponseWriter, look string) {
	if !looks[look] {
		return
	}
	c.move(w, LookCookie, look)
}

// move ends this host's own cookie name and writes value to the shared one,
// as ArchiveUx's lookCookie and themeCookie write it.
func (c Combined) move(w http.ResponseWriter, name, value string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1})
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Domain: c.LookDomain, Path: "/",
		MaxAge: int(lookCookieAge.Seconds()), SameSite: http.SameSiteLaxMode, Secure: c.Secure,
	})
}

// withSwitcher is the page with the app switcher's data-pagerarchive-name
// and -url on its root element. Standalone, the page is as it was.
func (c Combined) withSwitcher(page []byte) []byte {
	if c.PagerArchiveURL == "" {
		return page
	}
	return withAttrs(page, ` data-pagerarchive-name="`+html.EscapeString(c.PagerArchiveName)+`"`+
		` data-pagerarchive-url="`+html.EscapeString(c.PagerArchiveURL)+`"`)
}

// withShared is the page with data-look-shared, naming the shared look
// cookie's domain, first on its root element, as PagerArchive marks its
// page (<html data-look-shared data-look="console">), so the app's script
// follows the cookie too.
func (c Combined) withShared(page []byte) []byte {
	return withAttrs(page, ` data-look-shared="`+html.EscapeString(c.LookDomain)+`"`)
}

// withAttrs is the page with attrs first on its root element.
func withAttrs(page []byte, attrs string) []byte {
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
