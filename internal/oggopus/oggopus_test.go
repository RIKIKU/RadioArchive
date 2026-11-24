package oggopus

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestCRCCheckValue(t *testing.T) {
	// Ogg's CRC (init 0, no xorout) is CRC-32/POSIX without its final
	// inversion: POSIX's check value 0x765e7680, inverted.
	if got := crc([]byte("123456789")); got != 0x89a1897f {
		t.Fatalf("crc = %#x, want 0x89a1897f", got)
	}
}

type page struct {
	flags   byte
	granule uint64
	serial  uint32
	seq     uint32
	packets [][]byte
}

// parse reads pages back, checking each CRC, and joins packets across
// lacing values.
func parse(t *testing.T, b []byte) []page {
	t.Helper()
	var out []page
	for len(b) > 0 {
		if len(b) < 27 || string(b[:4]) != "OggS" || b[4] != 0 {
			t.Fatalf("bad page header at %d bytes left", len(b))
		}
		n := int(b[26])
		segs := b[27 : 27+n]
		size := 0
		for _, s := range segs {
			size += int(s)
		}
		total := 27 + n + size
		raw := append([]byte(nil), b[:total]...)
		want := binary.LittleEndian.Uint32(raw[22:])
		binary.LittleEndian.PutUint32(raw[22:], 0)
		if got := crc(raw); got != want {
			t.Fatalf("page %d: crc %#x, header says %#x", len(out), got, want)
		}
		p := page{flags: b[5], granule: binary.LittleEndian.Uint64(b[6:]),
			serial: binary.LittleEndian.Uint32(b[14:]), seq: binary.LittleEndian.Uint32(b[18:])}
		body := b[27+n : total]
		var cur []byte
		for _, s := range segs {
			cur = append(cur, body[:s]...)
			body = body[s:]
			if s < 255 {
				p.packets = append(p.packets, cur)
				cur = nil
			}
		}
		out = append(out, p)
		b = b[total:]
	}
	return out
}

func TestWriter(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, 7, 312, 8000)
	if err != nil {
		t.Fatal(err)
	}
	const n = 120
	for i := range n {
		pkt := bytes.Repeat([]byte{byte(i)}, 20+i%3)
		if i == 5 {
			pkt = bytes.Repeat([]byte{5}, 300) // needs two lacing values
		}
		if err := w.WritePacket(pkt, 960); err != nil {
			t.Fatal(err)
		}
	}
	// The last frame was half padding.
	total := uint64(n*960 - 480)
	if err := w.Close(total); err != nil {
		t.Fatal(err)
	}

	pages := parse(t, buf.Bytes())
	if len(pages) != 2+3 { // head, tags, 50 + 50 + 20 packets
		t.Fatalf("%d pages, want 5", len(pages))
	}
	head := pages[0].packets[0]
	if pages[0].flags != flagBOS || len(pages[0].packets) != 1 || string(head[:8]) != "OpusHead" ||
		head[8] != 1 || head[9] != 1 || binary.LittleEndian.Uint16(head[10:]) != 312 ||
		binary.LittleEndian.Uint32(head[12:]) != 8000 || len(head) != 19 {
		t.Fatalf("OpusHead = % x", head)
	}
	wantTags := append(append([]byte("OpusTags\x0c\x00\x00\x00"), Vendor...), 0, 0, 0, 0)
	if !bytes.Equal(pages[1].packets[0], wantTags) {
		t.Fatalf("OpusTags = % x, want % x (no comments, no padding)", pages[1].packets[0], wantTags)
	}
	var got [][]byte
	for i, p := range pages {
		if p.serial != 7 || p.seq != uint32(i) {
			t.Errorf("page %d: serial %d seq %d", i, p.serial, p.seq)
		}
		if i >= 2 {
			got = append(got, p.packets...)
		}
	}
	if len(got) != n || len(got[5]) != 300 || got[119][0] != 119 {
		t.Fatalf("read back %d packets", len(got))
	}
	if g := pages[2].granule; g != 312+50*960 {
		t.Errorf("first audio page granule %d", g)
	}
	last := pages[len(pages)-1]
	if last.flags != flagEOS || last.granule != 312+total {
		t.Errorf("last page flags %#x granule %d, want EOS and %d", last.flags, last.granule, 312+total)
	}
	if err := (&Writer{granule: 10}).Close(100); err == nil {
		t.Error("Close accepted a total past the packets")
	}
}
