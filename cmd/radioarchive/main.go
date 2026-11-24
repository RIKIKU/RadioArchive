// Command radioarchive is the RadioArchive server.
//
//	radioarchive [serve]     run the API (the default)
//	radioarchive healthcheck GET /healthz on the local API; exit 0 if it answers ok
//
// Settings come from the environment:
//
//	DATABASE_URL  PostgreSQL connection URL (required by serve)
//	LISTEN_ADDR   address to listen on (default :8080)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/server"
	"github.com/RIKIKU/RadioArchive/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve(log)
	case "healthcheck":
		err = healthcheck()
	default:
		err = fmt.Errorf("unknown command %q (want serve or healthcheck)", cmd)
	}
	if err != nil {
		log.Error(cmd+" failed", "error", err)
		os.Exit(1)
	}
}

func listenAddr() string {
	if a := os.Getenv("LISTEN_ADDR"); a != "" {
		return a
	}
	return ":8080"
}

func serve(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return errors.New("DATABASE_URL is not set")
	}
	startCtx, cancel := context.WithTimeout(ctx, time.Minute)
	st, err := store.Open(startCtx, url)
	cancel()
	if err != nil {
		return err
	}
	defer st.Close()
	log.Info("migrations applied")

	srv := &http.Server{
		Addr:              listenAddr(),
		Handler:           server.New(st, log),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", "addr", srv.Addr)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutCtx)
}

// healthcheck is for the container's health check: the image is distroless
// and has no curl.
func healthcheck() error {
	_, port, err := net.SplitHostPort(listenAddr())
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("/healthz answered %s", resp.Status)
	}
	return nil
}
