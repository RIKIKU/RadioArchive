#!/bin/sh
# Works out whether a commit is to be released, and as which version, from
# the previous release (SPEC.md R87; ADR 0043), by PagerArchive's
# deliberate-release rules (its ADR 0024). It changes nothing.
#
#   - No v* tag in the commit's history: the first release, FIRST_VERSION.
#   - Nothing but documentation changed since the previous release, or
#     nothing at all: release=false, "nothing to release".
#   - The next minor version if, since the previous release, a file a
#     deployment downloads changed (CONFIG_PATHS), a migration was added, or
#     a pull request labelled "feature" merged. "why" names each rule that
#     fired, and what it found.
#   - Anything else: the next patch version.
#   - Never a major version: that's the owner's, by hand.
#   - A release on a later commit already has this one: nothing to release.
#   - A v* tag on the commit itself: if it's the Latest release
#     ($RA_LATEST_RELEASE) or older, nothing to release; otherwise the run
#     finishes it, as the same version.
#
# With --dev, the version of a build that isn't a release: the version a
# release of this commit would get, then -dev+<short commit>.
#
# Needs the history and the v* tags (fetch-depth: 0). Prints why, then
# key=value lines, also appended to $GITHUB_OUTPUT when that's set:
#   release=true|false  version=  major_minor=  tag=  previous=  why=
#
# usage: RA_LATEST_RELEASE=<tag> scripts/ci/release-version.sh <commit>
#        scripts/ci/release-version.sh --dev <commit>
set -eu
here=$(cd "$(dirname "$0")" && pwd)
. "$here/versions-lib.sh"

DEV=""
if [ "${1:-}" = --dev ]; then
	DEV=1
	shift
fi
commit=$(git rev-parse "${1:?usage: release-version.sh [--dev] <commit>}^{commit}")
latest=${RA_LATEST_RELEASE:-}
failed=0

if [ -n "$DEV" ]; then
	previous=$(git tag -l 'v*' --sort=-v:refname --merged "$commit" | head -n 1)
else
	previous=$(previous_release "$commit")
fi

joined() {
	awk 'NF { n++; if (n <= 3) s = s (n > 1 ? ", " : "") $0 } END { if (n > 3) s = s " and " (n - 3) " more"; print s }'
}
release=true
if [ -z "$previous" ]; then
	version=$FIRST_VERSION
	why="the first release: there's no v* tag in this commit's history"
else
	prev=${previous#v}
	major=${prev%%.*} rest=${prev#*.}
	minor=${rest%%.*} patch=${rest#*.}
	reasons=""
	because() { reasons=${reasons:+$reasons; }$1; }
	files=$(changed_config "$previous" "$commit" | joined)
	[ -z "$files" ] || because "a file a deployment downloads changed ($files)"
	migrations=$(added_migrations "$previous" "$commit" | joined)
	[ -z "$migrations" ] || because "a migration was added ($migrations)"
	if prs=$(feature_prs "$previous" "$commit"); then
		prs=$(printf '%s\n' "$prs" | joined)
		[ -z "$prs" ] || because "a pull request labelled $FEATURE_LABEL merged ($prs)"
	elif [ -z "$DEV" ]; then
		fail VERSION "Couldn't ask GitHub for the pull requests labelled $FEATURE_LABEL since $previous, so the version can't be worked out. Run it again."
		exit 1
	fi
	if [ -n "$reasons" ]; then
		version=$major.$((minor + 1)).0
		why="a minor version, since $previous: $reasons"
	else
		version=$major.$minor.$((patch + 1))
		why="a patch: since $previous no file a deployment downloads changed, no migration was added and no pull request labelled $FEATURE_LABEL merged"
	fi
fi

if [ -n "$DEV" ]; then
	version=$version-dev+$(git rev-parse --short=7 "$commit")
	why="not a release: ${why#a }, then -dev+<commit>"
else
	later=$(later_release "$commit")
	own=$(own_release "$commit")
	if [ -n "$later" ]; then
		release=false
		why="nothing to release: $later, on a later commit, has everything this one has"
	elif [ -n "$own" ]; then
		version=${own#v}
		if [ -z "$latest" ] || [ "$latest" = "$own" ] || higher "${latest#v}" "$version"; then
			release=false
			why="nothing to release: $own is on this commit, and released"
		else
			why="$own is on this commit, but the Latest release is $latest: finishing $own"
		fi
	elif [ -n "$previous" ] && [ -z "$(code_changes "$previous" "$commit")" ]; then
		release=false
		if git diff --quiet "$previous" "$commit"; then
			why="nothing to release: nothing changed since $previous"
		else
			why="nothing to release: only documentation changed since $previous"
		fi
	fi
fi

echo "$why"
out="release=$release
version=$version
major_minor=$(major_minor "$version")
tag=v$version
previous=$previous
why=$why"
echo "$out"
if [ -n "${GITHUB_OUTPUT:-}" ]; then echo "$out" >>"$GITHUB_OUTPUT"; fi
exit 0
