package server_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
	"github.com/RIKIKU/RadioArchive/internal/public"
)

// zipReq is the request for a zip of req's calls by method: GET and HEAD
// with the list packed in the URL, QUERY and POST with it in the body.
func (f *a10World) zipReq(method string, req public.ZipRequest) *http.Request {
	switch method {
	case "GET", "HEAD":
		return f.req(method, public.ZipURL(req), "")
	}
	b, err := json.Marshal(req)
	if err != nil {
		panic(err)
	}
	return f.req(method, public.ZipListPath, string(b))
}

func (e *env) zip(t *testing.T, method string, req public.ZipRequest, h ...string) answer {
	t.Helper()
	r := (&a10World{e: e}).zipReq(method, req)
	for i := 0; i+1 < len(h); i += 2 {
		r.Header.Set(h[i], h[i+1])
	}
	return do(t, r)
}

type zipFile struct {
	name     string
	body     []byte
	modified time.Time
}

// unzip reads a zip with the standard library's reader, which checks each
// file's CRC.
func unzip(t *testing.T, b []byte) []zipFile {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	var out []zipFile
	for _, f := range zr.File {
		if f.Method != zip.Store {
			t.Errorf("%s is compressed (method %d)", f.Name, f.Method)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("%s: %v", f.Name, err)
		}
		out = append(out, zipFile{f.Name, body, f.Modified})
	}
	return out
}

func zipNames(fs []zipFile) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.name)
	}
	return out
}

func zipBody(t *testing.T, fs []zipFile, name string) []byte {
	t.Helper()
	for _, f := range fs {
		if f.name == name {
			return f.body
		}
	}
	t.Fatalf("no %s in the zip: %q", name, zipNames(fs))
	return nil
}

