package server_test

import (
	"bytes"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/account"
	"github.com/RIKIKU/RadioArchive/internal/gate"
	"github.com/RIKIKU/RadioArchive/internal/server"
)

// TestIntegrationSignedInSameAnswers (SPEC.md P3, R111, ADR 0009): signed in
// or not, every public route answers the same, byte for byte. The A10
// harness probes every public route the server mounts (TestIntegrationA10
// fails while one is missing), so this covers each of them, the catalogue
// and the live feed included as they arrive. They answer the same because
// the server hands a public route no cookie or credential at all.
func TestIntegrationSignedInSameAnswers(t *testing.T) {
	f := newA10World(t)
	n := 0
	for _, route := range f.e.api.Routes() {
		if route.Kind != server.PublicRoute && route.Kind != server.PageRoute {
			continue
		}
		for _, p := range a10Probes[route.Pattern] {
			for _, id := range []string{f.public[0], f.deleted, f.unavailable["hidden 1003 #1"]} {
				out := do(t, p.req(f, id))
				r := p.req(f, id)
				if route.Kind == server.PageRoute {
					// A page is written in the asker's look (ADR 0017), so
					// signing in adds the session to the probe's own cookies.
					cookie := "ra_session=a-listeners-session"
					if c := r.Header.Get("Cookie"); c != "" {
						cookie = c + "; " + cookie
					}
					r.Header.Set("Cookie", cookie)
				} else {
					r.Header.Set("Cookie", "ra_session=a-listeners-session; look=console; theme=dark")
				}
				r.Header.Set("Authorization", "Bearer a-listeners-token")
				in := do(t, r)
				if out.status != in.status || !bytes.Equal(out.body, in.body) || out.String() != in.String() {
					t.Errorf("%s/%s: signed in, the answer differs:\nsigned out: %s\nsigned in:  %s", route.Pattern, p.name, out, in)
				}
				n++
			}
		}
	}
	if n == 0 {
		t.Fatal("no public route was compared")
	}
}

// TestIntegrationSignedInSameLiveFeed: the live feed (R36) sends a
// signed-in listener exactly what it sends a signed-out one with the same
// set, event for event and frame for frame (P3, R111).
func TestIntegrationSignedInSameLiveFeed(t *testing.T) {
	e := newEnv(t)
	rec := e.liveRecorder(t)
	signedIn := http.Header{}
	signedIn.Set("Cookie", "ra_session=a-listeners-session; look=console")
	signedIn.Set("Authorization", "Bearer a-listeners-token")
	out := e.listen(t, tgs(1001, 1002)(e), nil)
	in := e.listenWith(t, tgs(1001, 1002)(e), nil, signedIn)
	for _, l := range []*listener{out, in} {
		l.wait("the set taken", func(m liveMsg) bool { return m.Type == "listening" })
	}
	now := time.Now().UnixMilli()
	a, aID := rec.start(1001, nil, 5001, now)
	rec.frames(a, 0, 5, 'A')
	b, bID := rec.start(1002, nil, 5001, now)
	rec.frames(b, 0, 5, 'B')
	rec.end(a, now+1000)
	rec.end(b, now+1000)
	for _, l := range []*listener{out, in} {
		l.wait("1001's end", isEvent(gate.TypeCallEnd, aID))
		l.wait("1002's end", isEvent(gate.TypeCallEnd, bID))
	}
	quiet()
	raw := func(l *listener) []string {
		var s []string
		for _, m := range l.all() {
			s = append(s, string(m.Raw))
		}
		return s
	}
	if o, i := raw(out), raw(in); len(o) < 14 || !slices.Equal(o, i) {
		t.Errorf("signed in, the live feed differs:\nsigned out (%d): %s\nsigned in (%d):  %s", len(o), out.summary(), len(i), in.summary())
	}
}

// Listener sign-in is no admin sign-in: its session can't be the admin
// API's authenticator (R111). (The binary serves no admin API at all until
// M4: cmd/radioarchive's TestIntegrationNoAdminAPI, with sign-in on.)
func TestListenerSessionIsNoAdmin(t *testing.T) {
	svc, err := account.New(account.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := any(svc).(server.AdminAuthenticator); ok {
		t.Fatal("the listener account is an admin authenticator")
	}
	api := server.New(nil, nil, server.WithAccount(svc))
	for _, r := range api.Routes() {
		if r.Kind == server.AdminRoute {
			t.Errorf("the account mounts admin route %s", r.Pattern)
		}
	}
	// The sign-in hops are the API's: the app shell never answers them.
	for _, p := range []string{"/auth/nothing", "/auth"} {
		rec := &recorder{header: http.Header{}}
		api.ServeHTTP(rec, mustRequest("GET", p))
		if rec.code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, rec.code)
		}
	}
}

type recorder struct {
	header http.Header
	code   int
}

func (r *recorder) Header() http.Header         { return r.header }
func (r *recorder) Write(b []byte) (int, error) { return len(b), nil }
func (r *recorder) WriteHeader(c int)           { r.code = c }

func mustRequest(method, path string) *http.Request {
	r, err := http.NewRequest(method, "http://radio.example.test"+path, nil)
	if err != nil {
		panic(err)
	}
	return r
}
