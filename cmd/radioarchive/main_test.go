package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/RIKIKU/RadioArchive/internal/server"
	"github.com/RIKIKU/RadioArchive/internal/store"
	"github.com/RIKIKU/RadioArchive/internal/store/storetest"
)

// TestIntegrationNoAdminAPI: the binary serves no admin API until admin
// sign-in exists, so the tests' stub sign-in can't be reached in production.
func TestIntegrationNoAdminAPI(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st, err := store.Open(ctx, storetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rs, err := server.NewRecorders(st, slog.New(slog.DiscardHandler), server.RecordersConfig{RecordingsDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(apiHandler(st, slog.New(slog.DiscardHandler), rs))
	defer srv.Close()
	for _, p := range []string{"/systems", "/attachments", "/rejections"} {
		resp, err := http.Get(srv.URL + server.AdminPath + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", server.AdminPath+p, resp.StatusCode)
		}
	}
}
