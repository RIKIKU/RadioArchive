package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
	"github.com/RIKIKU/RadioArchive/internal/limits"
	"github.com/RIKIKU/RadioArchive/internal/public"
)

// bigCall stores a call whose recording is size bytes.
func (e *env) bigCall(t *testing.T, key string, n int64, size int) string {
	t.Helper()
	audio := append([]byte("OggS"), bytes.Repeat([]byte{byte(n)}, size-4)...)
	m := recording(cp.CallID{CallNum: n, StartTime: 1764000200 + n}, audio)
	code, resp := e.upload(t, key, m, audio)
	if code != 201 {
		t.Fatalf("upload: %d %+v", code, resp)
	}
	return resp.CallID
}

// zipRefused checks a is a zip refused (R72, R73): status, code, a
// Retry-After the body repeats, and a message naming the limit.
func zipRefused(t *testing.T, what string, a answer, status int, code, msg string) {
	t.Helper()
	var body struct {
		Error      string `json:"error"`
		Message    string `json:"message"`
		RetryAfter int    `json:"retry_after"`
	}
	ra, err := strconv.Atoi(a.header.Get("Retry-After"))
	if a.status != status || err != nil || ra < 1 || json.Unmarshal(a.body, &body) != nil || body.Error != code ||
		body.RetryAfter != ra || !bytes.HasPrefix([]byte(body.Message), []byte(msg)) {
		t.Errorf("%s: want %d %s %q…:\n%s", what, status, code, msg, a)
	}
}

// TestIntegrationA20Zips is A20's zip half on the server (R72), at the
// default limits: a zip of two 3 MB recordings takes about 3 s at 2 MB/s;
// while one client has 2 zips being sent, its 3rd is refused with 429 and
// Retry-After, and the page's check (its first byte) says the same, while the 2
// carry on and are whole; with 6 being sent from 3 clients, a 7th from a
// 4th client is refused with 503; a signed-in admin's 3rd is let in; once
// they're done, the next is let in. A18's sameness holds through the
// pacing: two downloads are byte-identical, and a resumed one too.
func TestIntegrationA20Zips(t *testing.T) {
	if testing.Short() {
		t.Skip("timed")
	}
	e := newLimitedEnv(t, limits.Defaults())
	_, key := e.recorder(t)
	req := public.ZipRequest{Calls: []string{e.bigCall(t, key, 1, 3_000_000), e.bigCall(t, key, 2, 3_000_000)}}
	from := func(addr string, more ...string) []string { return append([]string{"X-Test-Remote", addr}, more...) }

	// One zip alone: ~6 MB at 2 MB/s.
	began := time.Now()
	whole := e.zip(t, "GET", req, from("192.0.2.40:1")...)
	took := time.Since(began)
	if whole.status != 200 || len(whole.body) < 6_000_000 {
		t.Fatalf("a zip: %d, %d bytes", whole.status, len(whole.body))
	}
	if took < 2500*time.Millisecond || took > 6*time.Second {
		t.Errorf("a %d-byte zip took %v, want about 3 s at 2 MB/s", len(whole.body), took)
	}

	// Zips being sent, each read to its end in the background.
	var wg sync.WaitGroup
	got := make(chan answer, 8)
	start := func(addr string, more ...string) {
		wg.Go(func() { got <- e.zip(t, "GET", req, from(addr, more...)...) })
	}
	// The first zip's place is given back as its handler returns, a
	// moment after its last byte has been read.
	time.Sleep(200 * time.Millisecond)
	start("192.0.2.40:2")
	start("192.0.2.40:3")
	// Both start sending before anything else from that client is asked:
	// a check sent first would take one's place.
	time.Sleep(500 * time.Millisecond)
	tooMany := "Too many downloads at once: at most 2 from here. Try again in "
	zipRefused(t, "the 3rd zip", e.zip(t, "GET", req, from("192.0.2.40:5")...), 429, "too_many_downloads", tooMany)
	zipRefused(t, "the 3rd by POST", e.zip(t, "POST", req, from("192.0.2.40:6")...), 429, "too_many_downloads", tooMany)
	zipRefused(t, "the page's check, its first byte", e.zip(t, "GET", req, from("192.0.2.40:7", "Range", "bytes=0-0")...), 429, "too_many_downloads", tooMany)
	zipRefused(t, "a signed-in listener's 3rd", e.zip(t, "GET", req, from("192.0.2.40:8", "Cookie", "radioarchive_session=made-up")...),
		429, "too_many_downloads", tooMany)
	// An admin's isn't refused.
	if a := e.zip(t, "GET", req, from("192.0.2.40:9", "Cookie", "radioarchive_session=made-up", "X-Test-Admin", "admin-1", "Range", "bytes=0-0")...); a.status != 206 || len(a.body) != 1 {
		t.Errorf("an admin's check: %s", a)
	}
	start("192.0.2.40:10", "Authorization", "Bearer admin-2")

	// 4 more from 2 other clients: 6 counted in all.
	for _, addr := range []string{"198.51.100.41:1", "198.51.100.41:2", "198.51.100.42:1", "198.51.100.42:2"} {
		start(addr)
	}
	time.Sleep(500 * time.Millisecond)
	zipRefused(t, "the 7th, from a 4th client", e.zip(t, "GET", req, from("203.0.113.40:1")...), 503, "downloads_busy",
		"The radio is busy with other downloads. Try again in ")

	wg.Wait()
	close(got)
	for a := range got {
		if a.status != 200 || !bytes.Equal(a.body, whole.body) {
			t.Errorf("a zip that was let in: %d, %d bytes, the same: %v", a.status, len(a.body), bytes.Equal(a.body, whole.body))
		}
	}
	// Done: the next is let in, and resumes byte for byte (A18).
	from40 := from("192.0.2.40:11", "Range", fmt.Sprintf("bytes=%d-", len(whole.body)-1_000_000))
	part := e.zip(t, "GET", req, from40...)
	if part.status != http.StatusPartialContent || !bytes.Equal(part.body, whole.body[len(whole.body)-1_000_000:]) {
		t.Errorf("resumed: %d, %d bytes", part.status, len(part.body))
	}
}
