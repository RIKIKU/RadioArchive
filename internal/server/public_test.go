package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
	"github.com/RIKIKU/RadioArchive/internal/gate"
	"github.com/RIKIKU/RadioArchive/internal/public"
)

func (e *env) pub(t *testing.T, method, path, body string, h ...string) answer {
	t.Helper()
	f := &a10World{e: e}
	r := f.req(method, path, body)
	for i := 0; i+1 < len(h); i += 2 {
		r.Header.Set(h[i], h[i+1])
	}
	return do(t, r)
}

func (e *env) publicCalls(t *testing.T, ids ...string) []public.Call {
	t.Helper()
	b, _ := json.Marshal(public.CallsRequest{IDs: ids})
	a := e.pub(t, "QUERY", public.CallsPath, string(b))
	if a.status != 200 {
		t.Fatalf("QUERY calls: %s", a)
	}
	return decodeJSON[public.CallsResponse](t, a.body).Calls
}

func (e *env) storeCall(t *testing.T, key string, n int64, tg int32, patched []int32) (string, []byte) {
	t.Helper()
	audio := oggBytes(byte(n))
	m := recording(cp.CallID{CallNum: n, StartTime: 1764000200 + n}, audio)
	m.Talkgroup, m.PatchedTalkgroups = tg, patched
	code, resp := e.upload(t, key, m, audio)
	if code != 201 {
		t.Fatalf("upload on %d: %d %+v", tg, code, resp)
	}
	return resp.CallID, audio
}

// TestIntegrationPlayback: a public recording is the stored file, served
// with range requests and cached as R74 says.
func TestIntegrationPlayback(t *testing.T) {
	e := newEnv(t)
	_, key := e.recorder(t)
	id, audio := e.storeCall(t, key, 1, 1001, []int32{1002})

	whole := e.pub(t, "GET", public.RecordingURL(id), "")
	if whole.status != 200 || !bytes.Equal(whole.body, audio) {
		t.Fatalf("GET recording: %s", whole)
	}
	for k, want := range map[string]string{
		"Content-Type":   "audio/ogg; codecs=opus",
		"Cache-Control":  "private, max-age=31536000, immutable",
		"Accept-Ranges":  "bytes",
		"Content-Length": fmt.Sprint(len(audio)),
	} {
		if got := whole.header.Get(k); got != want {
			t.Errorf("%s: %q, want %q", k, got, want)
		}
	}
	mod := whole.header.Get("Last-Modified")
	if _, err := http.ParseTime(mod); err != nil {
		t.Errorf("Last-Modified %q: %v", mod, err)
	}

	part := e.pub(t, "GET", public.RecordingURL(id), "", "Range", "bytes=4-9")
	if part.status != 206 || !bytes.Equal(part.body, audio[4:10]) ||
		part.header.Get("Content-Range") != fmt.Sprintf("bytes 4-9/%d", len(audio)) {
		t.Errorf("range: %s", part)
	}
	tail := e.pub(t, "GET", public.RecordingURL(id), "", "Range", "bytes=-4")
	if tail.status != 206 || !bytes.Equal(tail.body, audio[len(audio)-4:]) {
		t.Errorf("suffix range: %s", tail)
	}
	if a := e.pub(t, "GET", public.RecordingURL(id), "", "Range", "bytes=9999-"); a.status != 416 {
		t.Errorf("unsatisfiable range: %s", a)
	}
	if a := e.pub(t, "GET", public.RecordingURL(id), "", "If-Modified-Since", mod); a.status != 304 {
		t.Errorf("conditional GET: %s", a)
	}
	if a := e.pub(t, "HEAD", public.RecordingURL(id), ""); a.status != 200 || len(a.body) != 0 ||
		a.header.Get("Content-Length") != fmt.Sprint(len(audio)) {
		t.Errorf("HEAD: %s", a)
	}
	// Upper case names the same call.
	if a := e.pub(t, "GET", public.RecordingURL(strings.ToUpper(id)), ""); a.status != 200 {
		t.Errorf("upper-case ID: %s", a)
	}

	// A row whose file has gone (as while retention deletes both) is as
	// good as deleted.
	if err := os.Remove(filepath.Join(e.dir, id[:2], id+".opus")); err != nil {
		t.Fatal(err)
	}
	missing := e.pub(t, "GET", public.RecordingURL(id), "")
	never := e.pub(t, "GET", public.RecordingURL("6f1d2c3b-4a59-4e8f-9d7c-0b1a2c3d4e5f"), "")
	if missing.status != 404 || missing.String() != never.String() {
		t.Errorf("missing file:\n%s\nnever stored:\n%s", missing, never)
	}
	var body struct{ Error, Message string }
	if json.Unmarshal(never.body, &body) != nil || body.Error != "call_unavailable" || body.Message != "This call isn't available." {
		t.Errorf("unavailable body %s", never.body)
	}
}

