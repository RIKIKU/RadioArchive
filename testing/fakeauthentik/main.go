// Command fakeauthentik stands in for Authentik in the browser tests
// (scripts/ci/e2e-web.sh): an OIDC provider for the radio's client that
// signs everyone in at once as one made-up person, the fixture's admin,
// whose address is proven (ADR 0025). The radio's API signs in against it
// as it would against Authentik, and testing/webfixture uses that sign-in
// to change the fixture through the admin API, as an admin would. It is
// for tests only; the sign-in end-to-end check (scripts/ci/e2e-signin.sh)
// runs the real Authentik.
//
//	fakeauthentik -addr 127.0.0.1:18091 -secret <OIDC client secret> -email admin@example.test
package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/oidcclient/oidctest"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18091", "address to listen on; its URL is AUTHENTIK_URL")
	secret := flag.String("secret", "", "the radio's OIDC client secret (OIDC_CLIENT_SECRET)")
	email := flag.String("email", "admin@example.test", "the made-up person's email, proven")
	subject := flag.String("subject", "fixture-admin", "the made-up person's user ID")
	flag.Parse()
	if *secret == "" {
		log.Fatal("fakeauthentik: -secret is required")
	}
	_, h, err := oidctest.Standalone("http://"+*addr, *secret, *subject, *email, "Fixture Admin", log.Printf)
	if err != nil {
		log.Fatal(err)
	}
	srv := &http.Server{Addr: *addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
