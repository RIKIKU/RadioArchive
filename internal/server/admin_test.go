package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/RIKIKU/RadioArchive/internal/server"
)

// testAdmin stands in for admin sign-in, which is Authentik's and doesn't
// exist yet: a request with an X-Test-Admin header is from that admin. It's
// in a _test.go file, so no binary contains it.
type testAdmin struct{}

func (testAdmin) Admin(r *http.Request) (string, bool) {
	v := r.Header.Get("X-Test-Admin")
	return v, v != ""
}

// adminReq calls the admin API as an admin (or as nobody, with as "").
func (e *env) adminReq(t *testing.T, as, method, path string, body any) (int, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+server.AdminPath+path, rd)
	if as != "" {
		req.Header.Set("X-Test-Admin", as)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// mustAdmin calls the admin API as an admin and wants code.
func (e *env) mustAdmin(t *testing.T, method, path string, body any, code int) []byte {
	t.Helper()
	got, b := e.adminReq(t, "test admin", method, path, body)
	if got != code {
		t.Fatalf("%s %s = %d %s, want %d", method, path, got, b, code)
	}
	return b
}

// wantRefusal calls the admin API and wants a refusal with this code.
func (e *env) wantRefusal(t *testing.T, method, path string, body any, status int, code string) {
	t.Helper()
	got, b := e.adminReq(t, "test admin", method, path, body)
	var r struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(b, &r) != nil || got != status || r.Error != code || r.Message == "" {
		t.Errorf("%s %s = %d %s, want %d %q with a message", method, path, got, b, status, code)
	}
}

func newSystemID() int64 { return rand.Int64N(1<<52) + 1 }

// fixture creates the test fixture's system and talkgroups under a new ID
// (the tests share one database) and returns the ID.
func (e *env) fixture(t *testing.T) int64 {
	t.Helper()
	id := newSystemID()
	e.mustAdmin(t, "POST", "/systems", map[string]any{"id": id, "label": fmt.Sprintf("EX-%d", id), "name": "Example network"}, 201)
	for _, tg := range []struct {
		id                     int
		label, name, tag, stat string
	}{
		{1001, "DISP N", "Dispatch North", "dispatch", "public"},
		{1002, "FG 41", "Fireground 41", "fireground", "public"},
		{1003, "ADMIN", "Admin", "admin", "hidden"},
		{1004, "", "", "other", "ignored"},
	} {
		e.mustAdmin(t, "PUT", fmt.Sprintf("/systems/%d/talkgroups/%d", id, tg.id),
			map[string]any{"label": tg.label, "name": tg.name, "tag": tg.tag, "state": tg.stat}, 201)
	}
	return id
}

func decodeJSON[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	return v
}

func TestAdminNeedsAuthenticator(t *testing.T) {
	if _, err := server.NewAdmin(nil, slog.New(slog.DiscardHandler), nil); err == nil {
		t.Error("NewAdmin without an authenticator succeeded")
	}
}

func TestIntegrationAdminSignIn(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"GET /systems", "POST /systems", "GET /attachments", "GET /rejections",
		"PUT /systems/1/talkgroups/1001", "PUT /recorders/x/attachments/ex", "DELETE /systems/1"} {
		method, path, _ := strings.Cut(p, " ")
		code, b := e.adminReq(t, "", method, path, map[string]any{})
		if code != 401 || !strings.Contains(string(b), `"admin_sign_in_required"`) {
			t.Errorf("%s without sign-in = %d %s, want 401", p, code, b)
		}
	}
}

func TestIntegrationSystems(t *testing.T) {
	e := newEnv(t)
	id := newSystemID()
	label := fmt.Sprintf("EX-%d", id)
	b := e.mustAdmin(t, "POST", "/systems", map[string]any{"id": id, "label": label, "name": "Example network"}, 201)
	s := decodeJSON[server.System](t, b)
	if s.ID != id || s.Label != label || s.Name != "Example network" || s.AutoCreate {
		t.Errorf("created %+v; auto-create must be off by default", s)
	}
	e.wantRefusal(t, "POST", "/systems", map[string]any{"id": id, "label": label + "x"}, 409, "system_exists")
	e.wantRefusal(t, "POST", "/systems", map[string]any{"id": newSystemID(), "label": label}, 409, "label_taken")
	e.wantRefusal(t, "POST", "/systems", map[string]any{"id": newSystemID(), "label": " EX"}, 400, "invalid_label")
	e.wantRefusal(t, "POST", "/systems", map[string]any{"id": 0, "label": "x"}, 400, "invalid_id")
	e.wantRefusal(t, "POST", "/systems", map[string]any{"id": 1, "label": "x", "colour": "red"}, 400, "invalid_body")

	b = e.mustAdmin(t, "PATCH", fmt.Sprintf("/systems/%d", id), map[string]any{"auto_create": true, "name": "Renamed"}, 200)
	if s := decodeJSON[server.System](t, b); !s.AutoCreate || s.Name != "Renamed" || s.Label != label {
		t.Errorf("patched %+v", s)
	}
	if s := decodeJSON[server.System](t, e.mustAdmin(t, "GET", fmt.Sprintf("/systems/%d", id), nil, 200)); !s.AutoCreate {
		t.Errorf("got %+v", s)
	}
	all := decodeJSON[[]server.System](t, e.mustAdmin(t, "GET", "/systems", nil, 200))
	if !slices.ContainsFunc(all, func(x server.System) bool { return x.ID == id }) {
		t.Error("the new system isn't listed")
	}
	e.wantRefusal(t, "GET", "/systems/999999999999999999", nil, 404, "system_not_found")
	e.wantRefusal(t, "PATCH", "/systems/999999999999999999", map[string]any{"name": "x"}, 404, "system_not_found")

	// A system with talkgroups can't be deleted; an empty one can.
	full := e.fixture(t)
	e.wantRefusal(t, "DELETE", fmt.Sprintf("/systems/%d", full), nil, 409, "system_in_use")
	e.mustAdmin(t, "DELETE", fmt.Sprintf("/systems/%d", id), nil, 204)
	e.wantRefusal(t, "DELETE", fmt.Sprintf("/systems/%d", id), nil, 404, "system_not_found")
}

