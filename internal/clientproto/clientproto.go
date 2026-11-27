// Package clientproto is the recorder client -> server protocol of ADR 0002
// (docs/adr/0002-recorder-protocol.md): the live WebSocket's binary Opus
// frames and JSON events, and the recording upload's metadata and answers.
// The client and the server both use it, so they can't drift apart.
package clientproto

import (
	"encoding/binary"
	"errors"
)

// Protocol is the client -> server protocol version this build speaks.
const Protocol = 1

// Paths and headers.
const (
	LivePath       = "/api/v1/recorder/live"
	RecordingsPath = "/api/v1/recorder/recordings"

	HeaderClientVersion = "RadioArchive-Client-Version"
	HeaderProtocol      = "RadioArchive-Protocol"
)

// Limits.
const (
	FrameHeaderSize = 24
	MaxOpusPacket   = 1275
	MaxTextMessage  = 64 << 10
	MaxRecording    = 64 << 20
	MaxMetadata     = 64 << 10
)

// KindOpus is the only binary message kind.
const KindOpus = 0x01

// CallID is R2's identity without the recorder, which is the key that
// authenticated the socket.
type CallID struct {
	SysNum    uint16 `json:"sys_num"`
	CallNum   int64  `json:"call_num"`
	StartTime int64  `json:"start_time"`
}

// Frame is one binary message: one 20 ms Opus packet of a live call.
type Frame struct {
	CallID
	Seq    uint32
	Packet []byte
}

// AppendFrame encodes f.
func AppendFrame(dst []byte, f Frame) []byte {
	dst = append(dst, KindOpus, 0)
	dst = binary.LittleEndian.AppendUint16(dst, f.SysNum)
	dst = binary.LittleEndian.AppendUint64(dst, uint64(f.CallNum))
	dst = binary.LittleEndian.AppendUint64(dst, uint64(f.StartTime))
	dst = binary.LittleEndian.AppendUint32(dst, f.Seq)
	return append(dst, f.Packet...)
}

// Frame decoding errors.
var (
	ErrFrameShort   = errors.New("binary message shorter than 24 bytes")
	ErrUnknownKind  = errors.New("unknown binary message kind")
	ErrPacketLength = errors.New("opus packet must be 1-1275 bytes")
)

// DecodeFrame parses a binary message. The packet aliases b.
func DecodeFrame(b []byte) (Frame, error) {
	if len(b) < FrameHeaderSize {
		return Frame{}, ErrFrameShort
	}
	if b[0] != KindOpus {
		return Frame{}, ErrUnknownKind
	}
	le := binary.LittleEndian
	f := Frame{
		CallID: CallID{
			SysNum:    le.Uint16(b[2:]),
			CallNum:   int64(le.Uint64(b[4:])),
			StartTime: int64(le.Uint64(b[12:])),
		},
		Seq:    le.Uint32(b[20:]),
		Packet: b[FrameHeaderSize:],
	}
	if len(f.Packet) < 1 || len(f.Packet) > MaxOpusPacket {
		return Frame{}, ErrPacketLength
	}
	return f, nil
}

// Event types on the live socket.
const (
	TypeWelcome      = "welcome"
	TypeCallStart    = "call_start"
	TypeTransmission = "transmission"
	TypeCallEnd      = "call_end"
	TypeHeartbeat    = "heartbeat"
)

// Welcome is the server's first message.
type Welcome struct {
	Type          string `json:"type"`
	ServerVersion string `json:"server_version"`
	Protocol      int    `json:"protocol"`
}

// CallStart starts a live call. The client sends it again for every call
// still live after it reconnects.
type CallStart struct {
	Type string `json:"type"`
	CallID
	ShortName         string  `json:"short_name"`
	Talkgroup         int32   `json:"talkgroup"`
	Unit              uint32  `json:"unit"`
	PatchedTalkgroups []int32 `json:"patched_talkgroups"`
	StartTimeMS       int64   `json:"start_time_ms"`
	FirstPCMMS        int64   `json:"first_pcm_ms,omitempty"`
}

