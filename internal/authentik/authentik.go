// Package authentik is how the API reaches Authentik, which it uses only
// for sign-in (ADR 0009). The API holds no Authentik API token and never
// calls Authentik's API: a signed-in listener's saved settings live in
// RadioArchive's own database (ADR 0021).
package authentik

import (
	"net/http"
	"net/url"
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
