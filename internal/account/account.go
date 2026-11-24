// Package account is listeners' sign-in and the profile it saves (SPEC.md
// P3, P4, R41, R48, R103, R110, R111; ADR 0009, ADR 0021).
//
// Signing in only saves preferences. Nothing here is asked by the public
// API, the live feed or the gate, and the server hands public routes no
// cookie at all (internal/server), so being signed in can't change what a
// listener hears. Nothing here unlocks the admin API either: the session is
// not an admin authenticator, and the binary serves no admin API (ADR 0003).
//
// The server stores nothing about a listener who hasn't signed in (P4). A
// signed-in listener's session is a row of RadioArchive's database, found
// by the hash of the random ID their host-only cookie holds, so it can be
// ended at once (ADR 0022). Their saved settings are one row too, keyed by
// the OIDC issuer and subject they sign in with (ADR 0021); Authentik keeps
// only who they are. Nothing is logged about a request: no address, no
// email, no token.
package account

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/gate"
	"github.com/RIKIKU/RadioArchive/internal/oidcclient"
	"github.com/RIKIKU/RadioArchive/internal/store"
)

// Paths. AuthPath is the browser's top-level sign-in and sign-out hops;
// AccountPath is the account's API, which the browser app calls.
const (
	AuthPath    = "/auth"
	AccountPath = "/api/v1/account"
	ProfilePath = AccountPath + "/profile"

	signInPath     = AuthPath + "/signin"
	callbackPath   = AuthPath + "/callback"
	signOutPath    = AuthPath + "/signout"
	endSessionPath = AuthPath + "/end-session"
)

// Cookies. All host-only (no Domain), HttpOnly and SameSite=Lax, and Secure
// when APP_URL is https. The session cookie holds only a random session ID
// (ADR 0022), the sign-in cookie a sealed sign-in under way (state, nonce,
// PKCE verifier, return path), and the sign-out cookie a one-time handle
// to the ID token held in memory for the end-session hop.
const (
	sessionCookie = "ra_session"
	signInCookie  = "ra_signin"
	signOutCookie = "ra_signout"
)

// How long things last. A session lasts 30 days from its last use, renewed
// at most once an hour, so an unused one ends; expired ones are deleted
// hourly (ADR 0022).
const (
	sessionFor = 30 * 24 * time.Hour
	renewAfter = time.Hour
	sweepEvery = time.Hour
	signInFor  = 10 * time.Minute
	signOutFor = 2 * time.Minute
)

// Sessions is where signed-in listeners' sessions live: RadioArchive's
// database (store.Store, ADR 0022), one row a signed-in browser, found by
// the SHA-256 of its session ID.
type Sessions interface {
	// StartListenerSession keeps a new session, with the ID token
	// Authentik issued, for signing out.
	StartListenerSession(ctx context.Context, idHash []byte, se store.ListenerSession, idToken string) error
	// ListenerSession is the session live at now, nil for none.
	ListenerSession(ctx context.Context, idHash []byte, now time.Time) (*store.ListenerSession, error)
	// RenewListenerSession marks it used at now, live until expires.
	RenewListenerSession(ctx context.Context, idHash []byte, now, expires time.Time) error
	// EndListenerSession deletes it, returning its ID token ("" for none).
	EndListenerSession(ctx context.Context, idHash []byte) (string, error)
	// EndAccountListenerSessions deletes every session of the account.
	EndAccountListenerSessions(ctx context.Context, issuer, subject string) (int64, error)
	// EndExpiredListenerSessions deletes those that ended by now.
	EndExpiredListenerSessions(ctx context.Context, now time.Time) (int64, error)
}

// Profiles is where profiles live: RadioArchive's database (store.Store,
// ADR 0021), one an account, keyed by the issuer and subject it signs in
// with.
type Profiles interface {
	// ListenerProfile returns the account's saved profile, nil for none.
	ListenerProfile(ctx context.Context, issuer, subject string) ([]byte, error)
	// SaveListenerProfile saves it, in place of the one the account had.
	SaveListenerProfile(ctx context.Context, issuer, subject string, profile []byte) error
	// ClearListenerProfile deletes it, if the account has one.
	ClearListenerProfile(ctx context.Context, issuer, subject string) error
}

