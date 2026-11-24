---
status: accepted (2026-11-24, by the coordinator: the owner delegated protocol choices)
---

# 8. The listening set in the browser, and its share link

Date: 2026-11-24

The listening set and its priority marks live in the browser's `localStorage`; talkgroups added for the session live in its `sessionStorage`. A share link is `/?set=` followed by the set written as decimal talkgroup IDs, separated by commas, with runs of three or more as ranges and `p` after a priority one: `/?set=1001,1002p,1010-1015`. Opening one asks before it replaces the browser's set, and reads only what the public catalogue still holds, so a hidden talkgroup's ID is dropped without a word. A fireground link (`/?fireground=41`) adds its talkgroup to the session's additions. These are the implementer's choices where `SPEC.md` R33, R41, R43, R47, R101 and section 13 ("the listener's share link format … recorded in an ADR") leave room (spec "How to use this spec", item 6). Built in [Listening set: picker, share link and fireground links](https://github.com/RIKIKU/RadioArchive/issues/59). How the set travels on the live feed is ADR 0007's.

## Summary

| Part | Choice |
|---|---|
| **The set** | `localStorage["radioarchive.listening-set"]`: `{"v":1,"talkgroups":[{"id":1001},{"id":1002,"priority":true}]}`. A `system` beside the ID only when the deployment has several systems (R15) |
| **Session additions** | `sessionStorage["radioarchive.session-additions"]`, the same shape with no marks. They last until the tab closes and are never saved (R41, R47). One tap (**Keep**) moves one into the set |
| **Share link** | `/?set=<items>`; `item := [system "."] id ["-" id] ["p"]`, joined by `,`. IDs ascending; three or more consecutive IDs with the same system and mark as a range; `p` marks priority; the system only when there are several |
| **Opening one** | The page loads the public catalogue, keeps the link's talkgroups that are in it, and asks "Open this shared listening set?" with how many talkgroups and priority marks it has. **Replace my listening set** or **Keep mine**; either takes `set` out of the address. Nothing is asked when nothing public is left, or when the link holds the browser's set already |
| **Hidden IDs** | Dropped silently (R33): a hidden or ignored talkgroup isn't in the catalogue, so the link reads exactly as one naming a talkgroup that never existed |
| **Fireground link** | `/?fireground=<number>` asks `GET /api/v1/fireground/{number}` (ADR 0006). Found: added for the session, unless it's in the set already, which is left as it is; then **Start listening**. `404`: "This channel isn't available.", and nothing is added |
| **What the live feed hears** | The set plus the session's additions, the public ones only, handed to one function (`web/src/lib/live/listening-set.ts`) whenever either changes. Priority marks and hold stay in the browser: the player orders its own queue (R43, R44) |
| **Search** | In the browser, over the catalogue it already has, with the server's ranking (ADR 0006): exact, then starts with, then contains |

## Why the browser's storage

P4: the server keeps nothing about anonymous listeners, so the set can only be the browser's. `localStorage` outlives the tab, as a saved set must. `sessionStorage` is per tab and is cleared when the tab closes, which is R47's "until the tab closes" exactly, with no timer and nothing to clean up; a second tab opened on the radio has its own. Signed-in saving (R41's profile, R110) is a later slice; it will read and write the same entries.

The kept form is versioned (`"v":1`). Anything else in the key, written by another version or by hand, reads as an empty set rather than failing the page. A talkgroup that has been hidden since it was saved stays saved: it isn't shown, sent or shared while it isn't public (P1), and it's back if an admin makes it public again. Nothing about it reaches the page, because the page only shows what the catalogue holds.

## The share link's format

R41 asks for a link that copies the set "to another device or a bookmark", so it's text a person can read, bookmark and paste into a message:

- **Decimal IDs**, as the talkgroup is known on the radio and in the catalogue. Commas and `-` are plain characters in a query, never percent-encoded, and survive messaging apps' link detection.
- **Ranges** (`1010-1015`) keep a set of whole blocks short: talkgroups are often numbered in blocks, and about 1,000 IDs written out would be 5 KB. A range is read by testing each catalogue talkgroup against it, never by expanding it, so `1-2147483647` costs no more than the catalogue (1,000 comparisons) and can name only public talkgroups.
- **`p`** marks priority. A letter rather than `*` or `!`, which some chat apps take as formatting.
- **The system** (`2.1001`) only when the catalogue names several systems (R15); with one, the link doesn't name it. An ID without a system names the first talkgroup with that ID in the catalogue's order, and one with a system, opened where there's only one system, names the talkgroup with that ID. The same rule reads a set saved before a second system was added.
- At most 5,000 items are read; anything that isn't an item is skipped.

Considered and not chosen: a base64 packing of the IDs (shorter, but unreadable, and a person can't fix or check a bookmark), the set in the fragment (`#set=…`: never sent to the server, but that gains little here, since the server sends the same page whatever the query and logs no request's address, and a query is what R101's `?fireground=` already uses), and a link stored on the server (P4).

## Hidden talkgroups (R33, P1)

The link is read against the public catalogue only, which never holds a hidden or ignored talkgroup (ADR 0006). So a link naming 1003 opens as if 1003 had never been in it: the same question, with the same count, as a link naming 9999 instead. A link naming only hidden talkgroups asks nothing, as a link naming nothing would. The tests compare the offer's markup for links naming a hidden, an ignored and an unknown talkgroup, and find no ID, name or label of 1003 or 1004 anywhere on the page.

The fireground link relies on the server's one answer (ADR 0006): an unknown number, a hidden talkgroup's and an ignored talkgroup's all get the same `404`, and the page draws the same "This channel isn't available." for each, with no number or name in it. Something that isn't a number at all is answered the same way without asking. The tests compare the page's markup for each.

## Consequences

- The live WebSocket's client (#56's feed, #58's player) calls nothing new: it replaces the body of `sendListeningSet` with the message ADR 0007 defines. The Listen page already calls it whenever the set or the session's additions change.
- The browser tests run against the binary with the test fixture's setup (`testing/webfixture`), because the picker, the share link and fireground links need the catalogue.
- Signed-in saving (#61) adds a second home for the same entries; the share link and the session's additions don't change.
- ADR 0006's LED colour names were aligned with ArchiveUx 0.3.0's (`yellow`, `cyan` rather than `amber`, `teal`) on 2026-11-24, before anything was deployed.
