package server

import (
	"net/http"
	"testing"
)

// The route table refuses a route outside its kind's place: a data route
// that isn't under the API (and so from internal/public), or an app route
// that would answer for the API (ADR 0004).
func TestRouteTableRefusesMisplacedRoutes(t *testing.T) {
	nothing := http.NotFoundHandler()
	for _, c := range []struct {
		kind    RouteKind
		pattern string
		ok      bool
	}{
		{PublicRoute, "GET /api/v1/calls/{call}", true},
		{PublicRoute, "GET /calls/{call}", false},
		{PublicRoute, "GET /", false},
		{PublicRoute, "GET /healthz", false},
		{PublicRoute, "GET " + AdminPath + "/systems", false},
		{PublicRoute, "GET " + RecorderPath + "/live", false},
		{AppRoute, "GET /", true},
		{AppRoute, "GET /robots.txt", true},
		{AppRoute, "GET /api/v1/calls", false},
		{AppRoute, "GET /api", false},
		{AppRoute, "GET /api/", false},
		{AppRoute, "GET " + AdminPath + "/systems", false},
		{AppRoute, "GET /healthz", false},
		{HealthRoute, "GET /", false},
		{AdminRoute, "GET /api/v1/calls", false},
		// The listener's account (ADR 0009): its own places only, and
		// nothing else may sit in them.
		{AccountRoute, "GET /auth/signin", true},
		{AccountRoute, "GET /api/v1/account", true},
		{AccountRoute, "PUT /api/v1/account/profile", true},
		{AccountRoute, "GET /api/v1/catalogue", false},
		{AccountRoute, "GET " + AdminPath + "/systems", false},
		{AccountRoute, "GET /", false},
		{PublicRoute, "GET /api/v1/account", false},
		{PublicRoute, "GET /api/v1/account/profile", false},
		{AppRoute, "GET /auth/signin", false},
		{AppRoute, "GET /auth", false},
		{AdminRoute, "GET /api/v1/account", false},
	} {
		func() {
			defer func() {
				if got := recover() == nil; got != c.ok {
					t.Errorf("%s route %q mounted: %v, want %v", c.kind, c.pattern, got, c.ok)
				}
			}()
			a := &API{mux: http.NewServeMux(), app: http.NewServeMux()}
			a.handle(c.kind, c.pattern, nothing)
		}()
	}
}
