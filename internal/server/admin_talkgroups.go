package server

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/RIKIKU/RadioArchive/internal/gate"
	"github.com/RIKIKU/RadioArchive/internal/store/db"
)

// The admin table's data (R50, R34; ADR 0026): every talkgroup at once with
// last heard and calls in the last 30 days, and one talkgroup's calls,
// hidden and ignored ones too. Neither is audited (R34, R54) nor logged
// per request (R90).

// TableWindow is how far back the table counts a talkgroup's calls (R50).
const TableWindow = 30 * 24 * time.Hour

// TableRow is a talkgroup as the admin table shows it: every R22 field as
// the talkgroup holds it (null alert and LED colour are taken from its tag,
// groups or system, ADR 0006), when it was last heard, and how many calls it
// was heard on in the 30 days up to the answer.
type TableRow struct {
	Talkgroup
	LastHeardAt *time.Time `json:"last_heard_at"`
	Calls30Days int64      `json:"calls_30_days"`
	// Quiet: listeners' lists leave it out (R114; ADR 0054). Only a
	// public talkgroup is ever quiet. NotHeardDays is the catalogue's,
	// given when it's quiet.
	Quiet        bool `json:"quiet"`
	NotHeardDays int  `json:"not_heard_days,omitempty"`
}

// Table is GET /api/v1/admin/talkgroups.
type Table struct {
	// CallsSince is when the 30 days began: calls_30_days counts the calls
	// heard from then.
	CallsSince time.Time  `json:"calls_since"`
	Talkgroups []TableRow `json:"talkgroups"`
}

// talkgroupTable answers every talkgroup of every system in one answer, no
// paging (R50, A23): the admin UI searches, filters and sorts it in the
// browser.
func (a *Admin) talkgroupTable(r *http.Request, _ string) (int, any, error) {
	if len(r.URL.Query()) > 0 {
		return 0, nil, refuseAdmin(http.StatusBadRequest, "unknown_parameter", "This list takes no parameters.")
	}
	since := a.now().UTC().Add(-TableWindow).Truncate(time.Second)
	rows, err := a.st.AdminTalkgroupTable(r.Context(), db.AdminTalkgroupTableParams{
		Since: pgtype.Timestamptz{Time: since, Valid: true}, SinceUnix: since.Unix()})
	if err != nil {
		return 0, nil, dbErr("talkgroup table", err)
	}
	var quiet map[gate.TalkgroupKey]int
	if a.quiet != nil {
		if quiet, _, err = a.quiet.QuietTalkgroups(r.Context()); err != nil {
			return 0, nil, dbErr("quiet talkgroups", err)
		}
	}
	out := Table{CallsSince: since, Talkgroups: make([]TableRow, 0, len(rows))}
	for _, t := range rows {
		days, q := quiet[gate.TalkgroupKey{System: t.SystemID, Talkgroup: t.TalkgroupID}]
		out.Talkgroups = append(out.Talkgroups, TableRow{
			Talkgroup: talkgroupOf(db.GetTalkgroupRow{SystemID: t.SystemID, TalkgroupID: t.TalkgroupID,
				Label: t.Label, Name: t.Name, Tag: t.Tag, State: t.State, Unreviewed: t.Unreviewed,
				Groups: t.Groups, DelayMinutes: t.DelayMinutes, Alert: t.Alert, LedColour: t.LedColour,
				SortOrder: t.SortOrder, FiregroundChannel: t.FiregroundChannel, Type: t.Type, Frequency: t.Frequency,
				CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt, AlwaysShow: t.AlwaysShow}),
			LastHeardAt:  ts(t.LastHeardAt),
			Calls30Days:  t.CallsSince,
			Quiet:        q,
			NotHeardDays: days,
		})
	}
	return http.StatusOK, out, nil
}

// AdminCall is one of a talkgroup's calls as the admin API lists it.
type AdminCall struct {
	ID                string  `json:"id"`
	Talkgroup         int32   `json:"talkgroup"`
	PatchedTalkgroups []int32 `json:"patched_talkgroups"`
	// State is what the call is treated as now: the strictest state in its
	// patch (R16, R30).
	State      string          `json:"state"`
	StartedAt  time.Time       `json:"started_at"`
	EndedAt    *time.Time      `json:"ended_at"`
	InProgress bool            `json:"in_progress"`
	Recording  *AdminRecording `json:"recording"`
}

// AdminRecording is a call's recording, played through the admin API.
type AdminRecording struct {
	URL  string `json:"url"`
	Size int64  `json:"size"`
}

