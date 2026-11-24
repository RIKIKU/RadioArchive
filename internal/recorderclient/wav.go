package recorderclient

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// readWAV reads trunk-recorder's recording: a RIFF WAVE file of 16-bit
// mono PCM. It returns the sample rate and the samples.
func readWAV(r io.Reader) (rate int, pcm []int16, err error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return 0, nil, err
	}
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return 0, nil, errors.New("wav: not a RIFF WAVE file")
	}
	le := binary.LittleEndian
	var haveFmt bool
	for p := 12; p+8 <= len(b); {
		id := string(b[p : p+4])
		size := int(le.Uint32(b[p+4:]))
		body := b[p+8:]
		// A data chunk whose size was never written (a writer that didn't
		// finish) runs to the end of the file.
		if size < 0 || size > len(body) || (id == "data" && size == 0) {
			size = len(body)
		}
		body = body[:size]
		switch id {
		case "fmt ":
			if len(body) < 16 {
				return 0, nil, errors.New("wav: short fmt chunk")
			}
			format := le.Uint16(body[0:])
			channels := le.Uint16(body[2:])
			rate = int(le.Uint32(body[4:]))
			bits := le.Uint16(body[14:])
			const pcmFormat, extensible = 1, 0xfffe
			if format == extensible && len(body) >= 26 {
				format = le.Uint16(body[24:]) // the sub-format GUID's first two bytes
			}
			if format != pcmFormat || channels != 1 || bits != 16 {
				return 0, nil, fmt.Errorf("wav: format %d, %d channels, %d bits; want 16-bit mono PCM", format, channels, bits)
			}
			haveFmt = true
		case "data":
			if !haveFmt {
				return 0, nil, errors.New("wav: data before fmt")
			}
			pcm = make([]int16, len(body)/2)
			for i := range pcm {
				pcm[i] = int16(le.Uint16(body[2*i:]))
			}
			return rate, pcm, nil
		}
		p += 8 + size + size&1 // chunks are padded to an even length
	}
	return 0, nil, errors.New("wav: no data chunk")
}
