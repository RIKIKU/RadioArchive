package recorderproto_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"

	rp "github.com/RIKIKU/RadioArchive/internal/recorderproto"
)

var sampleAudio = rp.Audio{
	Identity:   rp.Identity{SysNum: 2, CallNum: 4182, StartTime: 1764000000, ShortName: "ex"},
	Talkgroup:  1001,
	Unit:       5001,
	Freq:       851012500,
	SampleRate: 8000,
	TDMASlot:   -1,
	PCM:        []byte{0x01, 0x00, 0xff, 0xff},
}

// The byte layout of ADR 0002, written out by hand so the encoder can't drift
// from the document without this failing.
func TestAudioGoldenBytes(t *testing.T) {
	got, err := rp.EncodeAudio(0xA1B2C3D4, 7, sampleAudio)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"5241", "01", "01", // magic "RA", version 1, type audio
		"d4c3b2a1",         // boot_id
		"07000000",         // seq
		"2800",             // length = 34 + 2 + 4 = 40
		"0000",             // reserved
		"5610000000000000", // call_num 4182
		"0081246900000000", // start_time 1764000000
		"e9030000",         // talkgroup 1001
		"89130000",         // unit 5001
		"946bb932",         // freq 851012500
		"0200",             // sys_num 2
		"401f",             // sample_rate 8000
		"ff",               // tdma_slot -1
		"02", "6578",       // short_name "ex"
		"0100ffff", // PCM
	}, "")
	if hex.EncodeToString(got) != want {
		t.Fatalf("audio datagram\n got %x\nwant %s", got, want)
	}
}

func TestRoundTrip(t *testing.T) {
	b, err := rp.EncodeAudio(1, 2, sampleAudio)
	if err != nil {
		t.Fatal(err)
	}
	m, err := rp.Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if m.Header.Type != rp.TypeAudio || m.Header.BootID != 1 || m.Header.Seq != 2 {
		t.Fatalf("header %+v", m.Header)
	}
	if !reflect.DeepEqual(*m.Audio, sampleAudio) {
		t.Fatalf("audio\n got %+v\nwant %+v", *m.Audio, sampleAudio)
	}

	id := sampleAudio.Identity
	ctrl := []any{
		rp.Transmission{Identity: id, Talkgroup: 1001, Unit: 5123, TimeMS: 1764000004950, Freq: 851012500, TDMASlot: -1, PatchedTalkgroups: []int32{1002}},
		rp.CallEnd{Identity: id, Talkgroup: 1001, StartTimeMS: 1, StopTimeMS: 2, Filename: "/a.wav", PatchedTalkgroups: []int32{},
			Transmissions: []rp.CallEndTransmission{{Unit: 5001, StartTimeMS: 1, StopTimeMS: 2}}},
		rp.Heartbeat{TimeMS: 3, PluginVersion: "0.1.0", Systems: []rp.SystemRate{{SysNum: 0, ShortName: "ex", SystemType: "p25", DecodeRate: 0}}},
		rp.CallWithdraw{Identity: id, Talkgroup: 30583, TimeMS: 4, Reason: rp.WithdrawNoTransmissions},
	}
	for i, v := range ctrl {
		b, err := rp.EncodeControl(9, uint32(i), v)
		if err != nil {
			t.Fatal(err)
		}
		m, err := rp.Decode(b)
		if err != nil {
			t.Fatalf("%T: %v", v, err)
		}
		var got any
		switch {
		case m.Transmission != nil:
			got = *m.Transmission
		case m.CallEnd != nil:
			got = *m.CallEnd
		case m.Heartbeat != nil:
			got = *m.Heartbeat
		case m.CallWithdraw != nil:
			got = *m.CallWithdraw
		}
		if !reflect.DeepEqual(got, v) {
			t.Fatalf("%T\n got %+v\nwant %+v", v, got, v)
		}
	}
}

func header(typ uint8, payload []byte) []byte {
	b := []byte{'R', 'A', 1, typ, 0, 0, 0, 0, 0, 0, 0, 0, byte(len(payload)), byte(len(payload) >> 8), 0, 0}
	return append(b, payload...)
}

