package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
	"github.com/RIKIKU/RadioArchive/internal/store"
	"github.com/RIKIKU/RadioArchive/internal/store/db"
	"github.com/RIKIKU/RadioArchive/internal/telemetry"
	"github.com/RIKIKU/RadioArchive/internal/version"
)

// LiveSink receives every live call the server accepts: its start, its
// transmissions and its end, each once its row is written, and its frames,
// with the ID of the call they belong to. The live feed to listeners
// (gate.Live, ADR 0007) plugs in here, and decides what listeners may have
// of each. It's called on the recorder's socket's goroutine, so it must not
// block.
type LiveSink interface {
	CallStarted(callID string, unit uint32)
	Transmission(callID string, unit uint32, offsetMS int32)
	CallEnded(callID string)
	Frame(callID string, f cp.Frame)
}

// RecordersConfig configures the recorder endpoints. Which system a call is
// filed under, and whether it's taken at all, is store.Admit's decision
// (R12, R17, R18, R21).
type RecordersConfig struct {
	// RecordingsDir is the recordings volume (R20). Uploads are written to
	// RecordingsDir/.incoming first, so the rename into place is atomic.
	RecordingsDir string
	// Live is optional.
	Live LiveSink
	// ServerVersion is sent in the welcome, and clients are checked against
	// it (R6). It must be MAJOR.MINOR.PATCH.
	ServerVersion string
	// Notice is told when the recorder notice goes on or off (R15).
	// Optional.
	Notice RecorderNotice
	// NoticeAfter is RECORDER_NOTICE_AFTER: how long a recorder is
	// disconnected or deaf before the notice goes on. Default
	// DefaultNoticeAfter.
	NoticeAfter time.Duration
	// Now is the signals' clock (default time.Now). Tests control it.
	Now func() time.Time
	// FreshMetrics makes every scrape read the database afresh, rather
	// than reusing a read for up to a few minutes (ADR 0040). For tests.
	FreshMetrics bool
	// Heard is told of talkgroups heard on a call whose patch is wholly
	// public, so a quiet one comes back at once (R117). The gate is the
	// one in the binary. Optional.
	Heard HeardSink
}

// HeardSink is told of talkgroups heard publicly.
type HeardSink interface {
	TalkgroupsHeard(system int64, talkgroups []int32)
}

// Counters are the recorder endpoints' totals, for metrics (R90) and tests.
type Counters struct {
	FramesUnstarted  atomic.Uint64 // frames or transmissions for a call the socket hasn't started
	UnknownTypes     atomic.Uint64 // unknown text type or binary kind
	ProtocolCloses   atomic.Uint64 // sockets closed with 1007 or 1008 for bad input
	Revoked          atomic.Uint64 // sockets closed because their key was rotated or retired
	Withdrawn        atomic.Uint64 // calls deleted because trunk-recorder concluded them without a recording (ADR 0053)
	FramesRefused    atomic.Uint64 // frames of live calls refused by R12, R17 or R18
	ClientsTooOld    atomic.Uint64 // live sockets and uploads refused with client_too_old (R6)
	ClientsOutOfDate atomic.Uint64 // live sockets opened by a client one minor version behind (R6)
	Uploads          sync.Map      // status -> *atomic.Uint64
	Rejected         sync.Map      // reason -> *atomic.Uint64: calls refused, each counted once (R90)
	// LiveDelay is each live call's delay from its first PCM to its first
	// frame passed on (R38).
	LiveDelay *telemetry.Hist
}

func (c *Counters) upload(status string) { inc(&c.Uploads, status) }

func (c *Counters) admitted(a store.Admission) {
	if a.Counted {
		inc(&c.Rejected, string(a.Rejected))
	}
}

func inc(m *sync.Map, k string) {
	v, _ := m.LoadOrStore(k, new(atomic.Uint64))
	v.(*atomic.Uint64).Add(1)
}

// RejectedCount returns how many calls were refused for reason since the
// server started.
func (c *Counters) RejectedCount(reason string) uint64 {
	v, ok := c.Rejected.Load(reason)
	if !ok {
		return 0
	}
	return v.(*atomic.Uint64).Load()
}

// UploadCount returns how many uploads ended with status (or error code).
func (c *Counters) UploadCount(status string) uint64 {
	v, ok := c.Uploads.Load(status)
	if !ok {
		return 0
	}
	return v.(*atomic.Uint64).Load()
}

