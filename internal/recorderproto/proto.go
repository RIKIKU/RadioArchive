// Package recorderproto encodes and decodes the recorder plugin -> recorder
// client protocol of ADR 0002 (docs/adr/0002-recorder-protocol.md): one UDP
// datagram per message, a 16-byte little-endian envelope, a binary audio
// payload or a JSON control payload.
//
// The fake recorder (testing/fakerecorder) encodes with it, and the recorder
// client decodes with it. The C++ recorder plugin implements the same byte
// layout on its own.
package recorderproto

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"
)

// Envelope constants.
const (
	Magic0  = 'R'
	Magic1  = 'A'
	Version = 1

	HeaderSize = 16
	// MaxDatagram is the largest datagram either side sends or accepts.
	MaxDatagram = 8192
	// MaxPayload is the largest payload that fits in MaxDatagram.
	MaxPayload = MaxDatagram - HeaderSize
)

// Message types.
const (
	TypeAudio        uint8 = 0x01
	TypeTransmission uint8 = 0x02
	TypeCallEnd      uint8 = 0x03
	TypeHeartbeat    uint8 = 0x04
	// TypeCallWithdraw (ADR 0053) is new in plugin 0.2.0. A client from
	// before it drops it as an unknown type, as ADR 0002 says.
	TypeCallWithdraw uint8 = 0x05
)

// Audio payload limits.
const (
	audioFixed      = 34
	MaxShortName    = 64
	MaxAudioSamples = 4000
)

// SampleRates are the PCM rates a v1 client accepts: 8 kHz for P25 and DMR,
// 16 kHz for trunk-recorder's analog recorders.
var SampleRates = []uint16{8000, 16000}

// Header is the envelope at the start of every datagram.
type Header struct {
	Version uint8
	Type    uint8
	BootID  uint32
	Seq     uint32
	Length  uint16
}

// Identity is a call's identity within one recorder (SPEC R2), plus the
// system's shortName.
type Identity struct {
	SysNum    uint16 `json:"sys_num"`
	CallNum   int64  `json:"call_num"`
	StartTime int64  `json:"start_time"`
	ShortName string `json:"short_name"`
}

// Audio is one audio datagram's payload.
type Audio struct {
	Identity
	Talkgroup  int32
	Unit       uint32 // 0 when unknown
	Freq       uint32 // Hz
	SampleRate uint16
	TDMASlot   int8 // -1 when not TDMA
	PCM        []byte
}

// Transmission marks a change of unit within a call.
type Transmission struct {
	Identity
	Talkgroup         int32   `json:"talkgroup"`
	Unit              uint32  `json:"unit"`
	TimeMS            int64   `json:"time_ms"`
	Freq              uint32  `json:"freq"`
	TDMASlot          int8    `json:"tdma_slot"`
	PatchedTalkgroups []int32 `json:"patched_talkgroups"`
}

// CallEndTransmission is one transmission listed in a call_end.
type CallEndTransmission struct {
	Unit        uint32 `json:"unit"`
	StartTimeMS int64  `json:"start_time_ms"`
	StopTimeMS  int64  `json:"stop_time_ms"`
}

// CallEnd is sent once trunk-recorder has written the recording.
type CallEnd struct {
	Identity
	Talkgroup         int32                 `json:"talkgroup"`
	StartTimeMS       int64                 `json:"start_time_ms"`
	StopTimeMS        int64                 `json:"stop_time_ms"`
	Freq              uint32                `json:"freq"`
	TDMASlot          int8                  `json:"tdma_slot"`
	PatchedTalkgroups []int32               `json:"patched_talkgroups"`
	Filename          string                `json:"filename"`
	Transmissions     []CallEndTransmission `json:"transmissions"`
}

// Reasons a call is withdrawn: how trunk-recorder dropped it when it
// concluded it (ADR 0053).
const (
	WithdrawNoTransmissions = "no_transmissions"
	WithdrawTooShort        = "too_short"
	WithdrawSuperseded      = "superseded"
	WithdrawEncrypted       = "encrypted"
)

// CallWithdraw says trunk-recorder concluded a call without a recording, so
// no call_end will come for it (ADR 0053).
type CallWithdraw struct {
	Identity
	Talkgroup int32  `json:"talkgroup"`
	TimeMS    int64  `json:"time_ms"`
	Reason    string `json:"reason"`
}

// SystemRate is one system's control-channel decode rate.
type SystemRate struct {
	SysNum     uint16  `json:"sys_num"`
	ShortName  string  `json:"short_name"`
	SystemType string  `json:"system_type"`
	DecodeRate float64 `json:"decode_rate"`
}