// Catalogue is the public catalogue (gate.Gate).
type Catalogue interface {
	Catalogue(ctx context.Context) (*gate.Catalogue, error)
}

// Config is the account's settings. With OIDC nil, sign-in is off: the
// account menu still works signed out, and Sign in says it isn't available.
type Config struct {
	AppURL    *url.URL
	OIDC      *oidcclient.Client
	Sessions  Sessions
	Profiles  Profiles
	Catalogue Catalogue
	// Secret is what the sign-in cookie's key is derived from: the OIDC
	// client secret.
	Secret []byte
	Log    *slog.Logger
	Now    func() time.Time
}

// Service is listeners' sign-in and their profile.
type Service struct {
	cfg  Config
	seal *sealer
	// signOuts holds the ID tokens of browsers signing out, for the hop
	// to Authentik.
	signOuts signOuts
}

// New returns the account. Sign-in on needs all of Config.
func New(cfg Config) (*Service, error) {
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	s := &Service{cfg: cfg}
	if cfg.OIDC == nil {
		return s, nil
	}
	if cfg.AppURL == nil || cfg.OIDC.Issuer == "" || cfg.Sessions == nil || cfg.Profiles == nil || cfg.Catalogue == nil {
		return nil, errors.New("account: sign-in needs the app's URL, the issuer, the sessions, the profiles and the catalogue")
	}
	var err error
	if s.seal, err = newSealer(cfg.Secret); err != nil {
		return nil, err
	}
	return s, nil
}

// On says whether listeners can sign in.
func (s *Service) On() bool { return s.cfg.OIDC != nil }

// Route is one of the account's routes.
type Route struct {
	Pattern string
	Handler http.Handler
}

// Routes are the account's routes, for the server to mount: the sign-in
// hops under AuthPath, and the account's API under AccountPath.
func (s *Service) Routes() []Route {
	return []Route{
		{"GET " + signInPath, http.HandlerFunc(s.signIn)},
		{"GET " + callbackPath, http.HandlerFunc(s.callback)},
		{"POST " + signOutPath, http.HandlerFunc(s.signOut)},
		{"GET " + endSessionPath, http.HandlerFunc(s.endSession)},
		{"GET " + AccountPath, http.HandlerFunc(s.account)},
		{"PUT " + ProfilePath, http.HandlerFunc(s.saveProfile)},
		{"DELETE " + ProfilePath, http.HandlerFunc(s.clearProfile)},
	}
}

// session is the signed-in listener, as their session's row holds them.
type session struct {
	// Issuer and Subject are the account, as its profile is keyed (ADR
	// 0021). A session from another issuer than this deployment's (its
	// AUTHENTIK_URL changed) isn't live: its subject would name someone
	// else's account there.
	Issuer, Subject string
	Name, Email     string
}

type attempt struct {
	oidcclient.Attempt
	Return string `json:"r"`
}

func (s *Service) secure() bool { return s.cfg.AppURL != nil && s.cfg.AppURL.Scheme == "https" }

func (s *Service) setCookie(w http.ResponseWriter, name, value, path string, maxAge time.Duration) {
	age := int(maxAge.Seconds())
	if maxAge < 0 {
		age = -1 // Max-Age=0: delete it
	}
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: path, MaxAge: age,
		HttpOnly: true, Secure: s.secure(), SameSite: http.SameSiteLaxMode,
	})
}

func (s *Service) clear(w http.ResponseWriter, name, path string) { s.setCookie(w, name, "", path, -1) }

// sessionIDLen is a session ID's length in bytes: 256 random bits. The
// cookie holds it base64url-encoded; the database holds only its SHA-256.
const sessionIDLen = 32

// idHash is the hash a session's row is kept under, from its cookie's
// value, and false for a value that isn't a session ID at all (an old
// sealed cookie, or junk), which is then never looked up.
func idHash(cookie string) ([]byte, bool) {
	b, err := base64.RawURLEncoding.DecodeString(cookie)
	if err != nil || len(b) != sessionIDLen {
		return nil, false
	}
	h := sha256.Sum256(b)
	return h[:], true
}

// sessionHash is the request's session cookie's hash, if it has one.
func sessionHash(r *http.Request) ([]byte, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil, false
	}
	return idHash(c.Value)
}

