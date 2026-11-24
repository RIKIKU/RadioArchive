package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
	"github.com/RIKIKU/RadioArchive/internal/store"
	"github.com/RIKIKU/RadioArchive/internal/store/db"
)

const (
	invalidPayload  = websocket.StatusInvalidFramePayloadData // 1007
	policyViolation = websocket.StatusPolicyViolation         // 1008
)

func dbKeyCheck(rec store.Recorder) db.RecorderKeyIsCurrentParams {
	return db.RecorderKeyIsCurrentParams{ID: rec.ID, KeyHash: rec.KeyHash}
}

// session is one recorder client's live socket.
type session struct {
	rs   *Recorders
	rec  store.Recorder
	conn *websocket.Conn
	log  *slog.Logger

	calls   map[cp.CallID]*liveCall // started on this socket
	refused map[cp.CallID]bool      // refused by store.Admit; their frames are dropped

	closeOnce sync.Once
}

type liveCall struct {
	id      pgtype.UUID
	idText  string
	unflush int64 // frames not yet added to the call's live_frames
}

// closeError ends the read loop with a close code and reason.
type closeError struct {
	code   websocket.StatusCode
	reason string
}

func (e *closeError) Error() string { return fmt.Sprintf("close %d: %s", e.code, e.reason) }

func protoErr(code websocket.StatusCode, format string, a ...any) error {
	return &closeError{code: code, reason: fmt.Sprintf(format, a...)}
}

func (s *session) close(code websocket.StatusCode, reason string) {
	s.closeOnce.Do(func() { go func() { _ = s.conn.Close(code, reason) }() })
}

func (s *session) revoke() {
	s.rs.Stat.Revoked.Add(1)
	s.log.Info("recorder key revoked; closing its live connection")
	s.close(policyViolation, "client key revoked")
}

func (rs *Recorders) serveLive(w http.ResponseWriter, r *http.Request) {
	rec, ok := rs.authenticate(w, r)
	if !ok {
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept has answered
	}
	// Text over 64 KB is closed with 1008 below, so the library's own limit
	// (which closes with 1009) sits above it.
	conn.SetReadLimit(2 * cp.MaxTextMessage)
	s := &session{rs: rs, rec: rec, conn: conn, calls: map[cp.CallID]*liveCall{}, refused: map[cp.CallID]bool{},
		log: rs.log.With("recorder", rec.Name)}
	ctx := r.Context()

	rs.register(s)
	defer rs.unregister(s)
	// The key may have changed between authenticating and registering, when
	// no notification could find this socket.
	if !rs.keyCurrent(ctx, rec) {
		s.revoke()
		return
	}
	cv := r.Header.Get(cp.HeaderClientVersion)
	s.log.Info("recorder connected", "client_version", clip(cv))
	rs.recordClient(ctx, rec, cv)

	welcome, _ := json.Marshal(cp.Welcome{Type: cp.TypeWelcome, ServerVersion: rs.cfg.ServerVersion, Protocol: cp.Protocol})
	if err := conn.Write(ctx, websocket.MessageText, welcome); err != nil {
		return
	}
	err = s.readLoop(ctx)
	s.flushFrames(context.WithoutCancel(ctx))
	var ce *closeError
	if errors.As(err, &ce) {
		if ce.code == websocket.StatusInternalError {
			s.log.Error("closing recorder connection", "reason", ce.reason)
		} else {
			rs.Stat.ProtocolCloses.Add(1)
			s.log.Warn("closing recorder connection for bad input", "code", int(ce.code), "reason", ce.reason)
		}
		s.close(ce.code, ce.reason)
		return
	}
	s.log.Info("recorder disconnected", "status", int(websocket.CloseStatus(err)))
	s.close(websocket.StatusNormalClosure, "")
}

func (s *session) readLoop(ctx context.Context) error {
	for {
		typ, rd, err := s.conn.Reader(ctx)
		if err != nil {
			return err
		}
		switch typ {
		case websocket.MessageText:
			b, err := io.ReadAll(io.LimitReader(rd, cp.MaxTextMessage+1))
			if err != nil {
				return err
			}
			if len(b) > cp.MaxTextMessage {
				return protoErr(policyViolation, "text message over %d bytes", cp.MaxTextMessage)
			}
			if err := s.text(ctx, b); err != nil {
				return err
			}
		case websocket.MessageBinary:
			b, err := io.ReadAll(io.LimitReader(rd, cp.FrameHeaderSize+cp.MaxOpusPacket+1))
			if err != nil {
				return err
			}
			if err := s.binary(b); err != nil {
				return err
			}
			// Drain anything past the limit (an oversized packet has failed above).
			_, _ = io.Copy(io.Discard, rd)
		}
	}
}

