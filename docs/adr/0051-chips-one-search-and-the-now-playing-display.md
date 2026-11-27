---
status: proposed
---

# 51. Chips, one search and the now-playing display

Date: 2026-11-27

The Listen page's talkgroups are now **chips**: a tap switches one on or off, and a long press or its star flips its priority mark. There is **one search**, under the box and above the tiles. What's playing moves out of the dock into a **now-playing display** at the top of the page, and the dock keeps only the controls. The owner tried all of this in a proof of concept on the cld01 demo (#174) and approved it in [Talkgroup chips with long-press priority, one search, and a fixed now-playing display](https://github.com/RIKIKU/RadioArchive/issues/176), which quotes the owner's decisions. The owner said "The chips are great", and earlier, "this interface is SO much better! I genuinely felt a bit of anxiety with the old one". The rest are the implementer's choices where `SPEC.md` R42 to R45, R112 and R113 leave room (spec "How to use this spec", item 6).

This amends ADR 0048 in three places:

- Its single talkgroups were "the search, as before": a checkbox per result, in a section of their own. They are now chips, from the one search, which has moved.
- Its talkgroups on were folded away in a `<details>`, with a Priority checkbox and Remove per row. They are now chips, grouped and folded per agency.
- Its Catch up showed "only when behind", as words by the queue count. It is now a control that's always in the bar, enabled only when behind. That's the one change here that the owner didn't decide in so many words; see "The dock's height", below.

It also amends ADR 0012's now playing in the dock.

## The owner's decisions

| Part | Decision |
|---|---|
| **Chips** | A tap or click toggles on or off. A 500 ms long press toggles priority. A visible ☆/★ star button does the same, for keyboard, screen-reader and mouse users. No menus anywhere. A chip turned off stays in place until the page is left |
| **Priority** | Kept: "Keep it. We can always change it later". Priority talkgroups show first |
| **The rest** | Grouped by agency, folded by default |
| **Search** | One search box under the summary box, above the tiles, covering number, name, label and tag, with an exact number match first. Results are chips, on or off. The talkgroups on are hidden while it has text: "you'd mainly use the search bar" |
| **The display** | Fixed at the top of the Listen page, in the app's normal look. Status, system · agency, and the call's start time, with no wall clock. The talkgroup's name, large. TG number, unit and elapsed, with the unit's alias if known, otherwise its ID, never both. Queued, priority and hold. Fixed positions, "—" when empty, an ellipsis, tabular figures |
| **Other pages** | A slim fixed line while listening. The dock keeps only the controls |
| **Recent calls heard** | The last 3–4 under the display, a tap replaying one: kept, since the owner didn't object |

## The chips

| Part | Choice |
|---|---|
| **What a chip shows** | A dot (filled on, hollow off), the name, and under it the label, or "TG <number>" when the label is just the name, since many talkgroups share a name. A star at its end. Off, its edge is dashed and it says "Off". The state is never colour alone, and screen readers hear `aria-pressed` rather than the word |
| **Buttons** | The chip is a toggle button (`aria-pressed`). The star is a second toggle button, named "Priority: <name>", a faint ☆ when off. Space and Enter on the chip switch it; on the star they flip priority. Neither opens anything |
| **Long press** | Pointer events. A press starts a 500 ms timer, and a thin bar fills along the chip's foot meanwhile. When it fires: `navigator.vibrate(30)` where there is one (not iOS Safari), and the star pops with a ring. The click that follows is swallowed; a keyboard's click (`detail` 0) never is. The next press starts afresh, so a long press whose click never comes, as some phones send none, can't eat the next tap. Reduced motion shortens the animations to nothing |
| **A press that wanders** | More than 8 px of movement lets the press go, and its click is swallowed too, so a drag that starts on a chip with a mouse switches nothing. A touch that becomes a scroll ends in `pointercancel`, which lets go the same way |
| **Touch** | `touch-action: manipulation` keeps panning and pinch zoom and drops the double-tap zoom's wait. The context menu is prevented on the chip, so Android's long-press menu doesn't open (a mouse's right-click menu is lost there too). Text selection and the iOS callout are off (`user-select`, `-webkit-touch-callout`). Hover shading only under `@media (hover: hover)`, so it doesn't stick after a tap |
| **A long press on a chip that's off** | Switches it on, marked priority, as the star does: marking something priority means you want to hear it |
| **Turned off** | It stays where it was, off, until the page is left (a reload, or another page and back), so a second tap undoes a stray one. One that was priority gets its mark back when tapped on again. A chip whose star is taken off stays in Priority the same way |
| **Layout** | A grid at most `--measure` wide, each column at least `--field-min`: 4 across on a desktop, 2 on a phone, with no wide gap between a name and its star |

