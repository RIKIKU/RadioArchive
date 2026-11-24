package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/RIKIKU/RadioArchive/internal/store"
	"github.com/RIKIKU/RadioArchive/internal/store/db"
	"github.com/RIKIKU/RadioArchive/internal/store/storetest"
)

// Issue #85: a live transmission arriving while the call's recording is
// being stored waits for the recording and is then ignored. It used to
// insert its row before taking the call's row lock, the upload's
// transaction held that lock and then inserted the same transmission, and
// PostgreSQL broke the deadlock by failing one of them: the recorder's
// socket was closed, or the upload failed.
func TestLiveTransmissionDuringUpload(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := store.Open(ctx, storetest.FreshURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	var recorder int64
	var call pgtype.UUID
	for _, step := range []func() error{
		func() error {
			return st.Pool.QueryRow(ctx, `INSERT INTO recorders (name, key_hash)
				VALUES ('r', decode(repeat('ab', 32), 'hex')) RETURNING id`).Scan(&recorder)
		},
		func() error {
			_, err := st.Pool.Exec(ctx, `INSERT INTO systems (id, label) VALUES (1, 'EX')`)
			return err
		},
		func() error {
			return st.Pool.QueryRow(ctx, `INSERT INTO calls (recorder_id, sys_num, call_num, start_time,
				system_id, short_name, talkgroup, started_at)
				VALUES ($1, 0, 1, 1795521580, 1, 'ex', 1001, '2026-11-24T11:59:40Z') RETURNING id`, recorder).Scan(&call)
		},
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}

	// The upload's transaction locks the call, as upload.go does.
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	q := db.New(tx)
	ts := func(s string) pgtype.Timestamptz {
		v, _ := time.Parse(time.RFC3339, s)
		return pgtype.Timestamptz{Time: v, Valid: true}
	}
	started, ended := ts("2026-11-24T11:59:40Z"), ts("2026-11-24T12:00:00Z")
	if _, err := q.LockCallForRecording(ctx, db.LockCallForRecordingParams{RecorderID: recorder, SysNum: 0,
		CallNum: 1, StartTime: 1795521580, SystemID: 1, ShortName: "ex", Talkgroup: 1001,
		PatchedTalkgroups: []int32{}, StartedAt: started, EndedAt: ended}); err != nil {
		t.Fatal(err)
	}

	// The live transmission comes meanwhile, and waits for a lock.
	live := make(chan error, 1)
	go func() {
		live <- st.AddLiveTransmission(ctx, db.AddLiveTransmissionParams{CallID: call, OffsetMs: 5000, Unit: 5002})
	}()
	for {
		var waiting int
		if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'
			AND query LIKE '%AddLiveTransmission%'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		select {
		case err := <-live:
			t.Fatalf("the live transmission didn't wait for the upload's lock: %v", err)
		case <-ctx.Done():
			t.Fatal("the live transmission never waited for the upload's lock")
		case <-time.After(5 * time.Millisecond):
		}
	}

	// The upload stores the recording and its transmissions, the same
	// offset among them, and commits.
	path, size := "x.ogg", int64(3)
	d0, d1 := int32(5000), int32(15000)
	if err := q.SetCallRecording(ctx, db.SetCallRecordingParams{ID: call, ShortName: "ex", Talkgroup: 1001,
		PatchedTalkgroups: []int32{}, StartedAt: started, EndedAt: ended,
		RecordingPath: &path, RecordingSize: &size, RecordingSha256: make([]byte, 32)}); err != nil {
		t.Fatal(err)
	}
	if err := q.DeleteTransmissions(ctx, call); err != nil {
		t.Fatal(err)
	}
	for _, x := range []db.AddTransmissionParams{
		{CallID: call, OffsetMs: 0, Unit: 5001, DurationMs: &d0},
		{CallID: call, OffsetMs: 5000, Unit: 5002, DurationMs: &d1},
	} {
		if err := q.AddTransmission(ctx, x); err != nil {
			t.Fatalf("the upload's transmission: %v", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("the upload's commit: %v", err)
	}
	if err := <-live; err != nil {
		t.Fatalf("the live transmission: %v", err)
	}

	// Only the recording's transmissions are kept.
	var n, timed int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*), count(duration_ms) FROM transmissions WHERE call_id = $1`,
		call).Scan(&n, &timed); err != nil {
		t.Fatal(err)
	}
	if n != 2 || timed != 2 {
		t.Errorf("%d transmissions, %d with durations; want the recording's 2", n, timed)
	}
}
