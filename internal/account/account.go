// Package account is listeners' sign-in and the profile it saves (SPEC.md
// P3, P4, R41, R48, R103, R110, R111; ADR 0009).
//
// Signing in only saves preferences. Nothing here is asked by the public
// API, the live feed or the gate, and the server hands public routes no
// cookie at all (internal/server), so being signed in can't change what a
// listener hears. Nothing here unlocks the admin API either: the session is
// not an admin authenticator, and the binary serves no admin API (ADR 0003).
//
// The server stores nothing about a listener (P4, R110): the session is a
// sealed cookie, host-only, and the saved settings are an attribute of the
// listener's Authentik user, written with the API's own token. Nothing is
// logged about a request: no address, no email, no token.
package account

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/authentik"
	"github.com/RIKIKU/RadioArchive/internal/gate"
	"github.com/RIKIKU/RadioArchive/internal/oidcclient"
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
// when APP_URL is https.
const (
	sessionCookie = "ra_session"
	signInCookie  = "ra_signin"
	signOutCookie = "ra_signout"
)

// How long things last. A session lasts 30 days from its last use, renewed
// at most once a day, so an unused one ends.
const (
	sessionFor   = 30 * 24 * time.Hour
	renewAfter   = 24 * time.Hour
	signInFor    = 10 * time.Minute
	signOutFor   = 2 * time.Minute
	maxCookieLen = 3800 // a browser keeps at least 4,096 bytes a cookie
)

// Directory is where profiles live: Authentik's users (authentik.Client).
type Directory interface {
	// Attribute returns one of a user's attributes, nil if unset, and
	// whether the user is active; authentik.ErrNoUser for no such user.
	Attribute(ctx context.Context, subject, key string) (json.RawMessage, bool, error)
	// SetAttribute sets one of a user's attributes, or removes it (nil).
	SetAttribute(ctx context.Context, subject, key string, value json.RawMessage) error
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
	Directory Directory
	Catalogue Catalogue
	// Secret is what the cookies' key is derived from: the OIDC client
	// secret.
	Secret []byte
	Log    *slog.Logger
	Now    func() time.Time
}

