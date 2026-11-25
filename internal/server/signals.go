package server

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
	"github.com/RIKIKU/RadioArchive/internal/store"
	"github.com/RIKIKU/RadioArchive/internal/store/db"
)

// Recorder signals (SPEC.md R14, R15, R90; ADR 0010). Each recorder has
// three:
//
//  1. Disconnected: it has connected before, and has no live socket now.
//  2. Deaf or down: its client, connected, relays no systems from the
//     recorder plugin (trunk-recorder down, ADR 0002), or a decode rate
//     below DecodeRateNearZero for an attached trunked system (its control
//     channel lost).
//  3. Quiet: no call for its quiet threshold, when one is set (off by
//     default).
//
// Once any recorder has been disconnected or deaf for NoticeAfter, every
// listener's live feed carries the recorder notice, which names nothing
// (R15). Quiet never raises it. Connection and heartbeat state is this
// process's, in memory; the database holds the last call and the quiet
// threshold.

// DefaultNoticeAfter is RECORDER_NOTICE_AFTER's default (R15).
const DefaultNoticeAfter = 2 * time.Minute

// DecodeRateNearZero is the decode rate, in control-channel messages a
// second, below which an attached trunked system's control channel counts
// as lost. A healthy P25 control channel decodes about 40.
const DecodeRateNearZero = 1.0

// signalTick is how often the signals are checked, besides at every
// connection, disconnection and heartbeat.
const signalTick = time.Second

// Signals, as the admin API and the metrics name them.
const (
	SignalDisconnected = "disconnected"
	SignalDeaf         = "deaf"
	SignalQuiet        = "quiet"
)

// Why a recorder is deaf.
const (
	DeafNoHeartbeat = "no_heartbeat" // the client relays no systems: trunk-recorder is down
	DeafDecodeRate  = "decode_rate"  // an attached system's decode rate is near 0
)

// RecorderNotice is told whenever the recorder notice (R15) goes on or off.
// gate.Live is the one in the binary.
type RecorderNotice interface {
	RecorderNotice(active bool)
}

// SignalCounters are the signals' totals, for metrics (R90).
type SignalCounters struct {
	Tripped       sync.Map      // signal -> *atomic.Uint64: a recorder's signal going from clear to tripped
	NoticeRaised  atomic.Uint64 // the recorder notice going on
	NoticeActive  atomic.Bool   // the recorder notice, now
	CheckFailed   atomic.Uint64 // checks that couldn't read the database
	BadHeartbeats atomic.Uint64 // systems dropped from heartbeats for a bad short_name
}

// TrippedCount returns how many times signal has tripped, over all
// recorders, since the server started.
func (c *SignalCounters) TrippedCount(signal string) uint64 {
	v, ok := c.Tripped.Load(signal)
	if !ok {
		return 0
	}
	return v.(*atomic.Uint64).Load()
}

// conventional says whether a system type has no control channel, so no
// decode rate to watch (ADR 0002): trunk-recorder's conventional,
// conventionalP25, conventionalDMR and conventionalSIGMF.
func conventional(systemType string) bool {
	return strings.HasPrefix(strings.ToLower(systemType), "conventional")
}

// recState is what this process knows of one recorder's socket.
type recState struct {
	connected bool
	since     time.Time // when it connected, or disconnected

	// From its client's heartbeats since it connected.
	heartbeat bool                     // one has come
	systems   map[string]cp.SystemRate // the last one's, by short_name
	downSince time.Time                // relaying no systems since
	zeroSince map[string]time.Time     // by short_name: a trunked system's rate near 0 since
}

type signals struct {
	now     func() time.Time
	after   time.Duration
	started time.Time
	kick    chan struct{}

	mu      sync.Mutex
	recs    map[int64]*recState       // recorders whose socket this process has seen
	tripped map[int64]map[string]bool // signals tripped at the last check, by recorder
	notice  bool
}

func newSignals(now func() time.Time, after time.Duration) *signals {
	return &signals{now: now, after: after, started: now(), kick: make(chan struct{}, 1),
		recs: map[int64]*recState{}, tripped: map[int64]map[string]bool{}}
}

// poke asks for a check now.
func (g *signals) poke() {
	select {
	case g.kick <- struct{}{}:
	default:
	}
}

func (g *signals) connected(id int64) {
	g.mu.Lock()
	g.recs[id] = &recState{connected: true, since: g.now(),
		systems: map[string]cp.SystemRate{}, zeroSince: map[string]time.Time{}}
	g.mu.Unlock()
	g.poke()
}

