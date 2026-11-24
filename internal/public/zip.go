package public

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/RIKIKU/RadioArchive/internal/filename"
	"github.com/RIKIKU/RadioArchive/internal/gate"
	"github.com/RIKIKU/RadioArchive/internal/storedzip"
)

// Zips (SPEC.md R64, R65, R67, R72; ADR 0015): a basket or a conversation
// in one zip, stored without compression, built while it streams and never
// kept, the same bytes every time for the same calls, so a range request
// resumes a broken download.
const (
	// ZipPath is the zip whose list its URL packs (ZipURL), so a browser's
	// download manager can resume it (R65).
	ZipPath = zipPrefix + "{packed}"
	// ZipListPath takes the same list as a ZipRequest body: QUERY, and
	// POST while browsers' support for QUERY is unsettled (R67).
	ZipListPath = "/api/v1/zip"
)

var (
	zipInvalid = apiError{Code: "invalid_zip", Message: "This download's list of calls isn't valid."}
	zipTooMany = apiError{Code: "too_many_calls",
		Message: fmt.Sprintf("A zip holds at most %d calls. Download it in parts.", gate.MaxCalls)}
	zipTooLong = apiError{Code: "zip_url_too_long",
		Message: fmt.Sprintf("A zip's link may be at most %d characters. Download it in parts.", MaxZipURL)}
	zipTooBig = apiError{Code: "zip_too_big", Message: "This zip would be over 4 GB. Download it in parts."}
	// zipNone is the answer when none of a list's calls is available,
	// whatever the reason for each (R64).
	zipNone = apiError{Code: "calls_unavailable", Message: "None of these calls are available."}
)

func (a *API) zipGet(w http.ResponseWriter, r *http.Request) {
	if len(r.RequestURI) > MaxZipURL {
		writeJSON(w, http.StatusRequestURITooLong, zipTooLong)
		return
	}
	l, err := unpack(r.PathValue("packed"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, zipInvalid)
		return
	}
	a.serveZip(w, r, l)
}

func (a *API) zipList(w http.ResponseWriter, r *http.Request) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
	dec.DisallowUnknownFields()
	var in ZipRequest
	if err := dec.Decode(&in); err != nil || dec.Decode(&struct{}{}) != io.EOF {
		writeJSON(w, http.StatusBadRequest, zipInvalid)
		return
	}
	// Ranges are GET's (RFC 9110, section 14.2): a QUERY or POST is
	// answered whole. The GET URL is the one that resumes.
	r.Header.Del("Range")
	r.Header.Del("If-Range")
	a.serveZip(w, r, listOf(in))
}

