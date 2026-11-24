package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

const page = `<!doctype html>
<html lang="en-AU">
	<head><link rel="stylesheet" href="/_app/immutable/app.css"></head>
	<body></body>
</html>`

func serveMux(a *App) *http.ServeMux {
	mux := http.NewServeMux()
	for _, r := range a.Routes() {
		mux.Handle(r.Pattern, r.Handler)
	}
	return mux
}

func get(t *testing.T, h http.Handler, path string, cookies ...*http.Cookie) (*http.Response, string) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	b, _ := io.ReadAll(w.Result().Body)
	return w.Result(), string(b)
}

// R82: the page goes out in the listener's saved look and theme, so the
// first paint is already theirs; Harbour, following the system, as built.
func TestPageIsSentInTheSavedLook(t *testing.T) {
	root := fstest.MapFS{"index.html": {Data: []byte(page)}, "_app/immutable/app.css": {Data: []byte("body{}")}}
	h := serveMux(Serve(root))
	look := func(v string) *http.Cookie { return &http.Cookie{Name: LookCookie, Value: v} }
	theme := func(v string) *http.Cookie { return &http.Cookie{Name: ThemeCookie, Value: v} }
	for _, path := range []string{"/", "/index.html", "/recent", "/calls/6f1d2c3b-4a59-4e8f-9d7c-0b1a2c3d4e5f"} {
		res, body := get(t, h, path, look("console"), theme("dark"))
		if !strings.Contains(body, `<html data-look="console" data-theme="dark" lang="en-AU">`) {
			t.Errorf("%s as console, dark: %q", path, body)
		}
		if res.Header.Get("Vary") != "Cookie" || res.Header.Get("Cache-Control") != "no-cache, private" ||
			res.Header.Get("Content-Type") != "text/html; charset=utf-8" || res.Header.Get("Content-Security-Policy") == "" {
			t.Errorf("%s: headers %v", path, res.Header)
		}
	}
	for _, c := range []struct {
		cookies []*http.Cookie
		want    string
	}{
		{nil, `<html lang="en-AU">`},
		{[]*http.Cookie{look("harbour")}, `<html lang="en-AU">`},
		{[]*http.Cookie{look("harbour"), theme("light")}, `<html data-theme="light" lang="en-AU">`},
		{[]*http.Cookie{look("console")}, `<html data-look="console" lang="en-AU">`},
	} {
		if _, body := get(t, h, "/", c.cookies...); !strings.Contains(body, c.want) {
			t.Errorf("%v: %q, want %s", c.cookies, body, c.want)
		}
	}
	// Anything but a known look or theme is never written into the page.
	for _, bad := range []string{`console" onload="x`, "Console", "con sole", "<b>", "system", strings.Repeat("a", 33)} {
		if _, body := get(t, h, "/", look(bad), theme(bad)); body != page {
			t.Errorf("look and theme %q: %q", bad, body)
		}
	}
	// Other files are served as they are.
	res, body := get(t, h, "/_app/immutable/app.css", look("console"))
	if body != "body{}" || res.Header.Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Errorf("stylesheet: %q %v", body, res.Header)
	}
}

// R74: robots.txt keeps every crawler out.
func TestRobots(t *testing.T) {
	res, body := get(t, serveMux(Serve(fstest.MapFS{})), "/robots.txt")
	if res.StatusCode != 200 || body != "User-agent: *\nDisallow: /\n" {
		t.Errorf("robots.txt: %d %q", res.StatusCode, body)
	}
}

// A Go-only build without the app says so, rather than serving nothing.
func TestNotBuilt(t *testing.T) {
	if res, body := get(t, serveMux(Serve(fstest.MapFS{})), "/"); res.StatusCode != 503 || !strings.Contains(body, "npm run build") {
		t.Errorf("unbuilt: %d %q", res.StatusCode, body)
	}
}