## The one search, and the talkgroups on

| Part | Choice |
|---|---|
| **Where** | Under the box (how many are on, All on, All off, what's on) and above the tiles, inside "Your listening set". The tiles take it as their `children`. The "Find a talkgroup" heading and section are gone |
| **What it finds** | Every public talkgroup, on or off, by number, name, label or tag, as the server's search does (ADR 0006), with one rank added before the others: the talkgroup whose number is the query. Then exact matches, word starts and anything containing the query, as before |
| **Results** | Chips, as above. One added for the session (R47) says so under its name |
| **The talkgroups on while it has text** | Hidden (`hidden`), not taken out of the page, so what was tapped off there stays as it was |
| **The talkgroups on** | At the page's foot, "Talkgroups on (N)": **Priority** first, as chips, or "None yet" and how to mark one; then **By agency**, a row per group ("North — 182 on"), with its system's name only when there are several (R15), and "Not in a group" last. Each row is a disclosure button (`aria-expanded`), folded by default, and its chips are drawn only when it's opened: with every talkgroup on there are over a thousand. A talkgroup in several groups is under each. The share link is under them, as before |
| **All off** | The section stays, saying nothing is on, so its last chip can still be tapped back on |

## The now-playing display

| Part | Choice |
|---|---|
| **Where** | In the layout, above the page's own content, so it's the same on every page: the whole display on the Listen page, idle too, and a slim line on every other page while listening. It's a region named "Now playing"; the dock is now the region "Player" (ArchiveUx's default) |
| **The four rows** | Status · system · agency · start time; the LED mark (R22) and the name, at `--text-2xl`, heavy; "TG 1001" · unit · elapsed; queued · priority · hold. The system shows only when there are several (R15) |
| **Fixed** | Each row has a fixed height from tokens, with `overflow: hidden`. Each field sits in a fixed grid column (the status and TG number in `--label-min`), never wraps, and is cut with an ellipsis, with the whole text in a `title`. The start time and elapsed are sized by their widest value ("00:00:00", "00:00") drawn hidden in the same cell, so "—" and a time take the same room. Numbers are tabular |
| **Status** | Idle, Connecting, Waiting, Live, Behind 9 s or Behind 2:05 (short, for its column), Delayed 5 min, Paused. Each has its word and a mark: a filled dot (Live), a hollow dot (Idle, Connecting, Waiting, Behind), two bars (Paused), the warning fill (Delayed). `data-state` on the display carries it for styles and tests |
| **The start time** | When the call was transmitted, HH:MM:SS in the deployment's time zone (`#lib/zone.svelte.ts`: the Listen page's catalogue tells it, and any other page asks the catalogue once). Never the time now, so there's no wall clock. Read out as "Started 14:05:09" |
| **The unit** | A live call's `unit` is `{id, name}`, and the server already puts the unit's alias in `name`, or its ID when it has none. The display shows `name`, so never both. It's "—" until a transmission names one, and changes with each |
| **Between calls** | The last call stays on the display with the status Waiting, and its duration in place of elapsed, as a scanner's does. A withdrawn call (R31) is taken off it |
| **Screen readers** | One visually hidden polite live region says "Now playing <talkgroup>, <group>" when the call changes, and nothing else. The ticking fields aren't in a live region |
| **The slim line** | One line at a fixed height: name, status, queued (not on a phone, where the name needs the room) and start time |
| **Recent calls heard** | While listening, under the display on the Listen page: the last 4 calls heard (3 on a phone), newest first, leaving out the one playing, each its name and start time. A tap plays it again through Previous's history (`Player.playHeard`), so the calls after it play too, then the queue, then live (ADR 0048). Each is named "Play again: <name>, started <time>" |