func (g *signals) disconnected(id int64) {
	g.mu.Lock()
	g.recs[id] = &recState{since: g.now()}
	g.mu.Unlock()
	g.poke()
}

// heartbeat takes a client's heartbeat (ADR 0002), relayed from the
// plugin's.
func (g *signals) heartbeat(id int64, systems []cp.SystemRate) {
	now := g.now()
	g.mu.Lock()
	st := g.recs[id]
	if st == nil || !st.connected {
		g.mu.Unlock()
		return
	}
	st.heartbeat = true
	st.systems = make(map[string]cp.SystemRate, len(systems))
	for _, s := range systems {
		st.systems[s.ShortName] = s
	}
	if len(systems) == 0 {
		if st.downSince.IsZero() {
			st.downSince = now
		}
	} else {
		st.downSince = time.Time{}
	}
	for sn := range st.zeroSince {
		if s, ok := st.systems[sn]; !ok || s.DecodeRate >= DecodeRateNearZero || conventional(s.SystemType) {
			delete(st.zeroSince, sn)
		}
	}
	for sn, s := range st.systems {
		if _, ok := st.zeroSince[sn]; !ok && s.DecodeRate < DecodeRateNearZero && !conventional(s.SystemType) {
			st.zeroSince[sn] = now
		}
	}
	g.mu.Unlock()
	g.poke()
}

// status is one recorder's signals at a moment.
type status struct {
	row       db.ListRecorderSignalsRow
	connected bool
	since     *time.Time // connected since, or disconnected since; nil if it never connected
	systems   map[string]cp.SystemRate

	disconnected bool
	deaf         string // "", DeafNoHeartbeat or DeafDecodeRate
	deafSince    time.Time
	deafSystems  []string // attached shortNames whose decode rate is near 0
	quiet        bool
	quietFor     time.Duration
}

func (s status) tripped(signal string) bool {
	switch signal {
	case SignalDisconnected:
		return s.disconnected
	case SignalDeaf:
		return s.deaf != ""
	default:
		return s.quiet
	}
}

// noticed says whether this recorder raises the recorder notice: it has
// been disconnected or deaf for after. Quiet never does (R15).
func (s status) noticed(now time.Time, after time.Duration) bool {
	if s.disconnected && s.since != nil && now.Sub(*s.since) >= after {
		return true
	}
	return s.deaf != "" && now.Sub(s.deafSince) >= after
}

// evaluate works out every recorder's signals. Called with g.mu held.
func (g *signals) evaluate(rows []db.ListRecorderSignalsRow, now time.Time) []status {
	out := make([]status, 0, len(rows))
	for _, r := range rows {
		s := status{row: r, systems: map[string]cp.SystemRate{}}
		st := g.recs[r.ID]
		switch {
		case st != nil && st.connected:
			s.connected = true
			since := st.since
			s.since = &since
			s.systems = st.systems
			switch {
			case !st.heartbeat:
				// Nothing heard yet: neither deaf nor well.
			case !st.downSince.IsZero():
				s.deaf, s.deafSince = DeafNoHeartbeat, st.downSince
			default:
				for _, sn := range r.Attached {
					at, ok := st.zeroSince[sn]
					if !ok {
						continue
					}
					s.deafSystems = append(s.deafSystems, sn)
					if s.deaf == "" || at.Before(s.deafSince) {
						s.deaf, s.deafSince = DeafDecodeRate, at
					}
				}
			}
		case st != nil:
			since := st.since
			s.since, s.disconnected = &since, true
		case r.ClientSeenAt.Valid:
			// Connected before this process started, and not since.
			since := g.started
			s.since, s.disconnected = &since, true
		}
		if q := r.QuietAfterMinutes; q != nil {
			from := g.started
			if r.LastCallAt.Valid {
				from = r.LastCallAt.Time
			}
			s.quietFor = now.Sub(from)
			s.quiet = s.quietFor >= time.Duration(*q)*time.Minute
		}
		out = append(out, s)
	}
	return out
}

// watch checks the signals every tick and whenever a socket or a heartbeat
// asks, until ctx ends.
func (rs *Recorders) watch(ctx context.Context) {
	t := time.NewTicker(signalTick)
	defer t.Stop()
	for {
		rs.check(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-rs.sig.kick:
		}
	}
}

