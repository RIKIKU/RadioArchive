package recorderclient

import (
	"testing"
	"time"
)

func TestIsPatchCopy(t *testing.T) {
	now := time.Unix(1764000000, 0)
	active := channelCall{SysNum: 0, ShortName: "ex", Freq: 851037500, TDMASlot: -1, Talkgroup: 1001,
		Patched: []int32{1002}, LastAudio: now.Add(-500 * time.Millisecond)}
	copyOf := func(f func(*channelCall)) channelCall {
		c := channelCall{SysNum: 0, ShortName: "ex", Freq: 851037500, TDMASlot: -1, Talkgroup: 1002}
		if f != nil {
			f(&c)
		}
		return c
	}
	tests := []struct {
		name   string
		active func(*channelCall)
		next   channelCall
		want   bool
	}{
		{"member of the active call's patch", nil, copyOf(nil), true},
		{"active call is in the new call's patch", func(a *channelCall) { a.Patched = nil },
			copyOf(func(c *channelCall) { c.Patched = []int32{1001} }), true},
		{"no shared patch: channel reuse", func(a *channelCall) { a.Patched = nil }, copyOf(nil), false},
		{"other frequency", nil, copyOf(func(c *channelCall) { c.Freq = 851050000 }), false},
		{"other TDMA slot", nil, copyOf(func(c *channelCall) { c.TDMASlot = 1 }), false},
		{"other system number", nil, copyOf(func(c *channelCall) { c.SysNum = 1 }), false},
		{"other shortName", nil, copyOf(func(c *channelCall) { c.ShortName = "other" }), false},
		{"active call quiet for over 2 s", func(a *channelCall) { a.LastAudio = now.Add(-2001 * time.Millisecond) }, copyOf(nil), false},
		{"active call quiet for exactly 2 s", func(a *channelCall) { a.LastAudio = now.Add(-2 * time.Second) }, copyOf(nil), true},
		{"active call has had no audio", func(a *channelCall) { a.LastAudio = time.Time{} }, copyOf(nil), false},
		{"same talkgroup", nil, copyOf(func(c *channelCall) { c.Talkgroup = 1001 }), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := active
			a.Patched = append([]int32(nil), active.Patched...)
			if tt.active != nil {
				tt.active(&a)
			}
			if got := isPatchCopy(a, tt.next, now); got != tt.want {
				t.Errorf("isPatchCopy = %v, want %v", got, tt.want)
			}
		})
	}
}
