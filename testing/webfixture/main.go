// Command webfixture puts the test fixture's setup (SPEC.md section 8) into
// a migrated database, for the browser tests (scripts/ci/e2e-web.sh): system
// EX "Example network" (ID 1) with 1001 "Dispatch North" (DISP N, tag
// dispatch, group North), 1002 "Fireground 41" (FG 41, tag fireground,
// fireground channel number 41), 1003 "Admin" (hidden) and 1004 (ignored).
// 1003 and 1004 have fireground channel numbers too (43 and 44), and 1003 is
// in group North, so the browser tests can show neither leaks through a
// fireground link or a group. The binary serves no admin API yet (ADR 0003),
// so this writes the rows directly. Running it again changes nothing.
//
// Given a recorder's name (one `radioarchive recorder add` made), it also
// attaches the recorder as the fixture's `Bench`: its `ex` is system EX, for
// the browser tests that play the fake recorder's calls (issue #58).
//
//	go run ./testing/webfixture <database URL> [recorder name]
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
)

const fixture = `
INSERT INTO systems (id, label, name) VALUES (1, 'EX', 'Example network') ON CONFLICT DO NOTHING;
INSERT INTO tags (name) VALUES ('dispatch'), ('fireground'), ('admin') ON CONFLICT DO NOTHING;
INSERT INTO groups (name) VALUES ('North') ON CONFLICT DO NOTHING;
INSERT INTO talkgroups (system_id, talkgroup_id, label, name, tag_id)
SELECT 1, 1001, 'DISP N', 'Dispatch North', id FROM tags WHERE name = 'dispatch' ON CONFLICT DO NOTHING;
INSERT INTO talkgroups (system_id, talkgroup_id, label, name, tag_id, fireground_channel)
SELECT 1, 1002, 'FG 41', 'Fireground 41', id, 41 FROM tags WHERE name = 'fireground' ON CONFLICT DO NOTHING;
INSERT INTO talkgroups (system_id, talkgroup_id, label, name, tag_id, state, fireground_channel)
SELECT 1, 1003, 'ADMIN', 'Admin', id, 'hidden', 43 FROM tags WHERE name = 'admin' ON CONFLICT DO NOTHING;
INSERT INTO talkgroups (system_id, talkgroup_id, label, name, tag_id, state, fireground_channel)
SELECT 1, 1004, 'TG 1004', '', id, 'ignored', 44 FROM tags WHERE name = 'dispatch' ON CONFLICT DO NOTHING;
INSERT INTO talkgroup_groups (system_id, talkgroup_id, group_id)
SELECT 1, t, id FROM groups, unnest(ARRAY[1001, 1003]) AS t WHERE name = 'North' ON CONFLICT DO NOTHING;
`

// attach is the recorder's attachment: its `ex` is system EX.
const attach = `
INSERT INTO attachments (recorder_id, short_name, system_id)
SELECT id, 'ex', 1 FROM recorders WHERE name = $1 ON CONFLICT DO NOTHING`

func main() {
	if len(os.Args) != 2 && len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: webfixture <database URL> [recorder name]")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "webfixture: connect:", err)
		os.Exit(1)
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(ctx, fixture); err != nil {
		fmt.Fprintln(os.Stderr, "webfixture:", err)
		os.Exit(1)
	}
	if len(os.Args) == 3 {
		tag, err := conn.Exec(ctx, attach, os.Args[2])
		if err == nil && tag.RowsAffected() == 0 {
			var n int
			err = conn.QueryRow(ctx, `SELECT count(*) FROM attachments a JOIN recorders r ON r.id = a.recorder_id WHERE r.name = $1`, os.Args[2]).Scan(&n)
			if err == nil && n == 0 {
				err = fmt.Errorf("no recorder named %q", os.Args[2])
			}
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "webfixture: attach:", err)
			os.Exit(1)
		}
	}
}
