// Package version is the version of the API and the recorder client, which
// are released together, and the rule for which clients a server accepts
// (SPEC.md R6).
package version

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is set at build time with -ldflags "-X .../internal/version.Version=..."
// for a release.
var Version = "0.1.0-dev"

// Semver is a version's major, minor and patch numbers. A pre-release or
// build suffix (-dev, +abc) is ignored: it never changes whether a client
// is accepted.
type Semver struct{ Major, Minor, Patch int }

func (v Semver) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Parse reads MAJOR.MINOR.PATCH, with an optional leading "v" and an
// optional -pre-release or +build suffix.
func Parse(s string) (Semver, error) {
	core := strings.TrimPrefix(s, "v")
	if i := strings.IndexAny(core, "-+"); i >= 0 {
		core = core[:i]
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return Semver{}, fmt.Errorf("version %q is not MAJOR.MINOR.PATCH", s)
	}
	var n [3]int
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 || p == "" || (len(p) > 1 && p[0] == '0') {
			return Semver{}, fmt.Errorf("version %q is not MAJOR.MINOR.PATCH", s)
		}
		n[i] = v
	}
	return Semver{n[0], n[1], n[2]}, nil
}

// Support is how a server treats a recorder client's version.
type Support int

const (
	// Current: the server's minor version or newer.
	Current Support = iota
	// OutOfDate: one minor version behind. Accepted, and recorded as out
	// of date for the Recorders page.
	OutOfDate
	// TooOld: refused with client_too_old.
	TooOld
)

func (s Support) String() string {
	return [...]string{"current", "out_of_date", "too_old"}[s]
}

// Check applies R6: a server accepts recorder clients up to one minor
// version behind its own. Patch versions never matter. A client of an
// older major version is too old; one newer than the server, in any part,
// is accepted, since both ends ignore what they don't know (ADR 0002) and
// the protocol header refuses a real incompatibility. A client whose
// version can't be parsed is too old. server must parse (NewRecorders
// checks it at start).
func Check(client, server string) Support {
	s, err := Parse(server)
	if err != nil {
		return TooOld
	}
	c, err := Parse(client)
	if err != nil {
		return TooOld
	}
	switch {
	case c.Major > s.Major:
		return Current
	case c.Major < s.Major:
		return TooOld
	case c.Minor >= s.Minor:
		return Current
	case c.Minor == s.Minor-1:
		return OutOfDate
	default:
		return TooOld
	}
}
