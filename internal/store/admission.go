package store

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/RIKIKU/RadioArchive/internal/store/db"
)

// Why a call is refused (SPEC.md R12, R17, R18). They're also the upload's
// "rejected" reasons (ADR 0002).
const (
	NotAttached      = db.RejectionReasonNotAttached
	UnknownTalkgroup = db.RejectionReasonUnknownTalkgroup
	IgnoredTalkgroup = db.RejectionReasonIgnoredTalkgroup
)

// TalkgroupState is public, hidden or ignored (R22, R30). A call has no
// state of its own: it's treated as call_state's (SQL) answer for its patch,
// which CallEffectiveState and CallState return.
type TalkgroupState = db.TalkgroupState

// Talkgroup states, strictest last.
const (
	Public  = db.TalkgroupStatePublic
	Hidden  = db.TalkgroupStateHidden
	Ignored = db.TalkgroupStateIgnored
)

// CallKey is R2's identity of a call.
type CallKey struct {
	RecorderID int64
	SysNum     uint16
	CallNum    int64
	StartTime  int64
}

// HeardCall is what the server hears of a call, from its live start or its
// recording's metadata.
type HeardCall struct {
	CallKey
	ShortName string
	Talkgroup int32
	Patched   []int32
}

// Admission is Admit's decision.
type Admission struct {
	// SystemID is the system the call is filed under, when it's admitted.
	SystemID int64
	// Rejected is why it isn't, or "" when it's admitted.
	Rejected db.RejectionReason
	// Counted reports that this decision counted a newly rejected call
	// (each call is counted once, however often it's heard).
	Counted bool
	// HeardSystem and HeardTalkgroups are the talkgroups this decision
	// hears the call on, for their last heard (ADR 0026): an admitted
	// call's own and its patch's, or, for a call refused because a
	// talkgroup in its patch is ignored, that talkgroup. None when the call
	// was decided before, or refused for any other reason. IDs the system
	// doesn't list may be among them.
	HeardSystem     int64
	HeardTalkgroups []int32
}

// heard is an admission of a call on talkgroup, patched with patched, in
// system.
func heard(system int64, talkgroup int32, patched []int32) Admission {
	return Admission{SystemID: system, HeardSystem: system, HeardTalkgroups: union([]int32{talkgroup}, patched)}
}

// Admit decides whether the server takes a call and under which system. It's
// the one place ingest's rules are applied, for the live call and its
// recording alike, and q must be in a transaction:
//
//  1. A call already refused stays refused, for the same reason.
//  2. A call already stored keeps its system: attachment changes affect only
//     calls heard after them (R12). If its patch has become ignored before
//     its recording arrived, its live row is deleted and it's refused (R18).
//  3. Otherwise its recorder + shortName pair must be attached (R12),
//  4. its talkgroup must be in the system's list, or is created public and
//     unreviewed when the system's auto-create is on (R17, R21),
//  5. and its patch's state must not be ignored (R16, R18).
//
// A refusal is recorded once per call (rejected_calls), for admins and
// metrics.
func Admit(ctx context.Context, q *db.Queries, c HeardCall) (Admission, error) {
	k := c.CallKey
	sysNum := int32(k.SysNum)
	reason, err := q.RejectionFor(ctx, db.RejectionForParams{RecorderID: k.RecorderID, SysNum: sysNum,
		CallNum: k.CallNum, StartTime: k.StartTime})
	if err == nil {
		return Admission{Rejected: reason}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Admission{}, dbErr("rejection", err)
	}
	patched := nonNil(c.Patched)

	reject := func(system *int64, talkgroup int32, why db.RejectionReason) (Admission, error) {
		n, err := q.RecordRejection(ctx, db.RecordRejectionParams{RecorderID: k.RecorderID, SysNum: sysNum,
			CallNum: k.CallNum, StartTime: k.StartTime, ShortName: c.ShortName, SystemID: system,
			Talkgroup: talkgroup, Reason: why})
		if err != nil {
			return Admission{}, dbErr("record rejection", err)
		}
		return Admission{Rejected: why, Counted: n == 1}, nil
	}
	ignored := func(system int64) (Admission, bool, error) {
		state, err := q.CallState(ctx, db.CallStateParams{SystemID: system, Talkgroup: c.Talkgroup, Patched: patched})
		if err != nil {
			return Admission{}, false, dbErr("call state", err)
		}
		if state != Ignored {
			return Admission{}, false, nil
		}
		tg, err := q.IgnoredInPatch(ctx, db.IgnoredInPatchParams{SystemID: system, Talkgroup: c.Talkgroup, Patched: patched})
		if err != nil {
			return Admission{}, false, dbErr("ignored talkgroup", err)
		}
		a, err := reject(&system, tg, IgnoredTalkgroup)
		a.HeardSystem, a.HeardTalkgroups = system, []int32{tg}
		return a, true, err
	}

	row, err := q.CallForAdmission(ctx, db.CallForAdmissionParams{RecorderID: k.RecorderID, SysNum: sysNum,
		CallNum: k.CallNum, StartTime: k.StartTime})
	switch {
	case err == nil:
		if row.Recorded {
			return Admission{SystemID: row.SystemID}, nil
		}
		patched = union(row.PatchedTalkgroups, patched)
		a, refused, err := ignored(row.SystemID)
		if err != nil {
			return Admission{}, err
		}
		if !refused {
			return heard(row.SystemID, c.Talkgroup, patched), nil
		}
		if err := q.DeleteUnrecordedCall(ctx, row.ID); err != nil {
			return Admission{}, dbErr("delete refused call", err)
		}
		return a, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return Admission{}, dbErr("call", err)
	}

	sys, err := q.AttachedSystem(ctx, db.AttachedSystemParams{RecorderID: k.RecorderID, ShortName: c.ShortName})
	if errors.Is(err, pgx.ErrNoRows) {
		return reject(nil, c.Talkgroup, NotAttached)
	}
	if err != nil {
		return Admission{}, dbErr("attachment", err)
	}

	listed, err := q.TalkgroupListed(ctx, db.TalkgroupListedParams{SystemID: sys.ID, TalkgroupID: c.Talkgroup})
	if err != nil {
		return Admission{}, dbErr("talkgroup", err)
	}
	if !listed {
		if !sys.AutoCreate || c.Talkgroup <= 0 {
			return reject(&sys.ID, c.Talkgroup, UnknownTalkgroup)
		}
		if err := q.AutoCreateTalkgroup(ctx, db.AutoCreateTalkgroupParams{SystemID: sys.ID, TalkgroupID: c.Talkgroup}); err != nil {
			return Admission{}, dbErr("auto-create talkgroup", err)
		}
	}

	if a, refused, err := ignored(sys.ID); err != nil || refused {
		return a, err
	}
	return heard(sys.ID, c.Talkgroup, patched), nil
}

func dbErr(what string, err error) error {
	return fmt.Errorf("%s: %s", what, SafeError(err))
}

func union(a, b []int32) []int32 {
	out := slices.Concat(a, b)
	slices.Sort(out)
	return slices.Compact(out)
}

func nonNil(a []int32) []int32 {
	if a == nil {
		return []int32{}
	}
	return a
}
