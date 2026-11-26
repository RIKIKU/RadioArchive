package recorderclient

import (
	"encoding/binary"
	"errors"
	"log/slog"
	"os"
	"slices"
	"time"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
	rp "github.com/RIKIKU/RadioArchive/internal/recorderproto"
	"github.com/RIKIKU/RadioArchive/internal/version"
)

// SampleRates are the PCM rates the client takes, live and recorded, and it
// encodes each at its own rate (ADR 0046). trunk-recorder's P25 recorders
// hand the plugin 8 kHz, so live audio from a P25 system is 8 kHz, but
// trunk-recorder v5 renders every call's WAV through ffmpeg at 16 kHz
// (call_concluder.cc, `-ar 16000`). Opus takes both rates natively, and
// R4's narrowband settings make the same kind of stream from either. Any
// other rate is dropped like malformed input.
var SampleRates = []int{8000, 16000}

func supportedRate(rate int) bool { return slices.Contains(SampleRates, rate) }

// Timings from ADR 0002.
const (
	idleEnd       = 30 * time.Second // end a call's live audio after this long with no packets
	pluginSilent  = 10 * time.Second // after this, the heartbeat relays no systems
	copyForget    = time.Minute      // forget a dropped copy this long after its last packet
	logEvery      = time.Minute      // at most one log line per malformed reason per minute
	heartbeatTick = 5 * time.Second
)

// Encoder is a 20 ms Opus encoder (internal/opus in the real client).
type Encoder interface {
	FrameSamples() int
	Lookahead() int
	Encode(pcm []int16) ([]byte, error)
	Close()
}

// NewEncoder makes an Encoder for mono PCM at rate.
type NewEncoder func(rate int) (Encoder, error)

// Live sends to the server's live WebSocket. Both drop the message when the
// socket is down: live audio missed in an outage is lost (R5).
type Live interface {
	SendText(v any)
	SendFrame(b []byte)
}

// Recording is a finished call to upload.
type Recording struct {
	Meta   cp.RecordingMetadata // without SHA256 and Size, which the uploader fills
	WAV    string               // trunk-recorder's file
	Queued time.Time
}

// Uploader takes finished calls' recordings: R5's disk queue (queue.go,
// upload.go). Enqueue returns once the entry is on disk.
type Uploader interface {
	Enqueue(Recording)
	Queue() cp.QueueState
}

// call is one call the client is tracking.
type call struct {
	id        rp.Identity
	talkgroup int32
	freq      uint32
	slot      int8
	patched   []int32 // union seen in transmissions and call_end, minus talkgroup

	started    bool  // call_start sent
	startMS    int64 // the live call's start, for offsets
	firstPCMMS int64
	unit       uint32
	pending    []rp.Transmission // transmissions before call_start

	lastPacket time.Time
	lastAudio  time.Time

	rate     int // the live PCM's rate, from the call's first audio
	enc      Encoder
	encErr   bool
	buf      []int16
	frameSeq uint32
}

func (c *call) channel() channelCall {
	return channelCall{SysNum: c.id.SysNum, ShortName: c.id.ShortName, Freq: c.freq, TDMASlot: c.slot,
		Talkgroup: c.talkgroup, Patched: c.patched, LastAudio: c.lastAudio}
}

func (c *call) callID() cp.CallID {
	return cp.CallID{SysNum: c.id.SysNum, CallNum: c.id.CallNum, StartTime: c.id.StartTime}
}

// engine turns plugin datagrams into live frames, events and recordings.
// It's used from one goroutine.
type engine struct {
	log     *slog.Logger
	m       *Metrics
	live    Live
	up      Uploader
	newEnc  NewEncoder
	now     func() time.Time
	remove  func(string) error // os.Remove; deletes a dropped copy's WAV
	version string             // this client's, in the heartbeat

	calls   map[rp.Identity]*call
	copies  map[rp.Identity]time.Time // dropped patch copies, by last packet
	logged  map[string]time.Time      // malformed reason -> last log
	seqBoot uint32
	seqLast uint32
	seqSeen bool

	plugin     *rp.Heartbeat
	pluginBoot uint32
	pluginAt   time.Time
}

