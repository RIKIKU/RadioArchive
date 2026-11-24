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
)

// LiveSink receives every live frame the server accepts, with the ID of the
// call it belongs to. The live feed to listeners plugs in here; until then
// it's a test hook.
type LiveSink interface {
	Frame(callID string, f cp.Frame)
}

// SystemResolver decides which system a recorder's call is filed under.
// Until systems and attachments arrive (issue #34) there's one system for
// every call; #34 replaces this with the recorder + shortName attachment
// and its rejections (R12).
type SystemResolver interface {
	SystemFor(ctx context.Context, recorderID int64, shortName string) (int64, error)
}

// SingleSystem files every call under one system, created on first use.
type SingleSystem struct {
	Store *store.Store
	Name  string

	mu sync.Mutex
	id int64
}

// SystemFor implements SystemResolver.
func (s *SingleSystem) SystemFor(ctx context.Context, _ int64, _ string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.id != 0 {
		return s.id, nil
	}
	id, err := s.Store.EnsureSystem(ctx, s.Name)
	if err != nil {
		return 0, fmt.Errorf("system: %s", store.SafeError(err))
	}
	s.id = id
	return id, nil
}

// RecordersConfig configures the recorder endpoints.
type RecordersConfig struct {
	// RecordingsDir is the recordings volume (R20). Uploads are written to
	// RecordingsDir/.incoming first, so the rename into place is atomic.
	RecordingsDir string
	// Systems defaults to one system named "default".
	Systems SystemResolver
	// Live is optional.
	Live LiveSink
	// ServerVersion is sent in the welcome.
	ServerVersion string
}

// Counters are the recorder endpoints' totals, for metrics (R90) and tests.
type Counters struct {
	FramesUnstarted atomic.Uint64 // frames or transmissions for a call the socket hasn't started
	UnknownTypes    atomic.Uint64 // unknown text type or binary kind
	ProtocolCloses  atomic.Uint64 // sockets closed with 1007 or 1008 for bad input
	Revoked         atomic.Uint64 // sockets closed because their key was rotated or retired
	Uploads         sync.Map      // status -> *atomic.Uint64
}

func (c *Counters) upload(status string) {
	v, _ := c.Uploads.LoadOrStore(status, new(atomic.Uint64))
	v.(*atomic.Uint64).Add(1)
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
	if err := os.MkdirAll(filepath.Join(cfg.RecordingsDir, incomingDir), 0o750); err != nil {
		return nil, fmt.Errorf("recordings directory: %w", err)
	}
	if cfg.Systems == nil {
		cfg.Systems = &SingleSystem{Store: st, Name: "default"}
	}
	return &Recorders{st: st, log: log, cfg: cfg, sessions: map[int64]*session{}, listening: make(chan struct{})}, nil
}

// Listening is closed once Run is listening for key changes.
func (rs *Recorders) Listening() <-chan struct{} { return rs.listening }

// Run listens for key rotations and retirements (store.KeyChannel) and
// closes the live socket of any recorder whose key is no longer current. It
// returns when ctx ends. After every (re)connection it rechecks every open
// socket, so a change made while it wasn't listening still takes effect.
func (rs *Recorders) Run(ctx context.Context) {
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
	}
	rs.mu.Unlock()
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
	// The client version (R6) is checked in issue #36.
	p, err := strconv.Atoi(r.Header.Get(cp.HeaderProtocol))
	if err != nil || p != cp.Protocol {
		writeJSON(w, http.StatusUpgradeRequired, cp.ErrorResponse{Error: "unsupported_protocol", Protocol: &p, Supported: []int{cp.Protocol}})
		return store.Recorder{}, false
	}
	return rec, true
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
