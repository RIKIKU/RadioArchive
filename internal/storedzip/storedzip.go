// Package storedzip lays out a zip whose entries are stored, not
// compressed, and reads it as a file of known size (SPEC.md R65; ADR 0015).
//
// Stored entries' sizes are known before anything is read, so the whole
// archive's size and every entry's offset are too: a Reader can start at
// any byte, which is how a range request resumes a broken download without
// the archive being built first. The bytes depend only on the entries
// (their names, times, sizes and contents), never on when or how they're
// read, so the same entries always give the same archive.
//
// Entries held in memory (Data) are small. The rest are read through a
// Source when their bytes are needed, one at a time: a Reader holds at most
// one open file and a 32 KB buffer. An entry's CRC-32 comes before its
// data, in its local header, so a Reader reads each such file twice, once
// for the CRC and once to send it; the second read is from the page cache.
//
// It reads nothing itself: callers hand it what may be read (a test keeps
// its imports to encoding, hashing and I/O interfaces).
package storedzip

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// File is an open entry's contents.
type File interface {
	io.ReaderAt
	io.Closer
}

// Source opens an entry's contents when its bytes are needed.
type Source interface {
	Open() (File, error)
}

// Entry is one file or directory in the archive.
type Entry struct {
	// Name is the entry's path, with slashes; a directory's ends in one.
	Name string
	// Modified is the entry's time. Its location sets the MS-DOS time,
	// which has none; an extended timestamp gives the instant too.
	Modified time.Time
	// Data is a file held in memory. A file read through Source has
	// Size bytes instead.
	Data   []byte
	Size   int64
	Source Source
}

// ErrTooBig is an archive past the classic zip format's limits (4 GiB, 65,535
// entries), which would need Zip64.
var ErrTooBig = errors.New("the zip would be over 4 GiB or 65,535 entries")

const (
	localLen   = 30
	centralLen = 46
	endLen     = 22
	extraLen   = 9 // the extended timestamp, with the modification time
	maxUint32  = 1<<32 - 1
	bufSize    = 32 << 10
)

type entry struct {
	Entry
	dir     bool
	utf8    bool
	extra   bool // an extended timestamp: Modified fits its 32 bits
	dosTime uint16
	dosDate uint16
	crc     uint32 // known for Data and directories
	offset  int64  // of its local header
}

func (e *entry) size() int64 {
	if e.Source != nil {
		return e.Size
	}
	return int64(len(e.Data))
}

func (e *entry) extraLen() int {
	if e.extra {
		return extraLen
	}
	return 0
}

type segKind int

const (
	segLocal segKind = iota
	segData
	segCentral
	segEnd
)

type segment struct {
	kind segKind
	i    int // the entry
	off  int64
	len  int64
}

// Archive is a laid-out zip. It never changes; Readers read it.
type Archive struct {
	entries   []entry
	segs      []segment
	size      int64
	cdOffset  int64
	cdSize    int64
	needsCRCs int // entries read through a Source
}

// New lays out entries in their order. Names must be valid UTF-8, relative,
// without "." or ".." parts, and unique.
func New(entries []Entry) (*Archive, error) {
	if len(entries) > 0xffff {
		return nil, ErrTooBig
	}
	a := &Archive{entries: make([]entry, len(entries))}
	seen := map[string]bool{}
	var off int64
	for i, in := range entries {
		if err := checkName(in.Name); err != nil {
			return nil, err
		}
		if seen[in.Name] {
			return nil, fmt.Errorf("zip entry %q: named twice", in.Name)
		}
		seen[in.Name] = true
		e := entry{Entry: in, dir: strings.HasSuffix(in.Name, "/")}
		if e.dir && (in.Source != nil || len(in.Data) > 0) {
			return nil, fmt.Errorf("zip entry %q: a directory has no contents", in.Name)
		}
		if in.Source == nil {
			e.crc = crc32.ChecksumIEEE(in.Data)
		} else {
			a.needsCRCs++
		}
		if e.size() < 0 || e.size() > maxUint32 {
			return nil, ErrTooBig
		}
		for _, r := range in.Name {
			if r >= utf8.RuneSelf {
				e.utf8 = true
				break
			}
		}
		u := in.Modified.Unix()
		e.extra = u >= -1<<31 && u < 1<<31
		e.dosTime, e.dosDate = dosTime(in.Modified)
		e.offset = off
		hl := int64(localLen + len(in.Name) + e.extraLen())
		a.segs = append(a.segs, segment{segLocal, i, off, hl})
		off += hl
		if n := e.size(); n > 0 {
			a.segs = append(a.segs, segment{segData, i, off, n})
			off += n
		}
		if off > maxUint32 {
			return nil, ErrTooBig
		}
		a.entries[i] = e
	}
	a.cdOffset = off
	for i := range a.entries {
		e := &a.entries[i]
		l := int64(centralLen + len(e.Name) + e.extraLen())
		a.segs = append(a.segs, segment{segCentral, i, off, l})
		off += l
	}
	a.cdSize = off - a.cdOffset
	a.segs = append(a.segs, segment{segEnd, 0, off, endLen})
	off += endLen
	if off > maxUint32 {
		return nil, ErrTooBig
	}
	a.size = off
	return a, nil
}

