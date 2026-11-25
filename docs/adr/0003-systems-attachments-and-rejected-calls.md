---
status: accepted (2026-11-24, by the owner)
---

# 3. Systems, attachments and rejected calls

Date: 2026-11-24

The server files each call under a **system** through the **attachment** of its recorder + `shortName` pair, decides once per call whether to take it, and counts every call it refuses once. A call's state is never stored: one SQL function, `call_state`, reads it from the talkgroups of its patch each time it's asked. These are the implementer's choices where `SPEC.md` R11, R12, R16-R18, R21 and R22 leave room (spec "How to use this spec", item 6). Built in [Systems, attachments, unknown and ignored talkgroups, and patches](https://github.com/RIKIKU/RadioArchive/issues/34).

## Summary

| Part | Choice |
|---|---|
| **System** | The admin's own ID (the fixture's `EX` is ID 1), fixed once set and what an import merges on; a unique label (`EX`), a name ("Example network"), and auto-create, off by default |
| **Attachment** | One row per recorder + `shortName`, naming one system. Moving it is an upsert |
| **When a call is decided** | When the server first hears of it, by its live `call_start` or its recording, whichever comes first. The decision is kept: an admitted call keeps its system, a refused one stays refused for the same reason |
| **Order of the rules** | Attached (R12), then a listed talkgroup or auto-create (R17, R21), then the patch's state not ignored (R16, R18). One function, `store.Admit`, for the live call and the upload alike |
| **Counting refusals** | One `rejected_calls` row per call by R2's identity. Counts are grouped per pair (not attached) and per system and talkgroup (unknown, ignored). The live call and its recording, and any retry, count once |
| **Effective state** | `call_state(system, talkgroup, patched)`: the strictest state among the patch's listed talkgroups (the enum's order, `public < hidden < ignored`), public when none is listed |
| **Patch growth** | Live `transmission` and `call_end` add their patched talkgroups to the call's. A patch that becomes ignored refuses the rest of the live audio and then the recording, and the live row is deleted |
| **Admin API** | Under `/api/v1/admin`. Sign-in is an interface the server needs before it will serve the API; the only implementation is in tests, and the binary doesn't mount it |

## Systems

