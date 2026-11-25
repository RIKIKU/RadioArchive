package server_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
	"github.com/RIKIKU/RadioArchive/internal/telemetry"
)

// scrape writes what the recorder endpoints' collector exports.
func (e *env) scrape(t *testing.T) string {
	t.Helper()
	reg := telemetry.NewRegistry(telemetry.All)
	reg.Add(e.rs)
	out := string(reg.Write(context.Background()))
	if reg.Dropped() != 0 {
		t.Errorf("the allowlist dropped %d samples:\n%s", reg.Dropped(), out)
	}
	return out
}

func wantMetrics(t *testing.T, out string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(out, w+"\n") {
			t.Errorf("no %q in the metrics:\n%s", w, out)
		}
	}
}

// TestIntegrationA29Metrics is A29's metric (R6, R90): a 1.4 server counts
// the 1.2 client it refuses, and shows the 1.3 one as out of date.
func TestIntegrationA29Metrics(t *testing.T) {
	e := newEnvAt(t, "1.4.0")
	name, key := e.recorder(t)
	withVersion := func(v string) http.Header { h := hdr(key); h.Set(cp.HeaderClientVersion, v); return h }
	if _, _, err := e.dial(t, withVersion("1.2.0")); err == nil {
		t.Fatal("a 1.2 client connected")
	}
	c, _, err := e.dial(t, withVersion("1.3.0"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := c.Read(ctx); err != nil {
		t.Fatal(err)
	}
	send(t, c, `{"type":"heartbeat","client_version":"1.3.0","systems":[{"sys_num":0,"short_name":"ex","system_type":"p25","decode_rate":38.5}],"queue":{"count":3,"oldest_age_s":4000}}`)
	var out string
	waitUntil(t, "the heartbeat in the metrics", func() bool {
		out = e.scrape(t)
		return strings.Contains(out, "radioarchive_recorder_queue_entries")
	})
	r := fmt.Sprintf("recorder=%q", name)
	wantMetrics(t, out,
		"radioarchive_recorder_clients_too_old_total 1",
		"radioarchive_recorder_clients_out_of_date_total 1",
		"radioarchive_recorder_client_out_of_date{"+r+"} 1",
		"radioarchive_recorder_client_info{"+r+`,version="1.3.0"} 1`,
		"radioarchive_recorder_connected{"+r+"} 1",
		"radioarchive_recorder_signal{"+r+`,signal="disconnected"} 0`,
		"radioarchive_recorder_decode_rate{"+r+`,short_name="ex"} 38.5`,
		"radioarchive_recorder_queue_entries{"+r+"} 3",
		"radioarchive_recorder_queue_oldest_age_seconds{"+r+"} 4000",
	)
	c.CloseNow()
	waitUntil(t, "the disconnection in the metrics", func() bool {
		return strings.Contains(e.scrape(t), "radioarchive_recorder_signal{"+r+`,signal="disconnected"} 1`+"\n")
	})
}

// TestIntegrationMissingRecording (R19): a call that ended 11 minutes ago
// with no recording is counted as missing; its recording arriving clears
// it. The live delay is observed from the call's first PCM.
func TestIntegrationMissingRecording(t *testing.T) {
	e := newEnv(t)
	_, key := e.recorder(t)
	c := e.open(t, key)
	now := time.Now()
	id := cp.CallID{SysNum: 0, CallNum: 77, StartTime: now.Add(-12 * time.Minute).Unix()}
	start := now.Add(-12 * time.Minute).UnixMilli()
	send(t, c, cp.CallStart{Type: "call_start", CallID: id, ShortName: "ex", Talkgroup: 1001, Unit: 5001,
		PatchedTalkgroups: []int32{}, StartTimeMS: start, FirstPCMMS: time.Now().Add(-700 * time.Millisecond).UnixMilli()})
	send(t, c, frame(id, 0))
	send(t, c, cp.CallEnd{Type: "call_end", CallID: id, StopTimeMS: now.Add(-11 * time.Minute).UnixMilli(), PatchedTalkgroups: []int32{}})
	waitUntil(t, "the missing recording", func() bool {
		return strings.Contains(e.scrape(t), "radioarchive_recordings_missing 1\n")
	})
	wantMetrics(t, e.scrape(t), `radioarchive_live_delay_seconds_bucket{le="0.5"} 0`, `radioarchive_live_delay_seconds_bucket{le="3"} 1`)

	audio := oggBytes(3)
	meta := recording(id, audio)
	meta.StartTimeMS, meta.StopTimeMS = start, now.Add(-11*time.Minute).UnixMilli()
	resp, err := http.DefaultClient.Do(uploadRequest(t, e.srv.URL, hdr(key), meta, audio))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		t.Fatalf("upload: %d %s", resp.StatusCode, b)
	}
	wantMetrics(t, e.scrape(t), "radioarchive_recordings_missing 0")
}
