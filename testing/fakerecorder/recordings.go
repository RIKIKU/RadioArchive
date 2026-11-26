package fakerecorder

import (
	"encoding/binary"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// RecordingRate is the rate trunk-recorder v5.2.1 writes every call's WAV
// at, whatever its recorders gave: the call concluder renders the
// transmissions through ffmpeg with `-ar 16000` (call_concluder.cc), so a
// P25 call's live audio is 8 kHz and its recording 16 kHz (ADR 0046).
const RecordingRate = 16000

// upsample takes 16-bit little-endian mono PCM from rate up to to, a whole
// multiple of it, by linear interpolation: near enough to ffmpeg's
// resampler for made-up tones, and the length in seconds stays the same.
func upsample(pcm []byte, rate, to int) []byte {
	k := to / rate
	if k <= 1 || to%rate != 0 {
		return pcm
	}
	le := binary.LittleEndian
	n := len(pcm) / 2
	out := make([]byte, 0, 2*k*n)
	for i := range n {
		a := int(int16(le.Uint16(pcm[2*i:])))
		b := a
		if i+1 < n {
			b = int(int16(le.Uint16(pcm[2*i+2:])))
		}
		for j := range k {
			out = le.AppendUint16(out, uint16(int16(a+(b-a)*j/k)))
		}
	}
	return out
}

// Recordings returns the WAV file trunk-recorder would have written for
// each call that ends with a filename: the call's scripted audio, in time
// order, as 16-bit mono PCM at RecordingRate. They're keyed by the
// call_end's filename.
//
// trunk-recorder writes the file before call_end (ADR 0002), so a test
// writes these (WriteRecordings) before it replays the scenario.
func Recordings(s *Scenario, fixtures fs.FS) (map[string][]byte, error) {
	type part struct {
		at  int64
		i   int
		pcm string
	}
	parts := map[string][]part{}
	files := map[string]string{}
	for i, e := range s.Events {
		switch e.Type {
		case EventAudio:
			parts[e.Call] = append(parts[e.Call], part{e.AtMS, i, e.PCM})
		case EventCallEnd:
			if e.Filename != "" {
				files[e.Call] = e.Filename
			}
		}
	}
	out := map[string][]byte{}
	for name, file := range files {
		ps := parts[name]
		sort.Slice(ps, func(a, b int) bool {
			if ps[a].at != ps[b].at {
				return ps[a].at < ps[b].at
			}
			return ps[a].i < ps[b].i
		})
		var pcm []byte
		for _, p := range ps {
			b, err := fs.ReadFile(fixtures, p.pcm)
			if err != nil {
				return nil, fmt.Errorf("fakerecorder: %w", err)
			}
			pcm = append(pcm, b...)
		}
		rate := int(s.Calls[name].SampleRate)
		if rate == 0 {
			rate = FixtureRate
		}
		out[file] = WAV(RecordingRate, upsample(pcm, rate, RecordingRate))
	}
	return out, nil
}

// WriteRecordings writes Recordings under root: a call_end filename
// /app/media/x.wav lands at root/app/media/x.wav.
func WriteRecordings(s *Scenario, fixtures fs.FS, root string) error {
	recs, err := Recordings(s, fixtures)
	if err != nil {
		return err
	}
	for name, b := range recs {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// WAV wraps 16-bit little-endian mono PCM in a WAV header, as
// trunk-recorder writes its recordings.
func WAV(rate int, pcm []byte) []byte {
	le := binary.LittleEndian
	h := make([]byte, 0, 44+len(pcm))
	h = append(h, "RIFF"...)
	h = le.AppendUint32(h, uint32(36+len(pcm)))
	h = append(h, "WAVEfmt "...)
	h = le.AppendUint32(h, 16)
	h = le.AppendUint16(h, 1) // PCM
	h = le.AppendUint16(h, 1) // mono
	h = le.AppendUint32(h, uint32(rate))
	h = le.AppendUint32(h, uint32(2*rate))
	h = le.AppendUint16(h, 2)
	h = le.AppendUint16(h, 16)
	h = append(h, "data"...)
	h = le.AppendUint32(h, uint32(len(pcm)))
	return append(h, pcm...)
}
