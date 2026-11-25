// Command fakeauthentik stands in for Authentik in the browser tests
// (scripts/ci/e2e-web.sh): an OIDC provider for the radio's client that
// signs everyone in at once as one made-up person, the fixture's admin,
// whose address is proven (ADR 0025). The radio's API signs in against it
// as it would against Authentik, and testing/webfixture uses that sign-in
// to change the fixture through the admin API, as an admin would. It is
// for tests only; the sign-in end-to-end check (scripts/ci/e2e-signin.sh)
// runs the real Authentik.
//
// A browser whose cookie fakeauthentik_person (oidctest.PersonCookie) is
// "listener", on this provider's /authorize, signs in as a second made-up
// person instead, a listener, for the admin pages' tests of who may open
// them (ADR 0027). A cookie "person-<word>" signs in a person of the
// test's own: subject person-<word>, email person-<word>@example.test,
// name "Made Up <word>", for the Accounts page's tests, each with
// accounts nobody else's test changes (ADR 0033).
//
// With -api-token it also answers Authentik's users API
// (/api/v3/core/users/{id}/) for that bearer token, as the radio's
// accounts service account reaches it: a switched-off or deleted user's
// sign-in is then refused. POST /fake/users-down?down=1 makes that API
// answer 503, as an Authentik that can't be reached; down=0 brings it
// back.
//
//	fakeauthentik -addr 127.0.0.1:18091 -secret <OIDC client secret> -email admin@example.test [-api-token <token>]
package main

import (
	"flag"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/oidcclient/oidctest"
)

// person is a test's own person's cookie value.
var person = regexp.MustCompile(`^person-([a-z0-9-]{1,40})$`)

func main() {
	addr := flag.String("addr", "127.0.0.1:18091", "address to listen on; its URL is AUTHENTIK_URL")
	secret := flag.String("secret", "", "the radio's OIDC client secret (OIDC_CLIENT_SECRET)")
	email := flag.String("email", "admin@example.test", "the made-up person's email, proven")
	subject := flag.String("subject", "fixture-admin", "the made-up person's user ID")
	listener := flag.String("listener-email", "listener@example.test", "the made-up listener's email, proven")
	token := flag.String("api-token", "", "answer Authentik's users API for this bearer token (AUTHENTIK_API_TOKEN)")
	flag.Parse()
	if *secret == "" {
		log.Fatal("fakeauthentik: -secret is required")
	}
	p, h, err := oidctest.Standalone("http://"+*addr, *secret, *subject, *email, "Fixture Admin", log.Printf)
	if err != nil {
		log.Fatal(err)
	}
	p.AddPerson("listener", oidctest.Person{Subject: "fixture-listener", Email: *listener, Name: "Fixture Listener"})
	p.PersonFor = func(key string) (oidctest.Person, bool) {
		m := person.FindStringSubmatch(key)
		if m == nil {
			return oidctest.Person{}, false
		}
		return oidctest.Person{Subject: key, Email: key + "@example.test", Name: "Made Up " + strings.ReplaceAll(m[1], "-", " ")}, true
	}
	if *token != "" {
		p.ServeUsers(*token)
	}
	mux := http.NewServeMux()
	mux.Handle("/", h)
	mux.HandleFunc("POST /fake/users-down", func(w http.ResponseWriter, r *http.Request) {
		p.SetUsersDown(r.URL.Query().Get("down") == "1")
		w.WriteHeader(http.StatusNoContent)
	})
	srv := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