## The POC's trade-offs

### The display stays in view: sticky, at the frame's own top-bar height

The POC's display scrolled away with the page. It is now `position: sticky` at `top: var(--topbar-height)`. ArchiveUx's frame measures its top bar and sets `--topbar-height` on its root, documented in its design system (the date range's phone panel uses it already). So the display follows the bar's real height, in either look, at any width, with no number of the app's own. `scripts/check-tokens.mjs` now knows what the frame sets on its root (it reads Frame.svelte's `style:--…` directives), as it knows tokens.

The sticky part spans the page's width, into its padding, on `--bg`, so the page doesn't scroll past beside it on a desktop, where the display is only `--measure` wide. It stacks under the top bar (`--layer-header`, inside `main`'s own stacking context). The slim line on other pages is sticky the same way. The recent rows aren't: they scroll away under it, since on a phone the display, the top bar, the dock and the bottom bar already take about a third of the screen.

A sticky panel can hide a focused control that the browser scrolls up to (WCAG 2.4.11). The display sets the page's `scroll-padding-top` to the top bar and itself, measured, so focus and `#` links land below both. It sets it back when it goes.

### The dock's height: Catch up is a control that's always there

In the POC the dock was only the controls, plus a line above them for "N queued · Catch up" while behind, Previous's "Back 3 calls…" and what an Actions menu's action did. The line came and went, so the dock grew and shrank by a whole target's height (Catch up's) while listening, moving the page above it.

