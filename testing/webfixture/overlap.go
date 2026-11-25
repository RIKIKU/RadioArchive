package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// storeOverlap attaches recorder's `ex` to EX and stores, as recorder's
// calls, a copy of each of other's calls on hidden 1003 that started in
// the last 24 hours, at the same times and from unit 9003: two recorders
// on one network, both recording 1003 (R13).
func storeOverlap(ctx context.Context, conn *pgx.Conn, recorder, other, dir string) error {
	tag, err := conn.Exec(ctx, attach, recorder)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var n int
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM recorders WHERE name = $1`, recorder).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("no recorder named %q", recorder)
		}
	}
	rows, err := conn.Query(ctx, `
SELECT c.started_at, c.ended_at FROM calls c JOIN recorders r ON r.id = c.recorder_id
WHERE r.name = $1 AND c.talkgroup = 1003 AND c.ended_at IS NOT NULL AND c.started_at > now() - interval '24 hours'
ORDER BY c.started_at`, other)
	if err != nil {
		return err
	}
	type span struct{ start, end time.Time }
	var spans []span
	for rows.Next() {
		var s span
		if err := rows.Scan(&s.start, &s.end); err != nil {
			return err
		}
		spans = append(spans, s)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(spans) == 0 {
		return fmt.Errorf("%q has no calls on 1003 in the last 24 hours to overlap", other)
	}
	for i, s := range spans {
		c := fixtureCall{talkgroup: 1003, start: s.start, length: s.end.Sub(s.start), units: []int64{9003}}
		if _, err := storeCall(ctx, conn, recorder, dir, 500+int64(i), c); err != nil {
			return err
		}
	}
	return nil
}
