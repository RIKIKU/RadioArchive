package server_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/RIKIKU/RadioArchive/internal/server"
	"github.com/RIKIKU/RadioArchive/internal/store"
	"github.com/RIKIKU/RadioArchive/internal/store/storetest"
)

type pinger struct{ err error }

func (p pinger) Ping(context.Context) (int32, error) { return 1, p.err }

func get(t *testing.T, h http.Handler, path string) (int, string) {
	t.Helper()
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestHealthzDatabaseDown(t *testing.T) {
	h := server.New(pinger{err: errors.New("down")}, slog.New(slog.DiscardHandler))
	if code, _ := get(t, h, "/healthz"); code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", code)
	}
}

// TestIntegrationHealthz runs the API's handler against the real database,
// migrated by store.Open as at start-up.
func TestIntegrationHealthz(t *testing.T) {
	st, err := store.Open(context.Background(), storetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	code, body := get(t, server.New(st, slog.New(slog.DiscardHandler)), "/healthz")
	if code != http.StatusOK || body != "ok\n" {
		t.Errorf("GET /healthz = %d %q, want 200 \"ok\\n\"", code, body)
	}
}