// current is the request's session, if it has a live one: one indexed
// read of the session's row, so a session ended in the database is ended
// for the very next request. A session last used more than renewAfter ago
// is renewed, row and cookie, for another sessionFor. err is the database
// failing, which isn't the same as signed out.
func (s *Service) current(w http.ResponseWriter, r *http.Request) (session, bool, error) {
	if !s.On() {
		return session{}, false, nil
	}
	h, ok := sessionHash(r)
	if !ok {
		return session{}, false, nil
	}
	now := s.cfg.Now()
	row, err := s.cfg.Sessions.ListenerSession(r.Context(), h, now)
	if err != nil {
		return session{}, false, err
	}
	if row == nil || row.Subject == "" || row.Issuer != s.cfg.OIDC.Issuer {
		return session{}, false, nil
	}
	if now.Sub(row.LastUsed) > renewAfter {
		if err := s.cfg.Sessions.RenewListenerSession(r.Context(), h, now, now.Add(sessionFor)); err != nil {
			s.cfg.Log.Warn("account: couldn't renew a session", "error", err.Error())
		} else if c, err := r.Cookie(sessionCookie); err == nil {
			s.setCookie(w, sessionCookie, c.Value, "/", sessionFor)
		}
	}
	return session{Issuer: row.Issuer, Subject: row.Subject, Name: row.Name, Email: row.Email}, true, nil
}

// startSession keeps a new session for the listener, with the ID token
// for signing out, and hands the browser its ID.
func (s *Service) startSession(ctx context.Context, w http.ResponseWriter, se session, idToken string) error {
	id := make([]byte, sessionIDLen)
	if _, err := rand.Read(id); err != nil {
		return err
	}
	v := base64.RawURLEncoding.EncodeToString(id)
	h, _ := idHash(v)
	now := s.cfg.Now()
	if err := s.cfg.Sessions.StartListenerSession(ctx, h, store.ListenerSession{Issuer: se.Issuer, Subject: se.Subject,
		Name: se.Name, Email: se.Email, LastUsed: now, Expires: now.Add(sessionFor)}, idToken); err != nil {
		return err
	}
	s.setCookie(w, sessionCookie, v, "/", sessionFor)
	return nil
}

// EndAccountSessions ends every session of the account with this subject
// at this deployment's issuer: each browser signed in as it is signed out
// at its next request. It's the first half of an admin disabling a
// listener (M4); disabling them in Authentik is the second. The live feed
// is anonymous (P3, R111) and never sees a session, so no live connection
// belongs to one, and none needs closing. It returns how many sessions
// there were.
func (s *Service) EndAccountSessions(ctx context.Context, subject string) (int64, error) {
	if !s.On() || subject == "" {
		return 0, nil
	}
	return s.cfg.Sessions.EndAccountListenerSessions(ctx, s.cfg.OIDC.Issuer, subject)
}

