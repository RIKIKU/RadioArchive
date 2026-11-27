package web

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// ask is a page asked for on host, with a raw Cookie header.
func ask(t *testing.T, h http.Handler, host, cookie string) (*http.Response, string) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/recent", nil)
	r.Host = host
	if cookie != "" {
		r.Header.Set("Cookie", cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Result(), w.Body.String()
}

// R102 (ADR 0054): with PagerArchive's address set, the page names it for
// the app switcher, escaped; standalone, the page is as it was.
func TestPageNamesPagerArchive(t *testing.T) {
	root := fstest.MapFS{"index.html": {Data: []byte(page)}}
	if _, body := ask(t, serveMux(Serve(root).Combined(Combined{})), "radio.example.test", "look=console"); !strings.Contains(body, `<html data-look="console" lang="en-AU">`) {
		t.Errorf("standalone: %q", body)
	}
	h := serveMux(Serve(root).Combined(Combined{PagerArchiveName: `Pager "&" <b>`, PagerArchiveURL: "https://pager.example.test/a?b"}))
	res, body := ask(t, h, "radio.example.test", "look=console")
	if !strings.Contains(body, `<html data-look="console" data-pagerarchive-name="Pager &#34;&amp;&#34; &lt;b&gt;" data-pagerarchive-url="https://pager.example.test/a?b" lang="en-AU">`) {
		t.Errorf("switcher: %q", body)
	}
	// The look isn't shared: no cookie is written.
	if c := res.Header.Values("Set-Cookie"); len(c) != 0 {
		t.Errorf("Set-Cookie without LOOK_COOKIE_DOMAIN: %v", c)
	}
}

// R104 (ADR 0054): with LOOK_COOKIE_DOMAIN set, the first paint is the
// shared look cookie's, read as ArchiveUx's look.js reads it; the page says
// the look is shared, and names the domain; and the look it was sent in is
// moved into the shared cookie, this host's own ended. On a host outside
// the domain, and standalone, nothing changes.
func TestSharedLook(t *testing.T) {
	root := fstest.MapFS{"index.html": {Data: []byte(page)}}
	h := serveMux(Serve(root).Combined(Combined{LookDomain: "example.test", Secure: true}))
	moved := func(look string) []string {
		return []string{"look=; Path=/; Max-Age=0", "look=" + look + "; Path=/; Domain=example.test; Max-Age=31536000; Secure; SameSite=Lax"}
	}
	for _, c := range []struct {
		host, cookie, html string
		set                []string
	}{
		// ArchiveUx's cases (test/look.test.mjs): the first look cookie
		// counts, as is or in double quotes; an unknown look is none.
		{"radio.example.test", "look=console", `<html data-look-shared="example.test" data-look="console" lang="en-AU">`, moved("console")},
		{"radio.example.test", `look="console"`, `<html data-look-shared="example.test" data-look="console" lang="en-AU">`, moved("console")},
		{"radio.example.test", "look=console; look=harbour", `<html data-look-shared="example.test" data-look="console" lang="en-AU">`, moved("console")},
		{"radio.example.test", "look=harbour; look=console", `<html data-look-shared="example.test" lang="en-AU">`, moved("harbour")},
		{"radio.example.test", "look=Console; look=console", `<html data-look-shared="example.test" lang="en-AU">`, nil},
		{"radio.example.test", "mylook=console", `<html data-look-shared="example.test" lang="en-AU">`, nil},
		{"radio.example.test", "", `<html data-look-shared="example.test" lang="en-AU">`, nil},
		{"RADIO.Example.test:443", "look=console", `<html data-look-shared="example.test" data-look="console" lang="en-AU">`, moved("console")},
		{"example.test", "look=console", `<html data-look-shared="example.test" data-look="console" lang="en-AU">`, moved("console")},
		// Not in the domain: this host's own cookie, as standalone.
		{"radio.example.org", "look=console", `<html data-look="console" lang="en-AU">`, nil},
		{"notexample.test", "look=console", `<html data-look="console" lang="en-AU">`, nil},
		{"127.0.0.1:8080", "look=console", `<html data-look="console" lang="en-AU">`, nil},
	} {
		res, body := ask(t, h, c.host, c.cookie)
		if !strings.Contains(body, c.html) {
			t.Errorf("%s with %q: %q, want %s", c.host, c.cookie, body, c.html)
		}
		if got := res.Header.Values("Set-Cookie"); !slices.Equal(got, c.set) {
			t.Errorf("%s with %q: Set-Cookie %q, want %q", c.host, c.cookie, got, c.set)
		}
	}
	// Plain HTTP (a test): not Secure.
	h = serveMux(Serve(root).Combined(Combined{LookDomain: "example.test"}))
	if res, _ := ask(t, h, "radio.example.test", "look=console"); !slices.Equal(res.Header.Values("Set-Cookie"),
		[]string{"look=; Path=/; Max-Age=0", "look=console; Path=/; Domain=example.test; Max-Age=31536000; SameSite=Lax"}) {
		t.Errorf("plain HTTP: %q", res.Header.Values("Set-Cookie"))
	}
	// With the switcher too: the shared mark first, then the look, as
	// PagerArchive's page has them.
	h = serveMux(Serve(root).Combined(Combined{LookDomain: "example.test", PagerArchiveName: "Pager", PagerArchiveURL: "https://pager.example.test"}))
	if _, body := ask(t, h, "radio.example.test", "look=console; theme=dark"); !strings.Contains(body,
		`<html data-look-shared="example.test" data-look="console" data-theme="dark" data-pagerarchive-name="Pager" data-pagerarchive-url="https://pager.example.test" lang="en-AU">`) {
		t.Errorf("shared, with the switcher: %q", body)
	}
	// A call link's page is sent the same way.
	a := Serve(root).Combined(Combined{LookDomain: "example.test", Secure: true})
	r := httptest.NewRequest(http.MethodGet, "/calls/x", nil)
	r.Host = "radio.example.test"
	r.Header.Set("Cookie", "look=console")
	w := httptest.NewRecorder()
	a.Page(w, r, 200, Preview{Title: "x", Description: "y"})
	if !strings.Contains(w.Body.String(), `<html data-look-shared="example.test" data-look="console" lang="en-AU">`) || len(w.Result().Header.Values("Set-Cookie")) != 2 {
		t.Errorf("call link: %v %q", w.Result().Header, w.Body.String())
	}
}

// The colour theme is shared the same way (ArchiveUx 0.7.0, its rule 8):
// with LOOK_COOKIE_DOMAIN set, the first paint's theme is the first light or
// dark `theme` cookie, as ArchiveUx's themeFromCookies reads it (an empty or
// unknown one passed over), else the system's; and it's moved into the
// shared cookie. Standalone, and on another host, it's this host's own.
func TestSharedTheme(t *testing.T) {
	root := fstest.MapFS{"index.html": {Data: []byte(page)}}
	h := serveMux(Serve(root).Combined(Combined{LookDomain: "example.test", Secure: true}))
	moved := func(theme string) []string {
		return []string{"theme=; Path=/; Max-Age=0", "theme=" + theme + "; Path=/; Domain=example.test; Max-Age=31536000; Secure; SameSite=Lax"}
	}
	for _, c := range []struct {
		host, cookie, html string
		set                []string
	}{
		{"radio.example.test", "theme=dark", `<html data-look-shared="example.test" data-theme="dark" lang="en-AU">`, moved("dark")},
		{"radio.example.test", `theme="light"`, `<html data-look-shared="example.test" data-theme="light" lang="en-AU">`, moved("light")},
		{"radio.example.test", "theme=light; theme=dark", `<html data-look-shared="example.test" data-theme="light" lang="en-AU">`, moved("light")},
		{"radio.example.test", "theme=; theme=dark", `<html data-look-shared="example.test" data-theme="dark" lang="en-AU">`, moved("dark")},
		{"radio.example.test", "theme=system; theme=Dark", `<html data-look-shared="example.test" lang="en-AU">`, nil},
		{"radio.example.test", "mytheme=dark", `<html data-look-shared="example.test" lang="en-AU">`, nil},
		{"radio.example.org", "theme=dark", `<html data-theme="dark" lang="en-AU">`, nil},
	} {
		res, body := ask(t, h, c.host, c.cookie)
		if !strings.Contains(body, c.html) {
			t.Errorf("%s with %q: %q, want %s", c.host, c.cookie, body, c.html)
		}
		if got := res.Header.Values("Set-Cookie"); !slices.Equal(got, c.set) {
			t.Errorf("%s with %q: Set-Cookie %q, want %q", c.host, c.cookie, got, c.set)
		}
	}
	// The look and the theme together: both moved.
	res, body := ask(t, h, "radio.example.test", "theme=dark; look=console")
	if !strings.Contains(body, `<html data-look-shared="example.test" data-look="console" data-theme="dark" lang="en-AU">`) || len(res.Header.Values("Set-Cookie")) != 4 {
		t.Errorf("look and theme: %q %q", res.Header.Values("Set-Cookie"), body)
	}
	// Standalone, the first theme cookie, as before, and nothing written.
	h = serveMux(Serve(root))
	if res, body := ask(t, h, "radio.example.test", "theme=dark"); !strings.Contains(body, `<html data-theme="dark" lang="en-AU">`) || len(res.Header.Values("Set-Cookie")) != 0 {
		t.Errorf("standalone: %q %q", res.Header.Values("Set-Cookie"), body)
	}
}