// TestIntegrationCallDetails: the call list gives each available call once,
// in the order asked, with what a listener may see of it, and drops the
// rest (R64, R66, R67).
func TestIntegrationCallDetails(t *testing.T) {
	e := newEnv(t)
	_, key := e.recorder(t)
	a, audio := e.storeCall(t, key, 1, 1001, []int32{1002})
	b, _ := e.storeCall(t, key, 2, 1002, nil)
	hidden, _ := e.storeCall(t, key, 3, 1003, nil)

	got := e.publicCalls(t, b, hidden, "nonsense", a, b)
	if len(got) != 2 || got[0].ID != b || got[1].ID != a {
		t.Fatalf("calls %+v, want %s then %s", got, b, a)
	}
	x := got[1]
	if x.Talkgroup != (public.Talkgroup{ID: 1001, Label: "DISP N", Name: "Dispatch North", Tag: "dispatch"}) ||
		x.System.ID != e.sys || x.System.Name != "Example network" ||
		len(x.PatchedTalkgroups) != 1 || x.PatchedTalkgroups[0] != 1002 ||
		x.StartedAt != "2025-11-24T16:00:00.120Z" || x.EndedAt == nil || *x.EndedAt != "2025-11-24T16:00:09.870Z" ||
		x.DurationMS == nil || *x.DurationMS != 9750 || x.InProgress ||
		len(x.Transmissions) != 2 || x.Transmissions[0].Unit != 5001 || x.Transmissions[0].OffsetMS != 292 ||
		x.Transmissions[0].DurationMS == nil || *x.Transmissions[0].DurationMS != 3788 ||
		x.Recording == nil || x.Recording.URL != public.RecordingURL(a) || x.Recording.Size != int64(len(audio)) {
		t.Errorf("call details %+v", x)
	}
	if len(got[0].PatchedTalkgroups) != 0 || got[0].PatchedTalkgroups == nil {
		t.Errorf("unpatched call's patched_talkgroups %v, want []", got[0].PatchedTalkgroups)
	}

	// Limits and bad bodies (R73, R78).
	ids := make([]string, gate.MaxCalls+1)
	for i := range ids {
		ids[i] = a
	}
	body, _ := json.Marshal(public.CallsRequest{IDs: ids})
	if r := e.pub(t, "POST", public.CallsPath, string(body)); r.status != 400 || !strings.Contains(string(r.body), `"too_many_calls"`) {
		t.Errorf("%d IDs: %s", len(ids), r)
	}
	body, _ = json.Marshal(public.CallsRequest{IDs: ids[:gate.MaxCalls]})
	if r := e.pub(t, "POST", public.CallsPath, string(body)); r.status != 200 {
		t.Errorf("%d IDs: %s", gate.MaxCalls, r)
	}
	for _, bad := range []string{"", "[]", `{"ids":"x"}`, `{"ids":[],"talkgroups":[1003]}`, `{"ids":[]} {}`} {
		if r := e.pub(t, "POST", public.CallsPath, bad); r.status != 400 || !strings.Contains(string(r.body), `"invalid_body"`) {
			t.Errorf("body %q: %s", bad, r)
		}
	}
	if r := e.pub(t, "GET", public.CallsPath, ""); r.status != 405 {
		t.Errorf("GET calls: %s", r)
	}
}

// TestIntegrationInProgress: a live call is listed while in progress, with
// no recording yet, which plays once it arrives.
func TestIntegrationInProgress(t *testing.T) {
	e := newEnv(t)
	name, key := e.recorder(t)
	c := e.open(t, key)
	id := cp.CallID{CallNum: 9, StartTime: 1764000000}
	send(t, c, callStart(id))
	send(t, c, frame(id, 0))
	row := e.waitCall(t, name, id, func(r callRow) bool { return r.frames >= 0 })
	got := e.publicCalls(t, row.id)
	if len(got) != 1 || !got[0].InProgress || got[0].Recording != nil || got[0].EndedAt != nil {
		t.Fatalf("in progress: %+v", got)
	}
	never := e.pub(t, "GET", public.RecordingURL("6f1d2c3b-4a59-4e8f-9d7c-0b1a2c3d4e5f"), "")
	if a := e.pub(t, "GET", public.RecordingURL(row.id), ""); a.String() != never.String() {
		t.Errorf("an unrecorded call's recording: %s", a)
	}
	send(t, c, cp.CallEnd{Type: "call_end", CallID: id, StopTimeMS: 1764000009870, PatchedTalkgroups: []int32{}})
	audio := oggBytes(9)
	if code, resp := e.upload(t, key, recording(id, audio), audio); code != 201 || resp.CallID != row.id {
		t.Fatalf("upload: %d %+v", code, resp)
	}
	got = e.publicCalls(t, row.id)
	if len(got) != 1 || got[0].InProgress || got[0].Recording == nil {
		t.Errorf("recorded: %+v", got)
	}
	if a := e.pub(t, "GET", public.RecordingURL(row.id), ""); a.status != 200 || !bytes.Equal(a.body, audio) {
		t.Errorf("recording: %s", a)
	}
}

