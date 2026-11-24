package recorderclient

import (
	"slices"
	"time"
)

// channelCall is what the patch-copy rule looks at in a call.
type channelCall struct {
	SysNum    uint16
	ShortName string
	Freq      uint32
	TDMASlot  int8
	Talkgroup int32
	Patched   []int32   // the other talkgroups in its patch, as far as known
	LastAudio time.Time // zero if no audio yet
}

// patchCopyLive is how recently the active call must have had audio.
const patchCopyLive = 2 * time.Second

// isPatchCopy is the R16 rule of ADR 0002 ("Patched calls"): next, a call
// the client is seeing for the first time at now, is a copy of active when
// they're on the same system, frequency and TDMA slot, active had audio in
// the last 2 s, and either talkgroup is in the other's patch. The active
// call is the first one seen and is kept; the copy is never sent.
//
// The rule is unproven on a real system (ADR 0002, "Confidence"), so it's
// kept here alone, where a change to it is one function and its test.
// Same channel without a shared patch is ordinary channel reuse, not a copy.
func isPatchCopy(active, next channelCall, now time.Time) bool {
	if active.SysNum != next.SysNum || active.ShortName != next.ShortName {
		return false
	}
	if active.Freq != next.Freq || active.TDMASlot != next.TDMASlot {
		return false
	}
	if active.LastAudio.IsZero() || now.Sub(active.LastAudio) > patchCopyLive {
		return false
	}
	return slices.Contains(active.Patched, next.Talkgroup) || slices.Contains(next.Patched, active.Talkgroup)
}
