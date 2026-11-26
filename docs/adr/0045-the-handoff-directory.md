---
status: proposed
---

# 45. The hand-off directory

Date: 2026-11-26

ADR 0002 has trunk-recorder run with `audioArchive: true`, because otherwise it deletes each recording as soon as every plugin's `call_end` returns, before the recorder client can read it. The client then deletes trunk-recorder's WAV once the server has it (issues #31 and #36). Both assume RadioArchive owns the recorder. A recorder that already feeds another site through another uploader plugin may not be changed: turning `audioArchive` on changes that site's behaviour and leaves its files to pile up, and the client deleting trunk-recorder's own file could take it from under the other uploader. This ADR adds an optional plugin setting so RadioArchive can share such a recorder without changing what it does.

## Summary

| Part | Choice |
|---|---|
| **The setting** | `"handoffDir"` in the plugin's entry in trunk-recorder's `plugins`: an absolute path, as trunk-recorder sees it, on the same mount as the recordings. Unset (the default), nothing changes |
| **The hand-off** | In `call_end`, before it returns, the plugin hard-links (`link(2)`) `Call_Data_t.filename` into `handoffDir` as `<shortName>_<boot_id as 8 hex digits>_<n>_<the WAV's own name>`, `n` counting from 0 at each start. An `EEXIST` takes the next `n`, up to three tries |
| **`call_end`'s `filename`** | The link's path. trunk-recorder may then delete its own name; the link keeps the data |
| **The client** | Unchanged. It reads and deletes the path `call_end` names, which is now only its link, so trunk-recorder's file and the other uploader are never touched |
| **A failed link** | `EXDEV`, a missing directory, a permission, anything: a warning at most once a minute, with the count so far, and `filename` is trunk-recorder's own path, as without the setting. Never a copy, a wait, a retry or a non-zero return |
| **Start-up** | The plugin logs whether `handoffDir` is a directory and on the same mount as `captureDir` (compared by `statx`'s mount ID), and doesn't warn about `audioArchive` when `handoffDir` is set. Each `call_end` tries the link anyway, so a directory made later still works |
| **The call JSON** | Not handed off. trunk-recorder keeps or deletes it by `callLog`, and the client never reads it: `call_end` carries everything the recording's metadata needs (ADR 0002) |

## Why a hard link

`call_end` runs in a concluder worker, and every plugin's `call_end` runs in turn in the same thread, so it must be quick. A link is one metadata write, whatever the recording's length, and no audio is read or written. Once it exists, trunk-recorder's `std::remove` of its own name leaves the data in place, so `audioArchive` can stay off and trunk-recorder's own clean-up stays exactly as it was. The other uploader gets the same `Call_Data_t` and sees nothing new.

A hard link can't cross mounts (`EXDEV`), even two bind mounts of one filesystem, so `handoffDir` must be inside the folder or volume that holds the recordings. The client needs only `handoffDir`, mounted at the same path.

## Considered options

| Option | Why not |
|---|---|
| Turn `audioArchive` on | Changes the shared recorder's behaviour, and its files pile up |
| Copy the WAV in `call_end` | Blocks the other plugins for as long as the copy takes, and doubles the writes |
| Read the WAV into memory, or send it over UDP | The plugin does no buffering (ADR 0002), and a call's WAV is far over a datagram |
| Return non-zero so trunk-recorder retries, and keep the file meanwhile | Retries are for failures, logged as errors, and give up on a schedule that isn't ours |
| A symbolic link | Points at the name trunk-recorder deletes |
| Hand off the call JSON too | The client doesn't read it |

## Consequences

- **Links are deleted only by the client.** One whose `call_end` datagram was lost, or written while the client was stopped, stays. `docs/deploy.md` gives a daily `find -mmin +1500 -delete` (just over `QUEUE_MAX_AGE`) for the hand-off folder. The client could sweep it itself later.
- **A link keeps its disk space** until the client deletes it, as an archived WAV would: while the server can't be reached, for up to `QUEUE_MAX_AGE`.
- **A failed link loses that recording** to RadioArchive when `audioArchive` is off, as before this ADR. trunk-recorder and the other uploader are unaffected.
- **Retries.** trunk-recorder calls `call_end` again only for a plugin that failed (`plugin_manager/plugin_manager.cc:187-205`), and this one never fails, so each call is linked once.

## How each choice is verified

- `recorder/plugin/plugintest` runs the real trunk-recorder v5.2.1 image twice against the P25 fixture: once as before (`audioArchive` on, `filename` is trunk-recorder's own), and once with `audioArchive` off and `handoffDir` on the media mount. In the second, every call's `filename` is a link in `handoffDir` with a WAV behind it, each link has one name left (trunk-recorder removed its own; nothing else ran), no WAV is left under trunk-recorder's own folders, its call JSON still matches `call_end`, the log says the plugin is hard-linking and has no `audioArchive` or link warning.
