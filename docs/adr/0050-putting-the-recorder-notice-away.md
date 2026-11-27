---
status: proposed
---

# 50. Putting the recorder notice away

Date: 2026-11-27

The owner: "Users should be able to dismiss the 'Some talkgroups are not being received right now.' as it gets in the way of other things on the page", and "It should also automatically dismiss after 10 seconds." Until now the recorder notice (R15, ADR 0010) stayed above the dock for as long as the outage lasted, which can be hours, over whatever the page had at its foot (ADR 0012: "a notice's look, with no timing").

This amends ADR 0012's recorder-notice row and `SPEC.md` R15 and A27. The server is unchanged: the feed still sends one fixed message, on or off, to every listener (ADR 0010), and the notice still names nothing.

## Decision

| Part | Choice |
|---|---|
| **Close button** | A close (×) button at the end of the notice's line, `--target` square, ArchiveUx's `close` icon with a "Dismiss" tooltip, and named "Dismiss: Some talkgroups aren't being received right now." It is the page's second Tab stop, after Skip to content, since the frame puts its notices there. Escape closes the notice too while the pointer is on it or the focus is in it |
| **The focus** | Closing it moves the focus to the page's content (`main`, focusable for that moment only), so the next Tab goes on into the page rather than back to the top |
| **By itself** | It goes after **10 s** on screen. Hovering, touching or focusing it holds the count, and leaving starts a whole 10 s again (WCAG 2.2.1). A countdown line along its foot shows the time left and waits with it, as ArchiveUx's timed notice's does. Going by itself counts as putting it away |
| **Announced** | Once, as it appears: the frame's notice place is a `role="status"` region (polite, not atomic, so one notice coming doesn't read the others again). It never takes the focus (WCAG 4.1.3) |
| **Motion** | It doesn't fade or slide, so reduced motion takes nothing away. The countdown line stays, as ArchiveUx's does, since it shows the time left |
| **How long** | Put away, it stays away for the **browser session** (`sessionStorage`, `radioarchive.recorder-notice-dismissed`): on every page, through a reload and on a new socket, until the outage is over. Storage switched off keeps it away for the page's life |
| **A new outage** | Shows it again. The outage is over once the server says the notice is off: it sends the notice's off, or a socket's `listening` comes without the notice's on since its welcome (the server sends a notice that's on straight after the welcome, before anything the set brings, ADR 0007 and 0010). A welcome's own reset of the notices is not an off: it means only that the socket is new |
| **Other notices** | "The radio is full" and too many sockets from here (R71; ADR 0037, 0039) keep `.feed-notice` with no close button and no timing. Nothing plays until the feed gets in, they say why and that it's trying again, and they go as soon as it does |
| **Room** | While a notice shows, the page keeps room at its foot as tall as the notice, so the end of the page can always be scrolled out from under it, on a phone (375 px) as on a desktop. The notice stays above the dock and the bottom bar, as `.notice-place` puts it |

## Telling one outage from the next

The feed carries no outage of its own, only on and off, and the browser can't see the recorders. So "the same outage" is: the browser has seen no off since the listener put it away. The rule needs nothing more than what the feed already sends, and it is right whenever the browser is listening when the outage ends.

It is wrong in one case: an outage that ends and a new one that starts while the browser hears nothing of either (not listening, closed, or its socket down the whole time). The second outage is then taken for the first, and stays away until the session ends. Both last at least `RECORDER_NOTICE_AFTER` (2 min), so the browser has to miss two whole state changes. A server-side outage number would close that gap, but it is a new field in a message ADR 0010 keeps constant, for a case this narrow. If it matters later, the notice can carry `since` without naming anything.

## Where it still shows

Once put away, nothing on the listener's pages says that some talkgroups aren't being received until the next outage. R15 keeps the listener's side to the one notice, naming nothing, so there's no other place for it: an admin sees it on the Recorders page as before. A reload or another tab doesn't bring it back in the same session; a new session does.

## ArchiveUx

ArchiveUx 0.6.0's notice rules (section 4, "Timing") describe the release notice: 8 s, closed by a tap anywhere on it, and no close button. The owner asked for a close button and 10 s, so this notice differs from that rule in both; it keeps the rest (the look, the placement, the hold on hover and focus, the countdown line, Escape, never taking the focus). ArchiveUx has no notice component to reuse, so `TimedNotice` lives in the app (`web/src/lib/live/TimedNotice.svelte`), from ArchiveUx's tokens, its `close` icon and its `.has-tip`, `.icon-button` and `.ghost` classes. If PagerArchive's notices take a close button too, the component can move to ArchiveUx then.

## Consequences

- The A27 browser tests look for the notice first, before it can go by itself.
- `FeedHandlers.noticeOff` is new: the feed says when the server has said a notice is off, apart from `notice(kind, false)`, which a welcome also calls.
- Checked by `web/tests/feed-notice.spec.ts`, against a feed of the test's own: closing, staying away across pages, a new socket and a reload, coming back after an off (sent, or found on a new socket), the keyboard, the 10 s and its hold under Playwright's clock, axe in both looks and themes at 375 px and 1,280 px, and the room at the page's foot.
