---
status: proposed
---

# 54. Quiet talkgroups

Date: 2026-11-28

A deployment's catalogue holds about 1,200 talkgroups, but its recorders hear only what their sites carry, so most of the list never comes alive. A **quiet talkgroup** is a public one that hasn't been heard for the quiet window, counting only the time its system was receiving. Listeners' lists leave it out, and its first call brings it back. The owner decided the rules in [issue #188](https://github.com/RIKIKU/RadioArchive/issues/188): a 7-day default window as an admin setting; traffic brings a talkgroup back; exemptions per talkgroup and by tag; the search's rules and the "Include quiet talkgroups" filter; listening sets kept; a recorder outage never makes anything quiet; a new deployment hides nothing for one window; admin pages show everything; every listener alike. This ADR records how it's built, and the choices the issue left to the implementer (spec "How to use this spec", item 6). It's `SPEC.md` R114-R117 and A44-A47.

Quiet is not *hidden*. Hidden is an admin's permanent decision, enforced by the gate (P1). Quiet is worked out, clears by itself, and is only a view: the catalogue still carries a quiet talkgroup, marked, and the live feed doesn't know about it.

## Summary

| Part | Choice |
|---|---|
| **The rule** | A public talkgroup is quiet when its system has been **receiving** for a whole window since the talkgroup was last **heard publicly**, or, never heard publicly, for a whole window at all. Never quiet: a talkgroup with **always show**, one whose tag has it, and a **delayed** one (R23) |
| **Receiving** | A system is receiving while any recorder attached to it is connected, its client's heartbeat is under 30 s old and relays the attached shortName, and, for a trunked system, its decode rate isn't near 0. That's R14's disconnected and deaf signals clear for that system. R14's *quiet* signal (no calls) doesn't count: a quiet radio is still listening |
| **The record** | `system_receiving (system_id, started_at, ended_at)`. Every minute, the API holding a recorder's socket extends the system's span that ended within the last 150 s, or starts a new one. Spans are merged when read, so two API servers or two recorders on one system are counted once. Kept 400 days |
| **The cut-off** | Per system: walk its spans newest first, adding up receiving time, until it reaches the window. The moment where it does is the cut-off. A talkgroup last heard before it is quiet. No cut-off (less than a window received) means nothing of that system is quiet: that's the grace |
| **Heard publicly** | `talkgroup_last_heard.public_heard_at`, moved by ingest with `last_heard_at`, but only when the call's whole patch is public when it's heard. Backfilled from the public calls still kept |
| **The window** | `quiet_window_days` in `settings`, 1 to 90 days, 7 by default. `GET` and `PUT /api/v1/admin/quiet`, audited under the settings' kind (`retention`, thing `quiet`), on the Settings page |
| **Exemptions** | `talkgroups.always_show` and `tags.always_show`, both setup: in the admin API, the editors, the module, the export and the import (as an update field) |
| **The API** | Each catalogue talkgroup has `quiet` (bool) and, when quiet, `not_heard_days`. The catalogue has `quiet_window_days`. `GET /api/v1/catalogue/search` leaves quiet talkgroups out, apart from an exact number, name or label, and takes `include_quiet=true`. The fireground lookup ignores quiet |
| **Coming back** | Ingest tells the gate the talkgroups it heard publicly. If the gate's last read holds any of them quiet, the next request reads again. Otherwise reads are reused for 30 s, so a talkgroup goes quiet within 30 s of its time, and comes back within 30 s on another API server |
| **The web** | The Listen page reads the catalogue again every 3 minutes while it's in view and when it comes back into view, and a call it plays on a quiet talkgroup brings that talkgroup back on the page at once |
| **Migration** | 00030. Nothing is quiet after it until each system has received for one window: there's no record of receiving before it |

## Why only the receiving time counts

The owner's rule is "a talkgroup is only quiet if its system's recorder was up and listening for the window". Counting receiving time, rather than asking whether the recorder was up for the whole of the last 7 calendar days, does what the rule is for: an outage pauses the clock, so the lists never empty during one, and they don't all flip back the moment it ends either. A talkgroup heard 12 days ago while its system was out for 8 of them has 4 days of receiving against it, and stays shown.

The same sum is the new-deployment grace. A system that has received for less than a window has no cut-off, so nothing of it is quiet. It's per system, not per deployment, because a new system with its own new radio needs its own window before its never-heard talkgroups go. A new radio on an existing system needs none: its talkgroups come back as their traffic arrives.

A system with no recorder attached never receives, so none of its talkgroups is ever quiet.

## Why a record of its own

The recorder signals are this process's memory (ADR 0010), and the database keeps only each recorder's last call. Neither says when a system was receiving last Tuesday. Spans are the smallest record that does: one row per stretch between outages, so a few hundred a year. A row per minute would be half a million.

