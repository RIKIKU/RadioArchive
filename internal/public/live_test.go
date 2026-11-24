package public

import (
	"encoding/json"
	"slices"
	"testing"
)

// The listening set message (ADR 0007): a number is a talkgroup in any
// system, an object one system's; anything else is refused.
func TestParseSet(t *testing.T) {
	var m SetMessage
	if err := json.Unmarshal([]byte(`{"type":"set","talkgroups":[1001,{"id":1004},{"system":2,"id":1002},{"id":1003,"system":2}],"last_event_id":7}`), &m); err != nil {
		t.Fatal(err)
	}
	set, err := ParseSet(m)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(set.Any, []int32{1001, 1004}) || !slices.Equal(set.InSystem[2], []int32{1002, 1003}) || len(set.InSystem) != 1 ||
		m.LastEventID == nil || *m.LastEventID != 7 {
		t.Errorf("set = %+v, last event %v", set, m.LastEventID)
	}
	for _, bad := range []string{`["1001"]`, `[1.5]`, `[true]`, `[{}]`, `[{"system":1}]`, `[{"system":1,"id":1,"x":2}]`, `[null]`, `[{"id":"1"}]`} {
		var m SetMessage
		if err := json.Unmarshal([]byte(`{"type":"set","talkgroups":`+bad+`}`), &m); err != nil {
			continue
		}
		if _, err := ParseSet(m); err == nil {
			t.Errorf("%s was taken", bad)
		}
	}
	if s, err := ParseSet(SetMessage{Type: "set"}); err != nil || len(s.Any) != 0 {
		t.Errorf("no talkgroups: %+v %v", s, err)
	}
}
