// Package server is the API's HTTP handler.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// Pinger reaches the database: store.Store's sqlc query Ping.
type Pinger interface {
	Ping(ctx context.Context) (int32, error)
}

// New returns the API's handler.
func New(db Pinger, log *slog.Logger) http.Handler {
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
	return mux
}
