// Command webfixture puts the test fixture's setup (SPEC.md section 8) into
// a migrated database, for the browser tests (scripts/ci/e2e-web.sh): system
// EX "Example network" (ID 1) with 1001 "Dispatch North" (DISP N, tag
// dispatch, group North), 1002 "Fireground 41" (FG 41, tag fireground,
// fireground channel number 41), 1003 "Admin" (hidden) and 1004 (ignored).
// 1003 and 1004 have fireground channel numbers too (43 and 44), and 1003 is
// in group North, so the browser tests can show neither leaks through a
// fireground link or a group. Its units (R26) are 5001 "Engine 1" and
// 5100-5199 "Portables", and 9003 "Covert radio", heard only on 1003, and
// 9000-9009 "Covert fleet", whose radios are heard only on 1003, 1004 and a
// call patched with 1003, which unit search must never find (ADR 0019). It
// writes the rows directly: the fixture's setup is test data, put in before
// the tests start, as the import (issue #115) will put a real setup in.
// Running it again changes nothing.
//
// Given a recorder's name (one `radioarchive recorder add` made), it also
// attaches the recorder as the fixture's `Bench`: its `ex` is system EX, for
// the browser tests that play the fake recorder's calls (issue #58), and
// gives it a quiet threshold of 1 minute (R14), so the browser tests can
// show that a quiet recorder raises no notice (A27).
//
// The queue's browser tests (issue #60) change a talkgroup as an admin
// would: its state (A11) or its delay (A14), its alert (R22) or its LED
// colour. Since the binary serves the admin API (ADR 0025), they do it
// through it, given the API's URL in place of the database's: the fixture
// signs in as the fixture's admin through the made-up Authentik
// (testing/fakeauthentik) and sends the change (admin.go). It prints the
// moment it sent it, in Unix milliseconds, and the live feed sees the
// catalogue's generation move.
//
//	go run ./testing/webfixture <database URL> [recorder name]
//	go run ./testing/webfixture <API URL> talkgroup <ID> state <public|hidden|ignored>
//	go run ./testing/webfixture <API URL> talkgroup <ID> delay <minutes>
//	go run ./testing/webfixture <API URL> talkgroup <ID> alert|led <name|null>
//	go run ./testing/webfixture <database URL> calls <recorder name> <recordings dir>
//	go run ./testing/webfixture <database URL> call-start <recorder name> <talkgroup> <unit>
//	go run ./testing/webfixture <database URL> call-end <call ID> <recordings dir>
//	go run ./testing/webfixture <database URL> call-delete <call ID> <recordings dir>
//
// A16's browser test (issue #80) follows one call's link through its life:
// `call-start` stores a call in progress (and prints its ID), `call-end`
// ends it with its recording, as the recorder client's upload would, and
// `call-delete` deletes its row and file, as retention does. They stay:
// they are the recorder's and retention's doing, which no admin route does.
//
//	go run ./testing/webfixture <database URL> call-dated <recorder name> <recordings dir> <talkgroup> <unit> <age>
//	go run ./testing/webfixture <API URL> retention <days>
//	go run ./testing/webfixture <database URL> nightly <recordings dir> <ahead>
//
// A19's browser test (issue #81) dates a basket item and lets retention
// delete it: `call-dated` stores an ended call, with its recording, that
// started age ago (a Go duration, 696h for 29 days), and prints its ID;
// `retention` sets the period through the admin API, as an admin does;
// `nightly` does the nightly retention run with the job's clock ahead of
// now by ahead (48h is that call's 31st day) and prints how many calls it
// deleted (retention.go). The clock and the nightly run stay the tests'
// own: no admin route runs retention (ADR 0005).
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
INSERT INTO units (system_id, first_id, last_id, alias)
VALUES (1, 5001, 5001, 'Engine 1'), (1, 5100, 5199, 'Portables'), (1, 9003, 9003, 'Covert radio'),
       (1, 9000, 9009, 'Covert fleet')
