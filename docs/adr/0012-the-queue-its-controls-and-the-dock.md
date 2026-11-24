---
status: proposed
---

# 12. The queue, its controls and the dock

Date: 2026-11-24

The player (#58's, ADR 0011) grows into the listener's queue. It lives in ArchiveUx 0.3.0's frame **dock**, on every page, from **Start listening** on. It keeps every call it has been sent, queued or playing, in the browser and nowhere else. Priority marks and hold are read only there: the live feed hears the same listening set whatever they are. A call that is still live at its turn plays from its start, from the frames heard while it waited, and goes on live behind the radio. One that has ended by its turn plays from its recording. Pause suspends the page's audio output, so a call playing keeps its place and calls keep queuing. Alert sounds are synthesised in the page. The recorder notice sits above the dock in the frame's notice place. These are the implementer's choices where `SPEC.md` R15, R22, R43-R45, R48 and R81, and section 13 ("the alert sounds … are the implementer's choice"), leave room (spec "How to use this spec", item 6). Built in [Queue, priority, controls and now playing in the dock](https://github.com/RIKIKU/RadioArchive/issues/60).

## Summary

| Part | Choice |
|---|---|
| **Where** | ArchiveUx's `Frame` `dock` snippet, `dockLabel` "Now playing", on every page (the layout's). One `Player` for the page, made by the layout; the Listen page no longer holds it. Start listening is the dock's button |
| **Order** (R43) | `web/src/lib/audio/queue.ts`: a call asked for again (Play again) first; then priority calls; then the rest. Within each, by `started_at`, then by arrival. Asked again each time nothing plays, so a mark changed while calls wait counts at once |
| **Priority** | A call is a priority call if any talkgroup of its patch (its own and `patched_talkgroups`, in its system) is marked priority in the saved set. The session's additions (R47) have no marks |
| **A queued call at its turn** | Still live: its frames, kept since its `call_start`, are decoded at once and it carries on live, behind the radio by what was kept. Ended: its recording plays (`GET /api/v1/calls/{id}/recording`), and the kept frames are dropped. A recording that isn't there after 5 tries a second apart (a call the recorder lost) falls back to the kept frames, if any |
| **Withdrawn** (R31) | `call_withdrawn` takes the call out of the queue, or stops its audio at once if it's playing, and the next call starts |
| **Skip** | Stops the call playing; the next starts. The skipped call's later events and frames are ignored |
| **Jump to live** | Empties the queue. A live call playing behind the radio (by 2 s or more, ArchiveUx's `LIVE_WITHIN`) drops what's scheduled and carries on from the next frame; a recording playing stops. Paused, it resumes |
| **Pause** | `AudioContext.suspend()`. The output's clock stops, so what's scheduled waits and the call playing doesn't end; frames keep arriving and are scheduled after it; calls keep queuing; nothing new starts. Resume is `resume()`, then the queue |
| **Hold** (R44) | Holds the playing call's talkgroup (and system, with several). While on, a call that starts on another talkgroup isn't queued at all (A9), and its later events and frames are ignored. Calls already queued stay, and play once hold is off: nothing is dropped automatically (R43). Shown as "Hold on: Dispatch North only" beside the queue, and on the pressed **Hold** button (`aria-pressed`). Never saved or sent |
| **Behind by N s** (R45) | Live: what's scheduled and not yet heard, less the 150 ms lead. A recording: now less the moment playing (the call's start plus what has played). ArchiveUx's `LiveBadge` draws Live, Behind by N s or Delayed N min |
| **Duration so far** | Live: since the call's `started_at`, and its `duration_ms` once it has ended. A recording: what has played |
| **Alert sounds** (R22, R48) | `beep`, `chime`, `pip`, `tone` (ADR 0006's names), synthesised with Web Audio oscillators (`web/src/lib/audio/alerts.ts`). Played when a call starts (`call_start`, or `call_delayed`) on a talkgroup with an alert, while listening, not paused, with **Alert sounds** on. Not for a call missed while away, nor one hold keeps out. Straight to the speakers, beside the calls |
| **Playback settings** (R48) | **Alert sounds**, a checkbox in the account menu beside the look, signed in or out. Kept in `localStorage["radioarchive.playback"]` and saved to the account with the set (ADR 0009's `alerts`) |
| **Recorder notice** (R15, R81) | The feed's `notice` (ADR 0010) shows "Some talkgroups aren't being received right now." in the frame's `notices` snippet, placed by `.notice-place` above the dock: a notice's look, with no timing, since it stays while it's on. Each welcome starts every notice off (ADR 0007) |
| **Announcements** | `radioarchive:ended` (skipped, cleared, withdrawn) and `radioarchive:alert` join #58's `first-frame` and `played`, for the browser tests |

## The queue lives in the browser

R43 makes falling behind the listener's to manage, and P4 keeps the server from holding anything about a listener. So the queue is the page's: the server sends every call the set names (ADR 0007) and the page orders them. Priority and hold could have been sent to the server, which would then send less. Hold sent as a narrower set would make the server stop a queued call's frames and later events (ADR 0007: "When a set stops naming a call … the client drops the call itself"), so calls queued before hold would be lost, against R43. Kept in the page, they don't change what the feed hears, and A9's "the saved set is unchanged" holds of what's sent too.

Frames of a call that waits are kept in memory, at about 1.5 KB a second of audio. A listener paused for an hour of busy traffic holds a few megabytes. There's no limit, because R43 drops nothing.

## Why a call still live at its turn plays from its start

R43 says a queued call that has ended by its turn plays from its recording. It doesn't say what a call still in progress does. Playing it from its start, from the frames kept, keeps every call whole, as A8 expects for the calls it orders. It then runs behind the radio, which R45 shows as "Behind by N s", and Jump to live brings it back. Starting it at the live edge would cut off its start, which is usually the part that says what the call is about.

## Pause

`AudioContext.suspend()` is the whole of pause. The output's clock stops, so the scheduling that already plays one call after another needs no second mechanism. The call playing keeps its frames, and its end waits for its audio. New calls queue as before, and no call starts while paused. It works the same way in all three browsers. Resume carries on where it stopped, behind the radio by the pause, and then through the queue (R44). An alert isn't played while paused, because it would sound only at resume, long after its call started.

## Alert sounds

Section 13 leaves the sounds to the implementer. Each is a few oscillator notes with a short envelope, made in the page when a call starts: about 0.15-0.5 s, below a call's level. Nothing is fetched or shipped as a file, so there's nothing to license and no real radio audio. The names are ADR 0006's, and a name the page doesn't know plays nothing. They're sent to the speakers beside the calls, not through the meter the tests read, so an alert is never counted as a call's audio.

## The dock's layout

On a desktop, now playing (the LED colour mark, the name and group, the unit, the system when there are several, the badge, the duration) sits beside the queue's count, with the controls in words: Pause or Resume, Skip, Jump to live, Hold, Stop listening, and Play again when nothing plays. On a phone, the dock is across the width above the bottom bar, and the controls are a row of icon buttons of at least `--target`, each with its name read out. Play, pause and skip are ArchiveUx's icons; live, hold, stop and again are the app's own (`web/src/lib/audio/icons.ts`, after Tabler Icons, MIT). Share and Add to basket (R45) are M3's.

## How each choice is verified

| Choice | Verification |
|---|---|
| Order, priority, patches, hold's filter | `web/tests/queue-rules.spec.ts`, on queue.ts alone |
| A8: priority without cutting off, the queued call from its recording, Skip, Jump to live, Pause | `web/tests/queue.spec.ts`, with the fake recorder through the recorder client and the binary, in Chromium, Firefox and WebKit |
| A9: hold, and the saved set and the feed's set unchanged | `queue.spec.ts` |
| A11, browser half: hiding stops the audio and removes the queued call within 1 s | `queue.spec.ts`; the fixture's command hides 1001 in the database as an admin's change would, and the test measures from its commit |
| A14, browser half: queued after its delay, "Delayed 1 min", from its recording | `queue.spec.ts`, with a real 1-minute delay (the binary's clock isn't controllable), in Chromium. The 5-minute case on a controllable clock is the server's (`TestIntegrationLiveA14`) |
| A27, browser half: the notice shows while a recorder is deaf and clears; quiet shows nothing | `queue.spec.ts`; `scripts/ci/e2e-web.sh` sends healthy heartbeats in the background unless held, and runs the server with `RECORDER_NOTICE_AFTER=3s`. The quiet test runs in Chromium |
| Alerts off by default, on in the account menu, played at a call's start | `queue.spec.ts`, by the player's `radioarchive:alert` |
| axe-core with a call playing, calls queued, hold on and the notice, in both looks and themes at 375 and 1,280 px | `queue.spec.ts` |

## Consequences

- The Listen page's own player section is gone; the dock holds it on every page, so a page added later has it with no work.
- The browser tests' job takes longer: the A14 and quiet tests wait a minute each. Its time limits are raised (the job 45 min, the step 40 min, Playwright's 30 min).
- `testing/webfixture` gains `talkgroup <ID> state|delay|alert|led <value>` for the browser tests, and gives Bench a quiet threshold of 1 minute. Both go when the admin API is served (M4).
- R44's "switching away to PagerArchive stops the audio" waits for the combined deployment (M7): the frame's `onswitch` can call `player.stop()`.