func newEngine(log *slog.Logger, m *Metrics, live Live, up Uploader, newEnc NewEncoder, now func() time.Time) *engine {
	return &engine{log: log, m: m, live: live, up: up, newEnc: newEnc, now: now, remove: os.Remove, version: version.Version,
		calls: map[rp.Identity]*call{}, copies: map[rp.Identity]time.Time{}, logged: map[string]time.Time{}}
}

func (e *engine) malformed(reason, detail string) {
	e.m.Inc(MetricMalformed, "reason", reason)
	now := e.now()
	if last, ok := e.logged[reason]; ok && now.Sub(last) < logEvery {
		return
	}
	e.logged[reason] = now
	e.log.Warn("dropped a malformed datagram from the recorder plugin (logged at most once a minute per reason)",
		"reason", reason, "detail", detail)
}

// handle takes one datagram. It never panics on bad input.
func (e *engine) handle(b []byte) {
	msg, err := rp.Decode(b)
	late := false
	if msg.Header.Version == rp.Version {
		late = e.track(msg.Header)
	}
	if errors.Is(err, rp.ErrUnknownType) {
		e.m.Inc(MetricUnknownType)
		return
	}
	var de *rp.DecodeError
	if errors.As(err, &de) {
		e.malformed(de.Reason, de.Detail)
		return
	}
	if err != nil {
		e.malformed("other", err.Error())
		return
	}
	switch {
	case msg.Audio != nil:
		if late {
			e.m.Inc(MetricLate) // no longer live; the recording has it
			return
		}
		e.audio(msg.Audio)
	case msg.Transmission != nil:
		e.transmission(msg.Transmission)
	case msg.CallEnd != nil:
		e.callEnd(msg.CallEnd)
	case msg.Heartbeat != nil:
		e.plugin, e.pluginBoot, e.pluginAt = msg.Heartbeat, msg.Header.BootID, e.now()
	}
}

// track follows the plugin's sequence numbers: a forward jump is loss, a
// step back is a late packet, and a new boot ID starts afresh.
func (e *engine) track(h rp.Header) (late bool) {
	if !e.seqSeen || h.BootID != e.seqBoot {
		e.seqBoot, e.seqLast, e.seqSeen = h.BootID, h.Seq, true
		return false
	}
	d := int32(h.Seq - e.seqLast) // wraps at 2^32
	if d <= 0 {
		return true
	}
	if d > 1 {
		e.m.Add(uint64(d-1), MetricLost)
	}
	e.seqLast = h.Seq
	return false
}

// find returns the call for id, starting to track it if it's new. It
// returns nil for a patch copy, which is dropped.
func (e *engine) find(id rp.Identity, talkgroup int32, freq uint32, slot int8, patched []int32) *call {
	now := e.now()
	if _, ok := e.copies[id]; ok {
		e.copies[id] = now
		return nil
	}
	if c := e.calls[id]; c != nil {
		return c
	}
	c := &call{id: id, talkgroup: talkgroup, freq: freq, slot: slot}
	c.addPatched(patched)
	for _, a := range e.calls {
		if isPatchCopy(a.channel(), c.channel(), now) {
			e.copies[id] = now
			e.m.Inc(MetricPatchCopies)
			e.log.Info("dropping a patched copy of a call on the same channel",
				"sys_num", id.SysNum, "call_num", id.CallNum, "talkgroup", talkgroup,
				"kept_call_num", a.id.CallNum, "kept_talkgroup", a.talkgroup)
			return nil
		}
	}
	e.calls[id] = c
	return c
}

func (c *call) addPatched(tgs []int32) {
	for _, tg := range tgs {
		if tg != c.talkgroup && !slices.Contains(c.patched, tg) {
			c.patched = append(c.patched, tg)
		}
	}
}

func (c *call) patchedList() []int32 {
	if c.patched == nil {
		return []int32{}
	}
	return slices.Clone(c.patched)
}

