package fakerecorder

import (
	"encoding/binary"
	"math"
)

// FixtureRate is the fixture files' sample rate. They're signed 16-bit
// little-endian mono PCM, as trunk-recorder hands P25 audio to its plugins
// (ADR 0002). v1 is P25 only.
const FixtureRate = 8000

// Fixtures returns the fixture PCM files by name: made-up tones and noise,
// never real radio traffic. They're committed in testdata/fixtures, and
// `go run ./testing/fakerecorder/cmd/genfixtures` rewrites them byte for
// byte from this function.
func Fixtures() map[string][]byte {
	return map[string][]byte{
		"tone-440hz-1s.pcm":      tone(440, 1000),
		"tone-880hz-1500ms.pcm":  tone(880, 1500),
		"noise-bursts-500ms.pcm": noiseBursts(500, 100),
	}
}

func samples(ms int) int { return FixtureRate * ms / 1000 }

// tone is a sine at a quarter of full scale, with 10 ms fades so it starts
// and stops without clicks.
func tone(hz float64, ms int) []byte {
	n := samples(ms)
	fade := samples(10)
	b := make([]byte, 0, 2*n)
	for i := range n {
		gain := 1.0
		if i < fade {
			gain = float64(i) / float64(fade)
		} else if n-1-i < fade {
			gain = float64(n-1-i) / float64(fade)
		}
		v := 8192 * gain * math.Sin(2*math.Pi*hz*float64(i)/FixtureRate)
		b = binary.LittleEndian.AppendUint16(b, uint16(int16(math.Round(v))))
	}
	return b
}

// noiseBursts alternates burstMS of noise and burstMS of silence, from a
// fixed xorshift generator so the file is the same on every run.
func noiseBursts(ms, burstMS int) []byte {
	n := samples(ms)
	burst := samples(burstMS)
	x := uint32(0x2545F491)
	b := make([]byte, 0, 2*n)
	for i := range n {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		var v int16
		if (i/burst)%2 == 0 {
			v = int16(x>>16) / 4
		}
		b = binary.LittleEndian.AppendUint16(b, uint16(v))
	}
	return b
}
