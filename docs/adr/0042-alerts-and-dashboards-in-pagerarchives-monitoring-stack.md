---
status: proposed
---

# 42. Alerts and dashboards in PagerArchive's monitoring stack

Date: 2026-11-26

RadioArchive ships no monitoring stack of its own. Its scrape, alert rules and dashboards drop into **PagerArchive's** (`SPEC.md` R91), through the include folders PagerArchive's stack has had since its v1.23.0 (#128): `include/alloy.d/`, `include/alerts.d/` and `include/dashboards.d/`. A deployment without PagerArchive downloads that stack from PagerArchive's release. These are the implementer's choices where R91 leaves room (spec "How to use this spec", item 6). Built in [Alerts and dashboards for PagerArchive's monitoring stack](https://github.com/RIKIKU/RadioArchive/issues/151).

## Summary

| Part | Choice |
|---|---|
| **Where the files live** | `deploy/monitoring/` in this repo, in the release's archive (#152): `alloy.d/radioarchive.alloy`, `alerts.d/radioarchive.yaml` and `dashboards.d/RadioArchive/*.json`. Each is copied into the folder of the same name under the stack's `include/` |
| **The scrape** | `prometheus.scrape "radioarchive"`, job `radioarchive`, every 15 s, of `radioarchive:8080`: the alias the API's `compose.override.yaml` gives it on the stack's `monitoring` network (the docs say how) |
| **The token** | Read by `local.file` (`is_secret`) from `radioarchive-metrics-token` beside the snippet: the stack's Alloy has only PagerArchive's settings in its environment, and its include folder is mounted read-only into it. The file holds the token alone, mode 600 |
| **The rules** | One group, **RadioArchive**, in a folder of the same name. UIDs start `ra-`, and every rule carries `app: radioarchive`. Each threshold is the condition's single `params:` value, and each `for:` is commented where R91 sets it. They go to the stack's default notification policy, as PagerArchive's do |
| **The dashboards** | Three, in a Grafana folder **RadioArchive** (the `dashboards.d` subfolder's name), tagged `radioarchive`: recorders; calls and listeners; disk, backups and the server |
| **Queries** | Every query says `job="radioarchive"`, so a companion app's series can never match, and uses only metrics the API exports (ADR 0040), which a test checks |

## The rules (R91)

| Rule | Fires when | For |
|---|---|---|
| Recorder disconnected | a recorder's disconnected signal | 2 min |
| Recorder deaf or down | deaf, by why (no heartbeat, or a decode rate near 0) | 2 min |
| Recorder quiet | the quiet signal, which only trips where an admin set a threshold | at once |
| Calls from an unattached recorder system | the rejected count for a recorder + `shortName` rose in 15 minutes (a pair first seen counts in full) | at once |
| Recordings missing | any (ADR 0040's window) | at once |
| Live delay high | p95 over 2 s | 5 min |
| Recordings disk low | free under 15 % | 5 min |
| Retention shortened | recordings deleted early in the last hour | at once |
| Recordings volume full | the volume's state is full | at once |
| Client queue backed up | a client's oldest queued recording is over an hour old | at once |
| RadioArchive API unreachable | the scrape fails, or there's no scrape at all | 2 min |
| RadioArchive backup failing | `radioarchive_backup_failing` (ADR 0041) | at once |
| The radio is full | every listener socket the radio allows is open | 10 min |

"Upgrade failed" joins them with the automatic upgrade (#153). Overlapping calls and limit hits are on the dashboards only, as R91 says.

## Why the stack's default policy

PagerArchive's operators already chose where alerts go (email, Pushover, a webhook) for its default notification policy. RadioArchive's rules join it, so a combined deployment has one place to set that up. The `app` label lets anyone route RadioArchive's alerts elsewhere with a policy of their own.

## How each choice is verified

- `deploy/monitoring_test.go`: R91's thirteen rules, in order, each with one threshold in `params:`, the right `for:` where R91 sets one, labels and annotations; every query in the rules and dashboards says `job="radioarchive"` and uses only exported metrics; the dashboards' UIDs and tags; the scrape's target, token file and job.
- CI's monitoring job (`scripts/ci/e2e-monitoring.sh`), as the deploy guide sets things up:
  - PagerArchive's stack comes from its v1.23.0 release assets, with RadioArchive's files in its include folders and the API joined to its network by an override;
  - every rule is provisioned and then evaluates without error;
  - Prometheus has the API's metrics, scraped with the token;
  - the three dashboards load, and every panel's query runs;
  - "Recorder disconnected" fires once a recorder's client has been gone for 2 minutes, and clears when it reconnects.