func (s *session) binary(b []byte) error {
	f, err := cp.DecodeFrame(b)
	switch {
	case errors.Is(err, cp.ErrFrameShort):
		return protoErr(invalidPayload, "binary message shorter than %d bytes", cp.FrameHeaderSize)
	case errors.Is(err, cp.ErrUnknownKind):
		s.rs.Stat.UnknownTypes.Add(1)
		return nil
	case errors.Is(err, cp.ErrPacketLength):
		return protoErr(policyViolation, "opus packet must be 1-%d bytes", cp.MaxOpusPacket)
	}
	c := s.calls[f.CallID]
	if c == nil {
		s.unstarted(f.CallID)
		return nil
	}
	c.unflush++
	if s.rs.cfg.Live != nil {
		f.Packet = bytes.Clone(f.Packet)
		s.rs.cfg.Live.Frame(c.idText, f)
	}
	return nil
}

// event is every field a known event can have. Pointers tell a missing field
// from a zero one.
type event struct {
	Type              string  `json:"type"`
	SysNum            *uint16 `json:"sys_num"`
	CallNum           *int64  `json:"call_num"`
	StartTime         *int64  `json:"start_time"`
	ShortName         *string `json:"short_name"`
	Talkgroup         *int32  `json:"talkgroup"`
	Unit              *uint32 `json:"unit"`
	PatchedTalkgroups []int32 `json:"patched_talkgroups"`
	StartTimeMS       *int64  `json:"start_time_ms"`
	FirstPCMMS        *int64  `json:"first_pcm_ms"`
	OffsetMS          *int64  `json:"offset_ms"`
	StopTimeMS        *int64  `json:"stop_time_ms"`
}

