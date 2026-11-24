//go:build !cgo

package opus

// Encoder is a stub: a build without cgo has no libopus.
type Encoder struct{}

// NewEncoder always fails without cgo.
func NewEncoder(int) (*Encoder, error) { return nil, ErrNoCgo }

// FrameSamples is never called on the stub.
func (*Encoder) FrameSamples() int { return 0 }

// Lookahead is never called on the stub.
func (*Encoder) Lookahead() int { return 0 }

// Encode is never called on the stub.
func (*Encoder) Encode([]int16) ([]byte, error) { return nil, ErrNoCgo }

// Close does nothing.
func (*Encoder) Close() {}
