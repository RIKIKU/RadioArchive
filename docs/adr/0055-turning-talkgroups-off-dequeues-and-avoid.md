---
status: proposed
---

# 55. Turning talkgroups off dequeues their calls, and Avoid

Date: 2026-11-28

The owner, 2026-11-28: "Turning off a talk group doesn't dequeue those calls. I've tested this by starting to listen to all talk groups, then pausing, then when a sufficient queue has formed, I turned off all talk groups. The queue didn't evict those matching talkgroups from the queue … it is a behavior I'd like to change. Also I'd like to add an 'Avoid' button that allows you to quickly avoid the current talk group that is playing." For Avoid the owner chose the permanent behaviour: it's the same as turning the talkgroup off, with Undo, not a session-only mute.

Until now the live feed stopped sending a talkgroup's calls once it was off, but the player kept what it had already queued, under R43's "nothing is dropped automatically". A listener who paused, built a queue, then turned everything off still heard all of it. This ADR amends ADR 0012's queue and ADR 0048's and 0051's player bar.

## Summary

| Part | Choice |
|---|---|
| **When** | Whenever the set the live feed hears gets smaller: the saved set plus the session's additions (R47), as `sendListeningSet` hands it over. So every path is covered at once: a chip, a tile (agency or category), All off, a search result's chip, a share link or the account's set taking this browser's place (R41), and Avoid |
| **What leaves** | Every queued call none of whose talkgroups (its own and its patch's, R16) is in the new set, by the same rule the server uses to send it (A4; `heardBy` in `queue.ts`). Each is announced `radioarchive:ended` with `how: 'off'` |
| **The call playing** | Stops at once if it's one of them, and the next call starts, as Skip does |
| **What stays** | The calls Previous plays (ADR 0048), from a press or a recent row, and the calls it plays forward through afterwards. The listener asked for those by name; Catch up is still the way to leave them. A replay (R46) is apart from the queue and is untouched |
| **Hold** | Let go if the held talkgroup is no longer in the set: holding a talkgroup that's off would hear nothing |
| **Calls in flight** | A call the feed sent before it heard the new set isn't queued (`#enqueue` checks the set too) |
| **On again** | Brings back nothing. The calls that left are remembered as gone, so when the server sends their `call_start` again on the new set (ADR 0007), the player ignores it. New calls queue as ever |
| **Avoid** | A button in the bar after Hold: one press turns the playing call's talkgroup off in the set (or the session's additions), skips the call, and so dequeues the talkgroup's calls. Disabled while nothing plays. Accessible name "Avoid Dispatch North" |
| **Saved** | Avoid goes through the listening state like a chip's tap: localStorage, and the account when signed in (ADR 0009). It reaches the live feed at once through the set last sent, so it works on the admin pages too, which never send the set themselves |
| **Toast** | "Avoided Dispatch North · Undo" in the frame's notice place above the dock, announced politely by its status role, never taking the focus. 6 s, with a countdown line |
| **Undo** | Turns the talkgroup back on where it was, in the set with its priority mark, or in the session's additions. The calls that left stay gone |
| **The bar** | Short names below 80rem (was 72rem). Share and Add to basket go in Actions when the bar is narrower than 48rem, on a narrow desktop window as on a phone. On a phone Stop listening goes in Actions too, which is always there now |
| **Icon** | Tabler's `ban` (a circle with a slash), in the app's dock icons. ArchiveUx 0.6.0 has no such icon |

## Why the set, not each control

The player already hears every set change: it attaches to the live feed through `attachFeed`, and the Listen page, Recent calls, Basket, a call's page and a conversation each send the set plus the session's additions on every change. Putting the eviction there, beside the feed's own `set` message, means no control has to remember to do it, and a control added later can't forget. The rule is the server's: a call is heard while any talkgroup of its patch is on. An entry without a system names that ID in any system, as `ParseSet` reads it.

One gap is unchanged: the admin pages don't load the catalogue, so they never send the set. The account's set adopted on an admin page reaches the feed, and so the queue, when the listener next opens a listener page, as before this ADR. Avoid doesn't have that gap because it edits the set last sent itself.

## Previous's calls stay

Previous plays calls the listener went back for, then plays forward from there until it has caught up (ADR 0048). If turning a talkgroup off pulled those out, a listener who goes back to hear a call again and then tidies the set would lose what they asked for, and the forward run would skip with no word. They stay, and Catch up leaves them, as before. That includes the call playing when it's one of Previous's: it plays on.

## Avoid

- **Permanent, with Undo**, as the owner chose. Avoid is a quicker way to the same change a chip's tap makes, so there is one meaning of "off", and the account, the share link and the Android app all see the same set.
- **Skipped either way.** When the call is heard only through a patch with a talkgroup that's still on, its own talkgroup isn't in the set. Avoid still skips it, and turns off nothing; Undo then has nothing to restore. That's rare, and the toast still names the talkgroup the listener pressed on.
- **Undo doesn't restore the evicted calls.** They were the listener's to drop. Restoring them would replay a backlog the listener has just said they don't want, out of order with what has queued since, and some would have ended and need their recordings. Undo is for a mistaken press: it turns the talkgroup back on so its next calls are heard.
- **Not for replays.** A replay is a call the listener chose from the archive (R46), and isn't fed by the listening set: turning the set off wouldn't stop a conversation's Play all, and Stop replay already ends it. The replay's row on a phone has room for two controls beside its name. So Avoid acts only on live listening's call. While a replay plays, live listening is paused and Avoid names the paused live call, which is what it would turn off.
- **Placement.** Avoid sits after Hold, the other control about the talkgroup playing, and before Stop.

