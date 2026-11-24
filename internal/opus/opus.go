// Package opus encodes PCM to Opus with libopus, with the settings of
// SPEC.md R4: 12 kbit/s VBR, narrowband, OPUS_APPLICATION_VOIP, 20 ms
// frames, mono.
//
// It needs cgo and libopus, so only the recorder client uses it. The API
// stays cgo-free (A38): built with CGO_ENABLED=0, this package compiles to a
// stub whose NewEncoder fails.
package opus

import "errors"

// Settings from R4.
const (
	Bitrate = 12000
	FrameMS = 20
)

// ErrNoCgo is NewEncoder's error in a build without cgo.
var ErrNoCgo = errors.New("opus: built without cgo, so libopus isn't available")

// FrameSamples is how many samples make one 20 ms frame at rate.
func FrameSamples(rate int) int { return rate * FrameMS / 1000 }