// Heartbeat is sent from trunk-recorder's system_rates callback.
type Heartbeat struct {
	TimeMS        int64        `json:"time_ms"`
	PluginVersion string       `json:"plugin_version"`
	Systems       []SystemRate `json:"systems"`
}

// Message is one decoded datagram. Exactly one of the payload fields is set.
type Message struct {
	Header       Header
	Audio        *Audio
	Transmission *Transmission
	CallEnd      *CallEnd
	Heartbeat    *Heartbeat
	CallWithdraw *CallWithdraw
}

// DecodeError says why a datagram was rejected. Reason is a short, fixed
// label, suitable for a metric.
type DecodeError struct {
	Reason string
	Detail string
}

func (e *DecodeError) Error() string {
	if e.Detail == "" {
		return "recorderproto: " + e.Reason
	}
	return "recorderproto: " + e.Reason + ": " + e.Detail
}

// ErrUnknownType is returned, with a valid Header, for a message type this
// version doesn't know. The client drops and counts it.
var ErrUnknownType = errors.New("recorderproto: unknown message type")

func malformed(reason, format string, a ...any) error {
	return &DecodeError{Reason: reason, Detail: fmt.Sprintf(format, a...)}
}

func appendHeader(dst []byte, typ uint8, bootID, seq uint32, length int) []byte {
	dst = append(dst, Magic0, Magic1, Version, typ)
	dst = binary.LittleEndian.AppendUint32(dst, bootID)
	dst = binary.LittleEndian.AppendUint32(dst, seq)
	dst = binary.LittleEndian.AppendUint16(dst, uint16(length))
	return binary.LittleEndian.AppendUint16(dst, 0)
}

func validShortName(s string) bool {
	return len(s) >= 1 && len(s) <= MaxShortName && utf8.ValidString(s)
}

func validRate(r uint16) bool {
	for _, ok := range SampleRates {
		if r == ok {
			return true
		}
	}
	return false
}

// EncodeAudio returns one audio datagram. It refuses what the client would
// refuse: a bad shortName or sample rate, or PCM that's empty, odd-length or
// longer than MaxAudioSamples.
func EncodeAudio(bootID, seq uint32, a Audio) ([]byte, error) {
	if !validShortName(a.ShortName) {
		return nil, fmt.Errorf("recorderproto: short_name must be 1-%d bytes of UTF-8", MaxShortName)
	}
	if !validRate(a.SampleRate) {
		return nil, fmt.Errorf("recorderproto: unsupported sample rate %d", a.SampleRate)
	}
	if len(a.PCM) == 0 || len(a.PCM)%2 != 0 || len(a.PCM) > 2*MaxAudioSamples {
		return nil, fmt.Errorf("recorderproto: PCM must be 2-%d bytes, even, got %d", 2*MaxAudioSamples, len(a.PCM))
	}
	n := audioFixed + len(a.ShortName) + len(a.PCM)
	b := make([]byte, 0, HeaderSize+n)
	b = appendHeader(b, TypeAudio, bootID, seq, n)
	b = binary.LittleEndian.AppendUint64(b, uint64(a.CallNum))
	b = binary.LittleEndian.AppendUint64(b, uint64(a.StartTime))
	b = binary.LittleEndian.AppendUint32(b, uint32(a.Talkgroup))
	b = binary.LittleEndian.AppendUint32(b, a.Unit)
	b = binary.LittleEndian.AppendUint32(b, a.Freq)
	b = binary.LittleEndian.AppendUint16(b, a.SysNum)
	b = binary.LittleEndian.AppendUint16(b, a.SampleRate)
	b = append(b, byte(a.TDMASlot), byte(len(a.ShortName)))
	b = append(b, a.ShortName...)
	return append(b, a.PCM...), nil
}

// EncodeControl returns one control datagram with v (a *Transmission,
// *CallEnd, *Heartbeat or *CallWithdraw, or the value) as its JSON payload.
func EncodeControl(bootID, seq uint32, v any) ([]byte, error) {
	var typ uint8
	switch v.(type) {
	case Transmission, *Transmission:
		typ = TypeTransmission
	case CallEnd, *CallEnd:
		typ = TypeCallEnd
	case Heartbeat, *Heartbeat:
		typ = TypeHeartbeat
	case CallWithdraw, *CallWithdraw:
		typ = TypeCallWithdraw
	default:
		return nil, fmt.Errorf("recorderproto: %T is not a control message", v)
	}
	p, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if len(p) > MaxPayload {
		return nil, fmt.Errorf("recorderproto: payload %d bytes, max %d", len(p), MaxPayload)
	}
	b := make([]byte, 0, HeaderSize+len(p))
	b = appendHeader(b, typ, bootID, seq, len(p))
	return append(b, p...), nil
}

