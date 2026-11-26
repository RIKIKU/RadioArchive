---
status: proposed
---

# 48. The Listen page's tiles, Previous and Catch up

Date: 2026-11-27

The Listen page shows the listening set as **tiles**: a box with how many talkgroups are on, All on, All off and what's on, then a tile per group and per tag, per system. Single talkgroups are found with the search, which is unchanged. In the dock's player bar, **Previous** replaces Play again and goes back through the calls heard. After going back, it plays forward from there until it has caught up, then live listening carries on by itself. **Catch up** replaces the Jump to live button, and shows only when the player is behind. The owner tried these in a proof of concept on the cld01 demo (#167), said "that's a lot better", and approved them in [The Listen page's tiles, Previous and Catch up in the player bar](https://github.com/RIKIKU/RadioArchive/issues/170). That issue quotes the owner's decisions. The rest are the implementer's choices where `SPEC.md` R42, R44 and R45 leave room (spec "How to use this spec", item 6). This amends ADR 0012's controls (Jump to live, Play again) and the dock's layout.

## Summary

| Part | Choice |
|---|---|
| **The page** (R42) | In order: the session's additions (R47), when there are any; **Your listening set** (the box, then the tiles); **Find a talkgroup** (the search); then the talkgroups on, folded away in a `<details>`, "Talkgroups on (N): priority, remove and share". The list inside is drawn only when it's opened, because with every talkgroup on it has over a thousand rows. `/listen-poc` and `?layout=tiles` are gone |
| **The box** | "N of M talkgroups on" (a status, so it's read out as it changes), **All on**, **All off** (each disabled when it would do nothing), and what's on: one chip per group with anything on, "all" or "68/351", with its system when there are several. Empty, it says "Your listening set is empty" and how to start |
| **Tiles** | Per system (a heading when there are several), an **Agencies** row, one tile per group with public talkgroups in that system, plus "Not in a group"; and a **Categories** row, one per tag. A tap switches on all of the tile's talkgroups, as they are now (R42), or, when they're all on already, switches them all off. The state is never colour alone: a mark (empty, half, filled), the count in words ("548 off", "68 of 351 on", "All 183 on") and `aria-pressed` false, mixed or true. A grid of tiles at least two targets wide: 3 across at 375 px, about 8 at 1,280 px |
| **Single talkgroups** | The search, as before: by name, label, ID or tag, a checkbox per result. The groups' browse list under the search is gone, since the tiles do its job. No tile per talkgroup |
| **Speed** | `ListeningState.add` looks talkgroups up by key, so All on with 1,200 talkgroups stays quick. The lookup was quadratic before |
| **The bar's order** (R44, R45) | Pause or Resume, **Previous**, Skip, Hold, Stop, then Share and Add to basket |
| **Names** | Beside each icon when the whole row fits in one line, else a short name under each icon, with the row shared equally. The bar measures its own width with a container query, so a phone, a narrow window and a desktop beside the side menu all get one row. Stop shows "Stop"; its accessible name stays "Stop listening" |
| **Phones** | Share and Add to basket go in an **Actions** menu (ArchiveUx `MoreActions`). It opens upwards because the dock is at the foot of the screen, and it's named "Actions", not "More", so it isn't taken for the bottom bar's More. What they did ("Link copied") shows by the queue count. A replay's own controls are laid out the same way: Stop, then Actions |
| **Share and Add to basket's words** (desktop) | "Link copied" and "Added to the basket" show in a small label above the button, so the row never grows or wraps while they show. This fixes the POC's Add to basket wrapping onto a second line at 1,280 px |
| **Catch up** (R44, Jump to live in ADR 0012) | Words by the queue count, "2 queued · Catch up", only when behind: calls are queued, or the call playing is further behind the radio than ArchiveUx's `LIVE_WITHIN` (5 s) and isn't a delayed talkgroup's. Its accessible name is "Catch up to live". It does what Jump to live did, and also leaves Previous's calls |
| **Previous** (R44, replaces Play again) | The skip icon, mirrored. Each press goes one call further back through the **history**: the last 50 calls that started playing, in the order they played. With a call playing, the first press goes to the call before it, and the call playing is never restarted. With nothing playing, it goes to the last call heard. Disabled when there's nothing earlier |
| **Quick presses** | Presses less than 400 ms apart are counted, not acted on. The queue line says "Back 3 calls…" while they're counted, and the audio changes once, after the last press |
| **Playing forward** | From the call it went back to, every call in the history after it plays, in order. Then the calls that queued meanwhile play, as the queue orders them (priority first, then oldest first, R43). Then live listening carries on by itself. What Previous plays goes first of all (queue.ts's `first`), so no live call cuts in. Skip moves to the next call in the history. Catch up is the only shortcut back to live |
| **With Hold on** | Previous goes back, and plays forward, through the held talkgroup's calls only |
| **A call still on air** | One that Previous reaches while it's still on air plays from its start, from the frames heard, and carries on live behind the radio. A queued call that's still live at its turn does the same (ADR 0012). See below |
| **A call that has ended** | Plays from its recording. If the recording isn't in after 5 tries a second apart, it plays from the frames heard instead |
| **Withdrawn** (R31) | A withdrawn call leaves the history too. If Previous has queued it, or is playing it, it's taken out or stopped at once, and Previous moves on |
| **Stop** | The history is kept for the next Start listening. A call still on air when listening stopped counts as ended, since nothing more of it will come |
| **Announcements** | `radioarchive:previous` `{call_id, steps}` when the presses settle, beside ADR 0012's, for the browser tests |

## A call still on air when Previous reaches it

In the POC, Previous played every call from its recording. A call still on air has no recording yet. The POC asked for it 5 times a second apart, then moved on, so the call was skipped without a word.

This happens often. Press Previous during a long call and the call before it plays, then the call you were on comes round again, usually still on air. Waiting for its recording would leave silence for as long as the call lasts, and longer if the recorder is slow to upload. So the player now keeps every frame the live feed sends of each call in the history: the frames heard while it was queued, while it played, and after it stopped playing (skipped, or left by Previous) until its `call_end`. A call that's still on air at its turn plays those frames from its start, then carries on live as its frames arrive. That is exactly how ADR 0012 plays a queued call that's still live at its turn. The dock shows how far behind the radio it is, and Catch up brings it to the live edge.

The same frames cover a call that has just ended. If its recording isn't in after the usual 5 tries, the frames play instead, so a call is never skipped just because its recording is slow. Only a call with neither a recording nor any frames (withdrawn, or deleted) is passed over.

The frames cost about 1.5 KB a second of audio, held only while the call is one of the last 50. A busy hour of short calls is well under a megabyte. One long call of 20 minutes is about 2 MB, and it goes when 50 more calls have played.

### Considered options

| Option | Why not |
|---|---|
| **Wait for the recording**, showing "Waiting for the recording" | Silence for as long as the call is still on air, which can be minutes, while live calls pile up behind it |
| **Skip a call still on air**, as the POC did | Previous's job is to play what was missed. A skipped call would be the one the listener most likely wanted |
| **Play it from the live edge** | Cuts off the call's start, usually the part that says what it's about. ADR 0012 rejected this for queued calls for the same reason |
| **Fetch a partial recording from the server** | The server has no recording until `call_end` (R20). Serving the live frames as a file would be a new endpoint for a case the browser can already cover |

## Why Previous plays forward

The owner was explicit: "It should not go back to live right away. It should start playing the recordings from that point onwards till it catches back up to live. Only then should live resume." So Previous works like a music player's back button: once you go back, you hear everything from there. Nothing between the call you went back to and the live edge is skipped. Calls that started while it was playing forward wait in the queue, since nothing is dropped automatically (R43), and play once the history runs out. Catch up is the way out for a listener who has heard enough.

## Why the tiles

R42 asks for browsing by group, switching on a whole group or single talkgroups, and search. A deployment may have over a thousand talkgroups in a handful of groups. A list of groups that each open into hundreds of checkboxes made switching a whole agency or category on and off slow, and it hid what was already on. The tiles make a whole group, or a whole tag, one tap, and show at a glance what's on. Single talkgroups stay with the search, which the owner wanted kept as it was. The Categories row uses the catalogue's tags, the same ones the search already matches.

## How each choice is verified

| Choice | Verification |
|---|---|
| A39: tiles on, partly on and off by mark, words and `aria-pressed`; a tap on a partly-on tile; a tap on an all-on tile; the tag's tile following; what's on; no tile per talkgroup; the search reaching one | `web/tests/tiles.spec.ts`, on a made-up catalogue of two systems and 1,200 talkgroups served in place of the API's; `web/tests/listening-set.spec.ts` on the test fixture, through to the live feed's set |
| A39: All on and All off, and their speed with 1,200 talkgroups | `tiles.spec.ts`, `listening-set.spec.ts` |
| A40: Previous over 3 steps counted as one, the call playing skipped once, every call after it in order, then the queued call, then live by itself, with no live call cutting in; disabled at the first call | `web/tests/previous.spec.ts`, with the fake recorder through the recorder client and the binary |
| A40: a call still on air played again from its start and on live, whole | `previous.spec.ts` |
| A40: with Hold on, Previous passes over another talkgroup's call | `previous.spec.ts` |
| A41: Catch up shown only when behind, leaving Previous's calls and going back to live | `previous.spec.ts`; A8 in `web/tests/queue.spec.ts` |
| The bar: its order and names, one row at 1,280 px beside the side menu and at 1,024 px, Add to basket's words not moving it; on a phone the short names, whole targets and the Actions menu | `previous.spec.ts` |
| A replay's controls on a phone in one row, whole targets | `web/tests/recent-live.spec.ts` |
| A36: axe on the tiles partly on, the search, the talkgroups on opened, the bar on a desktop and on a phone with Actions open, in both looks and themes at 375 and 1,280 px | `tiles.spec.ts`, `web/tests/listen.spec.ts`, `previous.spec.ts`, `queue.spec.ts` |

All of these run in Chromium, Firefox and WebKit.

## Consequences

- ADR 0012's **Jump to live** is now Catch up, shown only when behind. Its **Play again** is gone; Previous does more than it did. queue.ts's `first` now marks Previous's calls.
- The page holds up to 50 calls' frames in memory for Previous.
- Tests and anything else that read the Listen page's set list must open "Talkgroups on" first; the browse list's "Add all of North" is gone.
