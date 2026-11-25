package server_test

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"testing"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
	"github.com/RIKIKU/RadioArchive/internal/limits"
	"github.com/RIKIKU/RadioArchive/internal/public"
	"github.com/RIKIKU/RadioArchive/internal/server"
)

// newLimitedEnv is a test server behind cfg's limits, with testAdmin's
// admins exempt (ADR 0036).
func newLimitedEnv(t *testing.T, cfg limits.Config) *env {
	t.Helper()
	l, err := limits.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return newEnvAt(t, "0.1.0-test", server.WithLimits(l, testAdmin{}))
}

// refused checks a is R73's refusal: 429, Retry-After in whole seconds
// within a minute, and an R78 body with code, a message naming the limit,
// and the same wait.
func refused(t *testing.T, what string, a answer, code, names string) {
	t.Helper()
	var body struct {
		Error      string `json:"error"`
		Message    string `json:"message"`
		RetryAfter int    `json:"retry_after"`
	}
	ra, err := strconv.Atoi(a.header.Get("Retry-After"))
	if a.status != 429 || err != nil || ra < 1 || ra > 60 || json.Unmarshal(a.body, &body) != nil ||
		body.Error != code || body.RetryAfter != ra || !strings.Contains(body.Message, names) ||
		!strings.Contains(body.Message, "Try again in ") || a.header.Get("Cache-Control") != "no-store" {
		t.Errorf("%s: want 429 %s naming %q:\n%s", what, code, names, a)
	}
}

// TestIntegrationA21 is SPEC.md A21: given TRUSTED_PROXY=10.0.0.2, a
// request's X-Forwarded-For counts as its client when it comes from
// 10.0.0.2, and not when it comes from 10.0.0.3. Shown through the request
// limit: the forwarded address used up through 10.0.0.2 is refused there,
// while the same forwarded address from 10.0.0.3 is 10.0.0.3, still free.
func TestIntegrationA21(t *testing.T) {
	cfg := limits.Defaults()
	cfg.TrustedProxy = netip.MustParseAddr("10.0.0.2")
	e := newLimitedEnv(t, cfg)
	get := func(remote, forwarded string) answer {
		return e.pub(t, "GET", public.CataloguePath, "", "X-Test-Remote", remote, "X-Forwarded-For", forwarded)
	}
	for i := range 120 {
		if a := get(fmt.Sprintf("10.0.0.2:%d", 40000+i), "198.51.100.9"); a.status != 200 {
			t.Fatalf("request %d through the proxy: %s", i+1, a)
		}
	}
	refused(t, "198.51.100.9's 121st, through the proxy", get("10.0.0.2:41000", "198.51.100.9"), "too_many_requests", "at most 120 a minute")
	// Another listener behind the proxy is another client.
	if a := get("10.0.0.2:41001", "198.51.100.10"); a.status != 200 {
		t.Errorf("another forwarded address: %s", a)
	}
	// From 10.0.0.3 the header isn't read: it's 10.0.0.3 that counts.
	if a := get("10.0.0.3:41002", "198.51.100.9"); a.status != 200 {
		t.Errorf("the used-up forwarded address from 10.0.0.3: %s", a)
	}
	for i := range 119 {
		if a := get("10.0.0.3:41003", fmt.Sprintf("203.0.113.%d", i)); a.status != 200 {
			t.Fatalf("10.0.0.3's request %d: %s", i+2, a)
		}
	}
	refused(t, "10.0.0.3's 121st, whatever it forwards", get("10.0.0.3:41004", "203.0.113.200"), "too_many_requests", "at most 120 a minute")
}

