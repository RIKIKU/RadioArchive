---
status: proposed
---

# 43. Releases: their files, images and attestations

Date: 2026-11-26

RadioArchive is released the way PagerArchive is (its ADRs 0016 and 0024): deliberate versions worked out from what changed, weekly and on security fixes, every check run on the commit first, and every file and image attested (`SPEC.md` R85-R87). These are the implementer's choices where R85-R87 leave room (spec "How to use this spec", item 6). Built in [Releases: the workflow, the assets, the images and attestations](https://github.com/RIKIKU/RadioArchive/issues/152).

## Summary

| Part | Choice |
|---|---|
| **When** | `release.yml`: weekly (Friday 18:17 UTC), but only while the repository variable `RELEASES_SCHEDULED` is `true`, which the owner sets when releases should start; "Run workflow" by hand; and `security-release.yml`, 30 minutes after a `security` pull request merges |
| **Dry run** | A manual run is a dry run unless unticked. It works out the version, runs every check, builds every binary and image, and keeps the release's files as the run's artifact. Nothing is tagged, pushed, published or attested. Off main, only a dry run may run |
| **The version** | `scripts/ci/release-version.sh`, PagerArchive's rules: nothing to release if only documentation changed; minor if a file a deployment downloads changed (`deploy/`), a migration was added, or a pull request labelled `feature` merged; otherwise patch; never major. With no `v*` tag yet, the first release is **0.1.0** |
| **Where it lives** | Only in the tag. The binaries are stamped at build (`-X .../internal/version.Version`, the Dockerfiles' `VERSION` argument); the repository's files say `0.1.0-dev` and name our images `:main` |
| **Images** | `ghcr.io/rikiku/radioarchive` (amd64), `ghcr.io/rikiku/radioarchive-client` and `ghcr.io/rikiku/radioarchive-recorder` (amd64 and arm64, each built natively), tagged by the exact version and by major.minor. Nothing is pushed to `latest` or `main` |
| **Files** | `compose.yaml`, `env.example`, `radioarchive-config.tar.gz` (deploy/: Authentik, the backup and upgrade scripts, PostgreSQL's init script, the monitoring files), `recorder-compose.yaml`, `recorder-env.example`, `libradioarchive-{amd64,arm64}.so`, `radioarchive-client-{amd64,arm64}`, `SHA256SUMS`, and the PowerShell module's zip |
| **Attestations** | `actions/attest-build-provenance` on every file and on each image's manifest digest, pushed to GHCR beside it: `gh attestation verify <file> --repo RIKIKU/RadioArchive`, or `oci://<image>` |
| **The two compose files** | The root `compose.yaml` stays the development and test stack (it builds the image and has Mailpit). The release's is `deploy/release/compose.yaml`: the same services, the images by major.minor, no published database port, mail required, binds as `API_BIND`/`AUTHENTIK_BIND`. `release-assets.sh` stamps the major.minor and refuses a file still naming `:main` |
| **Renovate** | `.github/renovate.json5`, after PagerArchive's: digests pinned, base images held 3 days, vulnerability fixes at once with the `security` label. It runs once the owner turns Renovate on for the repository |
| **The module** | A zip on the release. The PowerShell Gallery would need an API key secret, which the owner holds |

## Why the schedule waits for a switch

The owner wants to see M6 on cld01 before anything is released or deployed. A merged `release.yml` with a live schedule would release on the first Friday after the merge. The schedule is in the file, and a scheduled run does nothing until `RELEASES_SCHEDULED` is `true`; nor does a security merge start a release until then. Manual runs are dry runs unless unticked, and a pull request's run is always a dry run.

## Why the recorder side is built per architecture

The plugin is compiled against trunk-recorder's own image, which is slow under emulation, and CI already builds it natively on amd64 and arm64 runners. Each architecture's job builds the plugin's `.so`, the client's binary, and both images, pushed by digest. Publishing then tags each image as a manifest list of the two digests.

## How each choice is verified

- `scripts/ci/release-version-checks.sh` (in the workflow's plan job, and on cld01): with no tag, the first release 0.1.0; the latest release's own commit, nothing; documentation only, nothing; code, a patch; `deploy/`, a migration, or a `feature` pull request, a minor version; `--dev`.
- `release-assets.sh` stamps every image of ours by major.minor and fails on one left `:main`. `deploy/compose_test.go` checks both compose files' config labels.
- A dry run of `release.yml` on the branch, which builds everything and publishes nothing. Its files are what the section 9 walk-through installs from (#155).