// TestIntegrationZipA18 is SPEC.md A18, the server's half: 3 calls in the
// conversation "Structure fire" and 1 loose call, downloaded twice, give
// the same bytes, laid out as R65 says, and a broken download resumed with
// a range request gives them too.
func TestIntegrationZipA18(t *testing.T) {
	e := newEnv(t)
	_, key := e.recorder(t)
	loc := deploymentTZ(t)
	at := time.Date(2026, 3, 4, 5, 6, 7, 120e6, loc)
	c1, a1 := e.storeStarted(t, key, 1, 1001, at, 9750*time.Millisecond)
	c2, a2 := e.storeStarted(t, key, 2, 1002, at.Add(time.Minute), 4*time.Second)
	c3, a3 := e.storeStarted(t, key, 3, 1001, at.Add(2*time.Minute), 3*time.Second)
	loose, a4 := e.storeStarted(t, key, 4, 1001, at.Add(-time.Hour), 2*time.Second)
	req := public.ZipRequest{
		Calls: []string{loose},
		// Not in time order: the playlist and the manifest keep the
		// conversation's order.
		Conversations: []public.ZipConversation{{Name: "Structure fire", Note: "Two alarms, then a third", Calls: []string{c2, c1, c3}}},
	}

	first := e.zip(t, "GET", req)
	second := e.zip(t, "GET", req)
	if first.status != 200 || !bytes.Equal(first.body, second.body) || first.String() != second.String() {
		t.Fatalf("two downloads differ:\n%s\n%s", first, second)
	}
	for k, want := range map[string]string{
		"Content-Type":        "application/zip",
		"Content-Disposition": `attachment; filename="Calls 2026-03-04.zip"`,
		"Cache-Control":       "no-store",
		"Accept-Ranges":       "bytes",
		"Content-Length":      fmt.Sprint(len(first.body)),
	} {
		if got := first.header.Get(k); got != want {
			t.Errorf("%s: %q, want %q", k, got, want)
		}
	}
	etag := first.header.Get("ETag")
	if !strings.HasPrefix(etag, `"`) || len(etag) != 26 {
		t.Errorf("ETag %q: want a strong one", etag)
	}

	fs := unzip(t, first.body)
	dir := "01 Structure fire/"
	names := []string{"manifest.json", "index.csv", dir, dir + "01 Structure fire.m3u",
		dir + "2026-03-04_050607_DISP N_1001.opus", dir + "2026-03-04_050707_FG 41_1002.opus",
		dir + "2026-03-04_050807_DISP N_1001.opus", "2026-03-04_040607_DISP N_1001.opus"}
	if got := zipNames(fs); !slices.Equal(got, names) {
		t.Fatalf("the zip holds\n%q\nwant\n%q", got, names)
	}
	for name, audio := range map[string][]byte{names[4]: a1, names[5]: a2, names[6]: a3, names[7]: a4} {
		if !bytes.Equal(zipBody(t, fs, name), audio) {
			t.Errorf("%s isn't the stored recording", name)
		}
	}
	// Times from the calls: each call's file at its start.
	if f := fs[5]; !f.modified.Equal(at.Add(time.Minute).Truncate(time.Second)) {
		t.Errorf("%s modified %s", f.name, f.modified)
	}
	if got, want := string(zipBody(t, fs, names[3])), "#EXTM3U\n#PLAYLIST:Structure fire\n"+
		"#EXTINF:4,Fireground 41 2026-03-04 05:07:07\n2026-03-04_050707_FG 41_1002.opus\n"+
		"#EXTINF:10,Dispatch North 2026-03-04 05:06:07\n2026-03-04_050607_DISP N_1001.opus\n"+
		"#EXTINF:3,Dispatch North 2026-03-04 05:08:07\n2026-03-04_050807_DISP N_1001.opus\n"; got != want {
		t.Errorf("the playlist:\n%s\nwant\n%s", got, want)
	}

	var m struct {
		Format        int `json:"format"`
		Conversations []struct {
			Number int    `json:"number"`
			Folder string `json:"folder"`
			Name   string `json:"name"`
			Note   string `json:"note"`
			Calls  []struct {
				ID   string `json:"id"`
				File string `json:"file"`
			} `json:"calls"`
		} `json:"conversations"`
		Calls []struct {
			ID   string `json:"id"`
			File string `json:"file"`
		} `json:"calls"`
	}
	if err := json.Unmarshal(zipBody(t, fs, "manifest.json"), &m); err != nil {
		t.Fatal(err)
	}
	if m.Format != 1 || len(m.Conversations) != 1 || len(m.Calls) != 1 || m.Calls[0].ID != loose || m.Calls[0].File != names[7] {
		t.Fatalf("manifest: %+v", m)
	}
	conv := m.Conversations[0]
	if conv.Number != 1 || conv.Folder != "01 Structure fire" || conv.Name != "Structure fire" ||
		conv.Note != "Two alarms, then a third" || len(conv.Calls) != 3 ||
		conv.Calls[0].ID != c2 || conv.Calls[1].ID != c1 || conv.Calls[2].ID != c3 || conv.Calls[0].File != names[5] {
		t.Errorf("manifest's conversation: %+v", conv)
	}

	rows, err := csv.NewReader(bytes.NewReader(zipBody(t, fs, "index.csv"))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 || !slices.Equal(rows[0], public.ZipIndexColumns) {
		t.Fatalf("index.csv: %q", rows)
	}
	want := []string{c1, "2026-03-03T18:06:07.120Z", "2026-03-03T18:06:16.870Z", "2026-03-04T05:06:07.120+11:00",
		"2026-03-04T05:06:16.870+11:00", "9.750", "Example network", "1001", "Dispatch North", "DISP N", "North",
		"dispatch", "5001;5123", ";", "0.292;4.830", "2026-03-04_050607_DISP N_1001.opus"}
	if !slices.Equal(rows[2], want) {
		t.Errorf("index.csv's row for the first call:\n%q\nwant\n%q", rows[2], want)
	}
	if rows[1][0] != loose || rows[3][0] != c2 || rows[4][0] != c3 {
		t.Errorf("index.csv's rows aren't by start time: %q", rows)
	}

	// Cut off, then resumed with a range request as a browser does it,
	// If-Range naming what it has.
	cut := len(first.body) / 2
	head := e.zip(t, "GET", req, "Range", fmt.Sprintf("bytes=0-%d", cut-1))
	rest := e.zip(t, "GET", req, "Range", fmt.Sprintf("bytes=%d-", cut), "If-Range", etag)
	if head.status != 206 || rest.status != 206 || !bytes.Equal(append(head.body, rest.body...), first.body) ||
		rest.header.Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", cut, len(first.body)-1, len(first.body)) {
		t.Errorf("resumed: %s\n%s", head, rest)
	}
	// Resumed from inside every part of the zip.
	for _, from := range []int{1, 31, 600, len(first.body) - 200, len(first.body) - 1} {
		if got := e.zip(t, "GET", req, "Range", fmt.Sprintf("bytes=%d-", from), "If-Range", etag); got.status != 206 ||
			!bytes.Equal(got.body, first.body[from:]) {
			t.Errorf("resumed from %d: %s", from, got)
		}
	}
	if a := e.zip(t, "HEAD", req); a.status != 200 || len(a.body) != 0 ||
		a.header.Get("Content-Length") != fmt.Sprint(len(first.body)) || a.header.Get("ETag") != etag {
		t.Errorf("HEAD: %s", a)
	}
	if a := e.zip(t, "GET", req, "If-None-Match", etag); a.status != 304 {
		t.Errorf("If-None-Match: %s", a)
	}

	// The same list by QUERY and POST: the same bytes, whole, even when a
	// range is asked for (ranges are GET's).
	for _, method := range []string{"QUERY", "POST"} {
		if got := e.zip(t, method, req, "Range", "bytes=100-"); got.status != 200 || !bytes.Equal(got.body, first.body) ||
			got.header.Get("ETag") != etag {
			t.Errorf("%s: %s", method, got)
		}
	}

	// Two conversations, numbered by their first start: the second named
	// is first. A call in both is in each folder.
	two := public.ZipRequest{Conversations: []public.ZipConversation{
		{Name: "Later", Calls: []string{c3, c2}},
		{Name: "Earlier: a/b", Calls: []string{c1, c2}},
	}}
	got := e.zip(t, "GET", two)
	if got.status != 200 {
		t.Fatalf("two conversations: %s", got)
	}
	names = []string{"manifest.json", "index.csv",
		"01 Earlier- a-b/", "01 Earlier- a-b/01 Earlier- a-b.m3u",
		"01 Earlier- a-b/2026-03-04_050607_DISP N_1001.opus", "01 Earlier- a-b/2026-03-04_050707_FG 41_1002.opus",
		"02 Later/", "02 Later/02 Later.m3u",
		"02 Later/2026-03-04_050707_FG 41_1002.opus", "02 Later/2026-03-04_050807_DISP N_1001.opus"}
	if fs := unzip(t, got.body); !slices.Equal(zipNames(fs), names) {
		t.Errorf("two conversations hold\n%q\nwant\n%q", zipNames(fs), names)
	}
	// One conversation alone is named for it, and a part says which.
	one := public.ZipRequest{Conversations: []public.ZipConversation{{Name: "Structure fire", Calls: []string{c1}}}, Part: 2, Parts: 3}
	if got := e.zip(t, "GET", one); got.header.Get("Content-Disposition") != `attachment; filename="Structure fire 2026-03-04 part 2 of 3.zip"` {
		t.Errorf("one conversation's part: %s", got)
	}
}