// Recorders serves the recorder client's live WebSocket and recording
// uploads, and closes live sockets whose key stops being current.
type Recorders struct {
	st   *store.Store
	log  *slog.Logger
	cfg  RecordersConfig
	Stat Counters
	Sig  SignalCounters

	sig     *signals
	metrics *metricsCache

	receivingMu sync.Mutex // one RecordReceiving at a time
	pruned      time.Time  // when RecordReceiving last pruned the spans

	mu       sync.Mutex
	sessions map[int64]*session // by recorder ID: one connection per client key

	listening chan struct{} // closed once the key listener first runs
	listenOne sync.Once
}

// NewRecorders returns the recorder endpoints. Call Run to enforce key
// rotation and retirement on open sockets.
func NewRecorders(st *store.Store, log *slog.Logger, cfg RecordersConfig) (*Recorders, error) {
	if cfg.RecordingsDir == "" {
		return nil, errors.New("recordings directory is not set")
	}
	if _, err := version.Parse(cfg.ServerVersion); err != nil {
		return nil, fmt.Errorf("server version: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(cfg.RecordingsDir, incomingDir), 0o750); err != nil {
		return nil, fmt.Errorf("recordings directory: %w", err)
	}
	if cfg.NoticeAfter < 0 {
		return nil, errors.New("the recorder notice's delay can't be negative")
	}
	if cfg.NoticeAfter == 0 {
		cfg.NoticeAfter = DefaultNoticeAfter
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Recorders{st: st, log: log, cfg: cfg, sessions: map[int64]*session{}, listening: make(chan struct{}),
		sig: newSignals(cfg.Now, cfg.NoticeAfter), metrics: newMetricsCache(cfg.FreshMetrics), Stat: Counters{LiveDelay: telemetry.NewHist(telemetry.LiveDelay)}}, nil
}

// Listening is closed once Run is listening for key changes.
func (rs *Recorders) Listening() <-chan struct{} { return rs.listening }

// Run listens for key rotations and retirements (store.KeyChannel) and
// closes the live socket of any recorder whose key is no longer current,
// and watches the recorders' signals (R14, R15). It returns when ctx ends.
// After every (re)connection to the database it rechecks every open
// socket, so a key change made while it wasn't listening still takes
// effect.
func (rs *Recorders) Run(ctx context.Context) {
	go rs.watch(ctx)
	for ctx.Err() == nil {
		err := rs.listen(ctx)
		if ctx.Err() != nil {
			return
		}
		rs.log.Warn("recorder key listener stopped; retrying", "error", store.SafeError(err))
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
}

func (rs *Recorders) listen(ctx context.Context) error {
	conn, err := rs.st.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN "+store.KeyChannel); err != nil {
		return err
	}
	rs.recheckAll(ctx)
	rs.listenOne.Do(func() { close(rs.listening) })
	for {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		id, err := strconv.ParseInt(n.Payload, 10, 64)
		if err != nil {
			continue
		}
		rs.recheck(ctx, id)
	}
}

func (rs *Recorders) recheckAll(ctx context.Context) {
	rs.mu.Lock()
	ids := make([]int64, 0, len(rs.sessions))
	for id := range rs.sessions {
		ids = append(ids, id)
	}
	rs.mu.Unlock()
	for _, id := range ids {
		rs.recheck(ctx, id)
	}
}

// recheck closes the recorder's socket if the key it opened with is no
// longer current.
func (rs *Recorders) recheck(ctx context.Context, recorderID int64) {
	rs.mu.Lock()
	s := rs.sessions[recorderID]
	rs.mu.Unlock()
	if s == nil {
		return
	}
	if !rs.keyCurrent(ctx, s.rec) {
		s.revoke()
	}
}

func (rs *Recorders) keyCurrent(ctx context.Context, rec store.Recorder) bool {
	ok, err := rs.st.RecorderKeyIsCurrent(ctx, dbKeyCheck(rec))
	if err != nil {
		// Fail closed: the client reconnects, and is checked afresh.
		rs.log.Warn("recorder key check failed", "recorder", rec.Name, "error", store.SafeError(err))
		return false
	}
	return ok
}

// register makes s its recorder's one live socket, closing any older one.
func (rs *Recorders) register(s *session) {
	rs.mu.Lock()
	old := rs.sessions[s.rec.ID]
	rs.sessions[s.rec.ID] = s
	rs.sig.connected(s.rec.ID)
	rs.mu.Unlock()
	if old != nil {
		rs.log.Warn("recorder opened a second connection; closing the older one", "recorder", s.rec.Name)
		old.close(policyViolation, "replaced by a newer connection")
	}
}

func (rs *Recorders) unregister(s *session) {
	rs.mu.Lock()
	if rs.sessions[s.rec.ID] == s {
		delete(rs.sessions, s.rec.ID)
		rs.sig.disconnected(s.rec.ID)
	}
	rs.mu.Unlock()
}

// heartbeat takes the client's heartbeat for the recorder's signals, if s
// is still its socket.
func (rs *Recorders) heartbeat(s *session, systems []cp.SystemRate, queue cp.QueueState) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.sessions[s.rec.ID] == s {
		rs.sig.heartbeat(s.rec.ID, systems, queue)
	}
}

// authenticate checks the bearer key and the protocol header, as the live
// socket and the upload both need, and answers the refusal itself.
func (rs *Recorders) authenticate(w http.ResponseWriter, r *http.Request) (store.Recorder, bool) {
	key, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		writeJSON(w, http.StatusUnauthorized, cp.ErrorResponse{Error: "invalid_client_key"})
		return store.Recorder{}, false
	}
	rec, err := rs.st.AuthenticateRecorder(r.Context(), strings.TrimSpace(key))
	if errors.Is(err, store.ErrUnknownKey) {
		writeJSON(w, http.StatusUnauthorized, cp.ErrorResponse{Error: "invalid_client_key"})
		return store.Recorder{}, false
	}
	if err != nil {
		rs.log.Error("recorder authentication failed", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, cp.ErrorResponse{Error: "unavailable"})
		return store.Recorder{}, false
	}
	// R6: up to one minor version behind the server.
	cv := r.Header.Get(cp.HeaderClientVersion)
	if version.Check(cv, rs.cfg.ServerVersion) == version.TooOld {
		rs.Stat.ClientsTooOld.Add(1)
		rs.log.Warn("refused a recorder client that is too old", "recorder", rec.Name,
			"client_version", clip(cv), "server_version", rs.cfg.ServerVersion)
		writeJSON(w, http.StatusUpgradeRequired, cp.ErrorResponse{Error: cp.ErrClientTooOld,
			ClientVersion: clip(cv), ServerVersion: rs.cfg.ServerVersion})
		return store.Recorder{}, false
	}
	p, err := strconv.Atoi(r.Header.Get(cp.HeaderProtocol))
	if err != nil || p != cp.Protocol {
		writeJSON(w, http.StatusUpgradeRequired, cp.ErrorResponse{Error: cp.ErrUnsupportedProtocol, Protocol: &p, Supported: []int{cp.Protocol}})
		return store.Recorder{}, false
	}
	return rec, true
}

