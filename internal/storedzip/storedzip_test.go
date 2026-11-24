package storedzip

import (
	"archive/zip"
	"bytes"
	"errors"
	"go/parser"
	"go/token"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The package reads nothing itself: its files are handed to it.
func TestImportsReadNothing(t *testing.T) {
	allowed := map[string]bool{"encoding/binary": true, "errors": true, "fmt": true, "hash/crc32": true,
		"io": true, "sort": true, "strings": true, "time": true, "unicode/utf8": true}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, im := range af.Imports {
			if p, _ := strconv.Unquote(im.Path.Value); !allowed[p] {
				t.Errorf("%s imports %s", f, p)
			}
		}
	}
}

// memSource is a file in memory that counts how many are open at once.
type memSource struct {
	b    []byte
	open *int
	most *int
	err  error
}

type memFile struct {
	*bytes.Reader
	open *int
}

func (f memFile) Close() error { *f.open--; return nil }

func (s memSource) Open() (File, error) {
	if s.err != nil {
		return nil, s.err
	}
	*s.open++
	*s.most = max(*s.most, *s.open)
	return memFile{bytes.NewReader(s.b), s.open}, nil
}

var sydney = func() *time.Location {
	l, err := time.LoadLocation("Australia/Sydney")
	if err != nil {
		panic(err)
	}
	return l
}()

func fixture(open, most *int) ([]Entry, map[string][]byte) {
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, sydney)
	audio := func(seed byte, n int) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = seed + byte(i*7)
		}
		return b
	}
	a, b := audio(1, 70_000), audio(2, 3)
	want := map[string][]byte{
		"manifest.json":                      []byte("{}\n"),
		"01 Structure fire/":                 nil,
		"01 Structure fire/a.opus":           a,
		"01 Structure fire/Nörd.m3u":         []byte("#EXTM3U\n"),
		"2026-03-04_050607_DISP N_1001.opus": b,
	}
	return []Entry{
		{Name: "manifest.json", Modified: at, Data: want["manifest.json"]},
		{Name: "01 Structure fire/", Modified: at},
		{Name: "01 Structure fire/a.opus", Modified: at.Add(time.Second), Size: int64(len(a)), Source: memSource{a, open, most, nil}},
		{Name: "01 Structure fire/Nörd.m3u", Modified: at, Data: want["01 Structure fire/Nörd.m3u"]},
		{Name: "2026-03-04_050607_DISP N_1001.opus", Modified: at, Size: 3, Source: memSource{b, open, most, nil}},
	}, want
}

func readAll(t *testing.T, a *Archive) []byte {
	t.Helper()
	r := a.NewReader()
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A standard reader reads it, CRCs and times included, and the same entries
// give the same bytes.
func TestArchive(t *testing.T) {
	var open, most int
	entries, want := fixture(&open, &most)
	a, err := New(entries)
	if err != nil {
		t.Fatal(err)
	}
	b := readAll(t, a)
	if int64(len(b)) != a.Size() {
		t.Fatalf("read %d bytes of a %d-byte archive", len(b), a.Size())
	}
	if most != 1 || open != 0 {
		t.Errorf("files open at once: %d, left open: %d; want 1 and 0", most, open)
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != len(want) {
		t.Fatalf("%d entries, want %d", len(zr.File), len(want))
	}
	for i, f := range zr.File {
		if f.Name != entries[i].Name || f.Method != zip.Store {
			t.Errorf("entry %d: %q method %d", i, f.Name, f.Method)
		}
		if !f.Modified.Equal(entries[i].Modified.Truncate(time.Second)) {
			t.Errorf("%s: modified %s, want %s", f.Name, f.Modified, entries[i].Modified)
		}
		if f.FileInfo().IsDir() != strings.HasSuffix(f.Name, "/") {
			t.Errorf("%s: directory %v", f.Name, f.FileInfo().IsDir())
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(rc) // checks the CRC
		rc.Close()
		if err != nil || !bytes.Equal(got, want[f.Name]) {
			t.Errorf("%s: %d bytes, %v", f.Name, len(got), err)
		}
	}
	again, _ := New(entries)
	if !bytes.Equal(readAll(t, again), b) {
		t.Error("the same entries gave different bytes")
	}
}

// Reading from any offset gives the rest of the archive: a range request
// resumes anywhere, in a header, a file, the central directory or its end.
func TestReadFromAnyOffset(t *testing.T) {
	var open, most int
	entries, _ := fixture(&open, &most)
	a, _ := New(entries)
	whole := readAll(t, a)
	for _, off := range []int64{0, 1, 29, 30, 100, 40_000, a.cdOffset - 1, a.cdOffset, a.cdOffset + 50, a.Size() - 22, a.Size() - 1} {
		r := a.NewReader()
		if _, err := r.Seek(off, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(r)
		r.Close()
		if err != nil || !bytes.Equal(got, whole[off:]) {
			t.Errorf("from %d: %d bytes, %v; want the rest, %d", off, len(got), err, len(whole)-int(off))
		}
	}
	r := a.NewReader()
	if n, _ := r.Seek(0, io.SeekEnd); n != a.Size() {
		t.Errorf("Seek to the end: %d, want %d", n, a.Size())
	}
	if n, err := r.Read(make([]byte, 1)); n != 0 || err != io.EOF {
		t.Errorf("at the end: %d, %v", n, err)
	}
	if most != 1 || open != 0 {
		t.Errorf("files open at once: %d, left open: %d", most, open)
	}
}

// A file that fails or is shorter than its size stops the archive with an
// error, never with made-up bytes.
func TestSourceFails(t *testing.T) {
	var open, most int
	gone := errors.New("gone")
	for why, src := range map[string]memSource{
		"fails to open": {nil, &open, &most, gone},
		"short":         {[]byte("ab"), &open, &most, nil},
	} {
		a, err := New([]Entry{{Name: "a.opus", Size: 3, Source: src}})
		if err != nil {
			t.Fatal(err)
		}
		r := a.NewReader()
		_, err = io.ReadAll(r)
		r.Close()
		if err == nil {
			t.Errorf("%s: no error", why)
		}
	}
}

func TestNewRefuses(t *testing.T) {
	src := memSource{}
	for why, es := range map[string][]Entry{
		"over 4 GiB":      {{Name: "a", Size: 1 << 32, Source: src}},
		"over 4 GiB, sum": {{Name: "a", Size: 1<<31 + 1, Source: src}, {Name: "b", Size: 1 << 31, Source: src}},
		"absolute":        {{Name: "/a"}},
		"climbing out":    {{Name: "a/../../b"}},
		"a backslash":     {{Name: `a\b`}},
		"named twice":     {{Name: "a"}, {Name: "a"}},
		"invalid UTF-8":   {{Name: "a\xff"}},
		"a full folder":   {{Name: "a/", Data: []byte("x")}},
	} {
		if _, err := New(es); err == nil {
			t.Errorf("%s: accepted", why)
		}
	}
}

func TestDOSTime(t *testing.T) {
	for _, c := range []struct {
		at         time.Time
		time, date uint16
	}{
		{time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC), 5<<11 | 6<<5 | 3, 46<<9 | 3<<5 | 4},
		{time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), 0, 1<<5 | 1},
	} {
		if tm, d := dosTime(c.at); tm != c.time || d != c.date {
			t.Errorf("%s: %#x %#x, want %#x %#x", c.at, tm, d, c.time, c.date)
		}
	}
}
