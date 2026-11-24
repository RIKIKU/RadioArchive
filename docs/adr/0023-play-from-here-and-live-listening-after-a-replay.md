---
status: proposed
---

# 23. Play from here, and live listening after a replay

Date: 2026-11-25

Each row of the recent calls list has **Play from here**. It replays that call, then the calls that started after it, oldest first, whatever order the list is sorted in. It fetches them through ADR 0013's route with the list's filters, a page at a time as the run goes, and stops at the moment it was pressed: what starts after that is live listening's. The dock shows "From here: call 2 of 7" and the playing call's start time. When the run is over, because it ran out, a call couldn't be played or the listener pressed Stop replay, live listening resumes by itself, if the replay was what paused it. The same now holds for one replayed call and a conversation's Play all, which amends ADR 0016. The owner answered #75's question 3 on 2026-11-25: "following" runs oldest first, towards newest, and live listening resumes by itself afterwards. The rest are the implementer's choices where `SPEC.md` R46 leaves room (spec "How to use this spec", item 6). Built in [Play from here on the Recent calls list](https://github.com/RIKIKU/RadioArchive/issues/92).

## Summary

| Part | Choice |
|---|---|
| **Where** | **Play from here** on every row with a recording, first among the row's actions, in `RecentList.svelte`, so the Recent calls page and the call card (ADR 0017) both have it. A call in progress has none (nothing to play yet) |
| **Order** | Oldest first from the call, towards newest (owner). In the default newest-first list that's up the list; under the call card, which is oldest first, down it |
| **Which calls** | The list's search (`runSearchOf`), with `from` the call's `started_at`, `order=oldest`, and `to` the press, or the list's own end if that's sooner, never more than 31 days on (R73). The calls at the call's very start time up to and including it are left out, as are calls in progress (`following`) |
| **Fetching** | ADR 0013's public route only, the page's size (50), with its cursor. The next page is fetched while the last queued call plays, so the run doesn't wait between pages |
| **The dock** | "From here: call 2 of 7 · Wed 25 Nov, 14:03:22". While more pages are left, "of at least 51". The row playing is marked, as for one replay |
| **Live listening** | Paused while the run plays and still queuing (ADR 0016). When the run is over it resumes by itself, if the replay paused it. Paused by the listener first, it stays paused |
| **Stop replay** | Ends the run, and live listening resumes as if it had run out. Resume, Jump to live and Start listening end it too, as before |
| **A call that can't be played** | Ends the run with ADR 0016's message ("This call isn't available any more."); live listening resumes. A page that can't be fetched ends the run after the calls already fetched, with "Couldn't load the calls after that one." |
| **One replay, Play all** | Live listening resumes by itself after them too, on the same rule (ADR 0016 amended) |

## Oldest first, whatever the list shows

"Play from here plays the following calls in order" (R46). The owner chose time order: what happened next. A listener who finds the start of an incident in a newest-first list presses Play from here and hears it unfold. That's up the list, which can look odd beside the rows, so the run never reads the rows. It asks the route again, oldest first from the call, and follows the cursor. The list can be newest first, cut off at a page, or Loaded more, and the run is the same. The dock says where the run is, so the listener isn't left to work it out from the rows.

The route's `from` takes the call's start time to the microsecond, and its order is `(started_at, id)` (ADR 0013). So the first page starts with the calls at exactly that moment, in ID order: those before the call and the call itself are dropped, and the rest are kept. Matching by start time and ID needs no cursor made in the browser, so the cursor stays opaque.

## Up to the press

The run stops at the moment Play from here was pressed. With live listening on, what starts after that queues there (R46: "live calls keep queuing meanwhile") and plays when live listening resumes. A run that also chased newer calls would play them twice, once in the run and again from the queue, and it could go on as long as the radio is busy. The end is never at or before the call's own start, in case the browser's clock is behind the server's.

## Live listening resumes by itself

ADR 0016 left live listening paused after a replay, for fear that what queued meanwhile would start at once. The owner answered that it resumes, so a listener who stepped back into the archive comes back to the radio without another press, and hears what queued meanwhile in order (from recordings where those calls have ended, ADR 0012).

It resumes only what the replay paused. A listener who pressed Pause before replaying paused it themselves, and a replay shouldn't undo that. The player keeps that as one flag: set when a replay pauses live listening, cleared when anything resumes it, and read when the replay says it's over (`Replay.onend`). The replay says so once: when the last call has played, when a call can't be played, or on Stop. It doesn't between a list's calls, nor when a new press replaces what was playing.

## Stop replay resumes live listening

Stop replay could have left live listening paused, as a way to stop everything. It doesn't: the owner wants live listening to carry on after a replay, and Stop replay is how a listener says they've heard enough of the archive. A listener who wants silence has Pause and Stop listening in the dock, beside it.

## The hidden-talkgroup gate

The run fetches only what the list could: ADR 0013's public route, with the list's own filters, and each call's recording from the route's answer. It never names a call ID of its own, so it can't reach anything the list couldn't, and a talkgroup hidden mid-run is simply missing from the next page (R74).

## How each choice is verified

| Choice | Verification |
|---|---|
| The run's search: from the call, oldest first, to the press or the list's end, never over 31 days nor at or before the call; the calls after the call at its start time; calls in progress left out | `web/tests/recent-rules.spec.ts` |
| Play from here in a newest-first list plays the call, then the later ones oldest first, through pages of the route (the test's pages are one call long), with the same filters; the dock shows "call 2 of 3" and the start time; live calls queue meanwhile and play by themselves when the run is over | `web/tests/recent-live.spec.ts` (`@live`), Chromium, Firefox and WebKit |
| One replay: live listening resumes by itself when it ends and on Stop replay; paused by the listener first, it stays paused | `recent-live.spec.ts` (`@live`) |
| axe in both looks and themes at 375 and 1,280 px with a run playing in the dock; the list with its new button | `recent-live.spec.ts`, `recent.spec.ts` |

## Consequences

- `Replay.playRun` takes a first call and a function for the next page. Anything else that plays an open-ended list in time order can use it.
- `Replay.onend` is the one place a replay says it's over. The player's `#pausedForReplay` is the one place it decides whether to resume.
- The conversation's Play all (ADR 0020) and the basket's now resume live listening when they end, too.