func checkName(name string) error {
	if name == "" || !utf8.ValidString(name) || len(name) > 0xffff || strings.HasPrefix(name, "/") ||
		strings.ContainsAny(name, "\\\x00") {
		return fmt.Errorf("zip entry %q: not a relative, slash-separated UTF-8 name", name)
	}
	for _, part := range strings.Split(strings.TrimSuffix(name, "/"), "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("zip entry %q: an empty, . or .. part", name)
		}
	}
	return nil
}

// dosTime is t's wall clock in its location as MS-DOS keeps it: to 2
// seconds, from 1980 to 2107.
func dosTime(t time.Time) (tm, date uint16) {
	switch {
	case t.Year() < 1980:
		return 0, 1<<5 | 1
	case t.Year() > 2107:
		return 23<<11 | 59<<5 | 29, 127<<9 | 12<<5 | 31
	}
	return uint16(t.Hour()<<11 | t.Minute()<<5 | t.Second()/2),
		uint16((t.Year()-1980)<<9 | int(t.Month())<<5 | t.Day())
}

// Size is the archive's length in bytes.
func (a *Archive) Size() int64 { return a.size }

// Reader reads the archive from any offset. It isn't safe for concurrent
// use.
type Reader struct {
	a    *Archive
	off  int64
	crcs []uint32
	have []bool
	cur  int // the entry f is open on, or -1
	f    File
	buf  []byte
	hdr  []byte
}

// NewReader returns a Reader at the archive's start. Close it to close the
// file it may hold open.
func (a *Archive) NewReader() *Reader {
	return &Reader{a: a, crcs: make([]uint32, len(a.entries)), have: make([]bool, len(a.entries)), cur: -1}
}

// Seek sets where the next Read starts (io.Seeker).
func (r *Reader) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += r.off
	case io.SeekEnd:
		offset += r.a.size
	default:
		return 0, errors.New("storedzip: bad whence")
	}
	if offset < 0 {
		return 0, errors.New("storedzip: negative offset")
	}
	r.off = offset
	return offset, nil
}

