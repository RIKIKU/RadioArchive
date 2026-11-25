---
status: proposed
---

# 44. Automatic upgrades

Date: 2026-11-26

RadioArchive's optional automatic upgrade is an adapted copy of PagerArchive's (its ADR 0030 and `deploy/auto-upgrade/auto-upgrade.sh`), as `SPEC.md` R87 and R106 say: verify, back up, install, health check, roll back, under one host-wide lock shared with PagerArchive. These are the implementer's choices where R87 leaves room (spec "How to use this spec", item 6). Built in [Automatic upgrades](https://github.com/RIKIKU/RadioArchive/issues/153).

## Summary

| Part | Choice |
|---|---|
| **The script** | `deploy/auto-upgrade/auto-upgrade.sh`, in the release's archive, run on the host hourly from cron or a systemd user timer once `enable` switches it on. Its commands, files and status fields are PagerArchive's |
| **What's upgraded** | The server only: the newest full release in the same major version, not marked failed, whose notes don't start "Upgrade steps". Recorder clients and the plugin stay manual (R86) |
| **Verify** | The release's `compose.yaml` and `radioarchive-config.tar.gz`, and the `radioarchive` image, each checked by `gh attestation verify` with a pinned CLI fetched and checked by checksum: signed by `.github/workflows/release.yml` on main (ADR 0043), on a GitHub-hosted runner, built from the tag's commit |
| **Back up** | The backup service's `now` (ADR 0041), kept aside by hard links in `backups/pre-upgrade/<set>/`, plus copies of `compose.yaml`, `.env`, `compose.override.yaml` and `deploy/`. The last 3 sets are kept |
| **Health** | The API answers `/healthz` as the new version within 10 minutes; every service runs; the image is the verified one; your own `AUTO_UPGRADE_CHECK_URLS` answer; and **the call rule** |
| **The call rule** (R87) | If at least 20 calls started in the 2 hours before the upgrade, a call must start within 30 minutes of the restart. With fewer, the check is skipped and says so: a quiet network is never a reason to roll back |
| **Roll back** | The previous files, the previous images retagged, and the database restored from the pre-upgrade backup when the new version changed it (migrations, Authentik's). The release is then marked failed and isn't tried again until `retry X.Y.Z` |
| **The lock** (R106) | `/run/lock/archive-upgrade.lock` with `flock -n`, held from the check to the end of any rollback. While it's held, the run logs "another upgrade is running" and exits 0. The script backs up and rolls back only RadioArchive's database |
| **For admins** | `AUTO_UPGRADE_STATUS_FILE`, the script's status file, read-only in the API: `GET /api/v1/admin/upgrade`, and while failing an ArchiveUx `Banner` (tone error) on every admin page. Failing means: an upgrade failed and wasn't cleared or upgraded past, or a run stopped part way |
| **Metrics and alert** | `radioarchive_auto_upgrade_failing` and the last check's time; the "RadioArchive upgrade failed" rule joins ADR 0042's |
| **Version** | `radioarchive version` prints the running version, which the script asks the API's container |

## Differences from PagerArchive's

- The call rule replaces PagerArchive's page rule: calls in place of pages, 20 in 2 hours before and one within 30 minutes after, as R87 sets.
- No monitoring stack is upgraded: RadioArchive has none of its own (ADR 0042).
- No client image tag is moved in `compose.override.yaml`: the recorder host is separate and upgraded by hand.
- The attestations are release.yml's, not ci.yml's, since RadioArchive's release attests in its own workflow.

## Listener sockets

The API's restart drops every listener's socket, and the browser reconnects with its last event ID, as after any restart (ADR 0007). Missed events are replayed for 5 minutes; missed audio is played from recordings. Nothing in the upgrade differs from a restart, so nothing new is needed.

## How each choice is verified

- `internal/upgrade`: off; ok; running; failing after a rolled-back failure, a failed rollback, an interrupted or stuck run, each in its own words; ok again once cleared or upgraded past; the metrics.
- CI's `auto-upgrade` job (`scripts/ci/e2e-auto-upgrade.sh`), on an install made from `release-assets.sh`'s files, with a local registry for GHCR, a local web server for GitHub's releases, commits and attestations, and a stand-in CLI:
  - the call rule: under 20 calls before, skipped; with 20 or more, a call after the restart passes and none within the window fails;
  - a run while the host-wide lock is held is skipped, exit 0, and nothing changes;
  - 0.1.0 upgrades to 0.1.1, with a pre-upgrade backup kept and the attestations asked for release.yml's signature;
  - 0.1.2, whose API never answers as 0.1.2, is rolled back to 0.1.1 and marked failed, isn't tried again, and `/metrics` says it failed until `clear`.
- `web/tests/upgrade.spec.ts`: the alert on every admin page and no listener page; axe-core in both looks and both themes at 375 and 1,280 px.