// TestIntegrationA20Requests is A20's request half on the server: at the
// default limits, one client's 121st public API request in a minute,
// across every kind of request R73 counts, is refused with 429 and
// Retry-After, naming the limit; its 601st playback request too, which the
// request limit doesn't count. Another client isn't affected, nor is a
// signed-in admin, by session or token. A signed-in listener is (P3), and
// the recorder client never is.
func TestIntegrationA20Requests(t *testing.T) {
	e := newLimitedEnv(t, limits.Defaults())
	_, key := e.recorder(t)
	id, _ := e.storeCall(t, key, 1, 1001, nil)
	// Every limit is taken by its own client, so the next uses one up
	// from nothing.
	from := func(remote string, h ...string) func(method, path, body string) answer {
		return func(method, path, body string) answer {
			return e.pub(t, method, path, body, append([]string{"X-Test-Remote", remote}, h...)...)
		}
	}
	calls := fmt.Sprintf(`{"ids":[%q]}`, id)
	reqs := []struct{ method, path, body string }{
		{"GET", public.CataloguePath, ""},
		{"GET", public.SearchPath + "?q=", ""},
		{"GET", "/api/v1/fireground/41", ""},
		{"GET", public.RecentCallsPath, ""},
		{"POST", public.RecentCallsPath, "{}"},
		{"QUERY", public.CallsPath, calls},
		{"POST", public.CallsPath, calls},
		{"GET", public.DownloadURL(id), ""},
		{"GET", public.UnitSearchPath + "?q=engine", ""},
		{"GET", "/calls/" + id, ""},
	}
	listener := from("192.0.2.10:5000")
	for i := range 120 {
		r := reqs[i%len(reqs)]
		if a := listener(r.method, r.path, r.body); a.status == 429 {
			t.Fatalf("request %d (%s %s) refused: %s", i+1, r.method, r.path, a)
		}
	}
	for _, r := range reqs {
		refused(t, "121st: "+r.method+" "+r.path, listener(r.method, r.path, r.body), "too_many_requests", "at most 120 a minute")
	}
	if a := from("192.0.2.11:5000")("GET", public.CataloguePath, ""); a.status != 200 {
		t.Errorf("another client: %s", a)
	}
	// Playback is counted apart: still open to the client that has used
	// up its requests, until its own 600.
	for i := range 600 {
		if a := listener("GET", public.RecordingURL(id), ""); a.status != 200 {
			t.Fatalf("playback request %d: %s", i+1, a)
		}
	}
	refused(t, "601st playback", listener("GET", public.RecordingURL(id), ""), "too_many_plays", "at most 600 requests a minute")

	// R70: a signed-in admin, by browser session or the module's token,
	// isn't limited; a signed-in listener is, as everyone is.
	asAdmin := from("192.0.2.10:5000", "Cookie", "radioarchive_session=made-up", "X-Test-Admin", "admin-1")
	asModule := from("192.0.2.10:5000", "Authorization", "Bearer admin-2")
	signedIn := from("192.0.2.10:5000", "Cookie", "radioarchive_session=made-up")
	for _, r := range append(reqs, struct{ method, path, body string }{"GET", public.RecordingURL(id), ""}) {
		if a := asAdmin(r.method, r.path, r.body); a.status == 429 {
			t.Errorf("an admin's %s %s: %s", r.method, r.path, a)
		}
		if a := asModule(r.method, r.path, r.body); a.status == 429 {
			t.Errorf("the module's %s %s: %s", r.method, r.path, a)
		}
		if a := signedIn(r.method, r.path, r.body); a.status != 429 {
			t.Errorf("a signed-in listener's %s %s: %s", r.method, r.path, a)
		}
	}

	// The recorder client's uploads are never limited, though its address
	// has used up its requests.
	local := from("127.0.0.1:5000")
	for range 120 {
		local("GET", public.CataloguePath, "")
	}
	refused(t, "127.0.0.1's 121st", local("GET", public.CataloguePath, ""), "too_many_requests", "at most 120 a minute")
	for n := int64(2); n <= 140; n++ {
		audio := oggBytes(byte(n))
		m := recording(cp.CallID{CallNum: n, StartTime: 1764000200 + n}, audio)
		if code, resp := e.upload(t, key, m, audio); code != 201 {
			t.Fatalf("upload %d: %d %+v", n, code, resp)
		}
	}
}

// TestLimitsOnRoutes: every public route and page is under one limit, the
// one R71-R73 give it, and no other route is under any (ADR 0036).
func TestLimitsOnRoutes(t *testing.T) {
	e := newLimitedEnv(t, limits.Defaults())
	want := map[string]limits.Kind{
		"GET " + public.RecordingPath: limits.Playback,
		"GET " + public.LivePath:      limits.Live,
		"GET " + public.ZipPath:       limits.Zip,
		"QUERY " + public.ZipListPath: limits.Zip,
		"POST " + public.ZipListPath:  limits.Zip,
	}
	n := 0
	for _, r := range e.api.Routes() {
		switch r.Kind {
		case server.PublicRoute, server.PageRoute:
			n++
			k, ok := want[r.Pattern]
			if !ok {
				k = limits.Requests
			}
			if r.Limit != k {
				t.Errorf("%s is under %s, want %s", r.Pattern, r.Limit, k)
			}
		default:
			if r.Limit != 0 {
				t.Errorf("%s route %s is under %s", r.Kind, r.Pattern, r.Limit)
			}
		}
	}
	if n < 16 {
		t.Errorf("only %d public routes and pages", n)
	}
}
