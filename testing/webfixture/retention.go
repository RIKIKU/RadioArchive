package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/RIKIKU/RadioArchive/internal/retention"
	"github.com/RIKIKU/RadioArchive/internal/store"
)

// A19's basket item (issue #81; SPEC.md A19, R64, R88): a call recorded
// days ago, and the nightly retention run on a later day, as the binary's
// job does it on its schedule. The binary has no route that runs it
// (ADR 0005), so the fixture runs the job itself, on a clock of its own.

// storeDated stores an ended call on talkgroup from unit, of 4 s, that
// started age ago, with its recording, and prints its ID.
func storeDated(ctx context.Context, conn *pgx.Conn, recorder, dir string, talkgroup int32, unit int64, age time.Duration) error {
	now := time.Now().Truncate(time.Millisecond)
	c := fixtureCall{talkgroup: talkgroup, start: now.Add(-age), length: 4 * time.Second, units: []int64{unit}}
	id, err := storeCall(ctx, conn, recorder, dir, 2_000_000+now.UnixMilli()%1_000_000, c)
	if err != nil {
		return err
	}
	fmt.Println(id)
	return nil
}

// runNightly does the nightly run as if the clock were ahead of now, and
// prints how many calls it deleted.
func runNightly(ctx context.Context, url, dir string, ahead time.Duration) error {
	at := time.Now().Add(ahead)
	return withJob(ctx, url, dir, func() time.Time { return at }, func(j *retention.Job) error {
		res, err := j.RunNightly(ctx)
		if err == nil {
			fmt.Println(res.Calls)
		}
		return err
	})
}

func withJob(ctx context.Context, url, dir string, now func() time.Time, f func(*retention.Job) error) error {
	st, err := store.Open(ctx, url)
	if err != nil {
		return err
	}
	defer st.Close()
	if dir == "" {
		dir = os.TempDir()
	}
	j, err := retention.New(retention.Config{Store: st, Dir: dir, Now: now,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))})
	if err != nil {
		return err
	}
	return f(j)
}
