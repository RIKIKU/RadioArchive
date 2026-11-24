// Command radioarchive-client is the recorder client (SPEC.md R3, ADR
// 0002). It runs beside trunk-recorder, takes the recorder plugin's UDP on
// localhost, and sends live Opus frames and Ogg/Opus recordings to the
// server. It needs cgo and libopus.
//
// Settings come from the environment:
//
//	SERVER_URL       the API's base URL, http:// or https:// (required)
//	CLIENT_KEY       this recorder's key from `radioarchive recorder add`, or
//	CLIENT_KEY_FILE  a file holding it (for a Docker secret)
//	UDP_ADDR         where the plugin sends (default 127.0.0.1:9850)
//	METRICS_ADDR     serve /metrics here (default off)
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/opus"
	"github.com/RIKIKU/RadioArchive/internal/recorderclient"
	"github.com/RIKIKU/RadioArchive/internal/version"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("recorder client failed", "error", err)
		os.Exit(1)
	}
}

func clientKey() (string, error) {
	if k := os.Getenv("CLIENT_KEY"); k != "" {
		return k, nil
	}
	if f := os.Getenv("CLIENT_KEY_FILE"); f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}
	return "", errors.New("CLIENT_KEY or CLIENT_KEY_FILE must be set")
}

func newEncoder(rate int) (recorderclient.Encoder, error) {
	e, err := opus.NewEncoder(rate)
	if err != nil {
		return nil, err
	}
	return e, nil
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Fail at start, not at the first call, in a build without libopus.
	probe, err := opus.NewEncoder(recorderclient.SampleRate)
	if err != nil {
		return err
	}
	probe.Close()

	key, err := clientKey()
	if err != nil {
		return err
	}
	m := recorderclient.NewMetrics()
	c, err := recorderclient.New(recorderclient.Config{
		ServerURL:  os.Getenv("SERVER_URL"),
		ClientKey:  key,
		UDPAddr:    os.Getenv("UDP_ADDR"),
		NewEncoder: newEncoder,
		Log:        log,
		Metrics:    m,
	})
	if err != nil {
		return err
	}
	if addr := os.Getenv("METRICS_ADDR"); addr != "" {
		mux := http.NewServeMux()
		mux.Handle("GET /metrics", m)
		srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
		go func() {
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("metrics server stopped", "error", err)
			}
		}()
		defer srv.Close()
	}
	log.Info("recorder client starting", "version", version.Version)
	return c.Run(ctx)
}