// TestIntegrationA10Zip is A10's zip half beyond the harness's probes: a
// list naming all of 1003's calls, and every other kind of unavailable
// call, loose, in a conversation with public calls and in one of their own,
// gives byte for byte the zip of the list without them, by GET, QUERY and
// POST, and nothing of 1003.
func TestIntegrationA10Zip(t *testing.T) {
	f := newA10World(t)
	var gone []string
	for _, id := range f.unavailable {
		gone = append(gone, id)
	}
	gone = append(gone, f.deleted)
	slices.Sort(gone)
	mixed := public.ZipRequest{
		Calls: append([]string{f.public[1]}, gone...),
		Conversations: []public.ZipConversation{
			{Name: "Structure fire", Note: "Mixed", Calls: append(slices.Clone(gone[:3]), f.public[0])},
			{Name: "Nothing left", Calls: gone[3:]},
		},
	}
	without := public.ZipRequest{
		Calls:         []string{f.public[1]},
		Conversations: []public.ZipConversation{{Name: "Structure fire", Note: "Mixed", Calls: []string{f.public[0]}}},
	}
	// Only unavailable calls loose: the zip is then one conversation's.
	onlyLoose := public.ZipRequest{Calls: gone, Conversations: without.Conversations}
	for _, method := range []string{"GET", "HEAD", "QUERY", "POST"} {
		for _, c := range [][2]public.ZipRequest{{mixed, without}, {onlyLoose, {Conversations: without.Conversations}}} {
			got, want := do(t, f.zipReq(method, c[0])), do(t, f.zipReq(method, c[1]))
			if got.status != 200 || got.String() != want.String() {
				t.Errorf("%s: the mixed list's zip\n%s\nisn't the zip without 1003's calls\n%s", method, got, want)
			}
			if l := f.leaks(got); len(l) != 0 {
				t.Errorf("%s: the zip leaks %q", method, l)
			}
		}
	}
	// And the zip holds the public calls, and only them.
	fs := unzip(t, do(t, f.zipReq("GET", mixed)).body)
	if n := len(fs); n != 6 {
		t.Errorf("the zip holds %q", zipNames(fs))
	}
}