// Read reads the archive's next bytes. A Source that fails, or a file
// shorter than its Size, is an error: the archive can't be finished.
func (r *Reader) Read(p []byte) (int, error) {
	if r.off >= r.a.size {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	segs := r.a.segs
	k := sort.Search(len(segs), func(k int) bool { return segs[k].off+segs[k].len > r.off })
	s := segs[k]
	within := r.off - s.off
	p = p[:min(int64(len(p)), s.len-within)]
	var n int
	var err error
	switch s.kind {
	case segData:
		n, err = r.data(s.i, within, p)
	default:
		var b []byte
		b, err = r.header(s)
		n = copy(p, b[within:])
	}
	r.off += int64(n)
	return n, err
}

// Close closes the file the Reader holds open, if any.
func (r *Reader) Close() error {
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f, r.cur = nil, -1
	return err
}

// open makes the Reader's open file entry i's, closing the one before.
func (r *Reader) open(i int) error {
	if r.cur == i {
		return nil
	}
	if err := r.Close(); err != nil {
		return err
	}
	f, err := r.a.entries[i].Source.Open()
	if err != nil {
		return fmt.Errorf("storedzip: %s: %w", r.a.entries[i].Name, err)
	}
	r.f, r.cur = f, i
	return nil
}

func (r *Reader) data(i int, off int64, p []byte) (int, error) {
	e := &r.a.entries[i]
	if e.Source == nil {
		return copy(p, e.Data[off:]), nil
	}
	if err := r.open(i); err != nil {
		return 0, err
	}
	n, err := r.f.ReadAt(p, off)
	if n == len(p) {
		return n, nil
	}
	if err == nil || err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return n, fmt.Errorf("storedzip: %s: %w", e.Name, err)
}

// crc is entry i's CRC-32, reading its file the first time it's asked.
func (r *Reader) crc(i int) (uint32, error) {
	e := &r.a.entries[i]
	if e.Source == nil {
		return e.crc, nil
	}
	if r.have[i] {
		return r.crcs[i], nil
	}
	if err := r.open(i); err != nil {
		return 0, err
	}
	if r.buf == nil {
		r.buf = make([]byte, bufSize)
	}
	var sum uint32
	for off := int64(0); off < e.Size; {
		b := r.buf[:min(int64(len(r.buf)), e.Size-off)]
		n, err := r.f.ReadAt(b, off)
		sum = crc32.Update(sum, crc32.IEEETable, b[:n])
		off += int64(n)
		if n < len(b) {
			if err == nil || err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return 0, fmt.Errorf("storedzip: %s: %w", e.Name, err)
		}
	}
	r.crcs[i], r.have[i] = sum, true
	return sum, nil
}

// header is a header segment's bytes.
func (r *Reader) header(s segment) ([]byte, error) {
	b := r.hdr[:0]
	le := binary.LittleEndian
	switch s.kind {
	case segLocal:
		e := &r.a.entries[s.i]
		crc, err := r.crc(s.i)
		if err != nil {
			return nil, err
		}
		b = le.AppendUint32(b, 0x04034b50)
		b = le.AppendUint16(b, 20) // version needed: 2.0
		b = le.AppendUint16(b, e.flags())
		b = le.AppendUint16(b, 0) // stored
		b = le.AppendUint16(b, e.dosTime)
		b = le.AppendUint16(b, e.dosDate)
		b = le.AppendUint32(b, crc)
		b = le.AppendUint32(b, uint32(e.size()))
		b = le.AppendUint32(b, uint32(e.size()))
		b = le.AppendUint16(b, uint16(len(e.Name)))
		b = le.AppendUint16(b, uint16(e.extraLen()))
		b = append(b, e.Name...)
		b = e.appendExtra(b)
	case segCentral:
		e := &r.a.entries[s.i]
		crc, err := r.crc(s.i)
		if err != nil {
			return nil, err
		}
		mode, attr := uint32(0o100644), uint32(0)
		if e.dir {
			mode, attr = 0o040755, 0x10
		}
		b = le.AppendUint32(b, 0x02014b50)
		b = le.AppendUint16(b, 3<<8|20) // made by: Unix, 2.0
		b = le.AppendUint16(b, 20)
		b = le.AppendUint16(b, e.flags())
		b = le.AppendUint16(b, 0)
		b = le.AppendUint16(b, e.dosTime)
		b = le.AppendUint16(b, e.dosDate)
		b = le.AppendUint32(b, crc)
		b = le.AppendUint32(b, uint32(e.size()))
		b = le.AppendUint32(b, uint32(e.size()))
		b = le.AppendUint16(b, uint16(len(e.Name)))
		b = le.AppendUint16(b, uint16(e.extraLen()))
		b = le.AppendUint16(b, 0) // comment
		b = le.AppendUint16(b, 0) // disk
		b = le.AppendUint16(b, 0) // internal attributes
		b = le.AppendUint32(b, mode<<16|attr)
		b = le.AppendUint32(b, uint32(e.offset))
		b = append(b, e.Name...)
		b = e.appendExtra(b)
	case segEnd:
		n := uint16(len(r.a.entries))
		b = le.AppendUint32(b, 0x06054b50)
		b = le.AppendUint16(b, 0)
		b = le.AppendUint16(b, 0)
		b = le.AppendUint16(b, n)
		b = le.AppendUint16(b, n)
		b = le.AppendUint32(b, uint32(r.a.cdSize))
		b = le.AppendUint32(b, uint32(r.a.cdOffset))
		b = le.AppendUint16(b, 0)
	}
	r.hdr = b
	if int64(len(b)) != s.len {
		return nil, fmt.Errorf("storedzip: a header of %d bytes laid out as %d", len(b), s.len)
	}
	return b, nil
}

func (e *entry) flags() uint16 {
	if e.utf8 {
		return 1 << 11 // the name is UTF-8
	}
	return 0
}

// appendExtra appends the extended timestamp (0x5455): the modification
// time, in seconds since 1970 UTC.
func (e *entry) appendExtra(b []byte) []byte {
	if !e.extra {
		return b
	}
	le := binary.LittleEndian
	b = le.AppendUint16(b, 0x5455)
	b = le.AppendUint16(b, 5)
	b = append(b, 1)
	return le.AppendUint32(b, uint32(int32(e.Modified.Unix())))
}
