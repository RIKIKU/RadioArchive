package server_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/RIKIKU/RadioArchive/internal/account"
	"github.com/RIKIKU/RadioArchive/internal/server"
)

// The account's sessions are what M4's admin pages will end.
var _ server.ListenerSessions = (*account.Service)(nil)

// endings stands in for the account's sessions: it counts what it's asked
// to end.
type endings struct {
	asked []string
	fail  bool
}

func (e *endings) EndAccountSessions(_ context.Context, subject string) (int64, error) {
	if e.fail {
		return 0, errors.New("end the account's sessions: SQLSTATE 57P01")
	}
	e.asked = append(e.asked, subject)
	return 2, nil
}

// An admin ends a listener's sessions (ADR 0022): only an admin, only when
// the API has the account's sessions to end, and the answer says how many.
func TestAdminEndsListenerSessions(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	ls := &endings{}
	admin, err := server.NewAdmin(nil, log, testAdmin{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(server.New(nil, log, server.WithAdmin(admin.WithListenerSessions(ls))))
	defer srv.Close()
	end := func(as string) (int, string) {
		req, _ := http.NewRequest("DELETE", srv.URL+server.AdminPath+"/listeners/42/sessions", nil)
		if as != "" {
			req.Header.Set("X-Test-Admin", as)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(bytes.TrimSpace(b))
	}
	if code, _ := end(""); code != http.StatusUnauthorized || len(ls.asked) != 0 {
		t.Errorf("not an admin: %d, asked %q", code, ls.asked)
	}
	if code, body := end("test admin"); code != http.StatusOK || body != `{"ended":2}` || len(ls.asked) != 1 || ls.asked[0] != "42" {
		t.Errorf("an admin: %d %s, asked %q", code, body, ls.asked)
	}
	ls.fail = true
	if code, _ := end("test admin"); code != http.StatusInternalServerError {
		t.Errorf("database down: %d", code)
	}

	// Without the account's sessions, the route isn't there.
	admin2, _ := server.NewAdmin(nil, log, testAdmin{}, t.TempDir())
	for _, r := range server.New(nil, log, server.WithAdmin(admin2)).Routes() {
		if r.Pattern == "DELETE "+server.AdminPath+"/listeners/{subject}/sessions" {
			t.Error("the route is mounted with no sessions to end")
		}
	}
}