func TestIntegrationTalkgroups(t *testing.T) {
	e := newEnv(t)
	sys := e.fixture(t)
	tg := func(id int) string { return fmt.Sprintf("/systems/%d/talkgroups/%d", sys, id) }

	got := decodeJSON[server.Talkgroup](t, e.mustAdmin(t, "GET", tg(1001), nil, 200))
	if got.Label != "DISP N" || got.Name != "Dispatch North" || got.Tag == nil || *got.Tag != "dispatch" ||
		got.State != "public" || got.Unreviewed || got.SystemID != sys {
		t.Errorf("1001 = %+v", got)
	}
	list := decodeJSON[[]server.Talkgroup](t, e.mustAdmin(t, "GET", fmt.Sprintf("/systems/%d/talkgroups", sys), nil, 200))
	var ids []int32
	for _, x := range list {
		ids = append(ids, x.ID)
	}
	if !slices.Equal(ids, []int32{1001, 1002, 1003, 1004}) {
		t.Errorf("listed %v", ids)
	}

	// A full edit replaces; a partial one changes only what's given.
	e.mustAdmin(t, "PUT", tg(1001), map[string]any{"label": "DISP N", "name": "Dispatch North", "tag": "dispatch"}, 200)
	got = decodeJSON[server.Talkgroup](t, e.mustAdmin(t, "PATCH", tg(1001), map[string]any{"state": "hidden"}, 200))
	if got.State != "hidden" || got.Name != "Dispatch North" {
		t.Errorf("after hiding: %+v", got)
	}
	e.wantRefusal(t, "PUT", tg(1005), map[string]any{"label": "X"}, 400, "tag_required")
	e.wantRefusal(t, "PATCH", tg(1001), map[string]any{"state": "secret"}, 400, "invalid_state")
	e.wantRefusal(t, "PATCH", tg(1999), map[string]any{"state": "public"}, 404, "talkgroup_not_found")
	e.wantRefusal(t, "GET", tg(0), nil, 404, "talkgroup_not_found")
	e.wantRefusal(t, "PUT", "/systems/999999999999999999/talkgroups/1", map[string]any{"tag": "x"}, 404, "system_not_found")
}

func TestIntegrationAttachments(t *testing.T) {
	e := newEnv(t)
	name, _ := e.recorder(t) // attaches `ex` to e.sys
	other := e.fixture(t)
	path := "/recorders/" + url.PathEscape(name) + "/attachments/"

	e.wantRefusal(t, "PUT", "/recorders/nobody-"+url.PathEscape(name)+"/attachments/ex", map[string]any{"system_id": other}, 404, "recorder_not_found")
	e.wantRefusal(t, "PUT", path+"ex", map[string]any{"system_id": 999999999999999999}, 404, "system_not_found")

	// Re-attaching moves the pair; each pair is attached to exactly one system.
	b := e.mustAdmin(t, "PUT", path+"ex", map[string]any{"system_id": other}, 200)
	if a := decodeJSON[server.Attachment](t, b); a.Recorder != name || a.ShortName != "ex" || a.SystemID != other {
		t.Errorf("attached %+v", a)
	}
	e.mustAdmin(t, "PUT", path+"metro", map[string]any{"system_id": e.sys}, 200)
	var mine []server.Attachment
	for _, a := range decodeJSON[[]server.Attachment](t, e.mustAdmin(t, "GET", "/attachments", nil, 200)) {
		if a.Recorder == name {
			mine = append(mine, a)
		}
	}
	if len(mine) != 2 || mine[0].ShortName != "ex" || mine[0].SystemID != other || mine[1].ShortName != "metro" || mine[1].SystemID != e.sys {
		t.Errorf("attachments %+v", mine)
	}
	e.mustAdmin(t, "DELETE", path+"metro", nil, 204)
	e.wantRefusal(t, "DELETE", path+"metro", nil, 404, "attachment_not_found")

	// A retired recorder can't be attached.
	if err := e.st.RetireRecorderByName(context.Background(), name); err != nil {
		t.Fatal(err)
	}
	e.wantRefusal(t, "PUT", path+"ex", map[string]any{"system_id": other}, 404, "recorder_not_found")
}