func TestMalformed(t *testing.T) {
	good, _ := rp.EncodeAudio(1, 1, sampleAudio)
	mutate := func(f func(b []byte) []byte) []byte { return f(bytes.Clone(good)) }
	cases := []struct {
		name, reason string
		b            []byte
	}{
		{"empty", "short", nil},
		{"bad magic", "magic", mutate(func(b []byte) []byte { b[0] = 'X'; return b })},
		{"version 2", "version", mutate(func(b []byte) []byte { b[2] = 2; return b })},
		{"length too long", "length", mutate(func(b []byte) []byte { b[12]++; return b })},
		{"truncated", "length", good[:len(good)-1]},
		{"audio short", "audio_short", header(rp.TypeAudio, make([]byte, 10))},
		{"short_name 0", "short_name", mutate(func(b []byte) []byte { b[16+33] = 0; return b })},
		{"short_name too long", "short_name", mutate(func(b []byte) []byte { b[16+33] = 200; return b })},
		{"sample rate", "sample_rate", mutate(func(b []byte) []byte { b[16+30] = 0x11; b[16+31] = 0x2b; return b })},
		{"odd pcm", "pcm", func() []byte {
			b := mutate(func(b []byte) []byte { return append(b, 0) })
			b[12]++
			return b
		}()},
		{"not json", "json", header(rp.TypeCallEnd, []byte("{nope"))},
		{"missing identity", "identity", header(rp.TypeTransmission, []byte(`{"sys_num":0,"call_num":1,"short_name":"ex"}`))},
		{"empty short_name", "identity", header(rp.TypeCallEnd, []byte(`{"sys_num":0,"call_num":1,"start_time":1,"short_name":""}`))},
		{"wrong field type", "json", header(rp.TypeHeartbeat, []byte(`{"systems":"x"}`))},
		{"withdraw missing identity", "identity", header(rp.TypeCallWithdraw, []byte(`{"sys_num":0,"call_num":1,"short_name":"ex"}`))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := rp.Decode(c.b)
			var de *rp.DecodeError
			if !errors.As(err, &de) || de.Reason != c.reason {
				t.Fatalf("got %v, want reason %q", err, c.reason)
			}
		})
	}
}

func TestUnknownType(t *testing.T) {
	m, err := rp.Decode(header(0x7f, []byte("future")))
	if !errors.Is(err, rp.ErrUnknownType) || m.Header.Type != 0x7f {
		t.Fatalf("got %+v, %v", m.Header, err)
	}
}

func TestEncodeRefuses(t *testing.T) {
	a := sampleAudio
	a.PCM = make([]byte, 2*rp.MaxAudioSamples+2)
	if _, err := rp.EncodeAudio(1, 1, a); err == nil {
		t.Fatal("oversize PCM accepted")
	}
	a = sampleAudio
	a.ShortName = strings.Repeat("x", rp.MaxShortName+1)
	if _, err := rp.EncodeAudio(1, 1, a); err == nil {
		t.Fatal("long short_name accepted")
	}
	a = sampleAudio
	a.PCM = make([]byte, 2*rp.MaxAudioSamples)
	a.ShortName = strings.Repeat("x", rp.MaxShortName)
	b, err := rp.EncodeAudio(1, 1, a)
	if err != nil || len(b) > rp.MaxDatagram {
		t.Fatalf("largest audio datagram: %d bytes, %v", len(b), err)
	}
}

// FuzzDecode checks that no datagram crashes the decoder (ADR 0002: a
// malformed datagram is dropped and counted, never a crash).
func FuzzDecode(f *testing.F) {
	good, _ := rp.EncodeAudio(1, 1, sampleAudio)
	f.Add(good)
	ce, _ := rp.EncodeControl(1, 2, rp.CallEnd{Identity: sampleAudio.Identity})
	f.Add(ce)
	f.Add(header(rp.TypeHeartbeat, []byte(`{}`)))
	f.Fuzz(func(t *testing.T, b []byte) {
		m, err := rp.Decode(b)
		if err == nil && m.Audio == nil && m.Transmission == nil && m.CallEnd == nil && m.Heartbeat == nil && m.CallWithdraw == nil {
			t.Fatal("no error and no payload")
		}
	})
}
