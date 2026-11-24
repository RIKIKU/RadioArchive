package server_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
	"github.com/RIKIKU/RadioArchive/internal/public"
	"github.com/RIKIKU/RadioArchive/internal/server"
)

// The A10 harness (SPEC.md A10, R32, ADR 0004): every public route,
// exercised with a hidden talkgroup's calls.
//
// a10Probes holds, for each public route the server mounts, requests that
// name one call. TestIntegrationA10 fails while any public route has no
// probes, so a new surface (the catalogue, search, the live feed, zips) joins
// the harness to exist. Surfaces that name no call (the catalogue, search)
// add probes that ignore the ID; the leak check still applies to them.
var a10Probes = map[string][]a10Probe{
	"QUERY " + public.CallsPath: callListProbes("QUERY"),
	"POST " + public.CallsPath:  callListProbes("POST"),
	"GET " + public.RecordingPath: {
		{"whole", func(f *a10World, id string) *http.Request { return f.req("GET", public.RecordingURL(id), "") }},
		{"a range", func(f *a10World, id string) *http.Request {
			r := f.req("GET", public.RecordingURL(id), "")
			r.Header.Set("Range", "bytes=0-99")
			return r
		}},
		{"HEAD", func(f *a10World, id string) *http.Request { return f.req("HEAD", public.RecordingURL(id), "") }},
	},
}

func callListProbes(method string) []a10Probe {
	return []a10Probe{
		{"alone", func(f *a10World, id string) *http.Request {
			return f.req(method, public.CallsPath, fmt.Sprintf(`{"ids":[%q]}`, id))
		}},
		{"between public calls", func(f *a10World, id string) *http.Request {
			return f.req(method, public.CallsPath, fmt.Sprintf(`{"ids":[%q,%q,%q]}`, f.public[0], id, f.public[1]))
		}},
	}
}

type a10Probe struct {
	name string
	req  func(f *a10World, id string) *http.Request
}

// a10World is the fixture A10 runs against: talkgroup 1003 (hidden) with 3
// recordings, beside public calls and calls unavailable for every other
// reason.
type a10World struct {
	e      *env
	public []string // on 1001
	// unavailable names every way a call can be unavailable; each must
	// answer exactly as deleted does.
	unavailable map[string]string
	deleted     string
	secrets     []string // nothing public may contain these
	secretAudio [][]byte
}

func (f *a10World) req(method, path, body string) *http.Request {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	r, err := http.NewRequest(method, f.e.srv.URL+path, rd)
	if err != nil {
		panic(err)
	}
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	return r
}

func newA10World(t *testing.T) *a10World {
	t.Helper()
	e := newEnv(t)
	_, key := e.recorder(t)
	f := &a10World{e: e, unavailable: map[string]string{}}
	n := int64(100)
	store1 := func(tg int32, patched []int32, seed byte) string {
		t.Helper()
		n++
		audio := oggBytes(seed)
		m := recording(cp.CallID{CallNum: n, StartTime: 1764000100 + n}, audio)
		m.Talkgroup, m.PatchedTalkgroups = tg, patched
		code, resp := e.upload(t, key, m, audio)
		if code != 201 {
			t.Fatalf("upload on %d: %d %+v", tg, code, resp)
		}
		return resp.CallID
	}
	f.public = []string{store1(1001, []int32{1002}, 'p'), store1(1001, nil, 'q')}
	for i, seed := range []byte{'h', 'i', 'j'} {
		f.unavailable[fmt.Sprintf("hidden 1003 #%d", i+1)] = store1(1003, nil, seed)
		f.secretAudio = append(f.secretAudio, oggBytes(seed)[:40])
	}
	f.unavailable["1001 patched with hidden 1003"] = store1(1001, []int32{1003}, 'k')
	f.secretAudio = append(f.secretAudio, oggBytes('k')[:40])
	// A stored call that's now ignored: patched with 1005, which is
	// listed as ignored after the call was stored (R16, R30).
	f.unavailable["1001 patched with 1005, since ignored"] = store1(1001, []int32{1005}, 'm')
	f.secretAudio = append(f.secretAudio, oggBytes('m')[:40])
	e.mustAdmin(t, "PUT", fmt.Sprintf("/systems/%d/talkgroups/1005", e.sys),
		map[string]any{"label": "SECRET 5", "name": "Covert ops", "tag": "other", "state": "ignored"}, 201)
	f.unavailable["never stored"] = "6f1d2c3b-4a59-4e8f-9d7c-0b1a2c3d4e5f"
	f.unavailable["not a call ID"] = "1003"

	// Deleted, as retention deletes a call: its row and its file.
	f.deleted = store1(1001, nil, 'd')
	var path string
	if err := e.st.Pool.QueryRow(context.Background(),
		`DELETE FROM calls WHERE id = $1::uuid RETURNING recording_path`, f.deleted).Scan(&path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(e.dir, filepath.FromSlash(path))); err != nil {
		t.Fatal(err)
	}

	f.secrets = []string{"1003", "1005", "Admin", "ADMIN", "Covert ops", "SECRET 5"}
	for k, id := range f.unavailable {
		if !strings.HasPrefix(k, "never") && !strings.HasPrefix(k, "not") {
			f.secrets = append(f.secrets, id)
		}
	}
	return f
}