// Run deletes expired sessions now and every hour until ctx ends. With
// sign-in off there are none to delete.
func (s *Service) Run(ctx context.Context) {
	if !s.On() {
		return
	}
	t := time.NewTicker(sweepEvery)
	defer t.Stop()
	for {
		if _, err := s.cfg.Sessions.EndExpiredListenerSessions(ctx, s.cfg.Now()); err != nil && ctx.Err() == nil {
			s.cfg.Log.Warn("account: couldn't delete expired sessions", "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// signOuts holds the ID tokens of browsers that have just signed out, each
// under a one-time handle, until the end-session hop takes it, at most
// signOutFor. In memory, as the token is never in a cookie: an API restart
// between the two hops leaves that browser signed out here, with its
// Authentik session to end by hand.
type signOuts struct {
	mu sync.Mutex
	m  map[string]heldToken
}

type heldToken struct {
	idToken string
	until   time.Time
}

func (h *signOuts) put(handle, idToken string, now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.m == nil {
		h.m = map[string]heldToken{}
	}
	for k, v := range h.m {
		if !now.Before(v.until) {
			delete(h.m, k)
		}
	}
	h.m[handle] = heldToken{idToken: idToken, until: now.Add(signOutFor)}
}

// take is the token held under handle, once, and false for none or one
// that expired.
func (h *signOuts) take(handle string, now time.Time) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v, ok := h.m[handle]
	delete(h.m, handle)
	if !ok || !now.Before(v.until) {
		return "", false
	}
	return v.idToken, true
}

// randomHandle is a fresh one-time handle: 256 random bits, base64url.
func randomHandle() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// sameOrigin refuses a state-changing request from another site. The
// session cookie is SameSite=Lax already; this is the second lock.
func (s *Service) sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return false
	}
	o := r.Header.Get("Origin")
	if o == "" {
		return r.Header.Get("Sec-Fetch-Site") == "same-origin"
	}
	return s.cfg.AppURL != nil && strings.EqualFold(o, s.cfg.AppURL.Scheme+"://"+s.cfg.AppURL.Host)
}

// apiError is a refusal's body (R78): a stable code and a message.
type apiError struct {
	Code    string `json:"error"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		code, b = http.StatusInternalServerError, []byte(`{"error":"internal","message":"Something went wrong."}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_, _ = w.Write(append(b, '\n'))
}

var (
	errSignInNeeded = apiError{Code: "sign_in_required", Message: "Sign in to save to your account."}
	errOtherSite    = apiError{Code: "other_site", Message: "That request didn't come from this site."}
	errUnavailable  = apiError{Code: "account_unavailable", Message: "Couldn't reach your account just now. Listening works as before; try again in a minute."}
)

// ---------------------------------------------------------------- sign-in

// Where a sign-in or sign-out ends up when it can't go on: the Listen page,
// which says so. Listening is never blocked (P3).
const (
	signInUnavailable = "/?signin=unavailable"
	signInFailed      = "/?signin=failed"
	signedOut         = "/?signed_out=1"
)

func (s *Service) signIn(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.On() {
		http.Redirect(w, r, signInUnavailable, http.StatusSeeOther)
		return
	}
	a := attempt{Attempt: oidcclient.NewAttempt(), Return: localPath(r.URL.Query().Get("return"))}
	to, err := s.cfg.OIDC.AuthURL(a.Attempt)
	if err != nil {
		s.cfg.Log.Warn("sign-in: Authentik couldn't be reached", "error", err.Error())
		http.Redirect(w, r, signInUnavailable, http.StatusSeeOther)
		return
	}
	v, err := s.seal.seal(signInCookie, a)
	if err != nil {
		http.Redirect(w, r, signInFailed, http.StatusSeeOther)
		return
	}
	s.setCookie(w, signInCookie, v, AuthPath+"/", signInFor)
	http.Redirect(w, r, to, http.StatusFound)
}

func (s *Service) callback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.On() {
		http.Redirect(w, r, signInUnavailable, http.StatusSeeOther)
		return
	}
	var a attempt
	c, err := r.Cookie(signInCookie)
	q := r.URL.Query()
	if err != nil || s.seal.open(signInCookie, c.Value, &a) != nil || a.State == "" || q.Get("state") != a.State {
		http.Redirect(w, r, signInFailed, http.StatusSeeOther)
		return
	}
	s.clear(w, signInCookie, AuthPath+"/")
	if q.Get("error") != "" {
		// The query is the browser's to write, so none of it is logged.
		s.cfg.Log.Info("sign-in refused by Authentik")
		http.Redirect(w, r, signInFailed, http.StatusSeeOther)
		return
	}
	id, err := s.cfg.OIDC.Finish(r.Context(), a.Attempt, q.Get("code"))
	if err != nil {
		s.cfg.Log.Warn("sign-in failed", "error", err.Error())
		http.Redirect(w, r, signInFailed, http.StatusSeeOther)
		return
	}
	if err := s.startSession(r.Context(), w, session{Issuer: s.cfg.OIDC.Issuer, Subject: id.Subject, Name: id.Name, Email: id.Email}, id.IDToken); err != nil {
		s.cfg.Log.Warn("sign-in: couldn't keep the session", "error", err.Error())
		http.Redirect(w, r, signInFailed, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, localPath(a.Return), http.StatusSeeOther)
}

// Next is where the browser app goes next, after signing out.
type Next struct {
	Redirect string `json:"redirect"`
}

// signOut ends the radio's session here, deleting its row at once, and
// sends the browser on to end its Authentik session too. The ID token
// Authentik wants with that comes out of the deleted row and is held in
// memory under a one-time handle, which a short-lived cookie only the next
// hop reads carries: the token itself is never in a cookie, and this
// answer, which the app's script reads, never holds it.
func (s *Service) signOut(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, errOtherSite)
		return
	}
	var idToken string
	if h, ok := sessionHash(r); ok && s.On() {
		var err error
		if idToken, err = s.cfg.Sessions.EndListenerSession(r.Context(), h); err != nil {
			// The cookie goes anyway; the row expires unused.
			s.cfg.Log.Warn("sign-out: couldn't end the session", "error", err.Error())
		}
	}
	s.clear(w, sessionCookie, "/")
	next := Next{Redirect: signedOut}
	if idToken != "" {
		if handle, err := randomHandle(); err == nil {
			s.signOuts.put(handle, idToken, s.cfg.Now())
			s.setCookie(w, signOutCookie, handle, endSessionPath, signOutFor)
			next.Redirect = endSessionPath
		}
	}
	writeJSON(w, http.StatusOK, next)
}

