// Package authentik is how the API reaches Authentik: for sign-in (ADR
// 0009), and, as the radio's own service account, to switch a user on or
// off and to delete one when an admin disables, enables or deletes their
// account (ADR 0033). A signed-in listener's saved settings and every
// account's role live in RadioArchive's own database (ADR 0021, ADR 0025),
// so nothing the token can change makes anyone a radio admin.
//
// The token is sent only to Authentik, in the Authorization header, and is
// never logged or put in an error. Errors name the operation and the HTTP
// status, never a body, a URL or a user's name or address.
package authentik

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

// Transport sends requests for the public URL to internal instead, keeping
// the public Host and scheme in the headers, so Authentik builds the same
// URLs (the OIDC issuer, links) as it does for a browser. With internal nil
// it changes nothing. The API's container uses it to reach Authentik on the
// compose network without going through the reverse proxy, as
// PagerArchive's does.
func Transport(public, internal *url.URL, base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if internal == nil {
		return base
	}
	return roundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != public.Host {
			return base.RoundTrip(r)
		}
		r = r.Clone(r.Context())
		r.Host = public.Host
		r.URL.Scheme, r.URL.Host = internal.Scheme, internal.Host
		r.Header.Set("X-Forwarded-Proto", public.Scheme)
		r.Header.Set("X-Forwarded-Host", public.Host)
		return base.RoundTrip(r)
	})
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Users is Authentik's user API (/api/v3/core/users/), as the radio's
// accounts service account (deploy/authentik/blueprints/radioarchive.yaml,
// radioarchive-accounts): view, change and delete users, and nothing
// else. A user is named by the OIDC subject, which is Authentik's user ID
// (sub_mode: user_id, ADR 0021).
type Users struct {
	// URL is Authentik's public URL, as browsers reach it; HTTP's
	// transport (Transport) may send it elsewhere.
	URL *url.URL
	// Token is the service account's API token (AUTHENTIK_API_TOKEN).
	Token string
	HTTP  *http.Client
}

// ErrProtected: the user is an Authentik superuser or a service account.
// The radio never switches one off or deletes one: that's for Authentik's
// own administrators, in Authentik.
var ErrProtected = errors.New("authentik: a superuser or service account")

// Error is a call Authentik answered with an unexpected status, or didn't
// answer. It names the operation and the status only.
type Error struct {
	Op     string
	Status int // 0: no answer
	Err    error
}

func (e *Error) Error() string {
	if e.Status == 0 {
		return fmt.Sprintf("authentik: %s: no answer (%v)", e.Op, e.Err)
	}
	return fmt.Sprintf("authentik: %s: HTTP %d", e.Op, e.Status)
}

func (e *Error) Unwrap() error { return e.Err }

// userID is what a user's ID may be: Authentik's are numbers; the tests'
// made-up provider's are words. Nothing that could leave the user's path.
var userID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,255}$`)

// user is what the radio reads of a user before changing them.
type user struct {
	IsActive    bool   `json:"is_active"`
	IsSuperuser bool   `json:"is_superuser"`
	Type        string `json:"type"`
}

// protected says the radio leaves the user alone: a superuser, or any
// account that isn't a person's (Authentik's service accounts, its
// internal ones, the radio's own).
func (u user) protected() bool {
	return u.IsSuperuser || (u.Type != "internal" && u.Type != "external")
}

// SetActive switches the user on or off. Authentik ends a switched-off
// user's sessions itself, so they can't sign in again anywhere it serves.
// A user already so is left as they are, and one Authentik no longer has
// is off already. A superuser or service account is refused
// (ErrProtected) before anything is changed.
func (c *Users) SetActive(ctx context.Context, subject string, active bool) error {
	u, found, err := c.get(ctx, subject)
	if err != nil {
		return err
	}
	if !found {
		// A user deleted in Authentik can't sign in: as good as off.
		if !active {
			return nil
		}
		return &Error{Op: "find the user", Status: http.StatusNotFound}
	}
	if u.protected() {
		return ErrProtected
	}
	if u.IsActive == active {
		return nil
	}
	_, err = c.do(ctx, "switch the user", http.MethodPatch, subject, map[string]bool{"is_active": active}, nil, http.StatusOK)
	return err
}

// Delete deletes the user. One that's gone already is deleted. A
// superuser or service account is refused (ErrProtected) before anything
// is changed.
func (c *Users) Delete(ctx context.Context, subject string) error {
	u, found, err := c.get(ctx, subject)
	if err != nil || !found {
		return err
	}
	if u.protected() {
		return ErrProtected
	}
	_, err = c.do(ctx, "delete the user", http.MethodDelete, subject, nil, nil, http.StatusNoContent, http.StatusNotFound)
	return err
}

func (c *Users) get(ctx context.Context, subject string) (user, bool, error) {
	var u user
	status, err := c.do(ctx, "find the user", http.MethodGet, subject, nil, &u, http.StatusOK, http.StatusNotFound)
	if err != nil {
		return user{}, false, err
	}
	return u, status == http.StatusOK, nil
}

// do calls the user's endpoint and decodes a success into out.
func (c *Users) do(ctx context.Context, op, method, subject string, body, out any, ok ...int) (int, error) {
	if !userID.MatchString(subject) {
		return 0, &Error{Op: op, Err: errors.New("not a user ID")}
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	u := c.URL.JoinPath("api", "v3", "core", "users", subject, "/")
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return 0, &Error{Op: op, Err: errors.New("bad request")}
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		// A *url.Error quotes the URL; its cause doesn't.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return 0, &Error{Op: op, Err: err}
	}
	defer resp.Body.Close()
	for _, s := range ok {
		if resp.StatusCode != s {
			continue
		}
		if out != nil && s >= 200 && s < 300 && s != http.StatusNoContent {
			if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
				return s, &Error{Op: op, Status: s, Err: errors.New("an answer that isn't a user")}
			}
		}
		return s, nil
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, &Error{Op: op, Status: resp.StatusCode}
}