// TestIntegrationStateChanges is R30 and SPEC.md A12: hiding a talkgroup
// hides its recordings at once and making it public shows them again;
// ignoring it stops recording and hides what it has; nothing is deleted.
func TestIntegrationStateChanges(t *testing.T) {
	e := newEnv(t)
	name, key := e.recorder(t)
	a, audio := e.storeCall(t, key, 1, 1001, nil)
	b, _ := e.storeCall(t, key, 2, 1002, []int32{1001}) // patched with 1001
	never := "6f1d2c3b-4a59-4e8f-9d7c-0b1a2c3d4e5f"
	path := fmt.Sprintf("/systems/%d/talkgroups/1001", e.sys)
	listed := func() int { return len(e.publicCalls(t, a, b)) }
	gone := func(why string) {
		t.Helper()
		if n := listed(); n != 0 {
			t.Errorf("%s: %d calls listed", why, n)
		}
		want := e.pub(t, "GET", public.RecordingURL(never), "").String()
		for _, id := range []string{a, b} {
			if got := e.pub(t, "GET", public.RecordingURL(id), "").String(); got != want {
				t.Errorf("%s: %s answers\n%s\nwant\n%s", why, id, got, want)
			}
		}
		// Nothing is deleted: the rows, the files and admin playback stay.
		var rows int
		if err := e.st.Pool.QueryRow(context.Background(), `SELECT count(*) FROM calls WHERE id = ANY ($1::uuid[])`,
			[]string{a, b}).Scan(&rows); err != nil || rows != 2 {
			t.Errorf("%s: %d rows (%v)", why, rows, err)
		}
		if _, err := os.Stat(filepath.Join(e.dir, a[:2], a+".opus")); err != nil {
			t.Errorf("%s: %v", why, err)
		}
		if code, body := e.adminReq(t, "test admin", "GET", "/calls/"+a+"/recording", nil); code != 200 || !bytes.Equal(body, audio) {
			t.Errorf("%s: admin playback %d", why, code)
		}
	}
	if listed() != 2 {
		t.Fatal("not public to begin with")
	}

	e.mustAdmin(t, "PATCH", path, map[string]any{"state": "hidden"}, 200)
	gone("hidden")
	e.mustAdmin(t, "PATCH", path, map[string]any{"state": "public"}, 200)
	if n := listed(); n != 2 {
		t.Errorf("public again: %d calls listed, want 2", n)
	}
	if r := e.pub(t, "GET", public.RecordingURL(a), ""); r.status != 200 || !bytes.Equal(r.body, audio) {
		t.Errorf("public again: %s", r)
	}

	// A12: ignored.
	e.mustAdmin(t, "PATCH", path, map[string]any{"state": "ignored"}, 200)
	gone("ignored")
	id := cp.CallID{CallNum: 3, StartTime: 1764000203}
	if code, resp := e.upload(t, key, recording(id, oggBytes(3)), oggBytes(3)); code != 200 || resp.Status != "rejected" {
		t.Errorf("a new call on ignored 1001: %d %+v", code, resp)
	}
	if _, ok := e.call(t, name, id); ok {
		t.Error("a new call on ignored 1001 was stored")
	}
}

// TestIntegrationAdminPlayback: the admin API plays a hidden talkgroup's
// recording, with range requests and uncached; the public API doesn't
// (R34).
func TestIntegrationAdminPlayback(t *testing.T) {
	e := newEnv(t)
	_, key := e.recorder(t)
	id, audio := e.storeCall(t, key, 1, 1003, nil)
	r := (&a10World{e: e}).req("GET", "/api/v1/admin/calls/"+id+"/recording", "")
	r.Header.Set("X-Test-Admin", "test admin")
	r.Header.Set("Range", "bytes=0-3")
	if a := do(t, r); a.status != 206 || string(a.body) != "OggS" || a.header.Get("Cache-Control") != "no-store" ||
		a.header.Get("Content-Type") != "audio/ogg; codecs=opus" {
		t.Errorf("admin range: %s", a)
	}
	if code, body := e.adminReq(t, "test admin", "GET", "/calls/"+id+"/recording", nil); code != 200 || !bytes.Equal(body, audio) {
		t.Errorf("admin playback: %d", code)
	}
	if code, _ := e.adminReq(t, "", "GET", "/calls/"+id+"/recording", nil); code != 401 {
		t.Errorf("admin playback signed out: %d, want 401", code)
	}
	e.wantRefusal(t, "GET", "/calls/6f1d2c3b-4a59-4e8f-9d7c-0b1a2c3d4e5f/recording", nil, 404, "recording_not_found")
	e.wantRefusal(t, "GET", "/calls/nonsense/recording", nil, 404, "recording_not_found")
	if a := e.pub(t, "GET", public.RecordingURL(id), ""); a.status != 404 {
		t.Errorf("public playback of 1003: %s", a)
	}
}
