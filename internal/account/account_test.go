package account

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/RIKIKU/RadioArchive/internal/gate"
	"github.com/RIKIKU/RadioArchive/internal/oidcclient"
	"github.com/RIKIKU/RadioArchive/internal/oidcclient/oidctest"
)

// A made-up listener, and secrets that must never reach a log.
const (
	subject      = "42"
	email        = "listener@example.test"
	clientSecret = "made-up-client-secret-0123456789abcdef"
	clientIP     = "203.0.113.45"
	forwardedIP  = "198.51.100.77"
)

// profiles is a made-up store: saved profiles, in memory, by issuer and
// subject.
type profiles struct {
	mu     sync.Mutex
	rows   map[[2]string][]byte
	refuse error // every call fails with this
	writes int
}

func (d *profiles) ListenerProfile(_ context.Context, iss, sub string) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.refuse != nil {
		return nil, d.refuse
	}
	return d.rows[[2]string{iss, sub}], nil
}

func (d *profiles) SaveListenerProfile(_ context.Context, iss, sub string, p []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.refuse != nil {
		return d.refuse
	}
	d.writes++
	d.rows[[2]string{iss, sub}] = p
	return nil
}

func (d *profiles) ClearListenerProfile(_ context.Context, iss, sub string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.refuse != nil {
		return d.refuse
	}
	d.writes++
	delete(d.rows, [2]string{iss, sub})
	return nil
}

// catalogue is the public catalogue: 1001 and 1002 on system 1. 1003 is
// hidden, so it isn't there.
type catalogue struct{}

func (catalogue) Catalogue(context.Context) (*gate.Catalogue, error) {
	ex := gate.System{ID: 1, Label: "EX", Name: "Example network"}
	return &gate.Catalogue{Systems: []gate.System{ex}, Talkgroups: []gate.CatalogueTalkgroup{
		{System: ex, ID: 1001, Name: "Dispatch North"}, {System: ex, ID: 1002, Name: "Fireground 41"}}}, nil
}

type world struct {
	p    *oidctest.Provider
	db   *profiles
	app  *httptest.Server
	logs *bytes.Buffer
	c    *http.Client // a browser: cookies, no redirects followed
	svc  *Service
}

// key is the listener's row: this provider's issuer and their subject.
func (w *world) key() [2]string { return [2]string{w.p.Issuer(), subject} }

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{p: oidctest.New(t, clientSecret, subject, email, "Made Up"), db: &profiles{rows: map[[2]string][]byte{}}, logs: &bytes.Buffer{}}
	mux := http.NewServeMux()
	w.app = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		// Every request comes from a listener's address, through a proxy.
		r.RemoteAddr = clientIP + ":5555"
		r.Header.Set("X-Forwarded-For", forwardedIP)
		mux.ServeHTTP(rw, r)
	}))
	t.Cleanup(w.app.Close)
	appURL, _ := url.Parse(w.app.URL)
	svc, err := New(Config{
		AppURL: appURL,
		OIDC: &oidcclient.Client{Issuer: w.p.Issuer(), ClientID: oidctest.ClientID,
			ClientSecret: clientSecret, RedirectURL: w.app.URL + "/auth/callback", HTTP: http.DefaultClient},
		Profiles: w.db, Catalogue: catalogue{}, Secret: []byte(clientSecret),
		Log: slog.New(slog.NewJSONHandler(w.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	if err != nil {
		t.Fatal(err)
	}
	w.svc = svc
	for _, r := range svc.Routes() {
		mux.Handle(r.Pattern, r.Handler)
	}
	jar, _ := cookiejar.New(nil)
	w.c = &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("the account's log:\n%s", w.logs)
		}
		// Nothing about a listener, or a secret, is ever logged (P4, R90).
		for _, s := range []string{clientIP, forwardedIP, email, clientSecret, "Made Up"} {
			if strings.Contains(w.logs.String(), s) {
				t.Errorf("the log holds %q:\n%s", s, w.logs)
			}
		}
	})
	return w
}