// Two calls on one talkgroup in the same second get file names of their
// own.
func TestIntegrationZipSameName(t *testing.T) {
	e := newEnv(t)
	_, key := e.recorder(t)
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, deploymentTZ(t))
	a, _ := e.storeStarted(t, key, 1, 1001, at, time.Second)
	b, _ := e.storeStarted(t, key, 2, 1001, at.Add(500*time.Millisecond), time.Second)
	got := e.zip(t, "GET", public.ZipRequest{Calls: []string{b, a}})
	want := []string{"manifest.json", "index.csv", "2026-03-04_050607_DISP N_1001.opus", "2026-03-04_050607_DISP N_1001 (2).opus"}
	if fs := unzip(t, got.body); !slices.Equal(zipNames(fs), want) {
		t.Errorf("the zip holds %q, want %q", zipNames(fs), want)
	}
}

// TestIntegrationZipA19 is A19's server half: a call the nightly run
// deletes is left out of the zip, which is then the zip of the list without
// it; a download of the old zip, resumed, starts again (ADR 0015).
func TestIntegrationZipA19(t *testing.T) {
	e := newEnv(t)
	_, key := e.recorder(t)
	e.era(t)
	old := e.storeAt(t, key, 1, 1001, eraNow.Add(-29*day))
	kept := e.storeAt(t, key, 2, 1001, eraNow.Add(-day))
	req := public.ZipRequest{Calls: []string{old}, Conversations: []public.ZipConversation{{Name: "Fire", Calls: []string{kept}}}}
	before := e.zip(t, "GET", req)
	if before.status != 200 || len(unzip(t, before.body)) != 6 {
		t.Fatalf("before the run: %s", before)
	}

	// Two days on, the nightly run deletes the call, now 31 days old.
	e.clock.set(eraNow.Add(2 * day))
	if res, err := e.job.RunNightly(context.Background()); err != nil || res.Calls != 1 {
		t.Fatalf("the run: %+v, %v", res, err)
	}
	e.wantGone(t, "past retention", old)
	after := e.zip(t, "GET", req)
	without := e.zip(t, "GET", public.ZipRequest{Conversations: req.Conversations})
	if after.status != 200 || after.String() != without.String() {
		t.Errorf("after the run, the zip\n%s\nisn't the zip without the deleted call\n%s", after, without)
	}
	if got := zipNames(unzip(t, after.body)); len(got) != 5 || slices.ContainsFunc(got, func(n string) bool { return !strings.HasPrefix(n, "01 Fire") && strings.HasSuffix(n, ".opus") }) {
		t.Errorf("after the run, the zip holds %q", got)
	}
	// The download begun before the run, resumed: If-Range no longer
	// matches, so the whole new zip comes back and the download starts
	// again, rather than the old zip's start with the new one's end.
	cut := len(before.body) / 2
	resumed := e.zip(t, "GET", req, "Range", fmt.Sprintf("bytes=%d-", cut), "If-Range", before.header.Get("ETag"))
	if resumed.status != 200 || !bytes.Equal(resumed.body, after.body) {
		t.Errorf("resumed after the run: %s", resumed)
	}
	// Nothing left: the zip isn't there.
	e.clock.set(eraNow.Add(40 * day))
	if _, err := e.job.RunNightly(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := e.zip(t, "GET", req); got.status != 404 || !strings.Contains(string(got.body), `"calls_unavailable"`) {
		t.Errorf("with every call deleted: %s", got)
	}
}

// The bounds (R72, ADR 0015): at most 500 calls a part, and a GET URL of
// at most MaxZipURL characters, which a URL at the budget meets.
func TestIntegrationZipBounds(t *testing.T) {
	e := newEnv(t)
	_, key := e.recorder(t)
	id, _ := e.storeCall(t, key, 1, 1001, nil)
	ids := make([]string, 501)
	ids[0] = id
	for i := 1; i < len(ids); i++ {
		ids[i] = fmt.Sprintf("%08x-0000-4000-8000-%012x", i, i)
	}
	for _, method := range []string{"GET", "QUERY", "POST"} {
		got := e.zip(t, method, public.ZipRequest{Calls: ids})
		want := 400
		if method == "GET" {
			want = 414 // 501 calls are past the URL's budget first
		}
		if got.status != want {
			t.Errorf("%s of 501 calls: %s", method, got)
		}
		if got := e.zip(t, method, public.ZipRequest{Calls: ids[:500]}); method != "GET" && got.status != 200 {
			t.Errorf("%s of 500 calls: %s", method, got)
		}
	}
	if got := e.zip(t, "POST", public.ZipRequest{Calls: ids}); !strings.Contains(string(got.body), `"too_many_calls"`) {
		t.Errorf("501 calls: %s", got)
	}

	// A URL at the budget, and the next longer: one available call, in
	// conversations whose notes fill the rest.
	var at, past public.ZipRequest
	for n := 0; n <= 3*public.MaxConversationNote && past.Calls == nil; n++ {
		req := public.ZipRequest{Calls: ids[1:300]}
		for left := n; left > 0 || len(req.Conversations) == 0; left -= public.MaxConversationNote {
			req.Conversations = append(req.Conversations, public.ZipConversation{Name: "Note",
				Note: strings.Repeat("n", max(0, min(left, public.MaxConversationNote))), Calls: []string{id}})
		}
		switch l := len(public.ZipURL(req)); {
		case l == public.MaxZipURL:
			at = req
		case l > public.MaxZipURL:
			past = req
		}
	}
	if at.Calls == nil || past.Calls == nil {
		t.Fatal("no list's URL is at the budget")
	}
	if got := e.zip(t, "GET", at); got.status != 200 {
		t.Errorf("a URL at the budget: %s", got)
	}
	if got := e.zip(t, "GET", past); got.status != 414 || !strings.Contains(string(got.body), `"zip_url_too_long"`) {
		t.Errorf("a URL past the budget (%d characters): %s", len(public.ZipURL(past)), got)
	}

	// Not a packed list, or not a valid one: refused, saying nothing of
	// any call.
	for _, path := range []string{"/api/v1/zip/AQ", "/api/v1/zip/not-base64!", "/api/v1/zip/" + strings.Repeat("A", 40)} {
		if got := e.pub(t, "GET", path, ""); got.status != 400 || !strings.Contains(string(got.body), `"invalid_zip"`) {
			t.Errorf("%s: %s", path, got)
		}
	}
	for _, body := range []string{`{"calls":"x"}`, `{"ids":[]}`, `{"calls":[]}{}`,
		`{"calls":[],"conversations":[{"name":"x","calls":[]}],"part":2,"parts":1}`} {
		if got := e.pub(t, "POST", public.ZipListPath, body); got.status != 400 {
			t.Errorf("%s: %s", body, got)
		}
	}
	if got := e.pub(t, "POST", public.ZipListPath, `{"calls":[]}`); got.status != 404 {
		t.Errorf("no calls: %s", got)
	}
}

// storeAudio stores a call on tg that started at start, with audio as its
// recording.
func (e *env) storeAudio(t *testing.T, key string, n int64, tg int32, start time.Time, audio []byte) string {
	t.Helper()
	m := recording(cp.CallID{CallNum: n, StartTime: start.Unix()}, audio)
	m.Talkgroup, m.PatchedTalkgroups = tg, []int32{}
	m.StartTimeMS, m.StopTimeMS = start.UnixMilli(), start.Add(30*time.Second).UnixMilli()
	code, resp := e.upload(t, key, m, audio)
	if code != 201 {
		t.Fatalf("upload on %d: %d %+v", tg, code, resp)
	}
	return resp.CallID
}

// bigAudio is n bytes of recording, made up, different for every seed.
func bigAudio(seed, n int) []byte {
	b := make([]byte, n)
	copy(b, "OggS")
	x := uint32(seed)*0x9e3779b9 | 1
	for i := 4; i < n; i++ {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		b[i] = byte(x)
	}
	return b
}

// A call hidden while its zip streams is never sent: the zip stops short
// before it (P1, R32).
func TestIntegrationZipHiddenWhileStreaming(t *testing.T) {
	e := newEnv(t)
	_, key := e.recorder(t)
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	const size = 8 << 20
	a := e.storeAudio(t, key, 1, 1001, at, bigAudio(1, size))
	b := e.storeAudio(t, key, 2, 1001, at.Add(time.Minute), bigAudio(2, size))
	later := bigAudio(3, size)
	c := e.storeAudio(t, key, 3, 1002, at.Add(2*time.Minute), later)

	// A small receive buffer, so the server can't run far ahead of what's
	// read.
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err == nil {
			err = c.(*net.TCPConn).SetReadBuffer(64 << 10)
		}
		return c, err
	}}}
	r := (&a10World{e: e}).zipReq("GET", public.ZipRequest{Calls: []string{a, b, c}})
	resp, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.ContentLength < 3*size {
		t.Fatalf("the zip: %d, %d bytes", resp.StatusCode, resp.ContentLength)
	}
	got := make([]byte, 64<<10)
	if _, err := io.ReadFull(resp.Body, got); err != nil {
		t.Fatal(err)
	}
	e.mustAdmin(t, "PATCH", fmt.Sprintf("/systems/%d/talkgroups/1002", e.sys), map[string]any{"state": "hidden"}, 200)
	rest, err := io.ReadAll(resp.Body)
	got = append(got, rest...)
	if err == nil || int64(len(got)) >= resp.ContentLength {
		t.Errorf("the zip ended with %v after %d of %d bytes: want it cut short", err, len(got), resp.ContentLength)
	}
	if bytes.Contains(got, later[1<<20:1<<20+64]) {
		t.Error("the hidden call's audio was sent")
	}
	if !bytes.Contains(got, bigAudio(2, size)[size-64:]) {
		t.Error("the public call before it wasn't sent whole")
	}
}