func (s *Service) endSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var (
		idToken string
		ok      bool
	)
	if c, err := r.Cookie(signOutCookie); err == nil {
		idToken, ok = s.signOuts.take(c.Value, s.cfg.Now())
		s.clear(w, signOutCookie, endSessionPath)
	}
	if !ok || !s.On() || idToken == "" {
		http.Redirect(w, r, signedOut, http.StatusSeeOther)
		return
	}
	to, err := s.cfg.OIDC.EndSessionURL(idToken, s.cfg.AppURL.JoinPath("/").String()+"?signed_out=1")
	if err != nil {
		s.cfg.Log.Warn("sign-out: Authentik couldn't be reached, so its session wasn't ended", "error", err.Error())
		http.Redirect(w, r, signedOut+"&authentik=unreachable", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// ---------------------------------------------------------------- account

// Who is a signed-in listener, as the account menu shows them.
type Who struct {
	// ID is Authentik's user ID: the browser remembers which account its
	// set was last matched with (ADR 0009).
	ID    string `json:"id"`
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// State is GET /api/v1/account.
type State struct {
	// SignIn says whether listeners can sign in here.
	SignIn  bool     `json:"signin"`
	Account *Who     `json:"account"`
	Profile *Profile `json:"profile"`
	// Saves says whether the account can hold settings. Since the profile
	// moved to RadioArchive's database (ADR 0021) every signed-in account
	// can, so it is true whenever Account is set; the browser app still
	// reads it.
	Saves bool `json:"saves"`
}

func (s *Service) account(w http.ResponseWriter, r *http.Request) {
	st := State{SignIn: s.On()}
	se, ok, err := s.current(w, r)
	if err != nil {
		s.cfg.Log.Warn("account: couldn't read the session", "error", err.Error())
		writeJSON(w, http.StatusServiceUnavailable, errUnavailable)
		return
	}
	if !ok {
		if _, err := r.Cookie(sessionCookie); err == nil {
			s.clear(w, sessionCookie, "/")
		}
		writeJSON(w, http.StatusOK, st)
		return
	}
	who := &Who{ID: se.Subject, Name: se.Name, Email: se.Email}
	p, err := s.profile(r.Context(), se)
	if err != nil {
		s.cfg.Log.Warn("account: couldn't read the profile", "error", err.Error())
		writeJSON(w, http.StatusServiceUnavailable, errUnavailable)
		return
	}
	if p != nil {
		cat, err := s.cfg.Catalogue.Catalogue(r.Context())
		if err != nil {
			s.cfg.Log.Warn("account: couldn't read the catalogue", "error", err.Error())
			writeJSON(w, http.StatusServiceUnavailable, errUnavailable)
			return
		}
		v := shown(*p, publicIn(cat))
		p = &v
	}
	st.Account, st.Profile, st.Saves = who, p, true
	writeJSON(w, http.StatusOK, st)
}

// profile is what the account holds, nil for nothing.
func (s *Service) profile(ctx context.Context, se session) (*Profile, error) {
	raw, err := s.cfg.Profiles.ListenerProfile(ctx, se.Issuer, se.Subject)
	if err != nil || raw == nil {
		return nil, err
	}
	p, err := parseProfile(raw)
	if err != nil {
		// Not one this version wrote: shown as nothing, replaced on save.
		s.cfg.Log.Warn("account: the saved profile isn't one, so it's left out", "error", err.Error())
		return nil, nil
	}
	return &p, nil
}

// signedInChange admits a change to the account: same site, signed in, and
// sign-in on.
func (s *Service) signedInChange(w http.ResponseWriter, r *http.Request) (session, bool) {
	if !s.sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, errOtherSite)
		return session{}, false
	}
	se, ok, err := s.current(w, r)
	if err != nil {
		s.cfg.Log.Warn("account: couldn't read the session", "error", err.Error())
		writeJSON(w, http.StatusServiceUnavailable, errUnavailable)
		return session{}, false
	}
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errSignInNeeded)
		return session{}, false
	}
	return se, true
}

