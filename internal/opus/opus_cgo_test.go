//go:build cgo

package opus

import (
	"math"
	"testing"
)

func TestEncoderR4(t *testing.T) {
	e, err := NewEncoder(8000)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if e.FrameSamples() != 160 {
		t.Fatalf("frame = %d samples, want 160 (20 ms at 8 kHz)", e.FrameSamples())
	}
	if e.Lookahead() <= 0 {
		t.Errorf("lookahead = %d, want > 0", e.Lookahead())
	}
	pcm := make([]int16, 160)
	total := 0
	const frames = 100 // 2 s
	for i := range frames {
		for j := range pcm {
			pcm[j] = int16(8000 * math.Sin(2*math.Pi*440*float64(i*160+j)/8000))
		}
		p, err := e.Encode(pcm)
		if err != nil {
			t.Fatal(err)
		}
		if len(p) == 0 {
			t.Fatal("empty packet")
		}
		// TOC byte: config 0-3 is SILK narrowband (RFC 6716 section 3.1),
		// and 1 or 3 of them is a 20 ms frame.
		if cfg := p[0] >> 3; cfg != 1 {
			t.Fatalf("frame %d: TOC config %d, want 1 (SILK NB 20 ms)", i, cfg)
		}
		if p[0]&0x04 != 0 {
			t.Fatalf("frame %d: stereo flag set", i)
		}
		total += len(p)
	}
	kbps := float64(total*8) / (frames * 0.02) / 1000
	if kbps < 6 || kbps > 16 {
		t.Errorf("bitrate %.1f kbit/s, want about 12", kbps)
	}
	if _, err := e.Encode(pcm[:100]); err == nil {
		t.Error("a short frame was accepted")
	}
}
