package account

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
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
	"time"

	"github.com/RIKIKU/RadioArchive/internal/gate"
	"github.com/RIKIKU/RadioArchive/internal/oidcclient"
	"github.com/RIKIKU/RadioArchive/internal/oidcclient/oidctest"
	"github.com/RIKIKU/RadioArchive/internal/store"
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

// accounts is a made-up store: accounts, in memory, by issuer and subject.
// The first proven INITIAL_ADMIN_EMAIL becomes admin, as the database's
// does; its locking is the store's tests'.
type accounts struct {
	mu   sync.Mutex
	rows map[[2]string]store.Account
}

func (d *accounts) SignInAccount(_ context.Context, in store.SignIn) (store.Account, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	k := [2]string{in.Issuer, in.Subject}
	a, ok := d.rows[k]
	if !ok {
		a = store.Account{Issuer: in.Issuer, Subject: in.Subject, Role: store.RoleListener, Status: store.StatusActive, FirstSignedIn: in.At}
	}
	a.Name, a.Email = in.Name, in.Email
	promote := false
	if in.EmailVerified && in.InitialAdmin != "" && strings.EqualFold(in.Email, in.InitialAdmin) && a.Status == store.StatusActive {
		promote = true
		for _, r := range d.rows {
			if r.Admin() {
				promote = false
			}
		}
	}
	if promote {
		a.Role = store.RoleAdmin
	}
	if a.Status == store.StatusActive {
		a.LastSignedIn = in.At
	}
	d.rows[k] = a
	return a, promote, nil
}

func (d *accounts) Accounts(_ context.Context, iss string) ([]store.Account, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []store.Account
	for _, a := range d.rows {
		if a.Issuer == iss {
			out = append(out, a)
		}
	}
	return out, nil
}

func (d *accounts) SetAccountRole(_ context.Context, iss, by, sub, role string) (store.Account, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	a, ok := d.rows[[2]string{iss, sub}]
	if !ok {
		return store.Account{}, store.ErrNoAccount
	}
	a.Role = role
	d.rows[[2]string{iss, sub}] = a
	return a, nil
}

// set changes an account's role and status, as an admin (or issue #105's
// disabling) would.
func (d *accounts) set(iss, sub, role, status string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	a := d.rows[[2]string{iss, sub}]
	a.Role, a.Status = role, status
	d.rows[[2]string{iss, sub}] = a
}

func (d *accounts) get(iss, sub string) (store.Account, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	a, ok := d.rows[[2]string{iss, sub}]
	return a, ok
}

// sessions is a made-up store: sessions, in memory, by their ID's hash,
// living by the times the service passes, as the database's do, read with
// their account's role and status.
type sessions struct {
	mu     sync.Mutex
	rows   map[string]sessionRow
	refuse error    // every call fails with this
	tokens []string // every ID token kept, which no log may hold
	acc    *accounts
}

type sessionRow struct {
	se      store.ListenerSession
	idToken string
}

func (d *sessions) StartListenerSession(_ context.Context, h []byte, se store.ListenerSession, tok string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.refuse != nil {
		return d.refuse
	}
	if len(h) != 32 {
		return errors.New("not a SHA-256")
	}
	d.rows[string(h)] = sessionRow{se: se, idToken: tok}
	if tok != "" {
		d.tokens = append(d.tokens, tok)
	}
	return nil
}

func (d *sessions) ListenerSession(_ context.Context, h []byte, now time.Time) (*store.ListenerSession, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.refuse != nil {
		return nil, d.refuse
	}
	r, ok := d.rows[string(h)]
	if !ok || !r.se.Expires.After(now) {
		return nil, nil
	}
	se := r.se
	if d.acc != nil {
		a, ok := d.acc.get(se.Issuer, se.Subject)
		if !ok {
			return nil, nil
		}
		se.Role, se.Status = a.Role, a.Status
	}
	return &se, nil
}