func (w *world) do(t *testing.T, method, path, body string, h ...string) *http.Response {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	r, _ := http.NewRequest(method, w.app.URL+path, rd)
	for i := 0; i+1 < len(h); i += 2 {
		r.Header.Set(h[i], h[i+1])
	}
	resp, err := w.c.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// signIn goes through the sign-in as a browser would, and returns the
// callback's answer.
func (w *world) signIn(t *testing.T, ret string) *http.Response {
	t.Helper()
	resp := w.do(t, "GET", "/auth/signin?return="+url.QueryEscape(ret), "")
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("GET /auth/signin = %d", resp.StatusCode)
	}
	code, state := w.p.Grant(t, resp.Header.Get("Location"))
	return w.do(t, "GET", "/auth/callback?code="+code+"&state="+state, "")
}

func (w *world) state(t *testing.T) State {
	t.Helper()
	resp := w.do(t, "GET", AccountPath, "")
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s = %d", AccountPath, resp.StatusCode)
	}
	var st State
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	return st
}

func (w *world) put(t *testing.T, body string) *http.Response {
	return w.do(t, "PUT", ProfilePath, body, "Origin", w.app.URL, "Content-Type", "application/json")
}

func cookieNamed(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// Sign-in is the authorization code flow with PKCE S256, state and nonce,
// and ends in a host-only session cookie, back where the listener was.
func TestSignIn(t *testing.T) {
	w := newWorld(t)
	if st := w.state(t); !st.SignIn || st.Account != nil || st.Profile != nil {
		t.Fatalf("signed out: %+v", st)
	}
	resp := w.signIn(t, "/?set=1001")
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/?set=1001" {
		t.Fatalf("callback = %d to %q, want 303 to /?set=1001", resp.StatusCode, resp.Header.Get("Location"))
	}
	c := cookieNamed(resp, sessionCookie)
	if c == nil || c.Domain != "" || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
		t.Fatalf("session cookie %+v: want host-only, HttpOnly, SameSite=Lax, Path=/", c)
	}
	if strings.Contains(c.Value, email) || strings.Contains(c.Value, subject+`"`) {
		t.Error("the session cookie can be read")
	}
	// The token request carried the verifier for the challenge (checked by
	// the provider, which answered).
	if v := w.p.Verifiers(); len(v) != 1 || len(v[0]) < 43 {
		t.Errorf("PKCE verifiers sent: %q", v)
	}
	st := w.state(t)
	if st.Account == nil || st.Account.ID != subject || st.Account.Email != email || st.Profile != nil || !st.Saves {
		t.Fatalf("signed in: %+v", st)
	}
}

