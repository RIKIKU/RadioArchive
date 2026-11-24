//go:build cgo

package opus

/*
#cgo pkg-config: opus
#include <opus.h>

// opus_encoder_ctl is variadic, which cgo can't call: one wrapper per request.
static int ra_set_bitrate(OpusEncoder *e, opus_int32 v) { return opus_encoder_ctl(e, OPUS_SET_BITRATE(v)); }
static int ra_set_vbr(OpusEncoder *e, opus_int32 v) { return opus_encoder_ctl(e, OPUS_SET_VBR(v)); }
static int ra_set_bandwidth(OpusEncoder *e, opus_int32 v) { return opus_encoder_ctl(e, OPUS_SET_BANDWIDTH(v)); }
static int ra_set_max_bandwidth(OpusEncoder *e, opus_int32 v) { return opus_encoder_ctl(e, OPUS_SET_MAX_BANDWIDTH(v)); }
static int ra_set_signal(OpusEncoder *e, opus_int32 v) { return opus_encoder_ctl(e, OPUS_SET_SIGNAL(v)); }
static int ra_get_lookahead(OpusEncoder *e, opus_int32 *v) { return opus_encoder_ctl(e, OPUS_GET_LOOKAHEAD(v)); }
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// Encoder is one libopus encoder. It isn't safe for concurrent use.
type Encoder struct {
	e         *C.OpusEncoder
	rate      int
	frame     int
	lookahead int
	out       []byte
}

// NewEncoder returns an encoder for mono PCM at rate (8000, 12000, 16000,
// 24000 or 48000 Hz) with R4's settings.
func NewEncoder(rate int) (*Encoder, error) {
	var errc C.int
	e := C.opus_encoder_create(C.opus_int32(rate), 1, C.OPUS_APPLICATION_VOIP, &errc)
	if errc != C.OPUS_OK || e == nil {
		return nil, fmt.Errorf("opus: create encoder at %d Hz: %s", rate, C.GoString(C.opus_strerror(errc)))
	}
	enc := &Encoder{e: e, rate: rate, frame: FrameSamples(rate), out: make([]byte, 1275)}
	for _, set := range []struct {
		name string
		rc   C.int
	}{
		{"bitrate", C.ra_set_bitrate(e, Bitrate)},
		{"vbr", C.ra_set_vbr(e, 1)},
		{"max bandwidth", C.ra_set_max_bandwidth(e, C.OPUS_BANDWIDTH_NARROWBAND)},
		{"bandwidth", C.ra_set_bandwidth(e, C.OPUS_BANDWIDTH_NARROWBAND)},
		{"signal", C.ra_set_signal(e, C.OPUS_SIGNAL_VOICE)},
	} {
		if set.rc != C.OPUS_OK {
			enc.Close()
			return nil, fmt.Errorf("opus: set %s: %s", set.name, C.GoString(C.opus_strerror(set.rc)))
		}
	}
	var la C.opus_int32
	if rc := C.ra_get_lookahead(e, &la); rc != C.OPUS_OK {
		enc.Close()
		return nil, fmt.Errorf("opus: get lookahead: %s", C.GoString(C.opus_strerror(rc)))
	}
	enc.lookahead = int(la)
	return enc, nil
}

// FrameSamples is the number of samples Encode takes.
func (e *Encoder) FrameSamples() int { return e.frame }

// Lookahead is the encoder's delay in samples at its input rate: the
// pre-skip an Ogg/Opus file declares (times 48000/rate).
func (e *Encoder) Lookahead() int { return e.lookahead }

// Encode encodes exactly one frame of PCM and returns the packet, which is
// only valid until the next call.
func (e *Encoder) Encode(pcm []int16) ([]byte, error) {
	if len(pcm) != e.frame {
		return nil, fmt.Errorf("opus: frame is %d samples, got %d", e.frame, len(pcm))
	}
	n := C.opus_encode(e.e, (*C.opus_int16)(unsafe.Pointer(&pcm[0])), C.int(e.frame),
		(*C.uchar)(unsafe.Pointer(&e.out[0])), C.opus_int32(len(e.out)))
	if n < 0 {
		return nil, fmt.Errorf("opus: encode: %s", C.GoString(C.opus_strerror(C.int(n))))
	}
	return e.out[:n], nil
}

// Close frees the encoder.
func (e *Encoder) Close() {
	if e.e != nil {
		C.opus_encoder_destroy(e.e)
		e.e = nil
	}
}
