package server_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/public"
)

// previewOf is what a call link's page tells link previews: its title and
// description, from the page's meta tags.
func previewOf(page string) (title, description string) {
	attr := func(key string) string {
		_, rest, ok := strings.Cut(page, key+` content="`)
		if !ok {
			return ""
		}
		v, _, _ := strings.Cut(rest, `"`)
		return v
	}
	return attr(`property="og:title"`), attr(`property="og:description"`)
}

// SPEC.md A10, call link and preview (R61, R74; ADR 0017): a public call's
// link names its talkgroup and its start on the deployment's clock, and
// holds no audio or recording URL; every unavailable call's link is the
// app's own page, 404, byte for byte the same whatever the reason. (The
// harness, a10Probes, checks every unavailable call against the deleted
// one, in every look, signed in or not.)
func TestIntegrationCallLinkPreview(t *testing.T) {
	f := newA10World(t)
	e := f.e
	id := f.public[0]
	pub := e.pub(t, "GET", public.CallLinkURL(id), "")
	title, desc := previewOf(string(pub.body))
	wantDesc := f.at[id].In(deploymentTZ(t)).Format(public.PreviewTime)
	if pub.status != 200 || title != "Dispatch North" || desc != wantDesc {
		t.Errorf("a public call's preview: %d %q %q, want 200 %q %q\n%s", pub.status, title, desc, "Dispatch North", wantDesc, pub)
	}
	if !strings.HasSuffix(wantDesc, "AEDT") {
		t.Errorf("the preview's time %q isn't on the deployment's clock", wantDesc)
	}
	for _, s := range []string{"/api/", "recording", "download", ".opus", "og:audio", "og:video", id} {
		if strings.Contains(string(pub.body), s) {
			t.Errorf("a public call's preview holds %q:\n%s", s, pub.body)
		}
	}
	if pub.header.Get("Cache-Control") != "no-store" || pub.header.Get("X-Robots-Tag") != "noindex" ||
		!strings.HasPrefix(pub.header.Get("Content-Type"), "text/html") {
		t.Errorf("a public call's link's headers: %v", pub.header)
	}

	// Unavailable: the app's own page, as "/" sends it, and 404.
	shell := e.pub(t, "GET", "/", "")
	gone := e.pub(t, "GET", public.CallLinkURL(f.deleted), "")
	if gone.status != 404 || string(gone.body) != string(shell.body) {
		t.Errorf("a deleted call's link: %s\nwant the app's page, 404:\n%s", gone, shell)
	}
	if title, d := previewOf(string(gone.body)); title != "" || d != "" {
		t.Errorf("a deleted call's link has a preview: %q %q", title, d)
	}
	for why, id := range f.unavailable {
		if got := e.pub(t, "GET", public.CallLinkURL(id), ""); got.String() != gone.String() {
			t.Errorf("%s's link answers\n%s\nbut a deleted call's\n%s", why, got, gone)
		}
	}
}

// SPEC.md A16, the server's half: a call on 1001 in progress has a link
// with its preview, and its details say it's in progress; once it ends and
// its recording arrives, the same link's details give the recording; once
// retention deletes it, its link answers exactly as a hidden call's.
func TestIntegrationA16CallLink(t *testing.T) {
	e := newEnv(t)
	rec := e.liveRecorder(t)
	start := time.Now().Truncate(time.Second).Add(-10 * time.Second)
	ident, id := rec.start(1001, nil, 5001, start.UnixMilli())
	hidden, _ := e.storeCall(t, rec.key, 1, 1003, nil)
	hiddenLink := e.pub(t, "GET", public.CallLinkURL(hidden), "")
	if hiddenLink.status != 404 {
		t.Fatalf("a hidden call's link: %s", hiddenLink)
	}

	link := e.pub(t, "GET", public.CallLinkURL(id), "")
	if title, _ := previewOf(string(link.body)); link.status != 200 || title != "Dispatch North" {
		t.Errorf("the link of a call in progress: %s", link)
	}
	if cs := e.publicCalls(t, id); len(cs) != 1 || !cs[0].InProgress || cs[0].Recording != nil {
		t.Fatalf("a call in progress: %+v", cs)
	}

	rec.end(ident, start.Add(4*time.Second).UnixMilli())
	audio := oggBytes(7)
	m := recording(ident, audio)
	m.PatchedTalkgroups = []int32{}
	m.StartTimeMS, m.StopTimeMS = start.UnixMilli(), start.Add(4*time.Second).UnixMilli()
	if code, resp := e.upload(t, rec.key, m, audio); code != 201 || resp.CallID != id {
		t.Fatalf("the recording: %d %+v", code, resp)
	}
	again := e.pub(t, "GET", public.CallLinkURL(id), "")
	if again.String() != link.String() {
		t.Errorf("the same link once the call ended:\n%s\nwas\n%s", again, link)
	}
	cs := e.publicCalls(t, id)
	if len(cs) != 1 || cs[0].InProgress || cs[0].Recording == nil || cs[0].Recording.URL != public.RecordingURL(id) {
		t.Fatalf("the ended call: %+v", cs)
	}

	// Retention deletes it (R88): the link is a hidden call's.
	e.clock.set(start.Add(40 * 24 * time.Hour))
	if res, err := e.job.RunNightly(context.Background()); err != nil || res.Calls == 0 {
		t.Fatalf("retention: %+v %v", res, err)
	}
	if cs := e.publicCalls(t, id); len(cs) != 0 {
		t.Fatalf("retention kept the call: %+v", cs)
	}
	if got := e.pub(t, "GET", public.CallLinkURL(id), ""); got.String() != hiddenLink.String() {
		t.Errorf("the deleted call's link answers\n%s\nbut a hidden call's\n%s", got, hiddenLink)
	}
}