func (d *sessions) RenewListenerSession(_ context.Context, h []byte, now, expires time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.refuse != nil {
		return d.refuse
	}
	if r, ok := d.rows[string(h)]; ok {
		r.se.LastUsed, r.se.Expires = now, expires
		d.rows[string(h)] = r
	}
	return nil
}

func (d *sessions) EndListenerSession(_ context.Context, h []byte) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.refuse != nil {
		return "", d.refuse
	}
	r := d.rows[string(h)]
	delete(d.rows, string(h))
	return r.idToken, nil
}

func (d *sessions) EndAccountListenerSessions(_ context.Context, iss, sub string) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.refuse != nil {
		return 0, d.refuse
	}
	var n int64
	for h, r := range d.rows {
		if r.se.Issuer == iss && r.se.Subject == sub {
			delete(d.rows, h)
			n++
		}
	}
	return n, nil
}

func (d *sessions) EndExpiredListenerSessions(_ context.Context, now time.Time) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.refuse != nil {
		return 0, d.refuse
	}
	var n int64
	for h, r := range d.rows {
		if !r.se.Expires.After(now) {
			delete(d.rows, h)
			n++
		}
	}
	return n, nil
}

// only is the one session kept, failing unless there's exactly one.
func (d *sessions) only(t *testing.T) (hash string, row sessionRow) {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.rows) != 1 {
		t.Fatalf("%d sessions kept, want 1", len(d.rows))
	}
	for h, r := range d.rows {
		return h, r
	}
	return "", sessionRow{}
}

func (d *sessions) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.rows)
}

