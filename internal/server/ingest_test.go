package server_test

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
	"github.com/RIKIKU/RadioArchive/internal/server"
	"github.com/RIKIKU/RadioArchive/internal/store"
)

// rejections returns the admin API's rejected-call counts for this test's
// recorder and system, as message strings.
func (e *env) rejections(t *testing.T, recorder string) []string {
	t.Helper()
	all := decodeJSON[server.Rejections](t, e.mustAdmin(t, "GET", "/rejections", nil, 200))
	var out []string
	for _, u := range all.Unattached {
		if u.Recorder == recorder {
			out = append(out, u.Message)
		}
	}
	mine := decodeJSON[server.Rejections](t, e.mustAdmin(t, "GET", fmt.Sprintf("/rejections?system=%d", e.sys), nil, 200))
	if len(mine.Unattached) != 0 {
		t.Errorf("a system's rejections include unattached pairs: %+v", mine.Unattached)
	}
	for _, r := range mine.Talkgroups {
		out = append(out, r.Message)
	}
	return out
}

// state is a stored call's effective state now (R16, R30).
func (e *env) state(t *testing.T, callID string) store.TalkgroupState {
	t.Helper()
	var u pgtype.UUID
	if err := u.Scan(callID); err != nil {
		t.Fatal(err)
	}
	s, err := e.st.CallEffectiveState(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (e *env) systemOf(t *testing.T, callID string) int64 {
	t.Helper()
	var sys int64
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT system_id FROM calls WHERE id = $1::uuid`, callID).Scan(&sys); err != nil {
		t.Fatal(err)
	}
	return sys
}

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestIntegrationUnattachedAndUnknown is SPEC.md A2 at the server: calls from
// an unattached pair and on an unknown talkgroup are refused live and as
// recordings, stored nowhere, and counted once each with R12's and R17's
// wording. With auto-create on, the talkgroup is created public and
// unreviewed and its call is stored.
func TestIntegrationUnattachedAndUnknown(t *testing.T) {
	e := newEnv(t)
	name, key := e.recorder(t)
	c := e.open(t, key)

	unattached := cp.CallID{SysNum: 1, CallNum: 6, StartTime: 1764000004}
	unknown := cp.CallID{SysNum: 0, CallNum: 5, StartTime: 1764000004}
	for _, x := range []struct {
		id     cp.CallID
		short  string
		tg     int32
		reason string
	}{
		{unattached, "other", 1001, "not_attached"},
		{unknown, "ex", 4711, "unknown_talkgroup"},
	} {
		cs := callStart(x.id)
		cs.ShortName, cs.Talkgroup = x.short, x.tg
		send(t, c, cs)
		send(t, c, frame(x.id, 0))
		send(t, c, cp.CallEnd{Type: "call_end", CallID: x.id, StopTimeMS: 1764000005000, PatchedTalkgroups: []int32{}})
		audio := oggBytes(byte(x.id.CallNum))
		m := recording(x.id, audio)
		m.ShortName, m.Talkgroup, m.PatchedTalkgroups = x.short, x.tg, nil
		// The answer is the same however often the client asks.
		for range 2 {
			code, resp := e.upload(t, key, m, audio)
			if code != 200 || resp.Status != "rejected" || resp.Reason != x.reason {
				t.Errorf("%s upload: %d %+v, want 200 rejected %s", x.short, code, resp, x.reason)
			}
		}
	}
	waitUntil(t, "both calls' live frames refused", func() bool { return e.rs.Stat.FramesRefused.Load() >= 2 })
	for _, id := range []cp.CallID{unattached, unknown} {
		if r, ok := e.call(t, name, id); ok {
			t.Errorf("refused call %d stored: %+v", id.CallNum, r)
		}
	}
	if n := e.sink.total(); n != 0 {
		t.Errorf("live sink got %d frames", n)
	}

	want := []string{
		name + " / other: 1 call rejected, not attached to a system",
		"TG 4711: 1 call rejected, not in the list",
	}
	if got := e.rejections(t, name); !slices.Equal(got, want) {
		t.Errorf("rejections:\n got %q\nwant %q", got, want)
	}
	if e.rs.Stat.RejectedCount("not_attached") != 1 || e.rs.Stat.RejectedCount("unknown_talkgroup") != 1 {
		t.Errorf("rejected counters: %d not attached, %d unknown", e.rs.Stat.RejectedCount("not_attached"),
			e.rs.Stat.RejectedCount("unknown_talkgroup"))
	}

	// Auto-create on: 4711 heard again is created public and unreviewed.
	e.mustAdmin(t, "PATCH", fmt.Sprintf("/systems/%d", e.sys), map[string]any{"auto_create": true}, 200)
	again := cp.CallID{CallNum: 7, StartTime: 1764000010}
	audio := oggBytes(7)
	m := recording(again, audio)
	m.Talkgroup, m.PatchedTalkgroups = 4711, nil
	if code, resp := e.upload(t, key, m, audio); code != 201 || resp.Status != "stored" {
		t.Fatalf("upload with auto-create on: %d %+v", code, resp)
	}
	tg := decodeJSON[server.Talkgroup](t, e.mustAdmin(t, "GET", fmt.Sprintf("/systems/%d/talkgroups/4711", e.sys), nil, 200))
	if tg.State != "public" || !tg.Unreviewed || tg.Tag != nil || tg.Label != "" || tg.Name != "" {
		t.Errorf("auto-created talkgroup = %+v, want public and unreviewed with nothing else", tg)
	}
	if r, ok := e.call(t, name, again); !ok || r.talkgroup != 4711 || r.path == nil {
		t.Errorf("auto-created call = %+v (found %v)", r, ok)
	}
	// The earlier refusal stays counted, and stays refused.
	if code, resp := e.upload(t, key, recording(unknown, oggBytes(5)), oggBytes(5)); code != 200 || resp.Reason != "unknown_talkgroup" {
		t.Errorf("earlier unknown call re-uploaded: %d %+v", code, resp)
	}

	// An admin's edit marks it reviewed, and needs a tag.
	path := fmt.Sprintf("/systems/%d/talkgroups/4711", e.sys)
	e.wantRefusal(t, "PATCH", path, map[string]any{"name": "Talkgroup 4711"}, 400, "tag_required")
	tg = decodeJSON[server.Talkgroup](t, e.mustAdmin(t, "PATCH", path, map[string]any{"name": "Talkgroup 4711", "tag": "other"}, 200))
	if tg.Unreviewed || tg.Name != "Talkgroup 4711" {
		t.Errorf("after review: %+v", tg)
	}
}

// TestIntegrationIgnored is SPEC.md A3 at the server: a call on 1004 has its
// live frames and its recording refused, nothing is stored, and the
// rejection is counted.
func TestIntegrationIgnored(t *testing.T) {
	e := newEnv(t)
	name, key := e.recorder(t)
	c := e.open(t, key)
	id := cp.CallID{CallNum: 8, StartTime: 1764000020}
	cs := callStart(id)
	cs.Talkgroup = 1004
	send(t, c, cs)
	for i := range 3 {
		send(t, c, frame(id, uint32(i)))
	}
	send(t, c, cp.Transmission{Type: "transmission", CallID: id, Unit: 5123, OffsetMS: 100})
	send(t, c, cp.CallEnd{Type: "call_end", CallID: id, StopTimeMS: 1764000021000, PatchedTalkgroups: []int32{}})
	waitUntil(t, "the frames refused", func() bool { return e.rs.Stat.FramesRefused.Load() >= 4 })

	audio := oggBytes(8)
	m := recording(id, audio)
	m.Talkgroup, m.PatchedTalkgroups = 1004, nil
	if code, resp := e.upload(t, key, m, audio); code != 200 || resp.Status != "rejected" || resp.Reason != "ignored_talkgroup" {
		t.Errorf("upload: %d %+v", code, resp)
	}
	if r, ok := e.call(t, name, id); ok {
		t.Errorf("ignored call stored: %+v", r)
	}
	if got, want := e.rejections(t, name), []string{"TG 1004: 1 call rejected, ignored"}; !slices.Equal(got, want) {
		t.Errorf("rejections %q, want %q", got, want)
	}
}

// TestIntegrationPatches is the storage half of SPEC.md A4, and R16's rule:
// a patched call is stored once, under its own talkgroup with the patched
// IDs, and is treated as the strictest state in its patch, read from the
// talkgroups now.
func TestIntegrationPatches(t *testing.T) {
	e := newEnv(t)
	name, key := e.recorder(t)
	ctx := context.Background()
	store1 := func(id cp.CallID, patched ...int32) (int, cp.UploadResponse) {
		audio := oggBytes(byte(id.CallNum))
		m := recording(id, audio)
		m.PatchedTalkgroups = patched
		return e.upload(t, key, m, audio)
	}

	public := cp.CallID{CallNum: 11, StartTime: 1764000030}
	if code, resp := store1(public, 1002); code != 201 {
		t.Fatalf("1001+1002: %d %+v", code, resp)
	}
	pub, _ := e.call(t, name, public)
	if pub.talkgroup != 1001 || !slices.Equal(pub.patched, []int32{1002}) || e.state(t, pub.id) != store.Public {
		t.Errorf("1001+1002 stored as %+v, state %s", pub, e.state(t, pub.id))
	}

	// Patched with the hidden 1003, learnt live from a transmission: the
	// live call is hidden at once, and its recording stays the one call.
	hidden := cp.CallID{CallNum: 12, StartTime: 1764000031}
	c := e.open(t, key)
	send(t, c, callStart(hidden))
	send(t, c, cp.Transmission{Type: "transmission", CallID: hidden, Unit: 5001, OffsetMS: 0, PatchedTalkgroups: []int32{1003}})
	live := e.waitCall(t, name, hidden, func(r callRow) bool { return slices.Equal(r.patched, []int32{1003}) })
	if s := e.state(t, live.id); s != store.Hidden {
		t.Errorf("live call patched with 1003: state %s, want hidden", s)
	}
	send(t, c, cp.CallEnd{Type: "call_end", CallID: hidden, StopTimeMS: 1764000032000, PatchedTalkgroups: []int32{}})
	if code, resp := store1(hidden, 1003); code != 201 || resp.CallID != live.id {
		t.Fatalf("1001+1003: %d %+v (live call %s)", code, resp, live.id)
	}
	if s := e.state(t, live.id); s != store.Hidden {
		t.Errorf("1001+1003: state %s, want hidden", s)
	}

	// Patched with the ignored 1004: not stored, counted against 1004.
	ignored := cp.CallID{CallNum: 13, StartTime: 1764000032}
	if code, resp := store1(ignored, 1002, 1004); code != 200 || resp.Reason != "ignored_talkgroup" {
		t.Errorf("1001+1004: %d %+v", code, resp)
	}
	// A patched ID the system doesn't list has no state of its own.
	unlisted := cp.CallID{CallNum: 14, StartTime: 1764000033}
	if code, _ := store1(unlisted, 9999); code != 201 {
		t.Errorf("1001+9999: %d", code)
	}
	unl, _ := e.call(t, name, unlisted)
	if s := e.state(t, unl.id); s != store.Public {
		t.Errorf("1001+9999: state %s", s)
	}

	var n int
	_ = e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM calls c JOIN recorders r ON r.id = c.recorder_id WHERE r.name = $1`, name).Scan(&n)
	if n != 3 {
		t.Errorf("%d calls stored, want 3", n)
	}
	if got, want := e.rejections(t, name), []string{"TG 1004: 1 call rejected, ignored"}; !slices.Equal(got, want) {
		t.Errorf("rejections %q, want %q", got, want)
	}

	// R30: the state is the talkgroups' now. Hiding 1002 hides the call
	// patched with it; making it public again shows it.
	tg1002 := fmt.Sprintf("/systems/%d/talkgroups/1002", e.sys)
	e.mustAdmin(t, "PATCH", tg1002, map[string]any{"state": "hidden"}, 200)
	if s := e.state(t, pub.id); s != store.Hidden {
		t.Errorf("after hiding 1002: %s", s)
	}
	e.mustAdmin(t, "PATCH", tg1002, map[string]any{"state": "ignored"}, 200)
	if s := e.state(t, pub.id); s != store.Ignored {
		t.Errorf("after ignoring 1002: %s", s)
	}
	e.mustAdmin(t, "PATCH", tg1002, map[string]any{"state": "public"}, 200)
	if s := e.state(t, pub.id); s != store.Public {
		t.Errorf("after making 1002 public: %s", s)
	}
}

// TestIntegrationPatchBecomesIgnored: a live call whose patch takes in an
// ignored talkgroup has the rest of its live audio refused, and its
// recording too, leaving nothing stored.
func TestIntegrationPatchBecomesIgnored(t *testing.T) {
	e := newEnv(t)
	name, key := e.recorder(t)
	c := e.open(t, key)
	id := cp.CallID{CallNum: 21, StartTime: 1764000040}
	send(t, c, callStart(id))
	send(t, c, frame(id, 0))
	send(t, c, frame(id, 1))
	live := e.waitCall(t, name, id, func(callRow) bool { return true })
	waitUntil(t, "2 live frames", func() bool { return e.sink.count(live.id) == 2 })
	send(t, c, cp.Transmission{Type: "transmission", CallID: id, Unit: 5123, OffsetMS: 500, PatchedTalkgroups: []int32{1004}})
	send(t, c, frame(id, 2))
	send(t, c, frame(id, 3))
	send(t, c, cp.CallEnd{Type: "call_end", CallID: id, StopTimeMS: 1764000041000, PatchedTalkgroups: []int32{1004}})
	waitUntil(t, "the later frames refused", func() bool { return e.rs.Stat.FramesRefused.Load() >= 2 })
	if n := e.sink.count(live.id); n != 2 {
		t.Errorf("live sink got %d frames, want the 2 before the patch", n)
	}

	audio := oggBytes(21)
	m := recording(id, audio)
	m.PatchedTalkgroups = []int32{1004}
	if code, resp := e.upload(t, key, m, audio); code != 200 || resp.Reason != "ignored_talkgroup" {
		t.Errorf("upload: %d %+v", code, resp)
	}
	if r, ok := e.call(t, name, id); ok {
		t.Errorf("call still stored: %+v", r)
	}
}

// TestIntegrationAttachmentAffectsFutureCalls is R12: moving a pair to
// another system changes only calls heard after the move.
func TestIntegrationAttachmentAffectsFutureCalls(t *testing.T) {
	e := newEnv(t)
	name, key := e.recorder(t)
	first := e.sys
	second := e.fixture(t)

	c := e.open(t, key)
	before := cp.CallID{CallNum: 31, StartTime: 1764000050}
	send(t, c, callStart(before))
	live := e.waitCall(t, name, before, func(callRow) bool { return true })

	e.mustAdmin(t, "PUT", "/recorders/"+url.PathEscape(name)+"/attachments/ex", map[string]any{"system_id": second}, 200)
	audio := oggBytes(31)
	if code, resp := e.upload(t, key, recording(before, audio), audio); code != 201 || resp.CallID != live.id {
		t.Fatalf("upload of the call heard before: %d %+v", code, resp)
	}
	if s := e.systemOf(t, live.id); s != first {
		t.Errorf("call heard before the move filed under %d, want %d", s, first)
	}
	after := cp.CallID{CallNum: 32, StartTime: 1764000051}
	audio = oggBytes(32)
	code, resp := e.upload(t, key, recording(after, audio), audio)
	if code != 201 {
		t.Fatalf("upload after: %d %+v", code, resp)
	}
	if s := e.systemOf(t, resp.CallID); s != second {
		t.Errorf("call heard after the move filed under %d, want %d", s, second)
	}
}