func (s *Service) saveProfile(w http.ResponseWriter, r *http.Request) {
	se, ok := s.signedInChange(w, r)
	if !ok {
		return
	}
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		writeJSON(w, http.StatusUnsupportedMediaType, apiError{Code: "json_required", Message: "Send the profile as JSON."})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, apiError{Code: "profile_too_big", Message: "That's more than an account holds."})
		return
	}
	next, err := parseProfile(body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Code: "not_a_profile", Message: err.Error()})
		return
	}
	before, err := s.profile(r.Context(), se)
	if err != nil {
		s.cfg.Log.Warn("account: couldn't read the profile", "error", err.Error())
		writeJSON(w, http.StatusServiceUnavailable, errUnavailable)
		return
	}
	cat, err := s.cfg.Catalogue.Catalogue(r.Context())
	if err != nil {
		s.cfg.Log.Warn("account: couldn't read the catalogue", "error", err.Error())
		writeJSON(w, http.StatusServiceUnavailable, errUnavailable)
		return
	}
	save := kept(next, before, publicIn(cat))
	raw, err := json.Marshal(save)
	if err == nil {
		err = s.cfg.Profiles.SaveListenerProfile(r.Context(), se.Issuer, se.Subject, raw)
	}
	if err != nil {
		s.cfg.Log.Warn("account: couldn't save the profile", "error", err.Error())
		writeJSON(w, http.StatusServiceUnavailable, errUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// clearProfile empties the account (R110): its row is deleted, everything
// it held with it, hidden talkgroups' entries too.
func (s *Service) clearProfile(w http.ResponseWriter, r *http.Request) {
	se, ok := s.signedInChange(w, r)
	if !ok {
		return
	}
	if err := s.cfg.Profiles.ClearListenerProfile(r.Context(), se.Issuer, se.Subject); err != nil {
		s.cfg.Log.Warn("account: couldn't clear the profile", "error", err.Error())
		writeJSON(w, http.StatusServiceUnavailable, errUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// ------------------------------------------------------------- return path

// localPath keeps the return path after sign-in on this site, as
// PagerArchive's does: a plain path, optionally with a query and fragment,
// and "/" for anything else. Browsers drop tabs and newlines in a URL and
// read a backslash as a slash, so a value holding a control character,
// whitespace, a backslash or a non-ASCII byte is refused whole; its path,
// decoded once, is checked the same way, mustn't start with "//", and
// mustn't lead back to the sign-in hops under /auth.
func localPath(p string) string {
	if !plainPath(p) {
		return "/"
	}
	u, err := url.Parse(p)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Opaque != "" || u.User != nil {
		return "/"
	}
	raw, _, _ := strings.Cut(p, "?")
	raw, _, _ = strings.Cut(raw, "#")
	dec, err := url.PathUnescape(raw)
	if err != nil || !plainPath(dec) {
		return "/"
	}
	if c := path.Clean(dec); c == AuthPath || strings.HasPrefix(c, AuthPath+"/") {
		return "/"
	}
	return p
}

func plainPath(p string) bool {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return false
	}
	for i := 0; i < len(p); i++ {
		if b := p[i]; b <= ' ' || b >= 0x7f || b == '\\' {
			return false
		}
	}
	return true
}