Now:
- **Catch up is a control in the bar**, after Skip, with an arrow-to-a-bar icon (Tabler's `arrow-bar-to-right`), "Catch up" under or beside it, and the accessible name "Catch up to live". It is always there and enabled only when behind, as Previous is disabled when there's nothing earlier. How many are queued is on the display.
- **What comes and goes** ("Back 3 calls…", "Link copied") shows in a small label above the controls, over the page, as Share's and Add to basket's own words already did on a desktop (ADR 0048). It's a `role="status"` that's always in the page, so it's announced as it changes.

So the dock's height is fixed while listening, behind or not.

_This contradicts ADR 0048's "Catch up … only when behind", which the owner approved._ It's worth it here because the owner's newer decisions are fixed positions and "the bottom dock keeps only the controls", and a line that appears only when behind is neither. Catch up still does exactly what it did, and is still unusable when there's nothing to catch up to; only its absence became a disabled state. With 7 controls on a phone (Actions holding Share and Add to basket), each is still a whole target at 375 px. With names beside the icons the row is about 68rem wide, so the bar switches to short names under the icons below 72rem (was 64rem).

| Option | Why not |
|---|---|
| **Keep the line, always there** ("No calls queued" when not behind) | The dock would no longer keep only the controls, a phone would lose a target's height for good, and the queue count would be in two places |
| **Catch up appears in the bar only when behind** | Every other control would move sideways or narrow under the thumb as it came and went |
| **Catch up on the display** | The controls stay at the bottom (the owner's decision), and the display's rows are a line high, smaller than a target |

### The Delayed badge isn't clipped

ArchiveUx draws Delayed with the warning fill and `--badge-pad-y` above and below its words, so it's taller than a line of `--text-sm`. The display's first row is now `--leading-tight` × `--text-sm` plus twice `--badge-pad-y`, every time, so the badge fits whole and the row is the same height whatever the status. A14's test checks the badge's box is inside its row, and the display's height, on a real delayed call.

### The recent rows reserve their places while listening, not while idle

While listening, the 4 places (3 on a phone) are always drawn, "—" until filled, so a call coming, or a fifth pushing the oldest out, moves nothing. While idle they aren't drawn at all: Start listening then moves the page below the display down by their height, once, as the result of the listener's own press (a change the listener asked for, which WCAG 2.2's and the Core Web Vitals' layout-shift rules don't count). Reserving them while idle would put 3 to 4 empty rows of dashes on the page of everyone who opens it, always, for the sake of that one move. The display itself is drawn while idle, so it doesn't move anything when listening starts.

### Touch thresholds, kept

`touch-action: manipulation`, 500 ms, 8 px of slop and `pointercancel` are unchanged from the POC, which was tested on the demo, by touch (Chromium's CDP touch) and by mouse. One fix: in the POC a mouse press that wandered more than 8 px still switched the chip when let go over it, since the browser sends the click. Its click is now swallowed as a long press's is.

## Smaller changes

- The time zone: the POC's display fetched the whole catalogue a second time on every page for `time_zone`. `#lib/zone.svelte.ts` holds the zone: the Listen page tells it what its catalogue said, and another page asks once, only when a time is shown.
- `Player.recent` and `Player.playHeard` are documented and kept. `RECENT_SHOWN` (4) is shared by the player and the display.
- The display is a region named "Now playing", and the dock a region named "Player". The browser tests wait for the display's `data-state` ("waiting") where they waited for the dock's old "Listening. Calls play here as they come in."

## How each choice is verified

| Choice | Verification |
|---|---|
| A42: a tap on and off, staying where it was off, gone after a reload | `web/tests/tiles.spec.ts`, on a made-up catalogue of two systems and 1,200 talkgroups |
| A42: a long press by mouse, and by touch (Chromium's own touch input, and touch pointer events in every browser), flipping priority and leaving the chip on; a long press on one that's off; a press that drifts 30 px, short or long, doing nothing; a scroll's `pointercancel`; `touch-action` | `tiles.spec.ts` |
| A42: Space and Enter on the star flip priority; on the chip they switch it | `tiles.spec.ts` |
| A42: the talkgroups on, priority first, then per agency with the system named, folded, drawn only when opened; a star taken off staying in Priority until a reload | `tiles.spec.ts` |
| A42: the search by number first, on or off, under the box and above the tiles, hiding the talkgroups on | `tiles.spec.ts`; `web/tests/listening-set.spec.ts` on the fixture, through to the live feed's set |
| A43: the display's height and every field's place through idle, waiting, live, paused, behind, another call and between calls, at 1,280 and 375 px; the dock's height while listening, Catch up enabled or not | `web/tests/now-playing.spec.ts`, with the fake recorder through the recorder client and the binary |
| A43: the unit's alias, never with its ID; a unit with no alias by its ID | `now-playing.spec.ts`, `web/tests/playback.spec.ts`, `web/tests/queue.spec.ts` |
| A43: the start time, the stored call's, in the deployment's zone, holding while elapsed ticks | `now-playing.spec.ts` |
| A43: sticky under the top bar, and the page's scroll padding | `now-playing.spec.ts` |
| A43: the slim line on Recent calls, at a fixed height, gone when stopped | `now-playing.spec.ts` |
| A43: the recent rows, newest first, nothing moving as calls come; a tap playing one again from its recording, then the one after it; 3 on a phone | `now-playing.spec.ts` |
| The Delayed badge inside its row | A14 in `queue.spec.ts` |
| A36: axe on the display idle, behind and with the recent rows, the slim line, the chips with one off, the search's chips, in both themes (and both looks on the Listen page) at 375 and 1,280 px | `web/tests/listen.spec.ts`, `tiles.spec.ts`, `now-playing.spec.ts`, `queue.spec.ts`, `previous.spec.ts` |
| A8, A40, A41: Catch up disabled, then enabled when behind, in the bar's order | `queue.spec.ts`, `web/tests/previous.spec.ts` |

All of these run in Chromium, Firefox and WebKit, except the CDP touch and A14's minute of waiting, which run in Chromium.

## Consequences

- Tests that read the Listen page's set list now open an agency and read its chips; those that read now playing read the display, not the dock.
- The display takes about 130 px at the top of the Listen page even when idle, in exchange for nothing moving when listening starts.
- On a phone, the top bar, the display, the dock and the bottom bar take about half of a 667 px screen while listening on the Listen page. The recent rows and the page scroll under the display.
- Priority might go later ("We can always change it later"). The star and the long press would go with it; the chips' tap wouldn't change.
