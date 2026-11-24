---
status: accepted (2026-11-25, by the owner)
---

# 20. Sharing a conversation: its link and its page

Date: 2026-11-25

Share on a conversation in the basket copies `/conversation#<packed>`: the conversation's name, note and call IDs in order, packed as ADR 0015 packs a zip's list, in the URL's fragment. The browser never sends a fragment, so the server sees only `/conversation`, serves the app's own page for it, and keeps nothing (R66). The page reads the fragment, asks the call details for the calls, and shows the name, the note and the calls that are still available as call cards in order, with **Play all**. These are the implementer's choices where `SPEC.md` P1, P4, R33, R61, R66, R67 and R74 leave room (spec "How to use this spec", item 6), with the owner's answer to #75's open question 2 (2026-11-25): the link carries the name and the note. Built in [Sharing a conversation](https://github.com/RIKIKU/RadioArchive/issues/82).

## Summary

| Part | Choice |
|---|---|
| **The link** | `/conversation#<packed>`: ADR 0015's packing of one conversation (name, note, calls in order), no loose calls, part 0, base64url without padding. About 22.7 characters a call (16 bytes of ID and 1 of place; 18 bytes past 128 calls) |
| **Where the list goes** | In the fragment. Never sent to the server, so it isn't in the reverse proxy's access log, the server can't keep it, and nginx's 8 KB request line doesn't bound it |
| **The server** | Nothing new: `/conversation` is the app shell's page, the same bytes whatever the link holds, `200`. No route, no page route (ADR 0017), no migration |
| **Length** | Share keeps the link, path and fragment, within ADR 0015's **7,680** characters (`MAX_LINK`). 325 calls fit with a short name and no note |
| **Past the budget** | The note is cut at a character, ending "…", to the longest that fits, and the basket says the shared link carries the note's start. If the calls and the name alone don't fit (over about 300 calls), Share isn't offered, and the basket says the conversation is too long to share in one link |
| **What's packed** | The conversation's calls in its order, less any the basket already knows are unavailable |
| **Opening it** | `web/src/routes/conversation/+page.svelte`: the fragment read strictly (`readConversation`); the calls asked of the call details (`POST /api/v1/calls`, R67), 500 at a time; the name (`h1`), the note, "*N* calls, in order", **Play all**, and a card for each available call in the link's order |
| **The card** | `web/src/lib/call/CallCard.svelte`: the call link's card (ADR 0017) made smaller: talkgroup, group, start in TZ, duration, units heard, Play, Download, Share, Add to basket, and **Open the call** (its own page, where Listen live is for a call in progress) |
| **Play all** | The available recorded calls, in the link's order, one after another in the dock (ADR 0018's `replayAll`), "Structure fire: call 2 of 3" |
| **Unavailable calls** | Left out of the call details' answer by the gate (hidden, ignored, patched with either, within their delay, deleted, never stored, not a call ID), so dropped without a word: the page is exactly the page of the link without them |
| **None left** | "This conversation isn't available", with links to Recent calls and Listen: one page for every reason, a link that isn't one included, showing nothing of the link's name or note |
| **Name and note** | Shown as text only (Svelte's text, never `{@html}`), `dir="auto"`, line breaks kept; the page's title is the name, as text |
| **The preview** | The site's generic one: the app's own page. A preview fetcher never sees the fragment, so no name, note or call reaches a preview (R74) |

## Why the fragment

R66 packs the calls "into the URL" and stores nothing. A path (`/conversation/<packed>`) or a query (`?c=`) would do too, and ADR 0008 chose a query for the listening set's link because the server sends the same page whatever the query and logs no request's address. A conversation's link differs in what it carries: the listener's own words. #75's open question 2 pointed out that a name and note in the link sit in the reverse proxy's access log, and a deployment's proxy logs every request line by default. In the fragment they never leave the browser, so the owner's yes to name and note costs nothing at the proxy.

It also means:

- **No route of the server's depends on the link.** The page is the app shell's, so there's no page route to gate and nothing for a preview to say. The server never reads the packing, so a malformed link can't reach it.
- **No 8 KB limit.** nginx's `large_client_header_buffers` bounds the request line, which a fragment isn't in. The link still keeps to ADR 0015's budget (below).
- **What the server does see is the call details' request**, a POST body of call IDs with no name or note, which is what the basket already sends (ADR 0018).

The cost: a link copied by a tool that drops fragments (few do; messaging apps keep them) opens as "This conversation isn't available".

Considered and not chosen:

- **A page route with a preview** (`og:title` the name). R74 keeps preview tags for call links only, and a preview would put listener-written text in HTML the server writes for anyone who crafts a link: one more place for markup to be escaped correctly, for no gain the spec asks for.
- **A path or query.** The name and note would be in every access log, and a long note would meet nginx's limit.
- **A different packing** (JSON, or IDs alone). ADR 0015's is already written and tested on both sides, bounds every length, and has one form per list. A conversation's link is the zip URL of the same conversation with another prefix.

## The link's length

Each call is 16 bytes of ID plus its place in the conversation (1 byte, 2 past 128 calls): 22.7 characters a call, 24 past 128. R66's "about 11 characters a call" assumed 64-bit IDs; ADR 0015 explains why the 128-bit UUID stays.

The fragment has no hard limit, but a link still has to be pasted into messages and kept in bookmarks, and the zip of the same conversation by GET has ADR 0015's 7,680. So Share keeps the link's path and fragment (not the origin, which the deployment chooses) within the same `MAX_LINK`. With "Structure fire" and no note, 325 calls fit, as in a zip's part.

**Past it.** The note goes first: it's cut at a character (never inside a UTF-8 sequence) and ends "…", the longest cut that fits, and the basket says under the conversation "The note is too long for a link: the shared link carries its start." The name is never cut (it's at most 200 bytes). If the calls and the name alone are too long, Share isn't offered for that conversation, and the basket says it's too long to share in one link. A conversation that long is rare (ADR 0018's whole basket holds 1,000 calls), and splitting it into links "part 1 of 2" would make the person it's sent to open several links for one conversation. Both are open points below.

**Opening a longer link.** The page reads any link that is otherwise one Share makes, however long, since no server refuses it: someone may have written one by hand.

## Reading the link

`readConversation` takes only what Share writes: exactly one conversation, no loose calls, part and parts 0, a name that isn't blank, and the bytes `pack` would write for that list (`unpack` re-packs and compares, as the server does for a zip URL): no padding, no trailing bytes, no longer varint than needed, calls in the packing's order, valid UTF-8, at most 500 calls, a name of 200 bytes and a note of 2,000. Anything else is "This conversation isn't available", the same page as a conversation of deleted calls.

## Hidden calls (P1, R33)

The page knows a call only from the call details, which go through the gate (ADR 0004) and leave out every call it holds back, exactly as they leave out a deleted call or an ID never stored. So a link naming a hidden call renders as the link without it: the same cards, the same count, the same markup. `conversation-live.spec.ts` stores a call on hidden 1003, one on ignored 1004 and two deleted ones, and compares the page's markup for each, and for links of only hidden or only deleted calls. The link itself names the hidden call's ID, which its sender already had: the ID says nothing (R60).

The server's side is the app shell, which mustn't depend on the call a path names (ADR 0017): the A10 harness probes `/conversation` with a conversation naming each unavailable call in a query and in a path below it, and `e2e-a10.sh` checks the image's `/conversation` answers as `/` does, whatever it names of 1003's calls. `cmd/radioarchive/p4_test.go` checks the page keeps nothing.

## Names and notes as text

The name and note are the sender's words, but anyone can write a link, so they're untrusted text. The page renders them with Svelte's text interpolation, which escapes them, never with `{@html}`; the title is set as text. `dir="auto"` keeps a right-to-left name from reordering the text around it. A name holding `<img onerror>` or a note holding `<script>` shows as those characters, and nothing in them runs (`conversation.spec.ts`). Links in a note are not made clickable.

## Open points for the owner

1. **Past the budget**: the note is cut short with "…" and the basket says so; a conversation of more than about 300 calls can't be shared in one link. The alternatives are refusing to share a long note instead of cutting it, or links in parts.
2. **The unavailable page names nothing**, not even the link's own name, as "This call isn't available" names no talkgroup. The name came from the link, so showing it would leak nothing, but it would make an unavailable conversation look different by link.
3. **Saving a shared conversation.** The person it's sent to can put each call in their basket (Add to basket on each card), but there's no "save this conversation" that recreates it, name and note, in their basket. Say if you want one.
4. **Calls in progress** show "In progress" with **Open the call**, which has Listen live; the conversation's page itself doesn't offer it. The page asks again every 5 s while a call has no recording, so a call that ends plays there once its recording is in.

## How each choice is verified

| Choice | Verification |
|---|---|
| The link is ADR 0015's packing of one conversation, as `public.ZipURL` writes it; its length per call; 325 calls at the budget and 326 refused; a note cut to fit; markup and any script round-trip as text; anything Share doesn't write is refused | `tests/conversation-rules.spec.ts` |
| Share in the basket copies the link; it opens the name, note and three cards in order, with Play all; the server's answer for `/conversation` is the app's own page | `tests/conversation.spec.ts` |
| Markup in the name and note shows as text, on the page and in the title; nothing runs | `tests/conversation.spec.ts` |
| Every unavailable link (calls never stored, no fragment, not a conversation) is one page naming nothing; a link at the budget opens | `tests/conversation.spec.ts` |
| A hidden or ignored call is dropped as a deleted one is; all hidden is unavailable as all deleted is; Play all plays in the link's order | `tests/conversation-live.spec.ts` (`@live`) |
| The app shell doesn't depend on a conversation's list sent in a query or path | `a10Probes["GET /"]`, `TestIntegrationA10`, `TestIntegrationSignedInSameAnswers`; `e2e-a10.sh` |
| P4: the page keeps nothing | `cmd/radioarchive/p4_test.go` |
| axe in both looks and both themes at 375 and 1,280 px: sharing from the basket, a shared conversation and an unavailable one | `tests/conversation.spec.ts` |

## Consequences

- `ShareButton` copies a link given whole (`href`) as well as a call's.
- `unpack` in `web/src/lib/basket/basket.ts` reads ADR 0015's packing in the browser; the zip's encoder and the conversation's link share it.
- The call link's page (`/calls/<id>`) still has its own card markup. Once #83's unit aliases are in, it can use `CallCard`, so the units heard are shown in one place.
- A deployment that logs request lines logs `/conversation` and the call details' POST, never a conversation's name or note.
