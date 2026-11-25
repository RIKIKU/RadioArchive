package main

import (
	"context"
	"net/http"
	"testing"

	"github.com/RIKIKU/RadioArchive/internal/oidcclient/oidctest"
)

// TestIntegrationA20AdminsNotLimited is A20's last sentence for the
// binary's handler, at the default limits (ADR 0036): once a client has
// made its 120 public API requests in a minute, its 121st is refused,
// signed in as a listener or not (P3), while an admin at the same address,
// signed in with the browser or the module, isn't refused.
func TestIntegrationA20AdminsNotLimited(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := oidctest.New(t, p4ClientSecret, "1", initialAdmin, "Made Up")
	_, logs, srv, _ := binaryServer(t, ctx, p.URL, environ{"INITIAL_ADMIN_EMAIL": initialAdmin})
	owner, _ := signInAs(t, p, srv.URL, "1", initialAdmin, true)
	if !isAdmin(t, owner, srv.URL) {
		t.Fatal("the owner isn't an admin")
	}
	listener, _ := signInAs(t, p, srv.URL, "2", "listener@example.test", true)
	tok := p.ModuleAccessToken(t, "1", nil, false)
	listenerTok := p.ModuleAccessToken(t, "2", nil, false)

	// Every request here comes from the one listener's address
	// (binaryServer).
	anyone := &http.Client{}
	for i := range 120 {
		if code, b := call(t, anyone, srv.URL, "GET", "/api/v1/catalogue", ""); code != 200 {
			t.Fatalf("request %d = %d %s", i+1, code, b)
		}
	}
	for name, c := range map[string]*http.Client{"anyone": anyone, "a signed-in listener": listener} {
		if code, b := call(t, c, srv.URL, "GET", "/api/v1/catalogue", ""); code != 429 {
			t.Errorf("%s's 121st = %d %s", name, code, b)
		}
	}
	if code, b := bearerCall(t, srv.URL, listenerTok, "GET", "/api/v1/catalogue", ""); code != 429 {
		t.Errorf("a listener's module token's 121st = %d %s", code, b)
	}
	for range 3 {
		if code, b := call(t, owner, srv.URL, "GET", "/api/v1/catalogue", ""); code != 200 {
			t.Errorf("the admin in the browser = %d %s", code, b)
		}
		if code, b := bearerCall(t, srv.URL, tok, "GET", "/api/v1/catalogue", ""); code != 200 {
			t.Errorf("the admin with the module = %d %s", code, b)
		}
	}
	// No limit writes an address to the log (R90).
	p4NoAddresses(t, logs)
}