// answer is everything a response says: status, headers (but Date) and
// body.
type answer struct {
	status int
	header http.Header
	body   []byte
}

func (a answer) String() string {
	var keys []string
	for k := range a.header {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	fmt.Fprintf(&b, "%d\n", a.status)
	for _, k := range keys {
		fmt.Fprintf(&b, "%s: %s\n", k, strings.Join(a.header[k], ", "))
	}
	fmt.Fprintf(&b, "\n%q", a.body)
	return b.String()
}

func do(t *testing.T, r *http.Request) answer {
	t.Helper()
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	h := resp.Header.Clone()
	h.Del("Date")
	return answer{status: resp.StatusCode, header: h, body: b}
}

// leaks reports what of the hidden talkgroup an answer gives away. Public
// call IDs are random, so they're blanked first: one may contain "1003".
func (f *a10World) leaks(a answer) []string {
	text := a.String()
	raw := string(a.body)
	for _, id := range f.public {
		text = strings.ReplaceAll(text, id, "<public call>")
		raw = strings.ReplaceAll(raw, id, "<public call>")
	}
	var out []string
	for _, s := range f.secrets {
		if strings.Contains(text, s) || strings.Contains(raw, s) {
			out = append(out, s)
		}
	}
	for _, s := range f.secretAudio {
		if bytes.Contains(a.body, s) {
			out = append(out, "hidden audio")
		}
	}
	return out
}

// TestIntegrationA10 is SPEC.md A10 for storage and the API (M1): with 1003
// hidden and 3 recordings on it, no public endpoint returns 1003, its name
// or its audio, and its calls answer exactly as a deleted call does.
func TestIntegrationA10(t *testing.T) {
	f := newA10World(t)

	// Every public route the server mounts is in the harness, and every
	// route in the harness is mounted.
	var mounted, probed []string
	for _, r := range f.e.api.Routes() {
		if r.Kind == server.PublicRoute {
			mounted = append(mounted, r.Pattern)
		}
	}
	for p := range a10Probes {
		probed = append(probed, p)
	}
	slices.Sort(mounted)
	slices.Sort(probed)
	if len(mounted) == 0 || !slices.Equal(mounted, probed) {
		t.Fatalf("public routes %q, A10 probes %q: every public route needs probes", mounted, probed)
	}

	for _, route := range probed {
		for _, p := range a10Probes[route] {
			t.Run(route+"/"+p.name, func(t *testing.T) {
				// The probe works: a public call is served.
				pub := do(t, p.req(f, f.public[0]))
				if pub.status/100 != 2 {
					t.Errorf("a public call: %s", pub)
				}
				if l := f.leaks(pub); len(l) != 0 {
					t.Errorf("a public call's answer leaks %q:\n%s", l, pub)
				}
				gone := do(t, p.req(f, f.deleted))
				if l := f.leaks(gone); len(l) != 0 {
					t.Errorf("a deleted call's answer leaks %q:\n%s", l, gone)
				}
				for why, id := range f.unavailable {
					got := do(t, p.req(f, id))
					if got.String() != gone.String() {
						t.Errorf("%s answers\n%s\nbut a deleted call answers\n%s", why, got, gone)
					}
					if l := f.leaks(got); len(l) != 0 {
						t.Errorf("%s leaks %q:\n%s", why, l, got)
					}
				}
			})
		}
	}

	// Nothing was deleted to hide them: the admin API still plays every
	// one (R30, R34).
	for why, id := range f.unavailable {
		if strings.HasPrefix(why, "never") || strings.HasPrefix(why, "not") {
			continue
		}
		code, _ := f.e.adminReq(t, "test admin", "GET", "/calls/"+id+"/recording", nil)
		if code != 200 {
			t.Errorf("admin playback of %s = %d, want 200", why, code)
		}
	}
}