A tick of a minute and a gap of 150 s mean a span can run up to a minute past the end of the receiving, and an outage shorter than the gap isn't seen. Against a window of days, that's nothing. Both errors would make a talkgroup quiet a few minutes early at most, and a missed tick only shortens a span.

## Why "heard publicly"

`last_heard_at` counts calls listeners can't have: a call patched with a hidden talkgroup, and a call refused because its talkgroup is ignored. A quiet talkgroup coming back on one would tell listeners a call happened that the gate keeps from them (P1). So the rule reads `public_heard_at`, which only a call whose whole patch is public moves. A call patched with a hidden talkgroup that's later made public doesn't bring its talkgroups back; that errs towards quiet, which hides nothing.

For the same reason, a delayed talkgroup is never quiet. Its first call would bring it back while the call is in progress, which R23 keeps from the public until the delay has passed after the call ends.

## Why the API keeps quiet talkgroups in the catalogue

The catalogue still lists every public talkgroup, each with `quiet`. A client needs them: the exact-match search, the "Include quiet talkgroups" filter, a listening set naming one, a share link, a fireground link and the now-playing display all need the talkgroup's name. Leaving quiet ones out would mean a second endpoint for each. And hiding nothing is the point: quiet is a view, not the gate.

`not_heard_days` is whole days since the talkgroup was last heard publicly, never less than the window, and the window itself for a talkgroup never heard publicly. It's given only for a quiet talkgroup. The exact time isn't given: rounded days of a talkgroup silent for a week tell no one anything.

## How the lists use it

- **Tiles.** A tile counts and shows only talkgroups that aren't quiet, and a tile whose talkgroups are all quiet isn't drawn. A tap still switches the whole group or tag, quiet ones too, so a talkgroup that comes back is on or off with its agency. All on and All off do the same. The box counts only what's shown.
- **The talkgroups on.** Quiet ones aren't listed. One line says how many are on: "Also on: 12 quiet talkgroups, not heard in 7 days. They play when they're next heard."
- **The one search.** It leaves quiet ones out, apart from one whose number, name or label is the query exactly. "Include quiet talkgroups", under the box and shown only when there are any, puts them back. A quiet chip says "Not heard in N days".
- **Recent calls.** The talkgroup filter's suggestions follow the same rule, with no filter: a quiet one is suggested only for an exact match, and says so.
- **Listening sets** don't change. A quiet talkgroup in one is still sent to the live feed and plays.
- **Admin pages** show every talkgroup. The Talkgroups page badges a quiet one "Quiet", with a "Quiet only" filter, and its editor has Always show. Tags' Always show is in their editor on the Systems page, where tags are edited.

## For the Android app

The catalogue's talkgroups each carry `"quiet": true|false`, and a quiet one `"not_heard_days": N`; the catalogue carries `"quiet_window_days": 7`. The app's browse tabs should leave out talkgroups with `quiet: true`, and the voice search should use `GET /api/v1/catalogue/search`, which already applies the rules. A talkgroup in the app's listening set stays in it, quiet or not.

## How each choice is verified

| Choice | Verification |
|---|---|
| The cut-off: a whole window, the grace, outages pausing the clock, overlapping spans counted once | `TestCutoffOf` (`internal/gate/quiet_test.go`) |
| Quiet or not: heard inside and outside the window, never heard, always show, a delay, a system in its grace; the days | `TestQuietOf` |
| The search's rules | `TestQuietShown`; `TestIntegrationQuietTalkgroups` |
| The window's default and setting, the grace, the outage rule, both exemptions, a delay, the search with `include_quiet`, the fireground lookup, the admin table's badge, a call patched with a hidden talkgroup bringing nothing back, and a public call bringing one back at once | `TestIntegrationQuietTalkgroups` (`internal/server/quiet_test.go`) |
| Receiving: not before a recorder connects, extended while its heartbeat is fresh, not while stale or deaf, a new span after an outage | `TestIntegrationQuietReceiving` |
| The window audited | `TestIntegrationAuditHarness` (`cmd/radioarchive`) |
| Always show round-trips through the export and import | `TestIntegrationImportRoundTrip` |
| The Listen page's tiles, counts, set, search, filter and chips; Recent calls' suggestions; the admin pages; axe at 375 and 1,280 px, light and dark, in 3 browsers | `web/tests/quiet.spec.ts` |

## Consequences

- After the upgrade, nothing is quiet for one window. On a deployment whose recorder has been up all along, that's 7 days.
- An admin who wants a channel always listed marks it Always show, or its tag. A fireground tag is the expected one.
- The catalogue's answer changes as talkgroups go quiet and come back, as well as at each edit. It's encoded again at most every 30 s.
