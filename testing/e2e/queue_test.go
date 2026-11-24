//go:build cgo

package e2e

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RIKIKU/RadioArchive/internal/recorderclient"
)

// uploadOrder is the recorder's stored recordings' call_nums in the order
// the server stored them.
func (r *rig) uploadOrder(t *testing.T) []int64 {
	t.Helper()
	rows, err := r.st.Pool.Query(context.Background(), `
		SELECT c.call_num FROM calls c JOIN recorders rec ON rec.id = c.recorder_id
		WHERE rec.name = $1 AND c.recording_path IS NOT NULL ORDER BY c.recording_at, c.call_num`, r.name)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var n int64
		_ = rows.Scan(&n)
		out = append(out, n)
	}
	return out
}

func queueFiles(t *testing.T, dir string) []string {
	t.Helper()
	des, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range des {
		out = append(out, d.Name())
	}
	return out
}

// TestA28DiskQueue is SPEC.md A28. The server is really stopped (its
// listener, connections and live socket closed) while 10 calls are
// recorded over an hour of the client's clock, then started again as a new
// server on the same address and database. All 10 recordings arrive oldest
// first through the disk queue and it empties; an entry queued a day
// earlier is older than QUEUE_MAX_AGE, so it's dropped and counted, and its
// WAV deleted.
func TestA28DiskQueue(t *testing.T) {
	r := start(t)
	r.serverDown(t)
	waitFor(t, "the client to see the server go", 10*time.Second, func() bool { return !r.client.LiveConnected() })

	// One call a day ago, still queued (over-age by the end).
	oldWAV := r.play(t, backToBack(100, 1, 1763900000), 0)[100]
	waitFor(t, "the first call to queue", 10*time.Second, func() bool { return r.client.Queue().Count == 1 })
	r.clock.advance(23*time.Hour + 30*time.Minute)

	// The hour's 10 calls, six minutes apart on the client's clock. The
	// first call passes QUEUE_MAX_AGE during the hour, and the client may
	// drop it at its next retry, before the server is back.
	m := r.client.Metrics()
	queued := func() int { return r.client.Queue().Count + int(m.Get(recorderclient.MetricQueueExpired)) }
	wavs := map[int64]string{}
	for i := range int64(10) {
		for k, v := range r.play(t, backToBack(1+i, 1, 1764000000+i*360), 0) {
			wavs[k] = v
		}
		want := int(i) + 2
		waitFor(t, "the call to queue", 10*time.Second, func() bool { return queued() >= want })
		r.clock.advance(6 * time.Minute)
	}
	if n := queued(); n != 11 {
		t.Errorf("%d entries queued or expired during the outage, want 11", n)
	}
	if q := r.client.Queue(); q.Count < 10 || q.OldestAgeS < 3600 {
		t.Errorf("queue during the outage = %+v, want at least 10 entries, the oldest over an hour", q)
	}
	if n := len(queueFiles(t, r.queue)); n != r.client.Queue().Count {
		t.Errorf("%d entries on disk, %d in the queue", n, r.client.Queue().Count)
	}

	r.serverUp(t)
	waitFor(t, "the queue to empty", 60*time.Second, func() bool { return r.client.Queue().Count == 0 })
	cs := r.waitRecordings(t, 10)

	if got := r.uploadOrder(t); !slices.Equal(got, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}) {
		t.Errorf("recordings stored in order %v, want 1-10, oldest first", got)
	}
	if len(cs) != 10 {
		t.Errorf("%d calls stored, want 10 (not the expired one)", len(cs))
	}
	if n := m.Get(recorderclient.MetricQueueExpired); n != 1 {
		t.Errorf("expired = %d, want 1", n)
	}
	if n := m.Get(recorderclient.MetricUploads, "result", "stored"); n != 10 {
		t.Errorf("stored uploads = %d, want 10", n)
	}
	// Live audio in the outage was lost, not queued (R5).
	if n := m.Get(recorderclient.MetricLiveDropped, "reason", "disconnected"); n == 0 {
		t.Error("no live frames counted as dropped during the outage")
	}
	for n, w := range wavs {
		if !gone(w) {
			t.Errorf("call %d: WAV still there after the server acknowledged it", n)
		}
	}
	if !gone(oldWAV) {
		t.Error("the expired call's WAV was kept; a given-up recording's WAV is deleted")
	}
	waitFor(t, "the queue directory to empty", 5*time.Second, func() bool { return len(queueFiles(t, r.queue)) == 0 })
	if g := m.Gauge(recorderclient.MetricQueueEntries); g != 0 {
		t.Errorf("queue gauge = %v", g)
	}
}

