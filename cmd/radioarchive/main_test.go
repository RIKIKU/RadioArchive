package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/RIKIKU/RadioArchive/internal/gate"
	"github.com/RIKIKU/RadioArchive/internal/public"
	"github.com/RIKIKU/RadioArchive/internal/server"
	"github.com/RIKIKU/RadioArchive/internal/store"
	"github.com/RIKIKU/RadioArchive/internal/store/storetest"
	"github.com/RIKIKU/RadioArchive/internal/version"
	"github.com/RIKIKU/RadioArchive/web"
)

// TestRetentionSettings: RECORDINGS_MIN_FREE, RECORDINGS_EARLY_DELETE_FLOOR
// and NIGHTLY_AT are checked at start.
func TestRetentionSettings(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	for _, c := range []struct{ minFree, floor, at, want string }{
		{"0", "", "", "RECORDINGS_MIN_FREE"},
		{"100%", "", "", "RECORDINGS_MIN_FREE"},
		{"5%", "24", "", "RECORDINGS_EARLY_DELETE_FLOOR"},
		{"5%", "0s", "", "RECORDINGS_EARLY_DELETE_FLOOR"},
		{"5%", "-1h", "", "RECORDINGS_EARLY_DELETE_FLOOR"},
		{"5%", "24h", "25:00", "NIGHTLY_AT"},
	} {
		t.Setenv("RECORDINGS_MIN_FREE", c.minFree)
		t.Setenv("RECORDINGS_EARLY_DELETE_FLOOR", c.floor)
		t.Setenv("NIGHTLY_AT", c.at)
		if _, err := retentionJob(nil, log); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("RECORDINGS_MIN_FREE=%q RECORDINGS_EARLY_DELETE_FLOOR=%q NIGHTLY_AT=%q: %v, want an error naming %s",
				c.minFree, c.floor, c.at, err, c.want)
		}
	}
}

// TestIntegrationNoAdminAPI:the binary serves no admin API until admin
// sign-in exists, so the tests' stub sign-in can't be reached in production.
// Its admin recording playback isn't served either.
func TestIntegrationNoAdminAPI(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st, err := store.Open(ctx, storetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	dir := t.TempDir()
	rs, err := server.NewRecorders(st, slog.New(slog.DiscardHandler), server.RecordersConfig{RecordingsDir: dir, ServerVersion: version.Version})
	if err != nil {
		t.Fatal(err)
	}
	api := apiHandler(st, slog.New(slog.DiscardHandler), rs, gate.New(st, dir, slog.New(slog.DiscardHandler)))
	srv := httptest.NewServer(api)
	defer srv.Close()
	// Nor the retention setting; and nothing in the binary triggers a
	// retention run but its schedule (the tests call the job directly).
	for _, p := range []string{"/systems", "/attachments", "/rejections", "/retention",
		"/calls/00000000-0000-4000-8000-000000000000/recording"} {
		resp, err := http.Get(srv.URL + server.AdminPath + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", server.AdminPath+p, resp.StatusCode)
		}
	}
	for _, r := range api.Routes() {
		if r.Kind == server.AdminRoute {
			t.Errorf("the binary serves admin route %s", r.Pattern)
		}
	}

	// It serves the whole public API, which is what the A10 harness
	// exercises (internal/server/a10_test.go), and no other public route.
	var got, want []string
	for _, r := range api.Routes() {
		if r.Kind == server.PublicRoute {
			got = append(got, r.Pattern)
		}
	}
	for _, r := range public.New(nil, nil).Routes() {
		want = append(want, r.Pattern)
	}
	slices.Sort(got)
	slices.Sort(want)
	if len(want) == 0 || !slices.Equal(got, want) {
		t.Errorf("the binary's public routes are %q, want the public API's %q", got, want)
	}

	// And the browser app's routes, which the harness probes too.
	got, want = nil, nil
	for _, r := range api.Routes() {
		if r.Kind == server.AppRoute {
			got = append(got, r.Pattern)
		}
	}
	for _, r := range web.New().Routes() {
		want = append(want, r.Pattern)
	}
	slices.Sort(got)
	slices.Sort(want)
	if len(want) == 0 || !slices.Equal(got, want) {
		t.Errorf("the binary's app routes are %q, want the app's %q", got, want)
	}
}
