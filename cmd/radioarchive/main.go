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
//	TZ              the deployment's time zone, e.g. Australia/Sydney (default UTC):
//	                the nightly run's, the one downloads are named in (ADR 0014),
//	                and the one the browser shows times in (ADR 0016)
//	NIGHTLY_AT      local time of the nightly retention run, HH:MM (default 03:00, in TZ)
//	RECORDER_NOTICE_AFTER  how long a recorder is disconnected or deaf before
//	                listeners see "Some talkgroups aren't being received
//	                right now", as a Go duration (default 2m)
//
// Heavy-use limits (SPEC.md section 6.11, ADR 0036), on the public API only:
//
//	TRUSTED_PROXY   the one reverse proxy's exact address, never a range: a request
//	                from it counts as the last address in its X-Forwarded-For.
//	                Unset, every request counts as its connecting address
//	LIMIT_REQUESTS  public API requests a minute per client (default 120)
//	LIMIT_PLAYBACK  recording playback requests a minute per client (default 600)
//
// A client is one IPv4 address or one IPv6 /64. Signed-in admins and the
// recorder client are never limited; signed-in listeners are, as everyone
// is (P3). No address is logged.
//
// Listeners' sign-in (ADR 0009), on when AUTHENTIK_URL is set:
//
//	APP_URL                 the radio's public URL, e.g. https://radio.example.org
//	AUTHENTIK_URL           Authentik's public URL, its own subdomain
//	AUTHENTIK_INTERNAL_URL  how the API reaches Authentik, e.g. http://authentik-server:9000
//	                        (default: AUTHENTIK_URL)
//	OIDC_CLIENT_ID          the radio's OIDC client and application slug (default radioarchive)
//	OIDC_CLIENT_SECRET      its secret, or OIDC_CLIENT_SECRET_FILE naming a file holding it
//
//	INITIAL_ADMIN_EMAIL     the first admin's email (ADR 0025): while no admin exists,
//	                        the account signing in with it, proven by Authentik,
//	                        becomes admin. Once any admin exists it does nothing
//	AUTHENTIK_API_TOKEN     the radio's accounts service account's Authentik token, or
//	                        AUTHENTIK_API_TOKEN_FILE naming a file holding it (ADR 0033):
//	                        disabling, enabling and deleting an account reach Authentik
//	                        with it. Without it they change the radio only, and say so
//
// The secret and the token are never logged. A signed-in listener's saved
// settings live in the radio's own database (ADR 0021), and so do
// accounts' roles (ADR 0025): nothing the token can change makes anyone a
// radio admin.
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
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // TZ's zone wherever the binary runs (ADR 0016)

	"github.com/RIKIKU/RadioArchive/internal/account"
	"github.com/RIKIKU/RadioArchive/internal/gate"
	"github.com/RIKIKU/RadioArchive/internal/limits"
	"github.com/RIKIKU/RadioArchive/internal/public"
	"github.com/RIKIKU/RadioArchive/internal/retention"
	"github.com/RIKIKU/RadioArchive/internal/server"
	"github.com/RIKIKU/RadioArchive/internal/store"
	"github.com/RIKIKU/RadioArchive/internal/version"
	"github.com/RIKIKU/RadioArchive/web"
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

// recorderNoticeAfter is RECORDER_NOTICE_AFTER (R15).
func recorderNoticeAfter() (time.Duration, error) {
	v := os.Getenv("RECORDER_NOTICE_AFTER")
	if v == "" {
		return server.DefaultNoticeAfter, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("RECORDER_NOTICE_AFTER: %q isn't a duration above 0, like 2m", v)
	}
	return d, nil
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

	noticeAfter, err := recorderNoticeAfter()
	if err != nil {
		return err
	}
	st, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer st.Close()
	log.Info("migrations applied")

	job, err := retentionJob(st, log)
	if err != nil {
		return err
	}
	go job.Run(ctx)

	// The gate, and its live feed, which the recorder endpoints hand every
	// live call to (ADR 0007). The retention job tells it when each call
	// will be deleted (ADR 0018).
	g := gate.New(st, recordingsDir(), log, gate.WithRetention(job))
	go g.Live().Run(ctx)
	rs, err := server.NewRecorders(st, log, server.RecordersConfig{
		RecordingsDir: recordingsDir(),
		Live:          g.Live(),
		ServerVersion: version.Version,
		// The recorder notice goes out on the live feed (R15).
		Notice:      g.Live(),
		NoticeAfter: noticeAfter,
	})
	if err != nil {
		return err
	}
	go rs.Run(ctx)

	sc, err := signInFromEnv(os.Getenv)
	if err != nil {
		return err
	}
	lc, err := limits.FromEnv(os.Getenv)
	if err != nil {
		return err
	}
	if !sc.on() {
		log.Warn("listener sign-in is off: AUTHENTIK_URL isn't set. Listening works as ever; nobody can save to an account")
	}
	handler, acc, err := apiHandler(st, log, rs, g, job, sc, lc)
	if err != nil {
		return err
	}
	// Expired listener sessions are deleted hourly (ADR 0022).
	go acc.Run(ctx)
	srv := &http.Server{
		Addr:              listenAddr(),
		Handler:           handler,
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

// apiHandler is everything the API serves: the recorder client's endpoints,
// the public API, which reads only through the hidden-talkgroup gate (ADR
// 0004), listeners' sign-in and account, the admin API, and the browser app
// built into the binary (web), which serves no data.
//
// Sign-in (internal/account, ADR 0009) saves listeners' preferences, in
// RadioArchive's database (ADR 0021), where its sessions are kept too (ADR
// 0022). The admin API (server.WithAdmin) is behind the account's admin
// check (ADR 0025): a live session whose account's row says admin and
// active, read at every request. Public routes still see no cookie, so an
// admin hears and sees exactly what anyone does (P3). With sign-in off,
// nobody is an admin, and every admin route answers 401. The account is
// returned for serve to run its hourly sweep of expired sessions.
func apiHandler(st *store.Store, log *slog.Logger, rs *server.Recorders, g *gate.Gate, job *retention.Job, sc signInConfig, lc limits.Config) (*server.API, *account.Service, error) {
	acc, err := sc.service(g, st, log)
	if err != nil {
		return nil, nil, err
	}
	lim, err := limits.New(lc)
	if err != nil {
		return nil, nil, err
	}
	admin, err := server.NewAdmin(st, log, acc, recordingsDir())
	if err != nil {
		return nil, nil, err
	}
	// The deployment's clock: the talkgroup table's 30 days (ADR 0026).
	admin.WithClock(time.Now).WithRetention(job).WithRecorders(rs).WithListenerSessions(acc).WithAccounts(acc)
	return server.New(st, log, server.WithRecorders(rs), server.WithPublic(public.New(g, log, public.WithLocation(deploymentZone()))),
		server.WithAccount(acc), server.WithAdmin(admin), server.WithApp(web.New()),
		// Admins are exempt, by the same check as the admin API's (R70).
		server.WithLimits(lim, acc)), acc, nil
}

// deploymentZone is TZ's zone, loaded by its name so the public API can tell
// the browser which zone to show times in (ADR 0016); Go calls the zone it
// reads from TZ "Local". The zone database is in the binary (time/tzdata),
// so TZ works wherever it runs. TZ unset, or a zone it doesn't know, is
// time.Local, as the retention job reads it.
func deploymentZone() *time.Location {
	name := strings.TrimPrefix(os.Getenv("TZ"), ":")
	if name == "" {
		return time.Local
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.Local
	}
	return loc
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
