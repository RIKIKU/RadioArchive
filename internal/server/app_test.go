package server_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RIKIKU/RadioArchive/internal/server"
	"github.com/RIKIKU/RadioArchive/web"
)

// The app shell answers every path outside the API, and none inside it; and
// every response, the API's and the app's, says not to index it (R74).
func TestAppShellBesideTheAPI(t *testing.T) {
	h := server.New(pinger{}, slog.New(slog.DiscardHandler), server.WithApp(web.Serve(appBuild)))
	srv := httptest.NewServer(h)
	defer srv.Close()
	for _, c := range []struct {
		method, path string
		code         int
		shell        bool
	}{
		{"GET", "/", 200, true},
		{"HEAD", "/", 200, false},
		{"GET", "/recent", 200, true},
		{"GET", "/calls/6f1d2c3b-4a59-4e8f-9d7c-0b1a2c3d4e5f", 200, true},
		{"GET", "/apiary", 200, true},
		{"GET", "/_app/immutable/start.js", 200, false},
		{"GET", "/robots.txt", 200, false},
		{"POST", "/", 405, false},
		{"GET", "/healthz", 200, false},
		{"GET", "/api", 404, false},
		{"GET", "/api/", 404, false},
		{"GET", "/api/v1/nothing", 404, false},
		{"GET", server.AdminPath + "/systems", 404, false},
		{"GET", server.RecorderPath + "/nothing", 404, false},
	} {
		r, _ := http.NewRequest(c.method, srv.URL+c.path, nil)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		isShell := strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html")
		if resp.StatusCode != c.code || (c.method == "GET" && isShell != c.shell) {
			t.Errorf("%s %s = %d (shell %v), want %d (shell %v)", c.method, c.path, resp.StatusCode, isShell, c.code, c.shell)
		}
		if got := resp.Header.Get("X-Robots-Tag"); got != "noindex" {
			t.Errorf("%s %s: X-Robots-Tag %q, want noindex", c.method, c.path, got)
		}
	}
}