// A sign-in that doesn't check out ends signed out, on the Listen page:
// another browser's state, a replayed code, the wrong nonce.
func TestSignInRefused(t *testing.T) {
	w := newWorld(t)
	resp := w.do(t, "GET", "/auth/signin", "")
	code, _ := w.p.Grant(t, resp.Header.Get("Location"))
	if resp := w.do(t, "GET", "/auth/callback?code="+code+"&state=someone-elses", ""); resp.Header.Get("Location") != signInFailed || cookieNamed(resp, sessionCookie) != nil {
		t.Errorf("wrong state: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	w = newWorld(t)
	w.p.SetNonce("not-the-one-sent")
	if resp := w.signIn(t, "/"); resp.Header.Get("Location") != signInFailed || cookieNamed(resp, sessionCookie) != nil {
		t.Errorf("wrong nonce: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	// Authentik's refusal (the listener cancelled).
	w = newWorld(t)
	resp = w.do(t, "GET", "/auth/signin", "")
	_, state := w.p.Grant(t, resp.Header.Get("Location"))
	if resp := w.do(t, "GET", "/auth/callback?error=access_denied&state="+state, ""); resp.Header.Get("Location") != signInFailed {
		t.Errorf("refused: %q", resp.Header.Get("Location"))
	}

	// A return path to another site is never followed.
	w = newWorld(t)
	if resp := w.signIn(t, "//evil.example/"); resp.Header.Get("Location") != "/" {
		t.Errorf("return to another site: %q", resp.Header.Get("Location"))
	}
}

// The profile: saved, read back, cleared; a hidden talkgroup in it is never
// shown to the browser, and never lost by a save (P1, R33, R110).
func TestProfile(t *testing.T) {
	w := newWorld(t)
	// Signed out, nothing can be saved.
	if resp := w.put(t, `{"v":1,"talkgroups":[{"id":1001}]}`); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("PUT signed out = %d, want 401", resp.StatusCode)
	}
	w.signIn(t, "/")
	if resp := w.put(t, `{"v":1,"talkgroups":[{"id":1001},{"id":1002,"priority":true}],"look":"console","theme":"dark","alerts":true}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT = %d", resp.StatusCode)
	}
	st := w.state(t)
	if st.Profile == nil || len(st.Profile.Talkgroups) != 2 || !st.Profile.Talkgroups[1].Priority || st.Profile.Look != "console" || !st.Profile.Alerts {
		t.Fatalf("read back: %+v", st.Profile)
	}

	// 1003 was saved while it was public, and is hidden now.
	w.db.rows[w.key()] = []byte(`{"v":1,"talkgroups":[{"id":1001},{"id":1003,"priority":true}],"alerts":false}`)
	resp := w.do(t, "GET", AccountPath, "")
	b, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(b), "1003") {
		t.Fatalf("GET %s shows hidden 1003: %s", AccountPath, b)
	}
	if resp := w.put(t, `{"v":1,"talkgroups":[{"id":1002}],"alerts":false}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT = %d", resp.StatusCode)
	}
	if got := string(w.db.rows[w.key()]); !strings.Contains(got, `{"id":1003,"priority":true}`) || strings.Contains(got, `"id":1001`) {
		t.Errorf("saved %s: want 1002, and hidden 1003 kept", got)
	}

	// Cleared: nothing at all is left, the hidden entry included.
	if resp := w.do(t, "DELETE", ProfilePath, "", "Origin", w.app.URL); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE = %d", resp.StatusCode)
	}
	if _, ok := w.db.rows[w.key()]; ok {
		t.Error("the profile is still there after clearing")
	}
	if st := w.state(t); st.Account == nil || st.Profile != nil {
		t.Errorf("after clearing: %+v", st)
	}

	// Only a profile is saved: no other field, no other shape, nothing huge.
	for _, bad := range []string{
		`{"v":2,"talkgroups":[]}`, `{"v":1,"talkgroups":[],"note":"x"}`, `{"v":1,"talkgroups":[{"id":0}]}`,
		`{"v":1,"talkgroups":[{"id":1001},{"id":1001}]}`, `{"v":1,"talkgroups":[],"look":"neon"}`, `[]`,
		`{"v":1,"talkgroups":[` + strings.Repeat(`{"id":1},`, maxEntries) + `{"id":2}]}`,
	} {
		if resp := w.put(t, bad); resp.StatusCode/100 != 4 {
			t.Errorf("PUT %.60s = %d, want refused", bad, resp.StatusCode)
		}
	}
}

// The download basket goes in the profile with the set (R64, R110; ADR
// 0018): as the browser sends it, kept when a page from before M3 sends
// none, cleared with the rest, and refused when it isn't one or is larger
// than a profile holds.
func TestProfileBasket(t *testing.T) {
	w := newWorld(t)
	w.signIn(t, "/")
	const a, b = "0f8e2c1a-6b3d-4e5f-9a7b-1c2d3e4f5a6b", "6f1d2c3b-4a59-4e8f-9d7c-0b1a2c3d4e5f"
	basket := fmt.Sprintf(`{"calls":[{"id":%q,"added":1764000000000},{"id":%q,"added":1764000001000}],`+
		`"conversations":[{"id":"c1","name":"Structure fire","note":"Two trucks","calls":[{"id":%q,"added":1764000002000}],"last":1764000003000}],"last":1764000004000}`, a, b, b)
	if resp := w.put(t, `{"v":1,"talkgroups":[{"id":1001}],"alerts":false,"basket":`+basket+`}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT = %d", resp.StatusCode)
	}
	st := w.state(t)
	if st.Profile == nil || st.Profile.Basket == nil || len(st.Profile.Basket.Calls) != 2 ||
		len(st.Profile.Basket.Conversations) != 1 || st.Profile.Basket.Conversations[0].Name != "Structure fire" ||
		st.Profile.Basket.Conversations[0].Calls[0].ID != b || *st.Profile.Basket.Last != 1764000004000 {
		t.Fatalf("read back: %+v", st.Profile)
	}
	// A page from before M3 sends no basket: the account keeps its own.
	if resp := w.put(t, `{"v":1,"talkgroups":[{"id":1002}],"alerts":false}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT = %d", resp.StatusCode)
	}
	if got := string(w.db.rows[w.key()]); !strings.Contains(got, `"Structure fire"`) || !strings.Contains(got, `"id":1002`) {
		t.Errorf("saved %s: want 1002 and the basket kept", got)
	}
	// An empty basket is the browser's to send.
	if resp := w.put(t, `{"v":1,"talkgroups":[],"alerts":false,"basket":{"calls":[],"conversations":[]}}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT = %d", resp.StatusCode)
	}
	if got := string(w.db.rows[w.key()]); strings.Contains(got, a) {
		t.Errorf("saved %s: want the basket emptied", got)
	}

	calls := func(n int) string {
		var s []string
		for i := range n {
			s = append(s, fmt.Sprintf(`{"id":"00000000-0000-4000-8000-%012d","added":1}`, i))
		}
		return strings.Join(s, ",")
	}
	conv := func(id, name, calls string) string {
		return fmt.Sprintf(`{"id":%q,"name":%q,"calls":[%s]}`, id, name, calls)
	}
	one := fmt.Sprintf(`{"id":%q,"added":1}`, a)
	convs := func(n int) string {
		var s []string
		for i := range n {
			s = append(s, conv(fmt.Sprintf("c%d", i), "Fire", one))
		}
		return strings.Join(s, ",")
	}
	for name, bk := range map[string]string{
		"not a call ID":                   `{"calls":[{"id":"1003","added":1}],"conversations":[]}`,
		"an ID in capitals":               `{"calls":[{"id":"0F8E2C1A-6B3D-4E5F-9A7B-1C2D3E4F5A6B","added":1}],"conversations":[]}`,
		"a call twice":                    `{"calls":[` + one + `,` + one + `],"conversations":[]}`,
		"an unknown field":                `{"calls":[],"conversations":[],"talkgroups":[1003]}`,
		"a conversation's call not in it": `{"calls":[` + one + `],"conversations":[` + conv("c1", "Fire", fmt.Sprintf(`{"id":%q,"added":1}`, b)) + `]}`,
		"a conversation unnamed":          `{"calls":[` + one + `],"conversations":[` + conv("c1", " ", one) + `]}`,
		"a conversation twice":            `{"calls":[` + one + `],"conversations":[` + conv("c1", "A", one) + `,` + conv("c1", "B", one) + `]}`,
		"a name too long":                 `{"calls":[` + one + `],"conversations":[` + conv("c1", strings.Repeat("x", 201), one) + `]}`,
		"an ID of odd characters":         `{"calls":[` + one + `],"conversations":[` + conv("c 1", "A", one) + `]}`,
		"too many calls":                  `{"calls":[` + calls(MaxBasketCalls+1) + `],"conversations":[]}`,
		"too many conversations":          `{"calls":[` + one + `],"conversations":[` + convs(MaxConversations+1) + `]}`,
	} {
		if resp := w.put(t, `{"v":1,"talkgroups":[],"alerts":false,"basket":`+bk+`}`); resp.StatusCode/100 != 4 {
			t.Errorf("%s: PUT = %d, want refused", name, resp.StatusCode)
		}
	}
	// The largest basket allowed, with the largest set, fits.
	var big []string
	for i := range MaxConversations {
		var in []string
		for j := range MaxConversationPlaces / MaxConversations {
			in = append(in, fmt.Sprintf(`{"id":"00000000-0000-4000-8000-%012d","added":1764000000000}`, (i*40+j)%MaxBasketCalls))
		}
		big = append(big, fmt.Sprintf(`{"id":"conversation-%02d","name":%q,"note":%q,"calls":[%s],"last":1764000000000}`,
			i, strings.Repeat("n", 200), strings.Repeat("\"", 2000), strings.Join(in, ",")))
	}
	var set []string
	for i := range maxEntries {
		set = append(set, fmt.Sprintf(`{"system":12345,"id":%d,"priority":true}`, 100000+i))
	}
	full := fmt.Sprintf(`{"v":1,"talkgroups":[%s],"look":"console","theme":"dark","alerts":true,"basket":{"calls":[%s],"conversations":[%s],"last":1764000000000}}`,
		strings.Join(set, ","), strings.ReplaceAll(calls(MaxBasketCalls), `"added":1`, `"added":1764000000000`), strings.Join(big, ","))
	if len(full) > maxBody {
		t.Fatalf("the largest profile is %d bytes, over the %d a PUT takes", len(full), maxBody)
	}
	if resp := w.put(t, full); resp.StatusCode != http.StatusNoContent {
		t.Errorf("the largest profile (%d bytes): PUT = %d", len(full), resp.StatusCode)
	}
}

// A change to the account must come from this site.
func TestProfileSameSite(t *testing.T) {
	w := newWorld(t)
	w.signIn(t, "/")
	for _, h := range [][]string{
		{"Content-Type", "application/json"},
		{"Content-Type", "application/json", "Origin", "https://evil.example"},
		{"Content-Type", "application/json", "Origin", w.app.URL, "Sec-Fetch-Site", "cross-site"},
	} {
		if resp := w.do(t, "PUT", ProfilePath, `{"v":1,"talkgroups":[]}`, h...); resp.StatusCode != http.StatusForbidden {
			t.Errorf("PUT with %q = %d, want 403", h, resp.StatusCode)
		}
		if resp := w.do(t, "DELETE", ProfilePath, "", h...); resp.StatusCode != http.StatusForbidden {
			t.Errorf("DELETE with %q = %d, want 403", h, resp.StatusCode)
		}
	}
	if w.db.writes != 0 {
		t.Errorf("%d writes from another site", w.db.writes)
	}
}

// The database down says so, saves nothing and signs no one out; back up,
// everything works again. Every signed-in account can save (ADR 0021).
func TestProfileStoreDown(t *testing.T) {
	w := newWorld(t)
	w.signIn(t, "/")
	w.db.refuse = errors.New("read the listener's profile: SQLSTATE 57P01")
	if resp := w.do(t, "GET", AccountPath, ""); resp.StatusCode != http.StatusServiceUnavailable || cookieNamed(resp, sessionCookie) != nil {
		t.Errorf("database down: GET = %d", resp.StatusCode)
	}
	if resp := w.put(t, `{"v":1,"talkgroups":[]}`); resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("database down: PUT = %d", resp.StatusCode)
	}
	if resp := w.do(t, "DELETE", ProfilePath, "", "Origin", w.app.URL); resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("database down: DELETE = %d", resp.StatusCode)
	}
	w.db.refuse = nil
	if st := w.state(t); st.Account == nil || !st.Saves {
		t.Errorf("back up: %+v", st)
	}
}

// The profile is the account's, by issuer and subject: another issuer's
// listener with the same subject is someone else (ADR 0021), and a session
// from another issuer than this deployment's isn't live.
func TestProfileKeyedByIssuer(t *testing.T) {
	w := newWorld(t)
	w.signIn(t, "/")
	w.db.rows[[2]string{"https://other.example.test/application/o/radioarchive/", subject}] = []byte(`{"v":1,"talkgroups":[{"id":1002}],"alerts":false}`)
	if st := w.state(t); st.Account == nil || st.Profile != nil {
		t.Fatalf("another issuer's profile was shown: %+v", st.Profile)
	}
	if resp := w.put(t, `{"v":1,"talkgroups":[{"id":1001}],"alerts":false}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT = %d", resp.StatusCode)
	}
	if len(w.db.rows) != 2 || !strings.Contains(string(w.db.rows[w.key()]), `"id":1001`) {
		t.Errorf("rows %q", w.db.rows)
	}
	// The same cookie, read by a deployment whose issuer has changed.
	s := w.svc
	cfg := s.cfg
	cfg.OIDC = &oidcclient.Client{Issuer: "https://other.example.test/application/o/radioarchive/", ClientID: oidctest.ClientID,
		ClientSecret: clientSecret, RedirectURL: w.app.URL + "/auth/callback", HTTP: http.DefaultClient}
	s2, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", AccountPath, nil)
	for _, c := range w.c.Jar.Cookies(mustURL(t, w.app.URL)) {
		r.AddCookie(c)
	}
	if _, ok := s2.current(r); ok {
		t.Error("a session from another issuer is live")
	}
	if _, ok := s.current(r); !ok {
		t.Error("the session isn't live where it was made")
	}
}

// Signing out ends the session here, then Authentik's, with the ID token
// only in a cookie for that one hop, and comes back to the Listen page.
func TestSignOut(t *testing.T) {
	w := newWorld(t)
	w.signIn(t, "/")
	if resp := w.do(t, "POST", signOutPath, ""); resp.StatusCode != http.StatusForbidden {
		t.Errorf("sign-out from no site = %d, want 403", resp.StatusCode)
	}
	resp := w.do(t, "POST", signOutPath, "", "Origin", w.app.URL)
	var next Next
	_ = json.NewDecoder(resp.Body).Decode(&next)
	if next.Redirect != endSessionPath || cookieNamed(resp, sessionCookie) == nil || cookieNamed(resp, sessionCookie).MaxAge >= 0 {
		t.Fatalf("sign-out: %+v", next)
	}
	if st := w.state(t); st.Account != nil {
		t.Fatalf("still signed in: %+v", st)
	}
	resp = w.do(t, "GET", endSessionPath, "")
	to, _ := url.Parse(resp.Header.Get("Location"))
	if !strings.HasPrefix(to.String(), w.p.URL+"/end-session?") || to.Query().Get("id_token_hint") == "" ||
		to.Query().Get("post_logout_redirect_uri") != w.app.URL+"/?signed_out=1" {
		t.Fatalf("end-session went to %s", to)
	}
	// The hop is once only.
	if resp := w.do(t, "GET", endSessionPath, ""); resp.Header.Get("Location") != signedOut {
		t.Errorf("second hop went to %q", resp.Header.Get("Location"))
	}
}

// With sign-in off, the account says so, and Sign in comes back to the
// Listen page saying it isn't available.
func TestSignInOff(t *testing.T) {
	svc, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	for _, r := range svc.Routes() {
		mux.Handle(r.Pattern, r.Handler)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", AccountPath, nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"signin":false`) {
		t.Errorf("GET account = %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", signInPath, nil))
	if rec.Header().Get("Location") != signInUnavailable {
		t.Errorf("sign-in went to %q", rec.Header().Get("Location"))
	}
}

func TestLocalPath(t *testing.T) {
	for _, p := range []string{"/", "/?set=1001,1002p", "/?fireground=41", "/recent?from=2026-01-01#x"} {
		if got := localPath(p); got != p {
			t.Errorf("localPath(%q) = %q, want it kept", p, got)
		}
	}
	for _, p := range []string{"", "x", "//evil.example", "/\\evil.example", "/%2F%2Fevil.example", "/\t/evil.example",
		"https://evil.example/", "/auth/signin", "/auth/callback?code=x", "/%61uth/signin", "javascript:alert(1)"} {
		if got := localPath(p); got != "/" {
			t.Errorf("localPath(%q) = %q, want /", p, got)
		}
	}
}

// One cookie's sealed value can't stand in for another's, and a changed
// value doesn't open.
func TestSealedCookies(t *testing.T) {
	s, err := newSealer([]byte(clientSecret))
	if err != nil {
		t.Fatal(err)
	}
	v, _ := s.seal(signOutCookie, "x")
	var out string
	if s.open(sessionCookie, v, &out) == nil {
		t.Error("a sign-out cookie opened as a session")
	}
	b := []byte(v)
	b[len(b)-2] ^= 1
	if s.open(signOutCookie, string(b), &out) == nil {
		t.Error("a changed cookie opened")
	}
	if _, err := newSealer([]byte("short")); err == nil {
		t.Error("a short secret was taken")
	}
}
