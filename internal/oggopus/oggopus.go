// Package oggopus writes Opus packets as an Ogg/Opus file (RFC 7845): an
// OpusHead page, an OpusTags page with no comments and no padding (SPEC.md
// R4), then the audio, several packets to a page.
package oggopus

import (
	"encoding/binary"
	"errors"
	"io"
)

// Vendor is written in OpusTags.
const Vendor = "RadioArchive"

// maxPagePackets bounds a page at about one second of 20 ms packets, so a
// page stays small and a seek lands close.
const maxPagePackets = 50

// Writer writes one logical Ogg/Opus stream.
type Writer struct {
	w       io.Writer
	serial  uint32
	seq     uint32
	preSkip uint16

	// granule counts 48 kHz samples, starting at the pre-skip.
	granule uint64
	pending [][]byte
	err     error
}

// NewWriter writes the two header pages. preSkip is in 48 kHz samples (the
// encoder's lookahead times 48000 / inputRate), inputRate is informational.
func NewWriter(w io.Writer, serial uint32, preSkip uint16, inputRate uint32) (*Writer, error) {
	ow := &Writer{w: w, serial: serial, preSkip: preSkip}
	head := make([]byte, 0, 19)
	head = append(head, "OpusHead"...)
	head = append(head, 1, 1) // version 1, mono
	head = binary.LittleEndian.AppendUint16(head, preSkip)
	head = binary.LittleEndian.AppendUint32(head, inputRate)
	head = binary.LittleEndian.AppendUint16(head, 0) // output gain
	head = append(head, 0)                           // channel mapping family 0
	if err := ow.page([][]byte{head}, 0, flagBOS); err != nil {
		return nil, err
	}
	tags := make([]byte, 0, 16+len(Vendor))
	tags = append(tags, "OpusTags"...)
	tags = binary.LittleEndian.AppendUint32(tags, uint32(len(Vendor)))
	tags = append(tags, Vendor...)
	tags = binary.LittleEndian.AppendUint32(tags, 0) // no comments, and no padding after them
	if err := ow.page([][]byte{tags}, 0, 0); err != nil {
		return nil, err
	}
	ow.granule = uint64(preSkip)
	return ow, nil
}

// WritePacket adds one Opus packet holding samples48 samples (at 48 kHz; 960
// for a 20 ms frame). The packet is copied.
func (ow *Writer) WritePacket(p []byte, samples48 int) error {
	if ow.err != nil {
		return ow.err
	}
	if len(p) == 0 {
		return errors.New("oggopus: empty packet")
	}
	ow.pending = append(ow.pending, append([]byte(nil), p...))
	ow.granule += uint64(samples48)
	if len(ow.pending) >= maxPagePackets {
		pk := ow.pending
		ow.pending = nil
		return ow.page(pk, ow.granule, 0)
	}
	return nil
}

// Close writes the last page, marked end of stream. total48 is the real
// length of the audio in 48 kHz samples, so a decoder trims the padding of
// the last frame (RFC 7845 section 4.5). It must not exceed what the
// packets hold.
func (ow *Writer) Close(total48 uint64) error {
	if ow.err != nil {
		return ow.err
	}
	end := uint64(ow.preSkip) + total48
	if end > ow.granule {
		return errors.New("oggopus: total longer than the packets written")
	}
	pk := ow.pending
	ow.pending = nil
	return ow.page(pk, end, flagEOS)
}

const (
	flagBOS = 0x02
	flagEOS = 0x04
)

func (ow *Writer) page(packets [][]byte, granule uint64, flags byte) error {
	var segs []byte
	size := 0
	for _, p := range packets {
		n := len(p)
		for n >= 255 {
			segs = append(segs, 255)
			n -= 255
		}
		segs = append(segs, byte(n))
		size += len(p)
	}
	if len(segs) > 255 {
		ow.err = errors.New("oggopus: page over 255 segments")
		return ow.err
	}
	b := make([]byte, 0, 27+len(segs)+size)
	b = append(b, "OggS"...)
	b = append(b, 0, flags)
	b = binary.LittleEndian.AppendUint64(b, granule)
	b = binary.LittleEndian.AppendUint32(b, ow.serial)
	b = binary.LittleEndian.AppendUint32(b, ow.seq)
	b = binary.LittleEndian.AppendUint32(b, 0) // CRC, filled below
	b = append(b, byte(len(segs)))
	b = append(b, segs...)
	for _, p := range packets {
		b = append(b, p...)
	}
	binary.LittleEndian.PutUint32(b[22:], crc(b))
	ow.seq++
	if _, err := ow.w.Write(b); err != nil {
		ow.err = err
		return err
	}
	return nil
}

// Ogg's CRC-32: polynomial 0x04c11db7, not reflected, initial 0, no final
// xor.
var crcTable = func() (t [256]uint32) {
	for i := range t {
		r := uint32(i) << 24
		for range 8 {
			if r&0x80000000 != 0 {
				r = r<<1 ^ 0x04c11db7
			} else {
				r <<= 1
			}
		}
		t[i] = r
	}
	return t
}()

func crc(b []byte) uint32 {
	var c uint32
	for _, x := range b {
		c = c<<8 ^ crcTable[byte(c>>24)^x]
	}
	return c
}
