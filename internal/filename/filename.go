// Package filename names the files listeners download (SPEC.md R63; ADR
// 0014): a call's file name, the file-safe rule its talkgroup label is
// reduced by, and the Content-Disposition header that carries a name.
//
// It reads no data: callers hand it what the gate gave them. The zip
// (R65) reuses Safe for its files and conversation folders.
package filename

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// MaxSafeBytes is the most bytes Safe keeps of a name, well inside the
// 255-byte limit of common file systems with room for the rest of a call's
// file name.
const MaxSafeBytes = 100

// replacement stands in for each character a file name may not hold.
const replacement = '-'

// Safe reduces s to characters a file name may hold on Linux, macOS and
// Windows (ADR 0014):
//
//  1. Invalid UTF-8, control characters (Unicode Cc), the path separators
//     / and \, and the characters : * ? " < > | each become a hyphen (-).
//  2. Every other kind of white space becomes a space, and runs of spaces
//     become one.
//  3. Dots and spaces are trimmed from both ends (a leading dot hides a
//     file, and Windows drops trailing ones).
//  4. It's cut to MaxSafeBytes at a character boundary, then trimmed again.
//  5. A name Windows keeps for a device (CON, PRN, AUX, NUL, COM1-9,
//     LPT1-9, in any case and before any extension) gets an underscore
//     after that word.
//
// Everything else is kept as it is, non-ASCII letters among it: a file
// name is UTF-8, and Attachment gives browsers an ASCII fallback. The
// result may be empty, for a name with nothing left; the caller decides
// what stands in for it.
func Safe(s string) string {
	var b strings.Builder
	space := false
	for len(s) > 0 {
		r, n := utf8.DecodeRuneInString(s)
		if r == utf8.RuneError && n == 1 {
			r = replacement
		}
		s = s[n:]
		switch {
		case unicode.IsControl(r) || strings.ContainsRune(`/\:*?"<>|`, r):
			r = replacement
		case unicode.IsSpace(r):
			r = ' '
		}
		if r == ' ' {
			if space {
				continue
			}
			space = true
		} else {
			space = false
		}
		b.WriteRune(r)
	}
	out := strings.Trim(b.String(), ". ")
	if len(out) > MaxSafeBytes {
		cut := MaxSafeBytes
		for cut > 0 && !utf8.RuneStart(out[cut]) {
			cut--
		}
		out = strings.Trim(out[:cut], ". ")
	}
	return unreserved(out)
}

// reserved are the device names Windows keeps.
var reserved = map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true}

func init() {
	for i := 1; i <= 9; i++ {
		reserved[fmt.Sprint("COM", i)] = true
		reserved[fmt.Sprint("LPT", i)] = true
	}
}

func unreserved(s string) string {
	stem, rest, dot := strings.Cut(s, ".")
	if !reserved[strings.ToUpper(strings.TrimRight(stem, " "))] {
		return s
	}
	if !dot {
		return stem + "_"
	}
	return stem + "_." + rest
}

// Call is a call's file name (R63):
// <YYYY-MM-DD>_<HHMMSS>_<talkgroup label>_<talkgroup ID>.opus, at the time
// the call started in loc (the deployment's TZ), with the label made safe.
// A label with nothing left once safe is left out, with its underscore:
// <YYYY-MM-DD>_<HHMMSS>_<talkgroup ID>.opus.
func Call(started time.Time, loc *time.Location, label string, talkgroup int32) string {
	at := started.In(loc).Format("2006-01-02_150405")
	if l := Safe(label); l != "" {
		return fmt.Sprintf("%s_%s_%d.opus", at, l, talkgroup)
	}
	return fmt.Sprintf("%s_%d.opus", at, talkgroup)
}

// Attachment is the Content-Disposition value that saves a download as
// name, a name Safe has made safe (RFC 6266). A name that's all printable
// ASCII is sent as filename alone. Any other also has filename*, the name
// in UTF-8 (RFC 8187), which browsers prefer, after a filename with each
// non-ASCII character as a hyphen for those that don't.
func Attachment(name string) string {
	ascii := true
	var fallback strings.Builder
	for _, r := range name {
		switch {
		case r == '"' || r == '\\':
			// Safe never leaves these; never break the quoted string.
			fallback.WriteRune(replacement)
		case r < 0x20 || r >= 0x7f:
			ascii = false
			fallback.WriteRune(replacement)
		default:
			fallback.WriteRune(r)
		}
	}
	v := `attachment; filename="` + fallback.String() + `"`
	if !ascii {
		v += "; filename*=UTF-8''" + extValue(name)
	}
	return v
}

// extValue percent-encodes s as RFC 8187's value-chars: attr-char as it
// is, every other byte as %XX.
func extValue(s string) string {
	const attr = "!#$&+-.^_`|~"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x80 && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte(attr, c) >= 0) {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}
