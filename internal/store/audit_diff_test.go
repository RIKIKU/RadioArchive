package store

import (
	"strings"
	"testing"
)

func TestAuditDiff(t *testing.T) {
	type tg struct {
		Label string  `json:"label"`
		State string  `json:"state"`
		Alert *string `json:"alert"`
	}
	red := "red"
	show := func(fs []FieldChange) string {
		var b strings.Builder
		for _, f := range fs {
			b.WriteString(f.Field + ":" + string(f.Before) + ">" + string(f.After) + " ")
		}
		return strings.TrimSpace(b.String())
	}
	for _, c := range []struct {
		name          string
		before, after any
		action, want  string
	}{
		{"created: every field, in order", nil, tg{"DISP N", "public", nil}, Created,
			`label:null>"DISP N" state:null>"public" alert:null>null`},
		{"deleted: every field kept", tg{"DISP N", "hidden", &red}, nil, Deleted,
			`label:"DISP N">null state:"hidden">null alert:"red">null`},
		{"changed: only what changed", tg{"DISP N", "public", nil}, tg{"DISP North", "public", &red}, Changed,
			`label:"DISP N">"DISP North" alert:null>"red"`},
		{"changed nothing", tg{"DISP N", "public", nil}, tg{"DISP N", "public", nil}, Changed, ``},
		{"a field only before", map[string]int{"a": 1, "b": 2}, map[string]int{"a": 1}, Changed, `b:2>null`},
	} {
		action, fs, err := diff(c.before, c.after)
		if err != nil || action != c.action || show(fs) != c.want {
			t.Errorf("%s: %s %q, %v; want %s %q", c.name, action, show(fs), err, c.action, c.want)
		}
	}
	if _, _, err := diff(nil, nil); err == nil {
		t.Error("neither before nor after: no error")
	}
	if _, _, err := diff(nil, "a string"); err == nil {
		t.Error("not an object: no error")
	}
}