// Transmission is a change of unit within a live call.
type Transmission struct {
	Type string `json:"type"`
	CallID
	Unit              uint32  `json:"unit"`
	OffsetMS          int64   `json:"offset_ms"`
	PatchedTalkgroups []int32 `json:"patched_talkgroups"`
}

// CallEnd ends a live call. Its recording follows by upload, unless
// Withdrawn says none will: trunk-recorder concluded the call without one
// (ADR 0053), and the server withdraws it. A server from before ADR 0053
// ignores the field and ends the call as any other.
type CallEnd struct {
	Type string `json:"type"`
	CallID
	StopTimeMS        int64   `json:"stop_time_ms"`
	PatchedTalkgroups []int32 `json:"patched_talkgroups"`
	Withdrawn         bool    `json:"withdrawn,omitempty"`
}

// Heartbeat is the client's, every 5 s.
type Heartbeat struct {
	Type          string       `json:"type"`
	ClientVersion string       `json:"client_version"`
	TimeMS        int64        `json:"time_ms"`
	Plugin        PluginState  `json:"plugin"`
	Systems       []SystemRate `json:"systems"`
	Queue         QueueState   `json:"queue"`
	UDP           UDPCounters  `json:"udp"`
}

// PluginState is what the client last heard from the recorder plugin.
type PluginState struct {
	LastHeartbeatMS int64  `json:"last_heartbeat_ms"`
	BootID          uint32 `json:"boot_id"`
}

// SystemRate is one system's control-channel decode rate, relayed from the
// plugin.
type SystemRate struct {
	SysNum     uint16  `json:"sys_num"`
	ShortName  string  `json:"short_name"`
	SystemType string  `json:"system_type"`
	DecodeRate float64 `json:"decode_rate"`
}

// QueueState is the client's queue of unsent recordings (R5).
type QueueState struct {
	Count      int   `json:"count"`
	OldestAgeS int64 `json:"oldest_age_s"`
}

// UDPCounters are totals since the client started.
type UDPCounters struct {
	Lost        uint64 `json:"lost"`
	Malformed   uint64 `json:"malformed"`
	PatchCopies uint64 `json:"patch_copies"`
}

// RecordingTransmission is one transmission in a recording's metadata.
type RecordingTransmission struct {
	Unit       uint32 `json:"unit"`
	OffsetMS   int64  `json:"offset_ms"`
	DurationMS int64  `json:"duration_ms"`
}

// RecordingMetadata is the upload's first part.
type RecordingMetadata struct {
	CallID
	ShortName         string                  `json:"short_name"`
	Talkgroup         int32                   `json:"talkgroup"`
	PatchedTalkgroups []int32                 `json:"patched_talkgroups"`
	StartTimeMS       int64                   `json:"start_time_ms"`
	StopTimeMS        int64                   `json:"stop_time_ms"`
	Transmissions     []RecordingTransmission `json:"transmissions"`
	SHA256            string                  `json:"sha256"`
	Size              int64                   `json:"size"`
}

// Upload statuses.
const (
	StatusStored    = "stored"
	StatusDuplicate = "duplicate"
	StatusRejected  = "rejected"
)

// UploadResponse is the server's answer to an upload: Status for 200 and
// 201, Error otherwise.
type UploadResponse struct {
	Status string `json:"status,omitempty"`
	Error  string `json:"error,omitempty"`
	CallID string `json:"call_id,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Size   int64  `json:"size,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// Refusals before the WebSocket upgrade or of an upload.
const (
	ErrInvalidClientKey    = "invalid_client_key"
	ErrClientTooOld        = "client_too_old"
	ErrUnsupportedProtocol = "unsupported_protocol"
)

// ErrorResponse is a refusal before the WebSocket upgrade or of an upload.
type ErrorResponse struct {
	Error         string `json:"error"`
	ClientVersion string `json:"client_version,omitempty"` // client_too_old
	ServerVersion string `json:"server_version,omitempty"` // client_too_old
	Protocol      *int   `json:"protocol,omitempty"`
	Supported     []int  `json:"supported,omitempty"`
}