// check reads the recorders, counts signals that have tripped, and turns
// the recorder notice on or off. A check that can't read the database
// changes nothing.
func (rs *Recorders) check(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := rs.st.ListRecorderSignals(ctx)
	if err != nil {
		if ctx.Err() == nil {
			rs.Sig.CheckFailed.Add(1)
			rs.log.Warn("recorder signals: couldn't read the recorders", "error", store.SafeError(err))
		}
		return
	}
	g := rs.sig
	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	notice := false
	for _, s := range g.evaluate(rows, now) {
		was := g.tripped[s.row.ID]
		if was == nil {
			was = map[string]bool{}
			g.tripped[s.row.ID] = was
		}
		for _, sig := range []string{SignalDisconnected, SignalDeaf, SignalQuiet} {
			on := s.tripped(sig)
			if on && !was[sig] {
				inc(&rs.Sig.Tripped, sig)
				rs.log.Warn("recorder signal tripped", "recorder", s.row.Name, "signal", sig, "deaf", s.deaf)
			} else if !on && was[sig] {
				rs.log.Info("recorder signal cleared", "recorder", s.row.Name, "signal", sig)
			}
			was[sig] = on
		}
		if s.noticed(now, g.after) {
			notice = true
		}
	}
	if notice == g.notice {
		return
	}
	g.notice = notice
	rs.Sig.NoticeActive.Store(notice)
	if notice {
		rs.Sig.NoticeRaised.Add(1)
		rs.log.Warn("recorder notice on: a recorder has been disconnected or deaf for " + g.after.String())
	} else {
		rs.log.Info("recorder notice off")
	}
	if n := rs.cfg.Notice; n != nil {
		n.RecorderNotice(notice)
	}
}

// RecorderSystem is one system a recorder hears: attached, reported by its
// plugin, or both.
type RecorderSystem struct {
	ShortName   string   `json:"short_name"`
	SystemID    *int64   `json:"system_id"`    // nil: not attached
	SystemLabel *string  `json:"system_label"` // likewise
	SystemType  *string  `json:"system_type"`  // as trunk-recorder reports it; nil until it does
	DecodeRate  *float64 `json:"decode_rate"`  // control-channel messages a second; nil until reported
	Deaf        bool     `json:"deaf"`         // attached, trunked, and its decode rate near 0
}

// RecorderSignals are a recorder's three signals (R14).
type RecorderSignals struct {
	Disconnected bool `json:"disconnected"`
	Deaf         bool `json:"deaf"`
	Quiet        bool `json:"quiet"`
}

// RecorderStatus is one active recorder's state, for the Recorders page
// (R14) through the admin API.
type RecorderStatus struct {
	Name              string           `json:"name"`
	Connected         bool             `json:"connected"`
	ConnectedSince    *time.Time       `json:"connected_since"`    // this connection, by this server
	DisconnectedSince *time.Time       `json:"disconnected_since"` // nil when connected, or never connected
	LastConnectedAt   *time.Time       `json:"last_connected_at"`  // its client's latest connection
	LastCallAt        *time.Time       `json:"last_call_at"`
	ClientVersion     *string          `json:"client_version"`
	ClientOutOfDate   bool             `json:"client_out_of_date"`
	QuietAfterMinutes *int32           `json:"quiet_after_minutes"` // nil: off
	Systems           []RecorderSystem `json:"systems"`
	Signals           RecorderSignals  `json:"signals"`
	DeafReason        *string          `json:"deaf_reason"` // DeafNoHeartbeat or DeafDecodeRate, when deaf
	// Warning is the page's badge: any signal tripped. Warnings say which,
	// in the page's words.
	Warning  bool     `json:"warning"`
	Warnings []string `json:"warnings"`
}

// Status is every active recorder's state and signals, by name.
func (rs *Recorders) Status(ctx context.Context) ([]RecorderStatus, error) {
	rows, err := rs.st.ListRecorderSignals(ctx)
	if err != nil {
		return nil, err
	}
	atts, err := rs.st.ListAttachments(ctx)
	if err != nil {
		return nil, err
	}
	g := rs.sig
	now := g.now()
	g.mu.Lock()
	ss := g.evaluate(rows, now)
	g.mu.Unlock()
	out := make([]RecorderStatus, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.public(atts, now))
	}
	return out, nil
}

