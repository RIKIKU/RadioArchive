//go:build cgo

package recorderclient

import (
	"bytes"
	"math"
	"testing"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
	"github.com/RIKIKU/RadioArchive/internal/opus"
)

func callID(n int64) cp.CallID { return cp.CallID{CallNum: n, StartTime: 1764000000} }

// TestEncodeDeterministic: with libopus, encoding the same recording twice
// gives the same bytes, so a re-upload after a lost acknowledgement (or a
// client restart) has the same SHA-256 and the server answers "duplicate"
// (queue.go, crash safety).
func TestEncodeDeterministic(t *testing.T) {
	newEnc := func(rate int) (Encoder, error) {
		e, err := opus.NewEncoder(rate)
		if err != nil {
			return nil, err
		}
		return e, nil
	}
	pcm := make([]int16, 3*8000+37)
	for i := range pcm {
		pcm[i] = int16(8000*math.Sin(float64(i)*0.3) + float64((i*7919)%2000) - 1000)
	}
	serial := oggSerial(callID(4182))
	a, err := encodeOggOpus(newEnc, serial, 8000, pcm)
	if err != nil {
		t.Fatal(err)
	}
	b, err := encodeOggOpus(newEnc, serial, 8000, pcm)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("two encodings of one recording differ")
	}
	if serial == oggSerial(callID(4183)) {
		t.Error("two calls share an Ogg serial")
	}
}
