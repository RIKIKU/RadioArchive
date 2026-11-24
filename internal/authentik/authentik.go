// Package authentik is the API's way to Authentik's API (ADR 0009): it
// reads and writes one attribute of a user, which is where a signed-in
// listener's saved settings live (SPEC.md R110), with the API's standing
// token.
//
// The token is sent only in the Authorization header of requests to
// Authentik. It is never logged, never put in an error, and never sent to a
// browser. Errors name the operation and the HTTP status, never a body or a
// URL, so no address or token can reach a log through them.
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
	"strconv"
	"time"
)

// ErrNoUser is a user Authentik doesn't have.
var ErrNoUser = errors.New("authentik: no such user")

// ErrNotPermitted is a user the API's token may not read or change: one
// who has never signed in to the radio, an administrator, or a member of
// radioarchive-admins (the blueprint's radioarchive-profile-access).
var ErrNotPermitted = errors.New("authentik: the API's token may not reach this user")

// Client is Authentik's API, as the API's account (the blueprint's
// radioarchive-api).
type Client struct {
	URL   *url.URL // Authentik's public URL, as browsers reach it
	Token string
	HTTP  *http.Client // see Transport
}

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

// Error is a call Authentik refused: the operation and status, never the
// body.
type Error struct {
	Op     string
	Status int
}

func (e *Error) Error() string { return fmt.Sprintf("authentik: %s: HTTP %d", e.Op, e.Status) }

// user is what the API reads of a user.
type user struct {
	PK         int64                      `json:"pk"`
	IsActive   bool                       `json:"is_active"`
	Attributes map[string]json.RawMessage `json:"attributes"`
}

// userPath is a user's API path. The subject is the user's ID (the
// provider's sub_mode is user_id), digits only; anything else is no user.
func userPath(subject string) (string, bool) {
	if n, err := strconv.ParseInt(subject, 10, 64); err != nil || n <= 0 || strconv.FormatInt(n, 10) != subject {
		return "", false
	}
	return "/api/v3/core/users/" + subject + "/", true
}

func (c *Client) do(ctx context.Context, op, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, c.URL.JoinPath(path).String(), rd)
	if err != nil {
		return fmt.Errorf("authentik: %s: %w", op, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		// An http.Client error quotes the URL; keep only its cause.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("authentik: %s: %w", op, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotFound:
		return ErrNoUser
	case http.StatusForbidden:
		return ErrNotPermitted
	}
	if resp.StatusCode != http.StatusOK {
		return &Error{Op: op, Status: resp.StatusCode}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out); err != nil {
		return fmt.Errorf("authentik: %s: decode: %w", op, err)
	}
	return nil
}

func (c *Client) user(ctx context.Context, subject string) (user, string, error) {
	path, ok := userPath(subject)
	if !ok {
		return user{}, "", ErrNoUser
	}
	var u user
	if err := c.do(ctx, "read user", http.MethodGet, path, nil, &u); err != nil {
		return user{}, "", err
	}
	return u, path, nil
}

// Attribute returns one of a user's attributes (nil when the user has none
// by that name), and whether the user is active.
func (c *Client) Attribute(ctx context.Context, subject, key string) (json.RawMessage, bool, error) {
	u, _, err := c.user(ctx, subject)
	if err != nil {
		return nil, false, err
	}
	v := u.Attributes[key]
	if string(v) == "null" {
		v = nil
	}
	return v, u.IsActive, nil
}

// SetAttribute sets one of a user's attributes, or with value nil removes
// it, leaving the others as they are. Authentik's API replaces a user's
// attributes whole, so the user is read first; a change another app makes
// to its own attributes in between would be lost, which at this scale (a
// listener saving a setting) is accepted (ADR 0009).
func (c *Client) SetAttribute(ctx context.Context, subject, key string, value json.RawMessage) error {
	u, path, err := c.user(ctx, subject)
	if err != nil {
		return err
	}
	attrs := u.Attributes
	if attrs == nil {
		attrs = map[string]json.RawMessage{}
	}
	if value == nil {
		if _, ok := attrs[key]; !ok {
			return nil
		}
		delete(attrs, key)
	} else {
		attrs[key] = value
	}
	return c.do(ctx, "write user attributes", http.MethodPatch, path, map[string]any{"attributes": attrs}, nil)
}
