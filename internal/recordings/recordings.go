// Package recordings is the recordings volume (SPEC.md R20): where a call's
// Ogg/Opus file lives, and serving it with range requests.
//
// Public surfaces never use it directly. They get a recording only from the
// gate (internal/gate), which opens it here once the call is public.
package recordings

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// ContentType is a recording's media type: Ogg/Opus (R4).
const ContentType = "audio/ogg; codecs=opus"

// Path is where a call's recording is kept, relative to the volume:
// <id[:2]>/<id>.opus, by its opaque ID (R19, R60).
func Path(callID string) string {
	return callID[:2] + "/" + callID + ".opus"
}

// ErrMissing is a recording whose file isn't on the volume.
var ErrMissing = errors.New("recording file is missing")

// File is an open recording.
type File struct {
	f       *os.File
	modTime time.Time
}

// Open opens rel, a path as Path gives it, on the volume at dir. modTime is
// when the recording was stored, for Last-Modified.
func Open(dir, rel string, modTime time.Time) (*File, error) {
	if !filepath.IsLocal(filepath.FromSlash(rel)) {
		return nil, fmt.Errorf("recording path isn't on the volume")
	}
	f, err := os.Open(filepath.Join(dir, filepath.FromSlash(rel)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrMissing
	}
	if err != nil {
		return nil, err
	}
	return &File{f: f, modTime: modTime}, nil
}

// Close closes the file.
func (f *File) Close() error { return f.f.Close() }

// Serve answers r with the file, with range requests (http.ServeContent),
// as audio/ogg with this Cache-Control.
func (f *File) Serve(w http.ResponseWriter, r *http.Request, cacheControl string) {
	w.Header().Set("Content-Type", ContentType)
	w.Header().Set("Cache-Control", cacheControl)
	http.ServeContent(w, r, "", f.modTime, f.f)
}
