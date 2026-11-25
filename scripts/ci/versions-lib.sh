# Helpers for the release scripts (ADR 0043), as PagerArchive's
# scripts/ci/versions-lib.sh. Sourced, not run.
#
# A release's version is MAJOR.MINOR.PATCH and lives in its tag, v<version>,
# and nowhere in the repository: the binaries are stamped at build time
# (internal/version), and deploy/release's compose files name our images
# :main, which release-assets.sh replaces with the release's major.minor.

# The first release's version, when there's no v* tag yet (ADR 0043).
FIRST_VERSION=0.1.0

# is_version <text>: MAJOR.MINOR.PATCH, numbers without leading zeros.
is_version() {
	printf '%s\n' "$1" | grep -Eqx '(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)'
}

# major_minor <version>: 1.8 for 1.8.0 and for 1.8.0-dev+3f2c1ab.
major_minor() {
	_v=${1%%-*}
	echo "${_v%.*}"
}

# higher <a> <b>: true if version a is higher than version b.
higher() {
	_a1=${1%%.*} _r=${1#*.}
	_a2=${_r%%.*} _a3=${_r#*.}
	_b1=${2%%.*} _r=${2#*.}
	_b2=${_r%%.*} _b3=${_r#*.}
	[ "$_a1" -ne "$_b1" ] && { [ "$_a1" -gt "$_b1" ]; return; }
	[ "$_a2" -ne "$_b2" ] && { [ "$_a2" -gt "$_b2" ]; return; }
	[ "$_a3" -gt "$_b3" ]
}

# fail <file> <message>: a GitHub error annotation, and the message.
fail() {
	echo "::error file=$1::$2"
	echo "FAIL: $2" >&2
	failed=1
}

# CONFIG_PATHS: what a deployment gets only by downloading a release's files.
CONFIG_PATHS="deploy/release deploy/authentik deploy/backup deploy/postgres deploy/auto-upgrade deploy/monitoring"

# changed_config <from> <to>: those files that changed, one per line.
changed_config() {
	# shellcheck disable=SC2086 # the paths, one argument each
	git diff --name-only "$1" "$2" -- $CONFIG_PATHS
}

MIGRATIONS=internal/store/migrations

# added_migrations <from> <to>: the migrations added since, by file name.
added_migrations() {
	git diff --name-only --diff-filter=A "$1" "$2" -- "$MIGRATIONS" | sed 's#.*/##'
}

# FEATURE_LABEL: the label on a pull request that adds a feature.
FEATURE_LABEL=feature

# feature_prs <from> <to>: the pull requests labelled $FEATURE_LABEL merged
# between them, as #<n> per line. Asks GitHub (gh, with GH_TOKEN); returns
# non-zero if it couldn't. RA_FEATURE_PRS ("<number> <merge commit>" lines)
# stands in for GitHub in the script's own checks.
feature_prs() {
	if [ -n "${RA_FEATURE_PRS+x}" ]; then
		_prs=$RA_FEATURE_PRS
	else
		# shellcheck disable=SC2016 # jq's own syntax
		_prs=$(gh pr list ${GITHUB_REPOSITORY:+--repo "$GITHUB_REPOSITORY"} --state merged --label "$FEATURE_LABEL" \
			--limit 1000 --json number,mergeCommit --jq '.[] | "\(.number) \(.mergeCommit.oid // "")"') || return 1
	fi
	printf '%s\n' "$_prs" | while read -r _n _sha; do
		[ -n "$_sha" ] && git cat-file -e "$_sha^{commit}" 2>/dev/null || continue
		if git merge-base --is-ancestor "$_sha" "$2" && { [ -z "$1" ] || ! git merge-base --is-ancestor "$_sha" "$1"; }; then
			echo "#$_n"
		fi
	done | sort -t'#' -k2 -n
}

# code_changes <from> <to>: the files that differ, other than documentation
# (what CI checks nothing for).
code_changes() {
	git diff --name-only "$1" "$2" | while IFS= read -r _f; do
		case $_f in
		SPEC.md | GLOSSARY.md | docs/* | *.md) ;;
		*) echo "$_f" ;;
		esac
	done
}

# own_release <rev>: the highest v* tag on the commit itself, if any.
own_release() {
	git tag -l 'v*' --sort=-v:refname --points-at "$1" | head -n 1
}

# later_release <rev>: the highest v* tag on a later commit whose history
# holds this one, if any.
later_release() {
	_commit=$(git rev-parse "$1^{commit}")
	git tag -l 'v*' --sort=-v:refname --contains "$_commit" | while read -r _t; do
		[ "$(git rev-parse "$_t^{commit}")" != "$_commit" ] && echo "$_t"
	done | head -n 1
}

# previous_release <rev>: the newest v* tag in the commit's history, other
# than one on the commit itself. Empty if there's none.
previous_release() {
	_commit=$(git rev-parse "$1^{commit}")
	git tag -l 'v*' --sort=-v:refname --merged "$_commit" | while read -r _t; do
		[ "$(git rev-parse "$_t^{commit}")" != "$_commit" ] && echo "$_t"
	done | head -n 1
}