// TalkgroupCalls is GET /api/v1/admin/systems/{system}/talkgroups/{talkgroup}/calls.
type TalkgroupCalls struct {
	Calls []AdminCall `json:"calls"`
	// Next is the cursor for the next page, or null when there's none.
	Next *string `json:"next"`
}

const (
	defaultCallsPage = 100
	maxCallsPage     = 500
)

// talkgroupCalls lists a talkgroup's stored calls for admins (R34): newest
// first, paged by start time, those on it and those patched with it, in any
// state, each playable through GET /calls/{call}/recording. It's the admin
// API's: no public route reads it, and the gate isn't involved.
func (a *Admin) talkgroupCalls(r *http.Request, _ string) (int, any, error) {
	sys, tg, err := talkgroupPath(r)
	if err != nil {
		return 0, nil, err
	}
	limit := defaultCallsPage
	cursorAt := pgtype.Timestamptz{InfinityModifier: pgtype.Infinity, Valid: true}
	var cursorID [16]byte
	for k, v := range r.URL.Query() {
		switch {
		case len(v) != 1:
			return 0, nil, refuseAdmin(http.StatusBadRequest, "invalid_"+k, "Give %s once.", k)
		case k == "limit":
			n, err := strconv.Atoi(v[0])
			if err != nil || n < 1 || n > maxCallsPage {
				return 0, nil, refuseAdmin(http.StatusBadRequest, "invalid_limit", "limit must be a whole number from 1 to %d.", maxCallsPage)
			}
			limit = n
		case k == "cursor":
			at, id, ok := parseAdminCursor(v[0])
			if !ok {
				return 0, nil, refuseAdmin(http.StatusBadRequest, "invalid_cursor", "That cursor isn't one this list gave.")
			}
			cursorAt, cursorID = pgtype.Timestamptz{Time: at, Valid: true}, id
		default:
			return 0, nil, refuseAdmin(http.StatusBadRequest, "unknown_parameter", "This list takes limit and cursor only.")
		}
	}
	if _, err := a.talkgroup(r.Context(), sys, tg); err != nil {
		return 0, nil, err
	}
	// Custom plans, in a read-only transaction, as the recent calls run
	// (ADR 0013): a generic plan reads the patched half by start time
	// across the month, 90 ms warm for a rare talkgroup instead of 15.
	ctx := r.Context()
	tx, err := a.st.Pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return 0, nil, dbErr("talkgroup calls", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, "SET LOCAL plan_cache_mode = force_custom_plan"); err != nil {
		return 0, nil, dbErr("talkgroup calls", err)
	}
	rows, err := a.st.WithTx(tx).AdminTalkgroupCalls(ctx, db.AdminTalkgroupCallsParams{SystemID: sys, Talkgroup: tg,
		CursorAt: cursorAt, CursorID: pgtype.UUID{Bytes: cursorID, Valid: true}, RowLimit: int32(limit + 1)})
	if err != nil {
		return 0, nil, dbErr("talkgroup calls", err)
	}
	out := TalkgroupCalls{Calls: make([]AdminCall, 0, min(len(rows), limit))}
	for i, c := range rows {
		if i == limit {
			next := adminCursor(rows[i-1].StartedAt.Time, rows[i-1].ID.Bytes)
			out.Next = &next
			break
		}
		id := uuidString(c.ID)
		call := AdminCall{ID: id, Talkgroup: c.Talkgroup, PatchedTalkgroups: c.PatchedTalkgroups,
			State: string(c.State), StartedAt: c.StartedAt.Time.UTC(), EndedAt: ts(c.EndedAt), InProgress: !c.EndedAt.Valid}
		if call.PatchedTalkgroups == nil {
			call.PatchedTalkgroups = []int32{}
		}
		if c.RecordingSize != nil {
			call.Recording = &AdminRecording{URL: AdminPath + "/calls/" + id + "/recording", Size: *c.RecordingSize}
		}
		out.Calls = append(out.Calls, call)
	}
	return http.StatusOK, out, nil
}

// adminCursor is a page's position: the last call's start time, to the
// microsecond, and its ID, as the recent calls' cursor is (ADR 0013).
func adminCursor(at time.Time, id [16]byte) string {
	var b [24]byte
	binary.BigEndian.PutUint64(b[:8], uint64(at.UnixMicro()))
	copy(b[8:], id[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func parseAdminCursor(s string) (time.Time, [16]byte, bool) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) != 24 {
		return time.Time{}, [16]byte{}, false
	}
	us := int64(binary.BigEndian.Uint64(b[:8]))
	if us < 0 {
		return time.Time{}, [16]byte{}, false
	}
	return time.UnixMicro(us).UTC(), [16]byte(b[8:]), true
}
