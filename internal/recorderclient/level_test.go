package recorderclient

import (
	"math"
	"math/rand/v2"
	"testing"
)

// sine is a tone of amplitude amp (of full scale) for seconds at rate.
func sine(rate int, hz, amp, seconds float64) []int16 {
	out := make([]int16, int(seconds*float64(rate)))
	for i := range out {
		out[i] = int16(math.Round(amp * 32767 * math.Sin(2*math.Pi*hz*float64(i)/float64(rate))))
	}
	return out
}

// speech is a stand-in for a radio call's voice: a few harmonics of a
// wandering pitch, in syllables of varying loudness with pauses between
// words, plus a little noise, at about level dBFS RMS while it talks.
func speech(rate int, seconds, level float64, seed uint64) []int16 {
	r := rand.New(rand.NewPCG(seed, 1))
	out := make([]int16, int(seconds*float64(rate)))
	amp := math.Pow(10, level/20) * 32767
	phase := 0.0
	for i := 0; i < len(out); {
		syl := int((0.12 + 0.2*r.Float64()) * float64(rate))
		loud := 0.4 + 1.2*r.Float64()
		f0 := 100 + 120*r.Float64()
		for j := 0; j < syl && i < len(out); j, i = j+1, i+1 {
			env := math.Sin(math.Pi * float64(j) / float64(syl))
			phase += 2 * math.Pi * f0 / float64(rate)
			v := 0.0
			for h := 1.0; h*f0 < 3400; h++ {
				v += math.Sin(h*phase) / h
			}
			out[i] = int16(max(-32768, min(32767, math.Round(amp*loud*env*v/1.6+30*r.NormFloat64()))))
		}
		if r.Float64() < 0.3 { // a pause between words: the noise floor
			for j := 0; j < int(0.25*float64(rate)) && i < len(out); j, i = j+1, i+1 {
				out[i] = int16(math.Round(30 * r.NormFloat64()))
			}
		}
	}
	return out
}

func peakDBFS(pcm []int16) float64 {
	var p int
	for _, v := range pcm {
		p = max(p, int(v), -int(v))
	}
	return 20 * math.Log10(float64(p)/32768)
}

// BS.1770's calibration: a full-scale 997 Hz sine reads -3.01 LUFS, at
// either of the client's rates.
func TestLoudnessCalibration(t *testing.T) {
	for _, rate := range SampleRates {
		for _, tt := range []struct{ amp, want float64 }{{1, -3.01}, {0.1, -23.01}} {
			got := Loudness(sine(rate, 997, tt.amp, 5), rate)
			if math.Abs(got-tt.want) > 0.2 {
				t.Errorf("%d Hz: a 997 Hz sine at %.1f of full scale reads %.2f LUFS, want %.2f", rate, tt.amp, got, tt.want)
			}
		}
		if got := Loudness(make([]int16, rate), rate); !math.IsInf(got, -1) {
			t.Errorf("%d Hz: silence reads %.2f LUFS, want -Inf", rate, got)
		}
	}
}

// trunk-recorder v5 runs every recording through ffmpeg's loudnorm, which
// puts P25 calls at about -19.5 LUFS, but the live audio it streams to the
// plugin is the decoder's own level, 5-23 dB quieter (ADR 0047). The client
// levels a call's live audio to the same loudness, whatever its level, and
// never clips it. Live audio can't wait for the rest of the call, as
// loudnorm does, so it's allowed 2 LU either way.
func TestLevelLiveMatchesTheRecordings(t *testing.T) {
	for _, rate := range SampleRates {
		for _, level := range []float64{-35, -28, -20, -12, -4} {
			in := speech(rate, 12, level, uint64(-level))
			out := LevelLive(in, rate)
			if len(out) != len(in) {
				t.Fatalf("%d Hz: %d samples out for %d in", rate, len(out), len(in))
			}
			before, after := Loudness(in, rate), Loudness(out, rate)
			t.Logf("%d Hz, %3.0f dBFS: %6.1f LUFS in, %6.1f LUFS out, peak %5.1f dBFS", rate, level, before, after, peakDBFS(out))
			if math.Abs(after-LiveLoudness) > 2 {
				t.Errorf("%d Hz: speech at %.1f LUFS levelled to %.1f LUFS, want %.0f±2, as trunk-recorder's recordings",
					rate, before, after, LiveLoudness)
			}
			if p := peakDBFS(out); p > -0.9 {
				t.Errorf("%d Hz: speech at %.1f LUFS peaks at %.2f dBFS levelled, want at most -1", rate, before, p)
			}
		}
	}
}

// Silence, and a quiet channel's noise floor, stay as they are: there's
// nothing to level, and a hiss shouldn't be turned up to speech.
func TestLevelLiveLeavesSilence(t *testing.T) {
	for _, rate := range SampleRates {
		if out := LevelLive(make([]int16, rate), rate); peakDBFS(out) > -200 {
			t.Errorf("%d Hz: silence levelled peaks at %.1f dBFS", rate, peakDBFS(out))
		}
		hiss := sine(rate, 997, 0.0002, 2) // about -77 LUFS: under BS.1770's -70 gate
		if out := LevelLive(hiss, rate); peakDBFS(out) > peakDBFS(hiss)+0.1 {
			t.Errorf("%d Hz: a -77 LUFS hiss levelled from %.1f to %.1f dBFS peak", rate, peakDBFS(hiss), peakDBFS(out))
		}
	}
}

// The live leveller works on each 20 ms frame as it comes, as the engine
// encodes it: the first second of a call is already near the target, not
// only the call as a whole.
func TestLevelLiveIsQuickToSettle(t *testing.T) {
	const rate = 8000
	in := speech(rate, 6, -35, 7)
	out := LevelLive(in, rate)
	first := out[:rate] // the first second
	if got := Loudness(first, rate); math.Abs(got-LiveLoudness) > 3 {
		t.Errorf("the first second levelled to %.1f LUFS, want %.0f±3", got, LiveLoudness)
	}
}