// Decode parses one datagram. A malformed datagram gives a *DecodeError. An
// unknown type gives ErrUnknownType and the Header. Decode never panics.
func Decode(b []byte) (Message, error) {
	var m Message
	if len(b) < HeaderSize {
		return m, malformed("short", "%d bytes", len(b))
	}
	if b[0] != Magic0 || b[1] != Magic1 {
		return m, malformed("magic", "% x", b[:2])
	}
	if b[2] != Version {
		return m, malformed("version", "%d", b[2])
	}
	if len(b) > MaxDatagram {
		return m, malformed("too_large", "%d bytes", len(b))
	}
	h := Header{
		Version: b[2],
		Type:    b[3],
		BootID:  binary.LittleEndian.Uint32(b[4:]),
		Seq:     binary.LittleEndian.Uint32(b[8:]),
		Length:  binary.LittleEndian.Uint16(b[12:]),
	}
	if int(h.Length) != len(b)-HeaderSize {
		return m, malformed("length", "header says %d, payload is %d", h.Length, len(b)-HeaderSize)
	}
	m.Header = h
	p := b[HeaderSize:]
	switch h.Type {
	case TypeAudio:
		a, err := decodeAudio(p)
		m.Audio = a
		return m, err
	case TypeTransmission:
		var t Transmission
		err := decodeJSON(p, &t, true)
		if err == nil {
			m.Transmission = &t
		}
		return m, err
	case TypeCallEnd:
		var c CallEnd
		err := decodeJSON(p, &c, true)
		if err == nil {
			m.CallEnd = &c
		}
		return m, err
	case TypeHeartbeat:
		var hb Heartbeat
		err := decodeJSON(p, &hb, false)
		if err == nil {
			m.Heartbeat = &hb
		}
		return m, err
	case TypeCallWithdraw:
		var w CallWithdraw
		err := decodeJSON(p, &w, true)
		if err == nil {
			m.CallWithdraw = &w
		}
		return m, err
	default:
		return m, ErrUnknownType
	}
}

func decodeAudio(p []byte) (*Audio, error) {
	if len(p) < audioFixed {
		return nil, malformed("audio_short", "%d bytes", len(p))
	}
	le := binary.LittleEndian
	a := &Audio{
		Identity: Identity{
			CallNum:   int64(le.Uint64(p[0:])),
			StartTime: int64(le.Uint64(p[8:])),
			SysNum:    le.Uint16(p[28:]),
		},
		Talkgroup:  int32(le.Uint32(p[16:])),
		Unit:       le.Uint32(p[20:]),
		Freq:       le.Uint32(p[24:]),
		SampleRate: le.Uint16(p[30:]),
		TDMASlot:   int8(p[32]),
	}
	n := int(p[33])
	if n < 1 || n > MaxShortName || audioFixed+n > len(p) {
		return nil, malformed("short_name", "length %d", n)
	}
	a.ShortName = string(p[audioFixed : audioFixed+n])
	if !utf8.ValidString(a.ShortName) {
		return nil, malformed("short_name", "invalid UTF-8")
	}
	if !validRate(a.SampleRate) {
		return nil, malformed("sample_rate", "%d", a.SampleRate)
	}
	pcm := p[audioFixed+n:]
	switch {
	case len(pcm) == 0:
		return nil, malformed("pcm", "empty")
	case len(pcm)%2 != 0:
		return nil, malformed("pcm", "odd length %d", len(pcm))
	case len(pcm) > 2*MaxAudioSamples:
		return nil, malformed("pcm", "%d bytes", len(pcm))
	}
	a.PCM = bytes.Clone(pcm)
	return a, nil
}

var identityKeys = []string{"sys_num", "call_num", "start_time", "short_name"}

func decodeJSON(p []byte, v any, call bool) error {
	if !utf8.Valid(p) {
		return malformed("json", "invalid UTF-8")
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(p, &keys); err != nil {
		return malformed("json", "%v", err)
	}
	if call {
		for _, k := range identityKeys {
			if _, ok := keys[k]; !ok {
				return malformed("identity", "missing %s", k)
			}
		}
	}
	if err := json.Unmarshal(p, v); err != nil {
		return malformed("json", "%v", err)
	}
	if call {
		var id *Identity
		switch x := v.(type) {
		case *Transmission:
			id = &x.Identity
		case *CallEnd:
			id = &x.Identity
		case *CallWithdraw:
			id = &x.Identity
		}
		if id != nil && !validShortName(id.ShortName) {
			return malformed("identity", "short_name")
		}
	}
	return nil
}
