package store_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/RIKIKU/RadioArchive/internal/store"
	"github.com/RIKIKU/RadioArchive/internal/store/storetest"
)

// TestPublicCallsIsCallState: public_calls's condition (migration 00012)
// is call_state's rule (ADR 0003) written for speed, not a second rule. For
// every state of a call's own talkgroup (public, hidden, ignored, or not
// listed) and of up to two patched talkgroups, a call is in the view
// exactly when call_state says it's public.
func TestPublicCallsIsCallState(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, storetest.FreshURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	exec(`INSERT INTO systems (id, label, name) VALUES (1, 'EX', 'Example network'), (2, 'OT', 'Other network')`)
	exec(`INSERT INTO tags (name) VALUES ('t')`)
	exec(`INSERT INTO recorders (name, key_hash) VALUES ('r', decode(repeat('ab', 32), 'hex'))`)
	// Talkgroups 1 (public), 2 (hidden), 3 (ignored) in system 1; 4 isn't
	// listed. System 2 lists 1, 2 and 3 with the opposite states, so a
	// state is never read from the wrong system.
	states := map[int]string{1: "public", 2: "hidden", 3: "ignored"}
	other := map[int]string{1: "hidden", 2: "public", 3: "public"}
	for tg, s := range states {
		exec(`INSERT INTO talkgroups (system_id, talkgroup_id, tag_id, state) SELECT 1, $1, id, $2 FROM tags`, tg, s)
		exec(`INSERT INTO talkgroups (system_id, talkgroup_id, tag_id, state) SELECT 2, $1, id, $2 FROM tags`, tg, other[tg])
	}
	n := 0
	for sys := 1; sys <= 2; sys++ {
		for own := 1; own <= 4; own++ {
			patches := [][]int32{{}}
			for a := int32(1); a <= 4; a++ {
				patches = append(patches, []int32{a})
				for b := a + 1; b <= 4; b++ {
					patches = append(patches, []int32{a, b})
				}
			}
			for _, p := range patches {
				n++
				exec(`INSERT INTO calls (recorder_id, sys_num, call_num, start_time, system_id, short_name, talkgroup,
				        patched_talkgroups, started_at)
				      SELECT id, 0, $1, 0, $2, 'ex', $3, $4, now() FROM recorders`, n, sys, own, p)
			}
		}
	}
	rows, err := st.Pool.Query(ctx, `SELECT c.system_id, c.talkgroup, c.patched_talkgroups,
	        call_state(c.system_id, c.talkgroup, c.patched_talkgroups) = 'public',
	        EXISTS (SELECT 1 FROM public_calls p WHERE p.id = c.id)
	    FROM calls c`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	checked := 0
	for rows.Next() {
		var sys int64
		var tg int32
		var patched []int32
		var byState, inView bool
		if err := rows.Scan(&sys, &tg, &patched, &byState, &inView); err != nil {
			t.Fatal(err)
		}
		checked++
		if byState != inView {
			t.Errorf("system %d, talkgroup %d patched with %v: call_state public %v, in public_calls %v",
				sys, tg, patched, byState, inView)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if checked != n || n == 0 {
		t.Fatal(fmt.Sprintf("checked %d calls of %d", checked, n))
	}
}