// clock is the service's clock, moved by the test.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
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
	ss   *sessions
	acc  *accounts
	clk  *clock
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
	acc := &accounts{rows: map[[2]string]store.Account{}}
	w := &world{p: oidctest.New(t, clientSecret, subject, email, "Made Up"), db: &profiles{rows: map[[2]string][]byte{}},
		ss: &sessions{rows: map[string]sessionRow{}, acc: acc}, acc: acc, clk: &clock{t: time.Now()}, logs: &bytes.Buffer{}}
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
		Sessions: w.ss, Profiles: w.db, Accounts: w.acc, Catalogue: catalogue{}, Secret: []byte(clientSecret), InitialAdmin: "Admin@Example.test",
		Log: slog.New(slog.NewJSONHandler(w.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Now: w.clk.now,
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
		w.ss.mu.Lock()
		secrets := append([]string{clientIP, forwardedIP, email, clientSecret, "Made Up"}, w.ss.tokens...)
		w.ss.mu.Unlock()
		for _, s := range secrets {
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
// and ends in a host-only session cookie, back where the listener was. The
// cookie holds only a random session ID; the session's row is kept under
// its SHA-256, with the account and the ID token (ADR 0022).
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
	if c.MaxAge != int(sessionFor.Seconds()) {
		t.Errorf("session cookie Max-Age %d, want 30 days", c.MaxAge)
	}
	id, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil || len(id) != 32 {
		t.Fatalf("session cookie %q: want 256 random bits, base64url", c.Value)
	}
	h, row := w.ss.only(t)
	if sum := sha256.Sum256(id); h != string(sum[:]) || bytes.Contains([]byte(h), id) {
		t.Error("the session isn't kept under its ID's SHA-256 alone")
	}
	if row.se.Issuer != w.p.Issuer() || row.se.Subject != subject || row.se.Email != email || row.se.Name != "Made Up" ||
		row.idToken == "" || !row.se.Expires.Equal(w.clk.now().Add(sessionFor)) {
		t.Errorf("the session's row: %+v", row)
	}
	if strings.Contains(c.Value, row.idToken) {
		t.Error("the cookie holds the ID token")
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
	if _, ok, err := s2.current(httptest.NewRecorder(), r); ok || err != nil {
		t.Errorf("a session from another issuer is live (%v)", err)
	}
	if _, ok, err := s.current(httptest.NewRecorder(), r); !ok || err != nil {
		t.Errorf("the session isn't live where it was made (%v)", err)
	}
}

// Signing out deletes the session's row at once, then ends Authentik's
// session, with the ID token held in memory under a one-time handle (never
// in a cookie or the answer) for that one hop, and comes back to the
// Listen page. The old cookie, replayed, is no session.
func TestSignOut(t *testing.T) {
	w := newWorld(t)
	w.signIn(t, "/")
	_, row := w.ss.only(t)
	old := w.c.Jar.Cookies(mustURL(t, w.app.URL))
	if resp := w.do(t, "POST", signOutPath, ""); resp.StatusCode != http.StatusForbidden || w.ss.count() != 1 {
		t.Errorf("sign-out from no site = %d, want 403 and the session kept", resp.StatusCode)
	}
	resp := w.do(t, "POST", signOutPath, "", "Origin", w.app.URL)
	body, _ := io.ReadAll(resp.Body)
	var next Next
	_ = json.Unmarshal(body, &next)
	if next.Redirect != endSessionPath || cookieNamed(resp, sessionCookie) == nil || cookieNamed(resp, sessionCookie).MaxAge >= 0 {
		t.Fatalf("sign-out: %+v", next)
	}
	if w.ss.count() != 0 {
		t.Fatal("the session's row outlived signing out")
	}
	so := cookieNamed(resp, signOutCookie)
	if so == nil || so.Path != endSessionPath || !so.HttpOnly || strings.Contains(so.Value, row.idToken) || bytes.Contains(body, []byte(row.idToken)) {
		t.Fatalf("sign-out handle %+v, answer %s: the ID token must stay on the server", so, body)
	}
	if st := w.state(t); st.Account != nil {
		t.Fatalf("still signed in: %+v", st)
	}
	r := httptest.NewRequest("GET", AccountPath, nil)
	for _, c := range old {
		r.AddCookie(c)
	}
	if _, ok, _ := w.svc.current(httptest.NewRecorder(), r); ok {
		t.Error("the signed-out cookie, replayed, is a session")
	}
	resp = w.do(t, "GET", endSessionPath, "")
	to, _ := url.Parse(resp.Header.Get("Location"))
	if !strings.HasPrefix(to.String(), w.p.URL+"/end-session?") || to.Query().Get("id_token_hint") != row.idToken ||
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
	v, _ := s.seal(signInCookie, "x")
	var out string
	if s.open("another use", v, &out) == nil {
		t.Error("a sign-in cookie opened as another")
	}
	b := []byte(v)
	b[len(b)-2] ^= 1
	if s.open(signInCookie, string(b), &out) == nil {
		t.Error("a changed cookie opened")
	}
	if _, err := newSealer([]byte("short")); err == nil {
		t.Error("a short secret was taken")
	}
}

// Ending an account's sessions (an admin disabling a listener, M4) signs
// every browser signed in as it out at its very next request, and leaves
// other accounts' sessions alone (ADR 0022).
func TestEndAccountSessions(t *testing.T) {
	w := newWorld(t)
	w.signIn(t, "/")
	// A second browser of the same listener, and someone else's session.
	jar, _ := cookiejar.New(nil)
	first := w.c
	w.c = &http.Client{Jar: jar, CheckRedirect: first.CheckRedirect}
	w.signIn(t, "/")
	w.c = first
	if err := w.ss.StartListenerSession(context.Background(), bytes.Repeat([]byte{7}, 32),
		store.ListenerSession{Issuer: w.p.Issuer(), Subject: "43", LastUsed: w.clk.now(), Expires: w.clk.now().Add(time.Hour)}, ""); err != nil {
		t.Fatal(err)
	}
	if st := w.state(t); st.Account == nil {
		t.Fatal("not signed in")
	}
	n, err := w.svc.EndAccountSessions(context.Background(), subject)
	if err != nil || n != 2 {
		t.Fatalf("EndAccountSessions = %d, %v; want 2", n, err)
	}
	if w.ss.count() != 1 {
		t.Errorf("%d sessions left, want only the other account's", w.ss.count())
	}
	resp := w.do(t, "GET", AccountPath, "")
	var st State
	_ = json.NewDecoder(resp.Body).Decode(&st)
	if st.Account != nil || cookieNamed(resp, sessionCookie) == nil || cookieNamed(resp, sessionCookie).MaxAge >= 0 {
		t.Errorf("after the sessions ended: %+v, cookie %+v", st, cookieNamed(resp, sessionCookie))
	}
	if resp := w.put(t, `{"v":1,"talkgroups":[]}`); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("save after the sessions ended = %d, want 401", resp.StatusCode)
	}
	if n, err := w.svc.EndAccountSessions(context.Background(), ""); n != 0 || err != nil {
		t.Errorf("no subject ended %d, %v", n, err)
	}
}

// A session lasts 30 days from its last use, renewed at most hourly, row
// and cookie; one unused for 30 days has ended, and the hourly sweep
// deletes it (ADR 0022).
func TestSessionLife(t *testing.T) {
	w := newWorld(t)
	w.signIn(t, "/")
	start := w.clk.now()
	w.clk.add(30 * time.Minute)
	resp := w.do(t, "GET", AccountPath, "")
	if _, row := w.ss.only(t); !row.se.LastUsed.Equal(start) || cookieNamed(resp, sessionCookie) != nil {
		t.Errorf("renewed within the hour: used %v, cookie %+v", row.se.LastUsed, cookieNamed(resp, sessionCookie))
	}
	w.clk.add(31 * time.Minute)
	resp = w.do(t, "GET", AccountPath, "")
	_, row := w.ss.only(t)
	if c := cookieNamed(resp, sessionCookie); !row.se.Expires.Equal(w.clk.now().Add(sessionFor)) || c == nil || c.MaxAge != int(sessionFor.Seconds()) {
		t.Errorf("not renewed after an hour: expires %v, cookie %+v", row.se.Expires, c)
	}
	// Signed in until 30 days after that use, and not a moment longer.
	w.clk.add(sessionFor - time.Second)
	if st := w.state(t); st.Account == nil {
		t.Fatal("signed out before 30 days")
	}
	w.clk.add(sessionFor)
	if st := w.state(t); st.Account != nil {
		t.Fatal("still signed in 30 days after the last use")
	}
	// The sweep runs as Run starts.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.svc.Run(ctx)
	if w.ss.count() != 0 {
		t.Error("the expired session wasn't deleted")
	}
}

// The sessions' database down: the account says it's unavailable rather
// than signed out, and a sign-in can't finish.
func TestSessionStoreDown(t *testing.T) {
	w := newWorld(t)
	w.signIn(t, "/")
	w.ss.refuse = errors.New("read the listener's session: SQLSTATE 57P01")
	if resp := w.do(t, "GET", AccountPath, ""); resp.StatusCode != http.StatusServiceUnavailable || cookieNamed(resp, sessionCookie) != nil {
		t.Errorf("database down: GET = %d", resp.StatusCode)
	}
	if resp := w.put(t, `{"v":1,"talkgroups":[]}`); resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("database down: PUT = %d", resp.StatusCode)
	}
	if resp := w.signIn(t, "/"); resp.Header.Get("Location") != signInFailed {
		t.Errorf("database down: sign-in went to %q", resp.Header.Get("Location"))
	}
	w.ss.refuse = nil
	if st := w.state(t); st.Account == nil {
		t.Errorf("back up: %+v", st)
	}
}

// A cookie that isn't a session ID (one sealed before ADR 0022, or junk)
// is never looked up, and is cleared.
func TestNotASessionID(t *testing.T) {
	w := newWorld(t)
	for _, v := range []string{"junk", strings.Repeat("A", 43), base64.RawURLEncoding.EncodeToString(make([]byte, 300))} {
		r := httptest.NewRequest("GET", AccountPath, nil)
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: v})
		if _, ok, err := w.svc.current(httptest.NewRecorder(), r); ok || err != nil {
			t.Errorf("%q is a session (%v)", v, err)
		}
	}
	if w.ss.count() != 0 {
		t.Error("a session was kept")
	}
}
