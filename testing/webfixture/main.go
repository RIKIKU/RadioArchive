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
// the browser tests that play the fake recorder's calls (issue #58), and
// gives it a quiet threshold of 1 minute (R14), so the browser tests can
// show that a quiet recorder raises no notice (A27).
//
// The queue's browser tests (issue #60) change a talkgroup as an admin
// would, which the binary can't do yet (M4): its state (A11) or its delay
// (A14), its alert (R22) or its LED colour. It prints the moment the change
// committed, in Unix milliseconds, and the live feed sees the catalogue's
// generation move.
//
//	go run ./testing/webfixture <database URL> [recorder name]
//	go run ./testing/webfixture <database URL> talkgroup <ID> state <public|hidden|ignored>
//	go run ./testing/webfixture <database URL> talkgroup <ID> delay <minutes>
//	go run ./testing/webfixture <database URL> talkgroup <ID> alert|led <name|null>
//	go run ./testing/webfixture <database URL> calls <recorder name> <recordings dir>
//	go run ./testing/webfixture <database URL> call-start <recorder name> <talkgroup> <unit>
//	go run ./testing/webfixture <database URL> call-end <call ID> <recordings dir>
//	go run ./testing/webfixture <database URL> call-delete <call ID> <recordings dir>
//
// A16's browser test (issue #80) follows one call's link through its life:
// `call-start` stores a call in progress (and prints its ID), `call-end`
// ends it with its recording, as the recorder client's upload would, and
// `call-delete` deletes its row and file, as retention does. Like the
// talkgroup changes, they go when the binary serves the admin API (M4).
//
// The Recent calls page's tests (issue #79) need an archive to search:
// `calls` stores made-up calls as Bench's, by the hour before now, with
// recordings (silent Ogg/Opus files) on the volume (calls.go). Each run
// adds them again, at times from its own now.
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

// quiet is the recorder's quiet threshold (ADR 0010).
const quiet = `UPDATE recorders SET quiet_after_minutes = 1 WHERE name = $1`

// changeTalkgroup sets one of system EX's talkgroups' state, delay, alert
// or LED colour ("null" for an alert or colour of none of its own).
func changeTalkgroup(ctx context.Context, conn *pgx.Conn, id, field, value string) error {
	tg, err := strconv.Atoi(id)
	if err != nil {
		return fmt.Errorf("talkgroup %q isn't a number", id)
	}
	orNull := func(v string) any {
		if v == "null" {
			return nil
		}
		return v
	}
	var tag pgconn.CommandTag
	switch field {
	case "state":
		tag, err = conn.Exec(ctx, `UPDATE talkgroups SET state = $1 WHERE system_id = 1 AND talkgroup_id = $2`, value, tg)
	case "delay":
		var m int
		if m, err = strconv.Atoi(value); err != nil {
			return fmt.Errorf("delay %q isn't a number of minutes", value)
		}
		tag, err = conn.Exec(ctx, `UPDATE talkgroups SET delay_minutes = $1 WHERE system_id = 1 AND talkgroup_id = $2`, m, tg)
	case "alert":
		tag, err = conn.Exec(ctx, `UPDATE talkgroups SET alert = $1 WHERE system_id = 1 AND talkgroup_id = $2`, orNull(value), tg)
	case "led":
		tag, err = conn.Exec(ctx, `UPDATE talkgroups SET led_colour = $1 WHERE system_id = 1 AND talkgroup_id = $2`, orNull(value), tg)
	default:
		return fmt.Errorf("can't change a talkgroup's %q: state, delay, alert or led", field)
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("no talkgroup %d in system EX", tg)
	}
	fmt.Println(time.Now().UnixMilli())
	return nil
}

func main() {
	talkgroup := len(os.Args) == 6 && os.Args[2] == "talkgroup"
	calls := len(os.Args) == 5 && os.Args[2] == "calls"
	one := len(os.Args) == 7 && os.Args[2] == "call"
	a16 := (len(os.Args) == 6 && os.Args[2] == "call-start") ||
		(len(os.Args) == 5 && (os.Args[2] == "call-end" || os.Args[2] == "call-delete"))
	if len(os.Args) != 2 && len(os.Args) != 3 && !talkgroup && !calls && !one && !a16 {
		fmt.Fprintln(os.Stderr, "usage: webfixture <database URL> [recorder name]\n       webfixture <database URL> talkgroup <ID> state|delay <value>\n"+
			"       webfixture <database URL> calls <recorder name> <recordings dir>\n       webfixture <database URL> call <recorder name> <recordings dir> <talkgroup> <unit>\n"+
			"       webfixture <database URL> call-start <recorder name> <talkgroup> <unit>\n       webfixture <database URL> call-end|call-delete <call ID> <recordings dir>")
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
	if a16 {
		var err error
		switch os.Args[2] {
		case "call-start":
			tg, err1 := strconv.ParseInt(os.Args[4], 10, 32)
			unit, err2 := strconv.ParseInt(os.Args[5], 10, 64)
			if err1 != nil || err2 != nil {
				fmt.Fprintln(os.Stderr, "webfixture: call-start: the talkgroup and the unit are numbers")
				os.Exit(2)
			}
			err = startCall(ctx, conn, os.Args[3], int32(tg), unit)
		case "call-end":
			err = endCall(ctx, conn, os.Args[3], os.Args[4])
		case "call-delete":
			err = deleteCall(ctx, conn, os.Args[3], os.Args[4])
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "webfixture: %s: %v\n", os.Args[2], err)
			os.Exit(1)
		}
		return
	}
	if calls {
		if err := storeCalls(ctx, conn, os.Args[3], os.Args[4], time.Now()); err != nil {
			fmt.Fprintln(os.Stderr, "webfixture: calls:", err)
			os.Exit(1)
		}
		return
	}
	if one {
		tg, err1 := strconv.ParseInt(os.Args[5], 10, 32)
		unit, err2 := strconv.ParseInt(os.Args[6], 10, 64)
		if err1 != nil || err2 != nil {
			fmt.Fprintln(os.Stderr, "webfixture: call: the talkgroup and the unit are numbers")
			os.Exit(2)
		}
		if err := storeOne(ctx, conn, os.Args[3], os.Args[4], int32(tg), unit); err != nil {
			fmt.Fprintln(os.Stderr, "webfixture: call:", err)
			os.Exit(1)
		}
		return
	}
	if talkgroup {
		if err := changeTalkgroup(ctx, conn, os.Args[3], os.Args[4], os.Args[5]); err != nil {
			fmt.Fprintln(os.Stderr, "webfixture:", err)
			os.Exit(1)
		}
		return
	}
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
		if err == nil {
			_, err = conn.Exec(ctx, quiet, os.Args[2])
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "webfixture: attach:", err)
			os.Exit(1)
		}
	}
}
