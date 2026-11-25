#!/bin/sh
# Writes the files every release attaches (SPEC.md R85, R86; ADR 0043) to a
# folder, as PagerArchive's scripts/ci/release-assets.sh. They're the
# repository's own, with our images named by the release's major.minor in
# place of :main, so what's attached is what the checks ran.
#
#   compose.yaml                  the server (deploy/release/compose.yaml)
#   env.example                   its settings (GitHub renames a file starting with a dot)
#   radioarchive-config.tar.gz    deploy/, unpacked next to compose.yaml: the
#                                 Authentik blueprints and templates, the backup
#                                 and upgrade scripts, the PostgreSQL init script,
#                                 and the monitoring files for PagerArchive's stack
#   recorder-compose.yaml         the recorder host: trunk-recorder with the plugin
#   recorder-env.example          its settings
#
# The binaries (the plugin's .so, the client, SHA256SUMS) and the module's
# zip are the release workflow's own jobs'.
#
# usage: scripts/ci/release-assets.sh <version> <folder>
#        scripts/ci/release-assets.sh --list
set -eu
ASSETS="compose.yaml env.example radioarchive-config.tar.gz recorder-compose.yaml recorder-env.example"
if [ "${1:-}" = --list ]; then
	echo "$ASSETS" | tr ' ' '\n'
	exit 0
fi
version=${1:?usage: release-assets.sh <version> <folder>}
here=$(cd "$(dirname "$0")" && pwd)
. "$here/versions-lib.sh"
mkdir -p "${2:?usage: release-assets.sh <version> <folder>}"
out=$(cd "$2" && pwd)
cd "$here/../.."
tag=$(major_minor "$version")

# stamp <file> <to>: our images named :<major.minor>, and no image left :main.
stamp() {
	sed "s#\(image: ghcr.io/rikiku/radioarchive[a-z-]*\):main\$#\1:$tag#" "$1" >"$2"
	if grep -q 'ghcr.io/rikiku/radioarchive[a-z-]*:main' "$2"; then
		echo "FAIL: $1 still names an image :main" >&2
		exit 1
	fi
	grep -q "ghcr.io/rikiku/radioarchive[a-z-]*:$tag\$" "$2" || { echo "FAIL: $1 names none of our images" >&2; exit 1; }
}
stamp deploy/release/compose.yaml "$out/compose.yaml"
stamp deploy/release/recorder-compose.yaml "$out/recorder-compose.yaml"
cp deploy/release/env.example "$out/env.example"
cp deploy/release/recorder-env.example "$out/recorder-env.example"
# The same bytes from the same files, whoever builds it.
dirs=""
for d in deploy/authentik deploy/backup deploy/postgres deploy/auto-upgrade deploy/monitoring; do
	[ ! -d "$d" ] || dirs="$dirs $d"
done
# shellcheck disable=SC2086 # the folders, one argument each
tar --sort=name --owner=0 --group=0 --numeric-owner --mtime=@0 --mode=u+rwX,go+rX -cf - $dirs |
	gzip -n >"$out/radioarchive-config.tar.gz"
ls -l "$out"
