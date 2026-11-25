#!/bin/sh
# The release version rules' own checks (ADR 0043): release-version.sh in a
# made-up repository, with stand-in pull request labels (RA_FEATURE_PRS).
#
#   sh scripts/ci/release-version-checks.sh
set -eu
here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cd "$work"
git init -q -b main .
git config user.email checks@example.test
git config user.name checks
export RA_FEATURE_PRS=""
failed=0

commit() { # <file> <text> [message]
	mkdir -p "$(dirname "$1")"
	printf '%s\n' "$2" >"$1"
	git add -A && git commit -q -m "${3:-change $1}"
}
want() { # <expected release> <expected version> <what>
	out=$(sh "$here/release-version.sh" HEAD)
	r=$(echo "$out" | sed -n 's/^release=//p') v=$(echo "$out" | sed -n 's/^version=//p')
	if [ "$r" = "$1" ] && [ "$v" = "$2" ]; then
		echo "ok: $3 -> release=$r $v"
	else
		echo "FAIL: $3: got release=$r $v, want release=$1 $2" >&2
		echo "$out" >&2
		failed=1
	fi
}

commit README.md "hello" "start"
commit cmd/x.go "package main"
want true 0.1.0 "no tag yet: the first release"
git tag v0.1.0
want false 0.1.0 "the commit is the latest release's"
commit docs/x.md "words"
want false 0.1.1 "only documentation changed"
commit cmd/x.go "package main // fix"
want true 0.1.1 "code changed: a patch"
commit deploy/release/compose.yaml "services: {}"
want true 0.2.0 "a file a deployment downloads changed: a minor version"
git tag v0.2.0
commit internal/store/migrations/00030_x.sql "-- x"
want true 0.3.0 "a migration added: a minor version"
git tag v0.3.0
commit cmd/y.go "package main"
RA_FEATURE_PRS="12 $(git rev-parse HEAD)"
want true 0.4.0 "a pull request labelled feature merged: a minor version"
RA_FEATURE_PRS=""
commit cmd/z.go "package main"
want true 0.3.1 "otherwise a patch"
dev=$(sh "$here/release-version.sh" --dev HEAD | sed -n 's/^version=//p')
case $dev in 0.3.1-dev+*) echo "ok: --dev gives $dev" ;; *) echo "FAIL: --dev gave $dev" >&2; failed=1 ;; esac
exit $failed