// Service is listeners' sign-in and their profile.
type Service struct {
	cfg  Config
	seal *sealer
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
	if cfg.AppURL == nil || cfg.Directory == nil || cfg.Catalogue == nil {
		return nil, errors.New("account: sign-in needs the app's URL, the directory and the catalogue")
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

// session is the signed-in listener, as their cookie holds it.
type session struct {
	Subject string `json:"s"`
	Name    string `json:"n,omitempty"`
	Email   string `json:"e,omitempty"`
	// IDToken is kept only for signing out (id_token_hint), and left out
	// when it would make the cookie too big.
	IDToken string `json:"t,omitempty"`
	Issued  int64  `json:"i"`
	Expires int64  `json:"x"`
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

// current is the request's session, if it has a live one.
func (s *Service) current(r *http.Request) (session, bool) {
	if s.seal == nil {
		return session{}, false
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return session{}, false
	}
	var se session
	if s.seal.open(sessionCookie, c.Value, &se) != nil || se.Subject == "" || s.cfg.Now().Unix() >= se.Expires {
		return session{}, false
	}
	return se, true
}

func (s *Service) startSession(w http.ResponseWriter, se session) error {
	now := s.cfg.Now()
	se.Issued, se.Expires = now.Unix(), now.Add(sessionFor).Unix()
	v, err := s.seal.seal(sessionCookie, se)
	if err == nil && len(v) > maxCookieLen && se.IDToken != "" {
		se.IDToken = ""
		v, err = s.seal.seal(sessionCookie, se)
	}
	if err != nil {
		return err
	}
	s.setCookie(w, sessionCookie, v, "/", sessionFor)
	return nil
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
	if err := s.startSession(w, session{Subject: id.Subject, Name: id.Name, Email: id.Email, IDToken: id.IDToken}); err != nil {
		http.Redirect(w, r, signInFailed, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, localPath(a.Return), http.StatusSeeOther)
}

// Next is where the browser app goes next, after signing out.
type Next struct {
	Redirect string `json:"redirect"`
}

// signOut ends the radio's session here and sends the browser on to end
// its Authentik session too. The ID token Authentik wants with that is
// moved into a short-lived cookie only the next hop reads, so this answer,
// which the app's script reads, never holds it.
func (s *Service) signOut(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, errOtherSite)
		return
	}
	se, ok := s.current(r)
	s.clear(w, sessionCookie, "/")
	next := Next{Redirect: signedOut}
	if ok && s.On() && se.IDToken != "" {
		if v, err := s.seal.seal(signOutCookie, se.IDToken); err == nil {
			s.setCookie(w, signOutCookie, v, endSessionPath, signOutFor)
			next.Redirect = endSessionPath
		}
	}
	writeJSON(w, http.StatusOK, next)
}

func (s *Service) endSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var idToken string
	c, err := r.Cookie(signOutCookie)
	if err != nil || !s.On() || s.seal.open(signOutCookie, c.Value, &idToken) != nil || idToken == "" {
		http.Redirect(w, r, signedOut, http.StatusSeeOther)
		return
	}
	s.clear(w, signOutCookie, endSessionPath)
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
	// Saves says whether the account can hold settings: false for one the
	// API's token may not reach (an administrator's, ADR 0009).
	Saves bool `json:"saves"`
}

func (s *Service) account(w http.ResponseWriter, r *http.Request) {
	st := State{SignIn: s.On()}
	se, ok := s.current(r)
	if !ok {
		if _, err := r.Cookie(sessionCookie); err == nil {
			s.clear(w, sessionCookie, "/")
		}
		writeJSON(w, http.StatusOK, st)
		return
	}
	who := &Who{ID: se.Subject, Name: se.Name, Email: se.Email}
	p, active, err := s.profile(r.Context(), se.Subject)
	switch {
	case unreachable(err):
		// An account the API's token may not reach (an administrator's),
		// which Authentik answers as if it weren't there: signed in, with
		// nothing saved.
		st.Account = who
		writeJSON(w, http.StatusOK, st)
		return
	case err == nil && !active:
		// Switched off in Authentik since: signed out.
		s.clear(w, sessionCookie, "/")
		writeJSON(w, http.StatusOK, st)
		return
	case err != nil:
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
	if s.cfg.Now().Unix()-se.Issued > int64(renewAfter.Seconds()) {
		_ = s.startSession(w, se)
	}
	st.Account, st.Profile, st.Saves = who, p, true
	writeJSON(w, http.StatusOK, st)
}

// unreachable is a user the API's token can't read or change.
func unreachable(err error) bool {
	return errors.Is(err, authentik.ErrNoUser) || errors.Is(err, authentik.ErrNotPermitted)
}

var errNotSaved = apiError{Code: "account_not_saved", Message: "This account's settings aren't saved here."}

// profile is what the account holds, nil for nothing.
func (s *Service) profile(ctx context.Context, subject string) (*Profile, bool, error) {
	raw, active, err := s.cfg.Directory.Attribute(ctx, subject, Attribute)
	if err != nil || raw == nil {
		return nil, active, err
	}
	p, err := parseProfile(raw)
	if err != nil {
		// Not one this version wrote: shown as nothing, replaced on save.
		s.cfg.Log.Warn("account: the saved profile isn't one, so it's left out", "error", err.Error())
		return nil, active, nil
	}
	return &p, active, nil
}

// signedInChange admits a change to the account: same site, signed in, and
// sign-in on.
func (s *Service) signedInChange(w http.ResponseWriter, r *http.Request) (session, bool) {
	if !s.sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, errOtherSite)
		return session{}, false
	}
	se, ok := s.current(r)
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
	before, active, err := s.profile(r.Context(), se.Subject)
	if unreachable(err) {
		writeJSON(w, http.StatusForbidden, errNotSaved)
		return
	}
	if err == nil && !active {
		s.clear(w, sessionCookie, "/")
		writeJSON(w, http.StatusUnauthorized, errSignInNeeded)
		return
	}
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
		err = s.cfg.Directory.SetAttribute(r.Context(), se.Subject, Attribute, raw)
	}
	if err != nil {
		s.cfg.Log.Warn("account: couldn't save the profile", "error", err.Error())
		writeJSON(w, http.StatusServiceUnavailable, errUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// clearProfile empties the account (R110): everything it holds, hidden
// talkgroups' entries too.
func (s *Service) clearProfile(w http.ResponseWriter, r *http.Request) {
	se, ok := s.signedInChange(w, r)
	if !ok {
		return
	}
	err := s.cfg.Directory.SetAttribute(r.Context(), se.Subject, Attribute, nil)
	if unreachable(err) {
		writeJSON(w, http.StatusForbidden, errNotSaved)
		return
	}
	if err != nil {
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