func (a *API) serveZip(w http.ResponseWriter, r *http.Request, l zipList) {
	switch err := l.check(); {
	case errors.Is(err, errZipTooMany):
		writeJSON(w, http.StatusBadRequest, zipTooMany)
		return
	case err != nil:
		writeJSON(w, http.StatusBadRequest, zipInvalid)
		return
	}
	z, err := a.zipOf(r.Context(), l)
	switch {
	case errors.Is(err, errNoCalls):
		writeJSON(w, http.StatusNotFound, zipNone)
		return
	case errors.Is(err, storedzip.ErrTooBig):
		writeJSON(w, http.StatusBadRequest, zipTooBig)
		return
	case err != nil:
		a.failed(w, r, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/zip")
	h.Set("Content-Disposition", filename.Attachment(z.name))
	h.Set("Cache-Control", "no-store")
	h.Set("ETag", z.etag)
	rd := &zipReader{Reader: z.archive.NewReader(), a: a, r: r}
	defer rd.Close()
	http.ServeContent(w, r, "", time.Time{}, rd)
}

// zipReader logs a zip that stops short: a call hidden, or its file gone,
// while it streams. The download breaks; resumed, its ETag has changed
// and it starts again (ADR 0015).
type zipReader struct {
	*storedzip.Reader
	a      *API
	r      *http.Request
	logged bool
}

func (z *zipReader) Read(p []byte) (int, error) {
	n, err := z.Reader.Read(p)
	if err != nil && err != io.EOF && !z.logged && z.r.Context().Err() == nil {
		z.logged = true
		z.a.log.Warn("a zip stopped short", "error", err)
	}
	return n, err
}

var errNoCalls = errors.New("none of the calls is available")

// zipCall is an available call in a zip, under its file name.
type zipCall struct {
	gate.Call
	groups []string
	file   string
}

type builtZip struct {
	archive *storedzip.Archive
	name    string // to save it as
	etag    string
}

// zipOf lays out the zip of l's available calls (R65): the calls the gate
// gives with a recording, the rest left out as if never named (R64).
func (a *API) zipOf(ctx context.Context, l zipList) (builtZip, error) {
	cat, err := a.g.Catalogue(ctx)
	if err != nil {
		return builtZip{}, err
	}
	cs, err := a.g.Calls(ctx, l.ids)
	if err != nil {
		return builtZip{}, err
	}
	byID := map[string]*zipCall{}
	var all []*zipCall
	for _, c := range cs {
		if c.RecordingSize == nil {
			continue
		}
		zc := &zipCall{Call: c}
		if t, ok := cat.Talkgroup(c.System.ID, c.Talkgroup.ID); ok {
			zc.groups = t.Groups
		}
		byID[c.ID] = zc
		all = append(all, zc)
	}
	if len(all) == 0 {
		return builtZip{}, errNoCalls
	}
	byStart := func(x, y *zipCall) int { return cmp.Or(x.StartedAt.Compare(y.StartedAt), cmp.Compare(x.ID, y.ID)) }
	slices.SortFunc(all, byStart)
	// File names by R63's rule, each once in the zip: a second call on
	// the same talkgroup in the same second gets " (2)".
	used := map[string]bool{}
	for _, c := range all {
		base := filename.Call(c.StartedAt, a.loc, c.Talkgroup.Label, c.Talkgroup.ID)
		name := base
		for k := 2; used[name]; k++ {
			name = strings.TrimSuffix(base, ".opus") + fmt.Sprintf(" (%d).opus", k)
		}
		used[name] = true
		c.file = name
	}

	// Conversations with an available call, numbered by their first
	// start time, ties in the list's order.
	type conv struct {
		zipConv
		calls  []*zipCall // in the conversation's order
		first  time.Time
		folder string
	}
	var convs []*conv
	for _, lc := range l.convs {
		c := &conv{zipConv: lc}
		for _, id := range lc.calls {
			if zc, ok := byID[id]; ok {
				c.calls = append(c.calls, zc)
				if c.first.IsZero() || zc.StartedAt.Before(c.first) {
					c.first = zc.StartedAt
				}
			}
		}
		if len(c.calls) > 0 {
			convs = append(convs, c)
		}
	}
	slices.SortStableFunc(convs, func(x, y *conv) int { return x.first.Compare(y.first) })
	width := max(2, len(strconv.Itoa(len(convs))))
	for i, c := range convs {
		c.folder = fmt.Sprintf("%0*d", width, i+1)
		if s := filename.Safe(c.name); s != "" {
			c.folder += " " + s
		}
	}
	var loose []*zipCall
	for _, id := range l.loose {
		if zc, ok := byID[id]; ok {
			loose = append(loose, zc)
		}
	}
	slices.SortFunc(loose, byStart)

	first := all[0].StartedAt.In(a.loc)
	m := manifest{Format: 1, Part: l.part, Parts: l.parts,
		Conversations: make([]manifestConversation, 0, len(convs)), Calls: make([]manifestCall, 0, len(loose))}
	for i, c := range convs {
		mc := manifestConversation{Number: i + 1, Folder: c.folder, Name: c.name, Note: c.note, Calls: []manifestCall{}}
		for _, zc := range c.calls {
			mc.Calls = append(mc.Calls, manifestCallOf(c.folder+"/", zc))
		}
		m.Conversations = append(m.Conversations, mc)
	}
	for _, c := range loose {
		m.Calls = append(m.Calls, manifestCallOf("", c))
	}
	manifest, err := jsonIndented(m)
	if err != nil {
		return builtZip{}, err
	}
	entries := []storedzip.Entry{
		{Name: "manifest.json", Modified: first, Data: manifest},
		{Name: "index.csv", Modified: first, Data: a.zipIndex(all)},
	}
	file := func(dir string, c *zipCall) storedzip.Entry {
		return storedzip.Entry{Name: dir + c.file, Modified: c.StartedAt.In(a.loc), Size: *c.RecordingSize,
			Source: recordingSource{ctx: ctx, g: a.g, id: c.ID}}
	}
	for _, c := range convs {
		at := c.first.In(a.loc)
		dir := c.folder + "/"
		entries = append(entries,
			storedzip.Entry{Name: dir, Modified: at},
			storedzip.Entry{Name: dir + c.folder + ".m3u", Modified: at, Data: a.zipPlaylist(c.name, c.calls)})
		files := slices.Clone(c.calls)
		slices.SortFunc(files, byStart)
		for _, zc := range files {
			entries = append(entries, file(dir, zc))
		}
	}
	for _, c := range loose {
		entries = append(entries, file("", c))
	}
	archive, err := storedzip.New(entries)
	if err != nil {
		return builtZip{}, err
	}

	// Named for what's in it, so a call left out changes nothing else.
	name := "Calls"
	if len(convs) == 1 && len(loose) == 0 {
		if s := filename.Safe(convs[0].name); s != "" {
			name = s
		}
	}
	name += " " + first.Format("2006-01-02")
	if l.parts > 0 {
		name += fmt.Sprintf(" part %d of %d", l.part, l.parts)
	}
	return builtZip{archive: archive, name: name + ".zip", etag: zipETag(entries)}, nil
}

// zipETag names the zip's bytes: everything they're made of, with a
// recording by its call and size, as a recording never changes once
// stored (ADR 0004). A call left out or added, a renamed talkgroup or a
// changed time zone changes it, and a resumed download with the old one
// in If-Range starts again (ADR 0015).
func zipETag(entries []storedzip.Entry) string {
	h := sha256.New()
	w := func(s string) { _, _ = h.Write(binary.AppendUvarint(nil, uint64(len(s)))); _, _ = io.WriteString(h, s) }
	w("radioarchive zip 1")
	for _, e := range entries {
		w(e.Name)
		w(e.Modified.Format(time.RFC3339Nano))
		switch src, ok := e.Source.(recordingSource); {
		case ok:
			w(src.id)
			w(strconv.FormatInt(e.Size, 10))
		default:
			w(string(e.Data))
		}
	}
	return `"` + base64.RawURLEncoding.EncodeToString(h.Sum(nil)[:18]) + `"`
}

// recordingSource opens a call's recording through the gate when the zip
// reaches it, so a call hidden or deleted while the zip streams is never
// sent: the zip stops short instead.
type recordingSource struct {
	ctx context.Context
	g   *gate.Gate
	id  string
}

func (s recordingSource) Open() (storedzip.File, error) { return s.g.Recording(s.ctx, s.id) }

// manifest.json: the conversations and their calls, with names and notes.

type manifestCall struct {
	ID        string `json:"id"`
	File      string `json:"file"`
	StartedAt string `json:"started_at"`
}

type manifestConversation struct {
	Number int            `json:"number"`
	Folder string         `json:"folder"`
	Name   string         `json:"name"`
	Note   string         `json:"note"`
	Calls  []manifestCall `json:"calls"`
}

type manifest struct {
	Format        int                    `json:"format"`
	Part          int                    `json:"part,omitempty"`
	Parts         int                    `json:"parts,omitempty"`
	Conversations []manifestConversation `json:"conversations"`
	Calls         []manifestCall         `json:"calls"`
}

func manifestCallOf(dir string, c *zipCall) manifestCall {
	return manifestCall{ID: c.ID, File: dir + c.file, StartedAt: c.StartedAt.Format(timeFormat)}
}

func jsonIndented(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// index.csv: one row per call (R65).

// ZipIndexColumns are index.csv's columns. A list's cells (groups, and
// each transmission's unit, alias and offset) are separated by ";". A unit
// with no alias has an empty one; an alias never holds ";" (migration
// 00016).
var ZipIndexColumns = []string{"call_id", "start_utc", "end_utc", "start_local", "end_local", "duration_s",
	"system", "talkgroup_id", "talkgroup_name", "talkgroup_label", "groups", "tag",
	"unit_ids", "unit_aliases", "unit_offsets_s", "file"}

const localTimeFormat = "2006-01-02T15:04:05.000-07:00"

func (a *API) zipIndex(calls []*zipCall) []byte {
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	w.UseCRLF = true
	_ = w.Write(ZipIndexColumns)
	for _, c := range calls {
		var end, endLocal, dur string
		if c.EndedAt != nil {
			end, endLocal = c.EndedAt.Format(timeFormat), c.EndedAt.In(a.loc).Format(localTimeFormat)
			dur = seconds(c.EndedAt.Sub(c.StartedAt).Milliseconds())
		}
		units, aliases, offsets := make([]string, 0, len(c.Transmissions)), make([]string, 0, len(c.Transmissions)),
			make([]string, 0, len(c.Transmissions))
		for _, t := range c.Transmissions {
			units = append(units, strconv.FormatInt(t.Unit, 10))
			aliases = append(aliases, t.Alias)
			offsets = append(offsets, seconds(int64(t.OffsetMS)))
		}
		system := c.System.Name
		if system == "" {
			system = c.System.Label
		}
		_ = w.Write([]string{c.ID, c.StartedAt.Format(timeFormat), end, c.StartedAt.In(a.loc).Format(localTimeFormat),
			endLocal, dur, system, strconv.Itoa(int(c.Talkgroup.ID)), c.Talkgroup.Name, c.Talkgroup.Label,
			strings.Join(c.groups, ";"), c.Talkgroup.Tag,
			strings.Join(units, ";"), strings.Join(aliases, ";"), strings.Join(offsets, ";"), c.file})
	}
	w.Flush()
	return b.Bytes()
}

func seconds(ms int64) string { return fmt.Sprintf("%d.%03d", ms/1000, ms%1000) }

// The conversation's playlist: its calls in its order.
func (a *API) zipPlaylist(name string, calls []*zipCall) []byte {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#PLAYLIST:" + oneLine(name) + "\n")
	for _, c := range calls {
		var secs int64
		if c.EndedAt != nil {
			secs = (c.EndedAt.Sub(c.StartedAt).Milliseconds() + 500) / 1000
		}
		fmt.Fprintf(&b, "#EXTINF:%d,%s %s\n%s\n", secs, oneLine(c.Talkgroup.Name),
			c.StartedAt.In(a.loc).Format("2006-01-02 15:04:05"), c.file)
	}
	return []byte(b.String())
}

// oneLine keeps a name on its playlist line.
func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			return ' '
		}
		return r
	}, s)
}