ON CONFLICT DO NOTHING;
`

// attach is the recorder's attachment: its `ex` is system EX.
const attach = `
INSERT INTO attachments (recorder_id, short_name, system_id)
SELECT id, 'ex', 1 FROM recorders WHERE name = $1 ON CONFLICT DO NOTHING`

// quiet is the recorder's quiet threshold (ADR 0010).
const quiet = `UPDATE recorders SET quiet_after_minutes = 1 WHERE name = $1`

func main() {
	talkgroup := len(os.Args) == 6 && os.Args[2] == "talkgroup"
	calls := len(os.Args) == 5 && os.Args[2] == "calls"
	one := len(os.Args) == 7 && os.Args[2] == "call"
	a16 := (len(os.Args) == 6 && os.Args[2] == "call-start") ||
		(len(os.Args) == 5 && (os.Args[2] == "call-end" || os.Args[2] == "call-delete"))
	a19 := (len(os.Args) == 8 && os.Args[2] == "call-dated") || (len(os.Args) == 4 && os.Args[2] == "retention") ||
		(len(os.Args) == 5 && os.Args[2] == "nightly")
	if len(os.Args) != 2 && len(os.Args) != 3 && !talkgroup && !calls && !one && !a16 && !a19 {
		fmt.Fprintln(os.Stderr, "usage: webfixture <database URL> [recorder name]\n       webfixture <API URL> talkgroup <ID> state|delay|alert|led <value>\n"+
			"       webfixture <database URL> calls <recorder name> <recordings dir>\n       webfixture <database URL> call <recorder name> <recordings dir> <talkgroup> <unit>\n"+
			"       webfixture <database URL> call-start <recorder name> <talkgroup> <unit>\n       webfixture <database URL> call-end|call-delete <call ID> <recordings dir>\n"+
			"       webfixture <database URL> call-dated <recorder name> <recordings dir> <talkgroup> <unit> <age>\n"+
			"       webfixture <API URL> retention <days>\n       webfixture <database URL> nightly <recordings dir> <ahead>")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// What an admin does goes through the admin API (admin.go).
	if len(os.Args) > 3 && (talkgroup || os.Args[2] == "retention") != apiURL(os.Args[1]) {
		fmt.Fprintf(os.Stderr, "webfixture: %s takes the API's URL; the others take the database's\n", os.Args[2])
		os.Exit(2)
	}
	if talkgroup {
		if err := changeTalkgroup(ctx, os.Args[1], os.Args[3], os.Args[4], os.Args[5]); err != nil {
			fmt.Fprintln(os.Stderr, "webfixture:", err)
			os.Exit(1)
		}
		return
	}
	if a19 && os.Args[2] != "call-dated" {
		var err error
		if os.Args[2] == "retention" {
			err = setRetention(ctx, os.Args[1], os.Args[3])
		} else {
			ahead, perr := time.ParseDuration(os.Args[4])
			if perr != nil {
				fmt.Fprintln(os.Stderr, "webfixture: nightly: the clock's lead is a duration, like 48h")
				os.Exit(2)
			}
			err = runNightly(ctx, os.Args[1], os.Args[3], ahead)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "webfixture: %s: %v\n", os.Args[2], err)
			os.Exit(1)
		}
		return
	}
	conn, err := pgx.Connect(ctx, os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "webfixture: connect:", err)
		os.Exit(1)
	}
	defer conn.Close(context.Background())
	if a19 {
		tg, err1 := strconv.ParseInt(os.Args[5], 10, 32)
		unit, err2 := strconv.ParseInt(os.Args[6], 10, 64)
		age, err3 := time.ParseDuration(os.Args[7])
		if err1 != nil || err2 != nil || err3 != nil {
			fmt.Fprintln(os.Stderr, "webfixture: call-dated: the talkgroup and the unit are numbers, the age a duration, like 696h")
			os.Exit(2)
		}
		if err := storeDated(ctx, conn, os.Args[3], os.Args[4], int32(tg), unit, age); err != nil {
			fmt.Fprintln(os.Stderr, "webfixture: call-dated:", err)
			os.Exit(1)
		}
		return
	}
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