func (e *engine) audio(a *rp.Audio) {
	rate := int(a.SampleRate)
	if !supportedRate(rate) {
		e.malformed("sample_rate", "unsupported")
		return
	}
	c := e.find(a.Identity, a.Talkgroup, a.Freq, a.TDMASlot, nil)
	if c == nil {
		return
	}
	// One encoder per call, at its first audio's rate. A recorder doesn't
	// change rate mid-call; if a datagram says it did, its PCM would play
	// at the wrong speed, so it's dropped.
	if c.rate == 0 {
		c.rate = rate
	} else if rate != c.rate {
		e.malformed("sample_rate", "changed mid-call")
		return
	}
	now := e.now()
	c.lastPacket, c.lastAudio = now, now
	if c.unit == 0 {
		c.unit = a.Unit
	}
	if !c.started {
		c.firstPCMMS = now.UnixMilli()
		e.start(c)
	}
	e.encode(c, a.PCM)
}

func (e *engine) transmission(t *rp.Transmission) {
	c := e.find(t.Identity, t.Talkgroup, t.Freq, t.TDMASlot, t.PatchedTalkgroups)
	if c == nil {
		return
	}
	c.lastPacket = e.now()
	c.addPatched(t.PatchedTalkgroups)
	if !c.started {
		if c.startMS == 0 {
			c.startMS = t.TimeMS
		}
		if c.unit == 0 {
			c.unit = t.Unit
		}
		c.pending = append(c.pending, *t)
		return
	}
	e.sendTransmission(c, *t)
}

func (e *engine) sendTransmission(c *call, t rp.Transmission) {
	e.live.SendText(cp.Transmission{Type: cp.TypeTransmission, CallID: c.callID(), Unit: t.Unit,
		OffsetMS: max(0, t.TimeMS-c.startMS), PatchedTalkgroups: c.patchedList()})
}

func (e *engine) callStart(c *call) cp.CallStart {
	return cp.CallStart{Type: cp.TypeCallStart, CallID: c.callID(), ShortName: c.id.ShortName,
		Talkgroup: c.talkgroup, Unit: c.unit, PatchedTalkgroups: c.patchedList(),
		StartTimeMS: c.startMS, FirstPCMMS: c.firstPCMMS}
}

// start sends call_start, then the transmissions seen before it.
func (e *engine) start(c *call) {
	if c.startMS == 0 {
		c.startMS = c.firstPCMMS
	}
	c.started = true
	e.live.SendText(e.callStart(c))
	for _, t := range c.pending {
		e.sendTransmission(c, t)
	}
	c.pending = nil
}

// encode sends a frame for every 20 ms of PCM buffered: 160 samples at
// 8 kHz, 320 at 16 kHz.
func (e *engine) encode(c *call, pcm []byte) {
	if c.enc == nil && !c.encErr {
		enc, err := e.newEnc(c.rate)
		if err != nil {
			c.encErr = true
			e.log.Error("can't create an Opus encoder; the call's live audio is lost", "error", err)
			return
		}
		c.enc = enc
	}
	if c.enc == nil {
		return
	}
	for i := 0; i+1 < len(pcm); i += 2 {
		c.buf = append(c.buf, int16(binary.LittleEndian.Uint16(pcm[i:])))
	}
	e.frames(c)
}

func (e *engine) frames(c *call) {
	n := c.enc.FrameSamples()
	sent := 0
	for len(c.buf)-sent >= n {
		pkt, err := c.enc.Encode(c.buf[sent : sent+n])
		sent += n
		if err != nil {
			e.m.Inc(MetricLiveDropped, "reason", "encode")
			continue
		}
		e.live.SendFrame(cp.AppendFrame(make([]byte, 0, cp.FrameHeaderSize+len(pkt)),
			cp.Frame{CallID: c.callID(), Seq: c.frameSeq, Packet: pkt}))
		c.frameSeq++
	}
	c.buf = append(c.buf[:0], c.buf[sent:]...)
}

// finish sends the last partial frame, padded with silence, and stops
// tracking the call.
func (e *engine) finish(c *call) {
	if c.enc != nil {
		if r := len(c.buf); r > 0 {
			c.buf = append(c.buf, make([]int16, c.enc.FrameSamples()-r)...)
			e.frames(c)
		}
		c.enc.Close()
		c.enc = nil
	}
	delete(e.calls, c.id)
}

