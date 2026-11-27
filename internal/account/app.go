package account

import (
	"errors"
	"net/http"
	"strings"

	"github.com/RIKIKU/RadioArchive/internal/oidcclient"
	"github.com/RIKIKU/RadioArchive/internal/store"
)

// The Android app (ADR 0052) signs in with its own public client, through
// the system browser with PKCE, and sends its access token as
// Authorization: Bearer. On the account's routes (GET /api/v1/account, PUT
// and DELETE /api/v1/account/profile) the token stands in for the browser's
// session cookie: the same account, keyed by the browser client's issuer
// and the token's subject, as the module's is (ADR 0028). Public routes
// still never see it (ADR 0009, P3).

// errBadBearer is an app token that isn't a live access token of the app's
// client. The routes answer 401 with WWW-Authenticate, so the app renews
// its token once and tries again (RFC 6750).
var errBadBearer = errors.New("account: not the app's access token")

// appSession is the account the app's token signs in as. An account seen
// for the first time is made, as a browser's first sign-in makes it, but
// never made the first admin: the app has no admin work, and the first
// admin is the browser's to become (ADR 0025). A disabled account isn't
// signed in.
func (s *Service) appSession(r *http.Request, raw string) (session, bool, error) {
	id, err := s.cfg.App.Identity(r.Context(), raw)
	if errors.Is(err, oidcclient.ErrBadToken) {
		s.cfg.Log.Info("account: an app token was refused", "reason", strings.TrimPrefix(err.Error(), oidcclient.ErrBadToken.Error()+": "))
		return session{}, false, errBadBearer
	}
	if err != nil {
		return session{}, false, err
	}
	issuer := s.cfg.OIDC.Issuer
	acc, err := s.cfg.Accounts.Account(r.Context(), issuer, id.Subject)
	if err != nil {
		return session{}, false, err
	}
	if acc == nil {
		made, _, err := s.cfg.Accounts.SignInAccount(r.Context(), store.SignIn{
			Issuer: issuer, Subject: id.Subject, Name: id.Name, Email: id.Email,
			EmailVerified: id.EmailVerified, At: s.cfg.Now(),
		})
		if err != nil {
			return session{}, false, err
		}
		acc = &made
	}
	if acc.Status == store.StatusDisabled {
		return session{}, false, nil
	}
	return session{Issuer: issuer, Subject: acc.Subject, Name: acc.Name, Email: acc.Email, Admin: acc.Admin(), Bearer: true}, true, nil
}

// refuseBearer answers a refused app token: 401, and the header that tells
// the app to renew its token.
func refuseBearer(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
	writeJSON(w, http.StatusUnauthorized, apiError{Code: "invalid_token", Message: "That sign-in isn't valid now. Sign in again."})
}

// AppSettings is what the app needs from the radio's URL alone (ADR 0052):
// the issuer of its own client, the client's ID, and the scopes it asks
// for, as ModuleSettings is for the module.
type AppSettings struct {
	Issuer   string `json:"issuer"`
	ClientID string `json:"client_id"`
	Scope    string `json:"scope"`
}

// AppScope is what the app asks for: offline_access gives it a refresh
// token, so it stays signed in.
const AppScope = "openid email profile offline_access"

// appPath tells the app where to sign in. Like the module's, it's among the
// sign-in hops and answers anyone.
const appPath = AuthPath + "/app"

func (s *Service) appSettings(w http.ResponseWriter, r *http.Request) {
	if !s.On() || s.cfg.App == nil {
		writeJSON(w, http.StatusNotFound, apiError{Code: "app_sign_in_off", Message: "This radio doesn't offer sign-in for the app."})
		return
	}
	writeJSON(w, http.StatusOK, AppSettings{Issuer: s.cfg.App.Issuer, ClientID: s.cfg.App.ClientID, Scope: AppScope})
}

// hasBearerOnly is a request with the app's token and no session cookie: no
// credential a browser sends by itself, so it can't be a cross-site forgery.
func hasBearerOnly(r *http.Request) bool {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		return false
	}
	_, ok := bearer(r)
	return ok
}
