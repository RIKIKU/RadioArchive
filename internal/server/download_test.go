package server_test

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
	"github.com/RIKIKU/RadioArchive/internal/public"
)

// deploymentTZ is the test deployment's time zone (its TZ), which names
// downloads (R63).
func deploymentTZ(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Australia/Sydney")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

// storeStarted stores a call on tg that started at start and lasted d.
func (e *env) storeStarted(t *testing.T, key string, n int64, tg int32, start time.Time, d time.Duration) (string, []byte) {
	t.Helper()
	audio := oggBytes(byte(n))
	m := recording(cp.CallID{CallNum: n, StartTime: start.Unix()}, audio)
	m.Talkgroup, m.PatchedTalkgroups = tg, []int32{}
	m.StartTimeMS, m.StopTimeMS = start.UnixMilli(), start.Add(d).UnixMilli()
	code, resp := e.upload(t, key, m, audio)
	if code != 201 {
		t.Fatalf("upload on %d: %d %+v", tg, code, resp)
	}
	return resp.CallID, audio
}

// TestIntegrationDownload is SPEC.md A17: a call on 1001 that started
// 2026-03-04 05:06:07 local downloads as the stored bytes, named
// 2026-03-04_050607_DISP N_1001.opus, and a range request resumes it.
func TestIntegrationDownload(t *testing.T) {
	e := newEnv(t)
	_, key := e.recorder(t)
	loc := deploymentTZ(t)
	id, audio := e.storeStarted(t, key, 1, 1001, time.Date(2026, 3, 4, 5, 6, 7, 120e6, loc), 9750*time.Millisecond)

	whole := e.pub(t, "GET", public.DownloadURL(id), "")
	stored, err := os.ReadFile(filepath.Join(e.dir, id[:2], id+".opus"))
	if err != nil {
		t.Fatal(err)
	}
	if whole.status != 200 || sha256.Sum256(whole.body) != sha256.Sum256(stored) || !bytes.Equal(stored, audio) {
		t.Fatalf("download isn't the stored file: %s", whole)
	}
	for k, want := range map[string]string{
		"Content-Disposition": `attachment; filename="2026-03-04_050607_DISP N_1001.opus"`,
		"Content-Type":        "audio/ogg; codecs=opus",
		"Cache-Control":       "private, max-age=31536000, immutable",
		"Accept-Ranges":       "bytes",
		"Content-Length":      fmt.Sprint(len(audio)),
	} {
		if got := whole.header.Get(k); got != want {
			t.Errorf("%s: %q, want %q", k, got, want)
		}
	}

	// A broken download resumes: the rest, if the file is unchanged.
	head := e.pub(t, "GET", public.DownloadURL(id), "", "Range", "bytes=0-99")
	rest := e.pub(t, "GET", public.DownloadURL(id), "", "Range", "bytes=100-",
		"If-Range", whole.header.Get("Last-Modified"))
	if head.status != 206 || rest.status != 206 || !bytes.Equal(append(head.body, rest.body...), audio) ||
		rest.header.Get("Content-Range") != fmt.Sprintf("bytes 100-%d/%d", len(audio)-1, len(audio)) ||
		rest.header.Get("Content-Disposition") != whole.header.Get("Content-Disposition") {
		t.Errorf("resumed:\n%s\n%s", head, rest)
	}
	if a := e.pub(t, "HEAD", public.DownloadURL(id), ""); a.status != 200 || len(a.body) != 0 ||
		a.header.Get("Content-Length") != fmt.Sprint(len(audio)) ||
		a.header.Get("Content-Disposition") != whole.header.Get("Content-Disposition") {
		t.Errorf("HEAD: %s", a)
	}

	// A label that needs the file-safe rule, and RFC 6266's filename*.
	e.mustAdmin(t, "PATCH", fmt.Sprintf("/systems/%d/talkgroups/1001", e.sys), map[string]any{"label": "../Nörd:N"}, 200)
	want := `attachment; filename="2026-03-04_050607_-N-rd-N_1001.opus"; filename*=UTF-8''2026-03-04_050607_-N%C3%B6rd-N_1001.opus`
	if got := e.pub(t, "GET", public.DownloadURL(id), ""); got.header.Get("Content-Disposition") != want || !bytes.Equal(got.body, audio) {
		t.Errorf("file-safe label: %q, want %q", got.header.Get("Content-Disposition"), want)
	}
}

// A call across a daylight-saving change is named in local time as TZ
// shows it, by when it started: in Sydney, 03:00 AEDT on 2026-04-05 is
// 02:00 AEST, so the call starting at 02:50 AEDT ends at 02:10 AEST.
func TestIntegrationDownloadDaylightSaving(t *testing.T) {
	e := newEnv(t)
	_, key := e.recorder(t)
	start := time.Date(2026, 4, 4, 15, 50, 0, 0, time.UTC) // 02:50 AEDT
	id, _ := e.storeStarted(t, key, 1, 1001, start, 20*time.Minute)
	if got := start.Add(20 * time.Minute).In(deploymentTZ(t)).Format("15:04 MST"); got != "02:10 AEST" {
		t.Fatalf("the call ends at %s, want 02:10 AEST: no daylight-saving change crossed", got)
	}
	want := `attachment; filename="2026-04-05_025000_DISP N_1001.opus"`
	if got := e.pub(t, "GET", public.DownloadURL(id), ""); got.status != 200 || got.header.Get("Content-Disposition") != want {
		t.Errorf("across the change: %s", got)
	}
	// An hour later by the clock, the same local time again.
	again, _ := e.storeStarted(t, key, 2, 1001, start.Add(time.Hour), time.Minute)
	want = `attachment; filename="2026-04-05_025000_DISP N_1001.opus"`
	if got := e.pub(t, "GET", public.DownloadURL(again), ""); got.header.Get("Content-Disposition") != want {
		t.Errorf("the second 02:50: %s", got)
	}
}

// An unavailable call's download answers exactly as its recording does,
// and as a call never stored (ADR 0004): here a call with no recording yet,
// and a delayed call until its delay has passed (R23). The A10 harness
// covers the rest.
func TestIntegrationDownloadUnavailable(t *testing.T) {
	e := newEnv(t)
	name, key := e.recorder(t)
	never := "6f1d2c3b-4a59-4e8f-9d7c-0b1a2c3d4e5f"
	want := e.pub(t, "GET", public.RecordingURL(never), "").String()
	if got := e.pub(t, "GET", public.DownloadURL(never), "").String(); got != want {
		t.Fatalf("never stored: the download answers\n%s\nbut the recording\n%s", got, want)
	}
	same := func(why, id string) {
		t.Helper()
		for _, h := range [][]string{nil, {"Range", "bytes=0-"}} {
			got := e.pub(t, "GET", public.DownloadURL(id), "", h...).String()
			if got != e.pub(t, "GET", public.DownloadURL(never), "", h...).String() {
				t.Errorf("%s (%q) answers\n%s", why, h, got)
			}
		}
	}

	// In progress: no recording yet.
	c := e.open(t, key)
	live := cp.CallID{CallNum: 9, StartTime: 1764000000}
	send(t, c, callStart(live))
	row := e.waitCall(t, name, live, func(r callRow) bool { return r.frames >= 0 })
	same("in progress", row.id)

	// Delayed 5 minutes: unavailable until 5 minutes after it ends.
	e.mustAdmin(t, "PATCH", fmt.Sprintf("/systems/%d/talkgroups/1002", e.sys), map[string]any{"delay_minutes": 5}, 200)
	ended := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	id, audio := e.storeStarted(t, key, 2, 1002, ended.Add(-10*time.Second), 10*time.Second)
	e.clock.set(ended.Add(4*time.Minute + 59*time.Second))
	same("within its delay", id)
	e.clock.set(ended.Add(5 * time.Minute))
	if got := e.pub(t, "GET", public.DownloadURL(id), ""); got.status != 200 || !bytes.Equal(got.body, audio) {
		t.Errorf("past its delay: %s", got)
	}
}
