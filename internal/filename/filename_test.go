package filename

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The public API may import this package because it reads nothing
// (internal/public/imports_test.go): it uses only these.
func TestImportsReadNothing(t *testing.T) {
	allowed := map[string]bool{"fmt": true, "strings": true, "time": true, "unicode": true, "unicode/utf8": true}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, im := range af.Imports {
			if p, _ := strconv.Unquote(im.Path.Value); !allowed[p] {
				t.Errorf("%s imports %s", f, p)
			}
		}
	}
}

// The file-safe rule (ADR 0014), on one table of labels.
func TestSafe(t *testing.T) {
	for _, c := range []struct{ why, in, want string }{
		{"a plain label", "DISP N", "DISP N"},
		{"path separators", "FG/41\\B", "FG-41-B"},
		{"a path climbing out", "../../etc/passwd", "-..-etc-passwd"},
		{"Windows' reserved characters", `A:B*C?D"E<F>G|H`, "A-B-C-D-E-F-G-H"},
		{"control characters", "A\x00B\x1fC\x7fD\u0085E", "A-B-C-D-E"},
		{"a tab and a newline", "A\tB\nC", "A-B-C"},
		{"other white space", "A   B", "A B"},
		{"runs of spaces", "A    B", "A B"},
		{"leading dots and spaces", " . .hidden", "hidden"},
		{"trailing dots and spaces", "FG 41. . ", "FG 41"},
		{"non-ASCII", "Dispatch Nörd 北", "Dispatch Nörd 北"},
		{"invalid UTF-8", "A\xffB", "A-B"},
		{"an empty label", "", ""},
		{"only dots and spaces", " ... ", ""},
		{"a reserved device name", "con", "con_"},
		{"a reserved name with an extension", "LPT1.txt", "LPT1_.txt"},
		{"a reserved name followed by a space", "NUL .x", "NUL _.x"},
		{"a name that only starts like one", "CONSOLE", "CONSOLE"},
	} {
		if got := Safe(c.in); got != c.want {
			t.Errorf("%s: Safe(%q) = %q, want %q", c.why, c.in, got, c.want)
		}
	}
}

func TestSafeLength(t *testing.T) {
	long := strings.Repeat("é", 80) // 160 bytes
	got := Safe(long)
	if len(got) > MaxSafeBytes || got != strings.Repeat("é", MaxSafeBytes/2) {
		t.Errorf("Safe of 160 bytes = %q (%d bytes)", got, len(got))
	}
	got = Safe("a" + strings.Repeat("é", 49) + ". . x")
	if got != "a"+strings.Repeat("é", 49) {
		t.Errorf("cut at a dot = %q, want it trimmed", got)
	}
}

// Safe is idempotent: a safe name stays as it is.
func TestSafeIdempotent(t *testing.T) {
	for _, s := range []string{"DISP N", " ../a:b ", "con", "A\x00B", strings.Repeat("x. ", 60)} {
		once := Safe(s)
		if twice := Safe(once); twice != once {
			t.Errorf("Safe(Safe(%q)) = %q, Safe once = %q", s, twice, once)
		}
	}
}

func sydney(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Australia/Sydney")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

// A17's name, and the time zone the deployment's TZ gives.
func TestCall(t *testing.T) {
	loc := sydney(t)
	started := time.Date(2026, 3, 4, 5, 6, 7, 890e6, loc).UTC()
	if got, want := Call(started, loc, "DISP N", 1001), "2026-03-04_050607_DISP N_1001.opus"; got != want {
		t.Errorf("Call = %q, want %q", got, want)
	}
	if got, want := Call(started, time.UTC, "DISP N", 1001), "2026-03-03_180607_DISP N_1001.opus"; got != want {
		t.Errorf("in UTC, Call = %q, want %q", got, want)
	}
	if got, want := Call(started, loc, "A/B: ok?", 7), "2026-03-04_050607_A-B- ok-_7.opus"; got != want {
		t.Errorf("Call = %q, want %q", got, want)
	}
	if got, want := Call(started, loc, " .. ", 1001), "2026-03-04_050607_1001.opus"; got != want {
		t.Errorf("no label: Call = %q, want %q", got, want)
	}
}

// Calls across a daylight-saving change are named in local time as TZ
// shows it: in Sydney, 03:00 AEDT on 2026-04-05 becomes 02:00 AEST, so
// 02:30 comes twice, and on 2026-10-04 02:00 AEST becomes 03:00 AEDT.
func TestCallDaylightSaving(t *testing.T) {
	loc := sydney(t)
	for _, c := range []struct {
		utc  string
		want string
	}{
		{"2026-04-04T15:30:00Z", "2026-04-05_023000_DISP N_1001.opus"}, // AEDT, +11
		{"2026-04-04T16:30:00Z", "2026-04-05_023000_DISP N_1001.opus"}, // AEST, +10
		{"2026-04-04T16:59:59Z", "2026-04-05_025959_DISP N_1001.opus"},
		{"2026-10-03T15:59:59Z", "2026-10-04_015959_DISP N_1001.opus"}, // AEST
		{"2026-10-03T16:00:00Z", "2026-10-04_030000_DISP N_1001.opus"}, // AEDT
	} {
		at, err := time.Parse(time.RFC3339, c.utc)
		if err != nil {
			t.Fatal(err)
		}
		if got := Call(at, loc, "DISP N", 1001); got != c.want {
			t.Errorf("%s: %q, want %q", c.utc, got, c.want)
		}
	}
}

func TestAttachment(t *testing.T) {
	for _, c := range []struct{ name, want string }{
		{"2026-03-04_050607_DISP N_1001.opus", `attachment; filename="2026-03-04_050607_DISP N_1001.opus"`},
		{"2026-03-04_050607_Nörd 北_1001.opus",
			`attachment; filename="2026-03-04_050607_N-rd -_1001.opus"; filename*=UTF-8''2026-03-04_050607_N%C3%B6rd%20%E5%8C%97_1001.opus`},
		{"a%b'c;d.opus", `attachment; filename="a%b'c;d.opus"`},
		{`a"b\c`, `attachment; filename="a-b-c"`},
	} {
		if got := Attachment(c.name); got != c.want {
			t.Errorf("Attachment(%q) =\n%s\nwant\n%s", c.name, got, c.want)
		}
	}
}
