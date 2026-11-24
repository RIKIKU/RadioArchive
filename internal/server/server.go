// Package server is the API's HTTP handler.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
)

// Pinger reaches the database: store.Store's sqlc query Ping.
type Pinger interface {
	Ping(ctx context.Context) (int32, error)
}

// Option adds to the API's handler.
type Option func(*http.ServeMux)

// WithRecorders serves the recorder client's endpoints (ADR 0002): the live
// WebSocket and the recording upload.
func WithRecorders(rs *Recorders) Option {
	return func(mux *http.ServeMux) {
		mux.HandleFunc("GET "+cp.LivePath, rs.serveLive)
		mux.HandleFunc("POST "+cp.RecordingsPath, rs.serveUpload)
	}
}

// New returns the API's handler.
func New(db Pinger, log *slog.Logger, opts ...Option) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if _, err := db.Ping(ctx); err != nil {
			log.Warn("health check: database unreachable", "error", err)
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("database unreachable\n"))
			return
		}
		_, _ = w.Write([]byte("ok\n"))
	})
	for _, o := range opts {
		o(mux)
	}
	return mux
}