R11 says import "merges on system ID" and the fixture gives `EX` an ID, so the ID is the admin's, as a talkgroup's is. It was an identity column while every call went to one system (issue #32), and migration 00003 drops the identity. The short `label` and the longer `name` follow the talkgroup's pair (R22), and the label is unique because R15 shows it to listeners when there are several systems.

A system that still has talkgroups or calls can't be deleted (R11); the admin API checks both in one transaction and answers `409 system_in_use`. Its attachments and rejected-call counts go with it.

Units (R26) aren't stored yet: ingest doesn't need them, and they arrive with their admin in M4.

## When a call is decided, and why it's kept

R12 says an attachment change "affects only future calls". Three readings were possible:

| Option | Problem |
|---|---|
| By the call's `start_time`, against an attachment history | Compares the recorder's clock with the server's, and a recording queued during an outage (R5) is filed by an attachment that may since have been corrected. It also needs a history table |
| At every message | The live call and its recording could land in different systems, or one be refused and the other stored |
| **When the server first hears of the call** | Chosen. One decision per call, made with what the server knows then |

So a live call admitted under system A keeps A when its recording arrives after the pair is moved to B, and a call refused live is refused again at upload. The same holds for talkgroups: a call refused as unknown stays refused if the admin adds the talkgroup meanwhile, because that call has already been counted.

The one exception is **ignored** (R18: "the server refuses their live frames and recordings"). If an admitted call's patch, or its talkgroup, is ignored by the time its recording arrives, the recording is refused, the live row (which has no recording) is deleted, and the call is counted. A call already stored is never deleted by a state change (R30).

## Rejected calls

Each refused call is one row in `rejected_calls`, keyed by R2's identity, with its reason, its system (none when not attached) and the talkgroup the rejection is about. That's the call's own talkgroup, except when the call is refused because another talkgroup in its patch is ignored: then it's that one, so "TG 1004: 3 calls rejected, ignored" counts every call 1004 kept out.

A row per call, rather than a counter, makes the count exact: the live call and its recording arrive separately, and a client retries an upload whose answer it lost, yet each call counts once. It also lets the upload answer a retry the same way. The rows are small; pruning them with the recordings is the retention job's (issue #37).

The admin API's `GET /api/v1/admin/rejections` gives each count with the wording the Recorders page and the talkgroup admin will show: "Bench / other: 1 call rejected, not attached to a system" (R12) and "TG 4711: 1 call rejected, not in the list" (R17). The server also counts refusals in memory by reason, and refused live frames, for metrics (R90).

## The effective state (R16, R30)

```sql
call_state(p_system bigint, p_talkgroup integer, p_patched integer[]) RETURNS talkgroup_state
```

is the `max` of the states of the call's talkgroup and its patched talkgroups in that system, or `public` if none is listed. `talkgroup_state` is an enum declared `public, hidden, ignored`, so `max` is the strictest: ignored wins over hidden, hidden over public. It's `STABLE` and reads the talkgroups at query time, so hiding a talkgroup hides every call patched with it at once, and making it public shows them again. A patched ID the system doesn't list has no state, so it can't make a call stricter; if the admin later lists it as hidden, the calls patched with it become hidden.

Ingest uses it to refuse ignored calls, and the gate (issue #35) is meant to use it, or the `CallEffectiveState` query, for every public surface. A stored call that `call_state` calls ignored is treated as hidden (R30).

## Admin API and sign-in

| Method and path | What |
|---|---|
| `GET`, `POST /systems`; `GET`, `PATCH`, `DELETE /systems/{id}` | Systems |
| `GET /systems/{id}/talkgroups`; `GET`, `PUT`, `PATCH /systems/{id}/talkgroups/{tg}` | Talkgroups: label, name, tag, state. `PUT` adds or replaces and needs a tag; `PATCH` changes what's given. Either marks the talkgroup reviewed |
| `GET /attachments`; `PUT`, `DELETE /recorders/{name}/attachments/{shortName}` | Attachments, by the recorder's name (admins see it, R10) |
| `GET /rejections[?system=id]` | Rejected-call counts with their wording |

Errors are `{"error": "<stable code>", "message": "…"}` (R78).

> **[ADR 0025](0025-admin-accounts-roles-and-admin-sign-in.md) (proposed, 2026-11-25)** supersedes the rest of this paragraph: admins are accounts in RadioArchive's database, `account.Service` is the admin authenticator, and the binary serves the admin API behind it. The text below is as it was decided.

Admin sign-in is Authentik's (R51, R55) and doesn't exist yet. The handler takes an `AdminAuthenticator`, and `NewAdmin` refuses to build without one. The only implementations are test stand-ins in `_test.go` files, which Go never compiles into a binary, and `cmd/radioarchive` doesn't mount the admin API at all; a test checks that it answers 404. So there's no setting or header that turns the stand-in on in production. The admin API joins the binary with Authentik, in M4. Until then the compose-based end-to-end check puts its fixture into the database with `psql`.

Changes are logged with the admin's name and the IDs involved, never talkgroup or system names (R90). The audit log (R54) comes in M4.

## Tags

R22 gives every talkgroup exactly one tag, required, and tags will carry alert, LED colour and sort order. So there's a `tags` table now, holding only a name, and the API takes and returns the tag by name, creating it on first use. A talkgroup may lack a tag only while it's unreviewed, which only auto-create makes (a table check).

## Consequences

- The recorder client keeps the WAV of a refused recording; it deletes only acknowledged ones (issue #32). For a deployment that records talkgroups it has ignored, the WAVs pile up on the recorder until trunk-recorder's config stops recording them. Whether the client should delete a refused recording's WAV is a question for the owner.
- Heartbeat decode rates (R14) aren't stored yet; they belong with the Recorders page's signals.
- A recorder that's retired can't be attached, and its existing attachments stay (its key no longer works, so they're inert).