// TestQueueSurvivesClientRestart: recordings queued while the server is
// down stay on disk when the client stops, and the next client process,
// on the same queue directory, sends them when the server is back.
func TestQueueSurvivesClientRestart(t *testing.T) {
	r := start(t)
	r.serverDown(t)
	waitFor(t, "the client to see the server go", 10*time.Second, func() bool { return !r.client.LiveConnected() })
	wavs := r.replay(t, "a1-overlapping.json")
	waitFor(t, "both calls to queue", 10*time.Second, func() bool { return r.client.Queue().Count == 2 })
	r.clientDown()

	files := queueFiles(t, r.queue)
	if len(files) != 2 || !strings.HasSuffix(files[0], ".json") || !strings.HasSuffix(files[1], ".json") {
		t.Fatalf("queue directory after the client stopped: %v", files)
	}
	for _, w := range wavs {
		if gone(w) {
			t.Fatal("a WAV was deleted before the server acknowledged it")
		}
	}

	r.serverUp(t)
	r.clientUp(t, "")
	cs := r.waitRecordings(t, 2)
	if len(cs) != 2 || cs[0].callNum != 1 || cs[1].callNum != 2 {
		t.Errorf("calls %+v, want 1 and 2", cs)
	}
	if got := r.uploadOrder(t); !slices.Equal(got, []int64{1, 2}) {
		t.Errorf("stored in order %v, want 1, 2", got)
	}
	waitFor(t, "the WAVs to go and the queue to empty", 10*time.Second, func() bool {
		return gone(wavs[1]) && gone(wavs[2]) && len(queueFiles(t, r.queue)) == 0
	})
}

// TestA29OldClient is SPEC.md A29 through the real client: a 1.2 client of
// a 1.4 server is refused (and counted), so its recordings stay queued; once
// upgraded to 1.3 it connects, is recorded as out of date, and its queue
// drains.
func TestA29OldClient(t *testing.T) {
	r := newRig(t, "1.4.0")
	r.serverUp(t)
	r.clientUp(t, "1.2.0")
	waitFor(t, "the refusal", 10*time.Second, func() bool { return r.rs.Stat.ClientsTooOld.Load() > 0 })
	if r.client.LiveConnected() {
		t.Fatal("a 1.2 client connected to a 1.4 server")
	}
	wavs := r.replay(t, "a1-overlapping.json")
	waitFor(t, "both calls to queue", 10*time.Second, func() bool { return r.client.Queue().Count == 2 })
	time.Sleep(time.Second) // the uploads are tried and refused
	if n := len(r.calls(t)); n != 0 {
		t.Errorf("%d calls stored from a refused client", n)
	}
	r.clientDown()

	r.clientUp(t, "1.3.0") // the upgrade, on the same queue
	waitFor(t, "the live connection", 10*time.Second, r.client.LiveConnected)
	r.waitRecordings(t, 2)
	var v string
	var out bool
	if err := r.st.Pool.QueryRow(context.Background(),
		`SELECT client_version, client_out_of_date FROM recorders WHERE name = $1`, r.name).Scan(&v, &out); err != nil {
		t.Fatal(err)
	}
	if v != "1.3.0" || !out {
		t.Errorf("recorder's client %q, out of date %v; want 1.3.0, true", v, out)
	}
	waitFor(t, "the WAVs to go", 10*time.Second, func() bool { return gone(wavs[1]) && gone(wavs[2]) })
}