// TestIntegrationZipMemory: a 500-call zip streams holding no more than
// one call's file at a time, well inside the API's 256 MB (SPEC.md section
// 7). The zip here is 500 calls of 256 KB, 128 MB; the server and this
// client share the process, so what's measured is both.
func TestIntegrationZipMemory(t *testing.T) {
	e := newEnv(t)
	_, key := e.recorder(t)
	const calls, size = 500, 256 << 10
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	req := public.ZipRequest{}
	stored := map[[32]byte]bool{}
	for i := range calls {
		audio := bigAudio(i, size)
		id := e.storeAudio(t, key, int64(i+1), 1001, at.Add(time.Duration(i)*time.Minute), audio)
		stored[sha256.Sum256(audio)] = true
		if i%5 == 0 {
			req.Conversations = append(req.Conversations, public.ZipConversation{Name: fmt.Sprintf("Conversation %d", i)})
		}
		c := &req.Conversations[len(req.Conversations)-1]
		c.Calls = append(c.Calls, id)
	}

	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)
	var peak atomic.Uint64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		var m runtime.MemStats
		for {
			select {
			case <-stop:
				return
			case <-time.After(2 * time.Millisecond):
			}
			runtime.ReadMemStats(&m)
			if m.HeapInuse > peak.Load() {
				peak.Store(m.HeapInuse)
			}
		}
	})
	r := (&a10World{e: e}).zipReq("POST", req)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	n, err := io.Copy(h, resp.Body)
	resp.Body.Close()
	close(stop)
	wg.Wait()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	if err != nil || resp.StatusCode != 200 || n != resp.ContentLength || n < calls*size {
		t.Fatalf("the zip: %d, %d of %d bytes, %v", resp.StatusCode, n, resp.ContentLength, err)
	}
	allocated := after.TotalAlloc - base.TotalAlloc
	grew := int64(peak.Load()) - int64(base.HeapInuse)
	t.Logf("a %d-call zip of %d MB: heap in use %d MB before, at most %d MB while streaming (+%d MB); %d MB allocated in all",
		calls, n>>20, base.HeapInuse>>20, peak.Load()>>20, grew>>20, allocated>>20)
	// Holding the calls' files would allocate at least the zip's size.
	if allocated > 32<<20 {
		t.Errorf("%d MB allocated while streaming %d MB: the zip isn't streamed", allocated>>20, n>>20)
	}
	if grew > 64<<20 {
		t.Errorf("the heap grew by %d MB while streaming", grew>>20)
	}

	// And it's whole: every call, in its conversation, byte for byte.
	again := e.zip(t, "POST", req)
	fs := unzip(t, again.body)
	if len(fs) != 2+2*len(req.Conversations)+calls {
		t.Fatalf("%d entries", len(fs))
	}
	if got := sha256.Sum256(again.body); !bytes.Equal(got[:], h.Sum(nil)) {
		t.Error("two downloads of the 500-call zip differ")
	}
	files := 0
	for _, f := range fs {
		if strings.HasSuffix(f.name, ".opus") {
			files++
			if !stored[sha256.Sum256(f.body)] {
				t.Errorf("%s isn't a stored recording", f.name)
			}
		}
	}
	if files != calls {
		t.Errorf("%d files, want %d", files, calls)
	}
}