func (s *session) text(ctx context.Context, b []byte) error {
	if !utf8.Valid(b) {
		return protoErr(invalidPayload, "text message is not UTF-8")
	}
	var head struct {
		Type any `json:"type"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return protoErr(invalidPayload, "text message is not a JSON object")
	}
	typ, ok := head.Type.(string)
	if !ok {
		return protoErr(policyViolation, "missing or invalid field type")
	}
	switch typ {
	case cp.TypeCallStart, cp.TypeTransmission, cp.TypeCallEnd:
	case cp.TypeHeartbeat:
		return s.heartbeat(b)
	default:
		s.rs.Stat.UnknownTypes.Add(1)
		return nil
	}
	var e event
	if err := json.Unmarshal(b, &e); err != nil {
		var te *json.UnmarshalTypeError
		if errors.As(err, &te) {
			return protoErr(policyViolation, "%s: invalid field %s", typ, te.Field)
		}
		return protoErr(invalidPayload, "%s: %v", typ, err)
	}
	if e.SysNum == nil || e.CallNum == nil || e.StartTime == nil {
		return protoErr(policyViolation, "%s: missing field %s", typ, firstMissing(map[string]bool{
			"sys_num": e.SysNum != nil, "call_num": e.CallNum != nil, "start_time": e.StartTime != nil}))
	}
	id := cp.CallID{SysNum: *e.SysNum, CallNum: *e.CallNum, StartTime: *e.StartTime}
	switch typ {
	case cp.TypeCallStart:
		return s.callStart(ctx, id, e)
	case cp.TypeTransmission:
		return s.transmission(ctx, id, e)
	default:
		return s.callEnd(ctx, id, e)
	}
}

// maxHeartbeatSystems bounds the systems one heartbeat may relay.
const maxHeartbeatSystems = 64

// heartbeat takes the client's heartbeat (ADR 0002) for the recorder's
// signals (R14): the plugin's systems and their decode rates, or none when
// the plugin has gone silent. A system with a bad short_name is dropped and
// counted; a heartbeat that isn't one closes the socket, as any bad event
// does.
func (s *session) heartbeat(b []byte) error {
	var hb cp.Heartbeat
	if err := json.Unmarshal(b, &hb); err != nil {
		var te *json.UnmarshalTypeError
		if errors.As(err, &te) {
			return protoErr(policyViolation, "heartbeat: invalid field %s", te.Field)
		}
		return protoErr(invalidPayload, "heartbeat: %v", err)
	}
	if len(hb.Systems) > maxHeartbeatSystems {
		return protoErr(policyViolation, "heartbeat: more than %d systems", maxHeartbeatSystems)
	}
	systems := make([]cp.SystemRate, 0, len(hb.Systems))
	for _, sr := range hb.Systems {
		if n := len(sr.ShortName); n < 1 || n > 64 || !utf8.ValidString(sr.ShortName) {
			s.rs.Sig.BadHeartbeats.Add(1)
			continue
		}
		systems = append(systems, sr)
	}
	s.rs.heartbeat(s, systems)
	return nil
}

func firstMissing(present map[string]bool) string {
	for _, k := range []string{"sys_num", "call_num", "start_time", "short_name", "talkgroup", "start_time_ms", "unit", "offset_ms", "stop_time_ms"} {
		if ok, asked := present[k]; asked && !ok {
			return k
		}
	}
	return "?"
}

func (s *session) callStart(ctx context.Context, id cp.CallID, e event) error {
	if e.ShortName == nil || e.Talkgroup == nil || e.StartTimeMS == nil {
		return protoErr(policyViolation, "call_start: missing field %s", firstMissing(map[string]bool{
			"short_name": e.ShortName != nil, "talkgroup": e.Talkgroup != nil, "start_time_ms": e.StartTimeMS != nil}))
	}
	if n := len(*e.ShortName); n < 1 || n > 64 {
		return protoErr(policyViolation, "call_start: invalid field short_name")
	}
	if s.calls[id] != nil || s.refused[id] {
		return nil // resent; already decided on this socket
	}
	var (
		adm    store.Admission
		callID pgtype.UUID
	)
	err := s.rs.st.InTx(ctx, func(_ pgx.Tx, q *db.Queries) error {
		var err error
		adm, err = store.Admit(ctx, q, store.HeardCall{
			CallKey:   store.CallKey{RecorderID: s.rec.ID, SysNum: id.SysNum, CallNum: id.CallNum, StartTime: id.StartTime},
			ShortName: *e.ShortName, Talkgroup: *e.Talkgroup, Patched: e.PatchedTalkgroups,
		})
		if err != nil || adm.Rejected != "" {
			return err
		}
		p := db.UpsertLiveCallParams{
			RecorderID: s.rec.ID, SysNum: int32(id.SysNum), CallNum: id.CallNum, StartTime: id.StartTime,
			SystemID: adm.SystemID, ShortName: *e.ShortName, Talkgroup: *e.Talkgroup,
			PatchedTalkgroups: nonNil(e.PatchedTalkgroups), StartedAt: tsMS(*e.StartTimeMS),
		}
		if e.FirstPCMMS != nil && *e.FirstPCMMS > 0 {
			p.LiveFirstPcmAt = tsMS(*e.FirstPCMMS)
		}
		callID, err = q.UpsertLiveCall(ctx, p)
		if err != nil {
			return fmt.Errorf("upsert: %s", store.SafeError(err))
		}
		return nil
	})
	if err != nil {
		return protoErr(websocket.StatusInternalError, "call_start: %v", err)
	}
	s.rs.Stat.admitted(adm)
	if adm.Rejected != "" {
		// Its frames are refused (R12, R17, R18). The client isn't told: its
		// recording's upload gets the same answer.
		s.refused[id] = true
		return nil
	}
	s.rs.touchCall(ctx, s.rec, *e.StartTimeMS)
	c := &liveCall{id: callID, idText: uuidString(callID)}
	s.calls[id] = c
	if live := s.rs.cfg.Live; live != nil {
		var unit uint32
		if e.Unit != nil {
			unit = *e.Unit
		}
		live.CallStarted(c.idText, unit)
	}
	return nil
}

// unstarted counts a message for a call this socket hasn't started: one it
// refused, or one it never heard start.
func (s *session) unstarted(id cp.CallID) {
	if s.refused[id] {
		s.rs.Stat.FramesRefused.Add(1)
		return
	}
	s.rs.Stat.FramesUnstarted.Add(1)
}

// extendPatch adds a live event's patched talkgroups to the call. A patch
// that has become ignored refuses the rest of the call's live audio; its
// recording is then refused too (store.Admit).
func (s *session) extendPatch(ctx context.Context, id cp.CallID, c *liveCall, patched []int32) error {
	if len(patched) == 0 {
		return nil
	}
	state, err := s.rs.st.ExtendLivePatch(ctx, db.ExtendLivePatchParams{ID: c.id, Patched: patched})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // deleted meanwhile: refused by its recording's upload
	}
	if err != nil {
		return fmt.Errorf("patch: %s", store.SafeError(err))
	}
	if state == store.Ignored {
		if err := s.rs.st.AddLiveFrames(ctx, db.AddLiveFramesParams{ID: c.id, Frames: c.unflush}); err != nil {
			return fmt.Errorf("frames: %s", store.SafeError(err))
		}
		delete(s.calls, id)
		s.refused[id] = true
	}
	return nil
}

func (s *session) transmission(ctx context.Context, id cp.CallID, e event) error {
	if e.Unit == nil || e.OffsetMS == nil {
		return protoErr(policyViolation, "transmission: missing field %s", firstMissing(map[string]bool{
			"unit": e.Unit != nil, "offset_ms": e.OffsetMS != nil}))
	}
	if *e.OffsetMS < 0 || *e.OffsetMS > math.MaxInt32 {
		return protoErr(policyViolation, "transmission: invalid field offset_ms")
	}
	c := s.calls[id]
	if c == nil {
		s.unstarted(id)
		return nil
	}
	err := s.rs.st.AddLiveTransmission(ctx, db.AddLiveTransmissionParams{CallID: c.id, OffsetMs: int32(*e.OffsetMS), Unit: int64(*e.Unit)})
	if err != nil {
		return protoErr(websocket.StatusInternalError, "transmission: %s", store.SafeError(err))
	}
	if err := s.extendPatch(ctx, id, c, e.PatchedTalkgroups); err != nil {
		return protoErr(websocket.StatusInternalError, "transmission: %v", err)
	}
	// Told even when the patch has just become ignored: the live feed
	// reads the call again, and withdraws it.
	if live := s.rs.cfg.Live; live != nil {
		live.Transmission(c.idText, *e.Unit, int32(*e.OffsetMS))
	}
	return nil
}

func (s *session) callEnd(ctx context.Context, id cp.CallID, e event) error {
	if e.StopTimeMS == nil {
		return protoErr(policyViolation, "call_end: missing field stop_time_ms")
	}
	c := s.calls[id]
	if c == nil {
		if s.refused[id] {
			delete(s.refused, id)
			return nil
		}
		s.rs.Stat.FramesUnstarted.Add(1)
		return nil
	}
	if err := s.extendPatch(ctx, id, c, e.PatchedTalkgroups); err != nil {
		return protoErr(websocket.StatusInternalError, "call_end: %v", err)
	}
	live := s.rs.cfg.Live
	if s.refused[id] {
		// Its patch has just become ignored: the live feed withdraws it.
		delete(s.refused, id)
		if live != nil {
			live.CallEnded(c.idText)
		}
		return nil
	}
	err := s.rs.st.EndLiveCall(ctx, db.EndLiveCallParams{ID: c.id, EndedAt: tsMS(*e.StopTimeMS), Frames: c.unflush})
	if err != nil {
		return protoErr(websocket.StatusInternalError, "call_end: %s", store.SafeError(err))
	}
	delete(s.calls, id)
	if live != nil {
		live.CallEnded(c.idText)
	}
	return nil
}

// flushFrames records the frame counts of calls still live when the socket
// closes.
func (s *session) flushFrames(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for _, c := range s.calls {
		if c.unflush == 0 {
			continue
		}
		if err := s.rs.st.AddLiveFrames(ctx, db.AddLiveFramesParams{ID: c.id, Frames: c.unflush}); err != nil {
			s.log.Warn("couldn't record a live call's frames", "error", store.SafeError(err))
		}
		c.unflush = 0
	}
}
