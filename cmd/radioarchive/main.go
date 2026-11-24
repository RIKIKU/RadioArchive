// Command radioarchive is the RadioArchive server.
//
//	radioarchive [serve]                    run the API (the default)
//	radioarchive healthcheck                GET /healthz on the local API; exit 0 if it answers ok
//	radioarchive recorder add "<name>"      create a recorder and print its client key once
//	radioarchive recorder list              list recorders
//	radioarchive recorder rotate-key <name> give a recorder a new key; the old one stops at once
//	radioarchive recorder retire <name>     revoke a recorder's key for good; its calls stay
//
// Settings come from the environment:
//
//	DATABASE_URL    PostgreSQL connection URL (required by serve and recorder)
//	LISTEN_ADDR     address to listen on (default :8080)
//	RECORDINGS_DIR  the recordings volume (default /var/lib/radioarchive/recordings)
//	RECORDINGS_MIN_FREE  below this much free on the recordings volume, the
//	                oldest recordings are deleted early (default 5%)
//	RECORDINGS_EARLY_DELETE_FLOOR  calls that started within this are never
//	                deleted early, as a Go duration (default 24h)
//	NIGHTLY_AT      local time of the nightly retention run, HH:MM (default 03:00, in TZ)
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/gate"
	"github.com/RIKIKU/RadioArchive/internal/public"
	"github.com/RIKIKU/RadioArchive/internal/retention"
	"github.com/RIKIKU/RadioArchive/internal/server"
	"github.com/RIKIKU/RadioArchive/internal/store"
	"github.com/RIKIKU/RadioArchive/internal/version"
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
	case "recorder":
		err = recorderMain(os.Args[2:], os.Stdout)
		if err != nil {
			fmt.Fprintln(os.Stderr, "radioarchive recorder:", err)
			os.Exit(1)
		}
		return
	default:
		err = fmt.Errorf("unknown command %q (want serve, healthcheck or recorder)", cmd)
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

func recordingsDir() string {
	if d := os.Getenv("RECORDINGS_DIR"); d != "" {
		return d
	}
	return "/var/lib/radioarchive/recordings"
}

func openStore(ctx context.Context) (*store.Store, error) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return nil, errors.New("DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	return store.Open(ctx, url)
}

func serve(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer st.Close()
	log.Info("migrations applied")

	rs, err := server.NewRecorders(st, log, server.RecordersConfig{
		RecordingsDir: recordingsDir(),
		ServerVersion: version.Version,
	})
	if err != nil {
		return err
	}
	go rs.Run(ctx)

	job, err := retentionJob(st, log)
	if err != nil {
		return err
	}
	go job.Run(ctx)

	srv := &http.Server{
		Addr:              listenAddr(),
		Handler:           apiHandler(st, log, rs, recordingsDir()),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", "addr", srv.Addr, "version", version.Version)

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

// apiHandler is everything the API serves: the recorder client's endpoints
// and the public API, which reads only through the hidden-talkgroup gate
// (ADR 0004). The admin API (server.WithAdmin) joins it once admin sign-in
// (Authentik, M4) exists. Until then only tests serve it, with an
// authenticator that lives in their _test.go files, so no build of this
// binary can serve it.
func apiHandler(st *store.Store, log *slog.Logger, rs *server.Recorders, recordings string) *server.API {
	return server.New(st, log, server.WithRecorders(rs),
		server.WithPublic(public.New(gate.New(st, recordings, log), log)))
}

// retentionJob is the nightly retention job and the low-disk check (R88),
// on the real clock and the real volume. The binary only runs it on its
// schedule: no route, setting or signal triggers a run.
func retentionJob(st *store.Store, log *slog.Logger) (*retention.Job, error) {
	cfg := retention.Config{Store: st, Dir: recordingsDir(), Log: log}
	if v := os.Getenv("RECORDINGS_MIN_FREE"); v != "" {
		f, err := retention.ParsePercent(v)
		if err != nil {
			return nil, fmt.Errorf("RECORDINGS_MIN_FREE: %w", err)
		}
		cfg.MinFree = f
	}
	if v := os.Getenv("RECORDINGS_EARLY_DELETE_FLOOR"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("RECORDINGS_EARLY_DELETE_FLOOR: %q isn't a duration above 0, like 24h", v)
		}
		cfg.EarlyDeleteFloor = d
	}
	if v := os.Getenv("NIGHTLY_AT"); v != "" {
		at, err := retention.ParseAt(v)
		if err != nil {
			return nil, fmt.Errorf("NIGHTLY_AT: %w", err)
		}
		cfg.At = at
	}
	return retention.New(cfg)
}

func recorderMain(args []string, out io.Writer) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	st, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer st.Close()
	return recorderCommand(ctx, st, args, out)
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
