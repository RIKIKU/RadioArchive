package clientproto

import (
	"bytes"
	"errors"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	f := Frame{CallID: CallID{SysNum: 3, CallNum: 4182, StartTime: 1764000000}, Seq: 7, Packet: []byte{0x08, 1, 2}}
	b := AppendFrame(nil, f)
	// The byte table in ADR 0002: kind, reserved, sys_num, call_num,
	// start_time, frame_seq, packet. Little-endian.
	want := []byte{1, 0, 3, 0, 0x56, 0x10, 0, 0, 0, 0, 0, 0, 0x00, 0x81, 0x24, 0x69, 0, 0, 0, 0, 7, 0, 0, 0, 0x08, 1, 2}
	if !bytes.Equal(b, want) {
		t.Fatalf("frame = % x\nwant    % x", b, want)
	}
	got, err := DecodeFrame(b)
	if err != nil || got.CallID != f.CallID || got.Seq != 7 || !bytes.Equal(got.Packet, f.Packet) {
		t.Fatalf("decode = %+v, %v", got, err)
	}
}

func TestDecodeFrameErrors(t *testing.T) {
	ok := AppendFrame(nil, Frame{Packet: []byte{1}})
	for _, tt := range []struct {
		name string
		b    []byte
		want error
	}{
		{"short", ok[:23], ErrFrameShort},
		{"unknown kind", append([]byte{2}, ok[1:]...), ErrUnknownKind},
		{"empty packet", ok[:24], ErrPacketLength},
		{"packet too long", append(ok[:24:24], make([]byte, MaxOpusPacket+1)...), ErrPacketLength},
	} {
		if _, err := DecodeFrame(tt.b); !errors.Is(err, tt.want) {
			t.Errorf("%s: %v, want %v", tt.name, err, tt.want)
		}
	}
}