// dropCopy ends a patch copy. Its recording is never sent, and its WAV is
// deleted: the kept call's recording holds the same audio, and there's no
// reason to keep trunk-recorder's files (owner, issue #31).
func (e *engine) dropCopy(ce *rp.CallEnd) {
	delete(e.copies, ce.Identity)
	if ce.Filename == "" {
		return
	}
	if err := e.remove(ce.Filename); err != nil && !errors.Is(err, os.ErrNotExist) {
		e.log.Warn("couldn't delete a patch copy's WAV", "file", ce.Filename, "error", err)
	}
}

func (e *engine) callEnd(ce *rp.CallEnd) {
	if _, ok := e.copies[ce.Identity]; ok {
		e.dropCopy(ce)
		return
	}
	c := e.find(ce.Identity, ce.Talkgroup, ce.Freq, ce.TDMASlot, ce.PatchedTalkgroups)
	if c == nil {
		e.dropCopy(ce)
		return
	}
	c.addPatched(ce.PatchedTalkgroups)
	if !c.started {
		// Nothing live reached the client (or it restarted): the call still
		// starts and ends on the server, and its recording follows.
		if c.startMS == 0 {
			c.startMS = ce.StartTimeMS
		}
		e.start(c)
	}
	e.finish(c)
	e.live.SendText(cp.CallEnd{Type: cp.TypeCallEnd, CallID: c.callID(), StopTimeMS: ce.StopTimeMS,
		PatchedTalkgroups: c.patchedList()})

	meta := cp.RecordingMetadata{CallID: c.callID(), ShortName: ce.ShortName, Talkgroup: ce.Talkgroup,
		PatchedTalkgroups: c.patchedList(), StartTimeMS: ce.StartTimeMS, StopTimeMS: ce.StopTimeMS,
		Transmissions: []cp.RecordingTransmission{}}
	for _, t := range ce.Transmissions {
		meta.Transmissions = append(meta.Transmissions, cp.RecordingTransmission{Unit: t.Unit,
			OffsetMS: max(0, t.StartTimeMS-ce.StartTimeMS), DurationMS: max(0, t.StopTimeMS-t.StartTimeMS)})
	}
	e.up.Enqueue(Recording{Meta: meta, WAV: ce.Filename, Queued: e.now()})
}

// tick ends the live audio of calls that have gone quiet (without an
// upload: that needs call_end) and forgets old copies.
func (e *engine) tick() {
	now := e.now()
	for _, c := range e.calls {
		if now.Sub(c.lastPacket) < idleEnd {
			continue
		}
		if c.started {
			e.finish(c)
			e.live.SendText(cp.CallEnd{Type: cp.TypeCallEnd, CallID: c.callID(),
				StopTimeMS: c.lastPacket.UnixMilli(), PatchedTalkgroups: c.patchedList()})
		} else {
			delete(e.calls, c.id)
		}
		e.log.Info("ended a call's live audio after 30 s with no packets", "sys_num", c.id.SysNum, "call_num", c.id.CallNum)
	}
	for id, at := range e.copies {
		if now.Sub(at) > copyForget {
			delete(e.copies, id)
		}
	}
}

// reconnected resends call_start for every call still live, so the server
// needs nothing from before the socket opened.
func (e *engine) reconnected() {
	for _, c := range e.calls {
		if c.started {
			e.live.SendText(e.callStart(c))
		}
	}
	e.live.SendText(e.heartbeat())
}

func (e *engine) heartbeat() cp.Heartbeat {
	now := e.now()
	hb := cp.Heartbeat{Type: cp.TypeHeartbeat, ClientVersion: e.version, TimeMS: now.UnixMilli(),
		Systems: []cp.SystemRate{}, Queue: e.up.Queue(),
		UDP: cp.UDPCounters{Lost: e.m.Sum(MetricLost), Malformed: e.m.Sum(MetricMalformed),
			PatchCopies: e.m.Sum(MetricPatchCopies)}}
	if e.plugin != nil {
		hb.Plugin = cp.PluginState{LastHeartbeatMS: e.pluginAt.UnixMilli(), BootID: e.pluginBoot}
		if now.Sub(e.pluginAt) < pluginSilent {
			for _, s := range e.plugin.Systems {
				hb.Systems = append(hb.Systems, cp.SystemRate(s))
			}
		}
	}
	return hb
}
