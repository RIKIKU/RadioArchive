package version

import "testing"

func TestParse(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want Semver
		ok   bool
	}{
		{"1.4.0", Semver{1, 4, 0}, true},
		{"v0.12.3", Semver{0, 12, 3}, true},
		{"0.1.0-dev", Semver{0, 1, 0}, true},
		{"1.2.3+abc", Semver{1, 2, 3}, true},
		{"1.2", Semver{}, false},
		{"1.2.3.4", Semver{}, false},
		{"1.02.3", Semver{}, false},
		{"1.-2.3", Semver{}, false},
		{"", Semver{}, false},
		{"e2e", Semver{}, false},
	} {
		got, err := Parse(tt.in)
		if (err == nil) != tt.ok || got != tt.want {
			t.Errorf("Parse(%q) = %v, %v", tt.in, got, err)
		}
	}
}

// TestCheck includes SPEC.md A29's versions: a 1.4 server refuses 1.2 and
// flags 1.3 as out of date.
func TestCheck(t *testing.T) {
	for _, tt := range []struct {
		client, server string
		want           Support
	}{
		{"1.4.0", "1.4.0", Current},
		{"1.4.7", "1.4.0", Current},
		{"1.4.0", "1.4.9", Current},
		{"1.3.0", "1.4.0", OutOfDate},
		{"1.3.9-dev", "1.4.0", OutOfDate},
		{"1.2.0", "1.4.0", TooOld},
		{"1.2.99", "1.4.0", TooOld},
		{"1.5.0", "1.4.0", Current},
		{"2.0.0", "1.4.0", Current},
		{"0.9.0", "1.0.0", TooOld},
		{"0.3.0", "0.4.0", OutOfDate},
		{"0.1.0-dev", "0.1.0-dev", Current},
		{"", "1.4.0", TooOld},
		{"garbage", "1.4.0", TooOld},
	} {
		if got := Check(tt.client, tt.server); got != tt.want {
			t.Errorf("Check(%q, %q) = %v, want %v", tt.client, tt.server, got, tt.want)
		}
	}
}