### The toast's timing

ArchiveUx says a notice that leaves by itself is on screen 8 s (design system, section 4). The owner asked for "about 6 s", so this one stays 6 s; everything else follows ArchiveUx's rules: the countdown line, waiting while touched, hovered or focused (letting go starts the count again, as the recorder notice's does, ADR 0050), a tap anywhere on it closing it, Escape closing it from anywhere, no close button, the notice look with its edge, and the polite announcement. Undo's accessible name is "Undo: turn Dispatch North back on". A new Avoid press replaces the toast.

## The bar's room

Avoid makes nine controls while a call plays. Measured in Chromium:

| Where | The bar's width | What happened with Avoid added |
|---|---|---|
| 1,600 px beside the side menu | 1,265 px | Names beside the icons need about 76rem with Resume: past the old 72rem switch, so the row would have wrapped just above it |
| 1,280 px beside the side menu | 945 px | One row, short names, about 103 px each |
| 1,024 px | 689 px | "Add to basket" wrapped onto two lines at 66 px, so the row was no longer one line |
| 375 px phone | 328 px | Eight controls of at least a 44 px target need 366 px: 38 px over |

So:

- Short names below **80rem** (was 72rem). 1,600 px beside the side menu now has short names; 1,700 px has names beside the icons.
- **Share and Add to basket go in Actions when the bar is narrower than 48rem**, by the bar's own container query rather than the phone's media query, so the narrow desktop window gets the phone's arrangement. Actions opens upwards everywhere, since the dock is always at the foot.
- **On a phone, Stop listening goes in Actions**, after Share link and Add to basket. That leaves seven controls, as before. Stop was chosen because it's pressed once a session, never in a hurry (Pause silences at once), and it's the one control whose mistaken press costs the queue, which is worth a second tap now that Avoid sits beside it. Avoid stays out of the menu because the owner wants it quick; Hold, Catch up and Previous are pressed in the moment too. Actions is now always on a phone, holding just Stop listening while nothing plays, so the bar no longer changes its count of controls between calls.

## Considered options

| Option | Rejected because |
|---|---|
| **Evict in each control** (Tiles, chips, All off, the account) | Five places to remember, and the share link and account paths go through `replace`, not `remove`. The set the feed hears is the one place every path meets |
| **Evict Previous's calls too** | They're the listener's explicit choice; dropping them would cut a forward run short with no word (above) |
| **Keep hold when its talkgroup is off** | Hold would let nothing through and the queue would sit, held back, with no sign why |
| **Avoid for the session only** (a mute) | The owner chose the permanent behaviour |
| **Undo restores the evicted calls** | See Avoid, above |
| **Avoid in Actions on a phone** | Two taps for the control the owner wants quick |
| **Hold or Catch up in Actions on a phone** | Both are pressed in the moment, as Avoid is |
| **8 s toast, per ArchiveUx** | The owner asked for about 6 s |
| **A ban icon added to ArchiveUx** | A release of ArchiveUx and a pin bump for one icon; the app's own icons are the design system's place for an app's controls (ArchiveUx's Icon, `svg`) |

## The behaviour for other clients

The Android app mirrors this, so the rule in words:

1. Whenever the effective set (saved set plus session additions) changes, remove from the queue every call that isn't one Previous queued and none of whose talkgroups (own plus patched) is in the set; an entry without a system matches that ID in any system. Stop the call playing if it's such a call, and move to the next.
2. Let go of hold if its talkgroup isn't in the set.
3. Don't queue a call that arrives for a talkgroup no longer in the set. Remember the evicted calls' IDs as gone, so their later events and frames, including a repeated `call_start` after the talkgroup is turned on again, are ignored.
4. Avoid: turn the playing call's own talkgroup off (out of the set, keeping what it was with its priority mark, or out of the session's additions), send the new set, skip the call if it's still playing, and show "Avoided <name> · Undo" for 6 s. Undo puts back exactly what was removed, and nothing else.

## Consequences

- R43's "nothing is dropped automatically" still holds: nothing leaves the queue unless the listener turns its talkgroup off.
- `radioarchive:ended` has a fourth `how`, `off`, and there's a new `radioarchive:avoided` `{call_id, talkgroup, from}` (`from` is `set`, `session` or null), for the browser tests.
- Tests that press Stop listening at phone width open Actions first (`stopListening` in `web/tests/live.ts`).
- The bar test's names-beside check moves from 1,600 px to 1,700 px.

## How each choice will be verified

| Choice | Verification |
|---|---|
| A queue built while paused, then All off, empties, stops the call playing; All on brings none back | `web/tests/avoid.spec.ts`, with the fake recorder, in 3 browsers |
| One agency's tile off evicts only its calls; the call playing stops and the other talkgroup's plays next | `avoid.spec.ts` |
| Hold let go when its talkgroup is turned off from the search | `avoid.spec.ts` |
| Avoid: disabled while idle, its name, the set saved without it, the call stopped, its queued call gone, the toast and axe in both themes; Undo with the priority mark; the toast leaving by itself after about 6 s | `avoid.spec.ts` |
| Previous's calls stay | `avoid.spec.ts` |
| The rule, systems and patches | `web/tests/queue-rules.spec.ts` |
| The bar: one row at 1,024, 1,280, 1,600 and 1,700 px beside the side menu and at 375 px; Actions holding Share and Add to basket below 48rem, and Stop listening on a phone; whole targets; axe in both looks and themes | `web/tests/previous.spec.ts` |