// clip bounds a client-supplied version for logs, answers and the database.
func clip(s string) string {
	if len(s) > maxVersionLen {
		return s[:maxVersionLen]
	}
	return s
}

const maxVersionLen = 100

// recordClient stores the client version the recorder connected with, and
// whether it's out of date, for the Recorders page (R6, R14). A failure is
// logged, not fatal: the connection matters more.
func (rs *Recorders) recordClient(ctx context.Context, rec store.Recorder, clientVersion string) {
	out := version.Check(clientVersion, rs.cfg.ServerVersion) == version.OutOfDate
	if out {
		rs.Stat.ClientsOutOfDate.Add(1)
		rs.log.Warn("recorder client is one minor version behind the server; upgrade it", "recorder", rec.Name,
			"client_version", clip(clientVersion), "server_version", rs.cfg.ServerVersion)
	}
	v := clip(clientVersion)
	if err := rs.st.SetRecorderClient(ctx, db.SetRecorderClientParams{ID: rec.ID, ClientVersion: &v, ClientOutOfDate: out}); err != nil {
		rs.log.Warn("couldn't record the recorder client's version", "recorder", rec.Name, "error", store.SafeError(err))
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func uuidString(u pgtype.UUID) string {
	b := u.Bytes
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func tsMS(ms int64) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: time.UnixMilli(ms).UTC(), Valid: true}
}

func nonNil(a []int32) []int32 {
	if a == nil {
		return []int32{}
	}
	return a
}