func (s status) public(atts []db.ListAttachmentsRow, now time.Time) RecorderStatus {
	r := s.row
	o := RecorderStatus{Name: r.Name, Connected: s.connected, LastConnectedAt: ts(r.ClientSeenAt),
		LastCallAt: ts(r.LastCallAt), ClientVersion: r.ClientVersion, ClientOutOfDate: r.ClientOutOfDate,
		QuietAfterMinutes: r.QuietAfterMinutes, Systems: []RecorderSystem{}, Warnings: []string{},
		Signals: RecorderSignals{Disconnected: s.disconnected, Deaf: s.deaf != "", Quiet: s.quiet}}
	if s.connected {
		o.ConnectedSince = utc(s.since)
	} else {
		o.DisconnectedSince = utc(s.since)
	}
	seen := map[string]bool{}
	for _, a := range atts {
		if a.Recorder != r.Name {
			continue
		}
		id, label := a.SystemID, a.SystemLabel
		o.Systems = append(o.Systems, s.system(a.ShortName, &id, &label))
		seen[a.ShortName] = true
	}
	var extra []string
	for sn := range s.systems {
		if !seen[sn] {
			extra = append(extra, sn)
		}
	}
	slices.Sort(extra)
	for _, sn := range extra {
		o.Systems = append(o.Systems, s.system(sn, nil, nil))
	}

	if s.disconnected {
		o.Warnings = append(o.Warnings, "Disconnected for "+minutes(now.Sub(*s.since)))
	}
	switch s.deaf {
	case DeafNoHeartbeat:
		o.Warnings = append(o.Warnings, "No heartbeat from trunk-recorder for "+minutes(now.Sub(s.deafSince)))
	case DeafDecodeRate:
		o.Warnings = append(o.Warnings, fmt.Sprintf("Control channel lost on %s: decode rate near 0 for %s",
			strings.Join(s.deafSystems, ", "), minutes(now.Sub(s.deafSince))))
	}
	if s.deaf != "" {
		reason := s.deaf
		o.DeafReason = &reason
	}
	if s.quiet {
		o.Warnings = append(o.Warnings, "Quiet: no calls for "+minutes(s.quietFor))
	}
	o.Warning = len(o.Warnings) > 0
	return o
}

func (s status) system(sn string, id *int64, label *string) RecorderSystem {
	out := RecorderSystem{ShortName: sn, SystemID: id, SystemLabel: label}
	if rate, ok := s.systems[sn]; ok {
		t, d := rate.SystemType, rate.DecodeRate
		out.SystemType, out.DecodeRate = &t, &d
	}
	out.Deaf = slices.Contains(s.deafSystems, sn)
	return out
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// minutes says how long d is, as the Recorders page does.
func minutes(d time.Duration) string {
	switch m := int(d / time.Minute); {
	case m < 1:
		return "under a minute"
	case m == 1:
		return "1 min"
	default:
		return fmt.Sprintf("%d min", m)
	}
}

// touchCall records that the recorder delivered a call that started at
// startMS (the client's clock), or now, if that's earlier, for the quiet
// signal and the Recorders page. A failure is logged: the call matters
// more.
func (rs *Recorders) touchCall(ctx context.Context, rec store.Recorder, startMS int64) {
	at := rs.sig.now()
	if t := time.UnixMilli(startMS); startMS > 0 && t.Before(at) {
		at = t
	}
	err := rs.st.TouchRecorderCall(ctx, db.TouchRecorderCallParams{ID: rec.ID,
		At: pgtype.Timestamptz{Time: at.UTC(), Valid: true}})
	if err != nil {
		rs.log.Warn("couldn't record the recorder's last call", "recorder", rec.Name, "error", store.SafeError(err))
	}
}

// touchHeard records the admission's talkgroups as heard on a call that
// started at startMS (the client's clock), or now, if that's earlier: the
// admin table's last heard (ADR 0026). After the call's transaction, as
// touchCall is, so it holds no talkgroup's row while a recording is stored.
// A failure is logged with IDs only: the call matters more.
func (rs *Recorders) touchHeard(ctx context.Context, adm store.Admission, startMS int64) {
	if len(adm.HeardTalkgroups) == 0 {
		return
	}
	at := rs.sig.now()
	if t := time.UnixMilli(startMS); startMS > 0 && t.Before(at) {
		at = t
	}
	err := rs.st.TouchTalkgroupsHeard(ctx, db.TouchTalkgroupsHeardParams{SystemID: adm.HeardSystem,
		Talkgroups: adm.HeardTalkgroups, At: pgtype.Timestamptz{Time: at.UTC(), Valid: true}})
	if err != nil {
		rs.log.Warn("couldn't record when talkgroups were last heard", "system", adm.HeardSystem, "error", store.SafeError(err))
	}
}
