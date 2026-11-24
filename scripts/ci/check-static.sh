#!/bin/sh
# A38 (SPEC.md section 8): the server stays a static, cgo-free binary.
#
#   sh scripts/ci/check-static.sh <binary>
#
# Fails unless the Linux binary given was built with CGO_ENABLED=0 (the
# build setting Go records in every binary, read by `go version -m`) and is
# statically linked: no program interpreter (dynamic loader) and no shared
# libraries needed. Either alone could be fooled (a cgo build can be linked
# statically, and a cgo-free one could in principle be linked dynamically),
# so both are checked.
set -eu

bin=${1:?usage: check-static.sh <binary>}
fail=0

if ! info=$(go version -m "$bin"); then
	echo "FAIL: $bin isn't a Go binary" >&2
	exit 1
fi
if ! printf '%s\n' "$info" | grep -Eq '^[[:space:]]+build[[:space:]]+CGO_ENABLED=0$'; then
	echo "FAIL: $bin wasn't built with CGO_ENABLED=0:" >&2
	printf '%s\n' "$info" | grep -E 'CGO_ENABLED' >&2 || echo "  (no CGO_ENABLED build setting)" >&2
	fail=1
fi
# Read first, so a missing readelf or a file that isn't ELF fails the check
# rather than looking like a binary with nothing dynamic in it.
if ! headers=$(readelf -lW "$bin") || ! dynamic=$(readelf -dW "$bin"); then
	echo "FAIL: can't read $bin's ELF headers" >&2
	exit 1
fi
if printf '%s\n' "$headers" | grep -q 'INTERP'; then
	echo "FAIL: $bin asks for a dynamic loader:" >&2
	printf '%s\n' "$headers" | grep -A1 'INTERP' >&2
	fail=1
fi
if printf '%s\n' "$dynamic" | grep -q '(NEEDED)'; then
	echo "FAIL: $bin needs shared libraries:" >&2
	printf '%s\n' "$dynamic" | grep '(NEEDED)' >&2
	fail=1
fi

if [ "$fail" -ne 0 ]; then
	exit 1
fi
echo "ok: $bin is static and cgo-free"
