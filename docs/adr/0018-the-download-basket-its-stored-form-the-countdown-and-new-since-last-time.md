---
status: accepted (2026-11-25, by the owner)
---

# 18. The download basket: its stored form, its place in the profile, the countdown and "new since last time"

Date: 2026-11-25

> **Where the profile lives is superseded by [ADR 0021](0021-the-saved-profile-lives-in-radioarchives-database.md).** The basket is still the profile's `basket` field, in the same form; the profile is now a row of RadioArchive's database rather than ADR 0009's Authentik attribute, and clearing deletes that row.

The download basket keeps call IDs, when each was put in, and the listener's own conversations (a name, an optional note, calls in order), never anything of a call's talkgroup. It lives in the browser's `localStorage`, and in the listener's profile when signed in, in ADR 0009's `radioarchive` attribute, in the same form, going with the listening set as one profile. What the Basket page shows of a call comes from the call details each time. The call details gain `deletes_at`, the moment the nightly retention run will delete the call, so the browser counts down without knowing the setting. "New since last time" is the calls put in a scope since that scope's last download. These are the implementer's choices where `SPEC.md` P1, P4, P5, R41, R45, R46, R64, R65, R67, R84, R88 and R110 leave room (spec "How to use this spec", item 6). Built in [The download basket and its conversations](https://github.com/RIKIKU/RadioArchive/issues/81).

## Summary

| Part | Choice |
|---|---|
| **Stored form** | `{"calls": [{"id", "added"}], "conversations": [{"id", "name", "note"?, "calls": [{"id", "added"}], "last"?}], "last"?}`. `id` is the call's UUID as the API writes it; `added` and `last` are Unix milliseconds. A conversation's `id` is the basket's own (12 random base64url characters), never shown. `web/src/lib/basket/basket.ts` |
| **In the browser** | `localStorage["radioarchive.basket"]`, the form above with `"v": 1`. Another tab's change arrives by the `storage` event. Read forgivingly: an entry that isn't one is left out and the rest kept |
| **In the profile** | The attribute's `basket` field, the same form (`internal/account/profile.go`, `Basket`). A profile saved before M3 has none |
| **How large** | At most **1,000 calls**, **50 conversations** and **2,000 places in conversations** all together; a name at most 200 bytes and a note at most 2,000 (ADR 0015's bounds). The largest set (5,000 talkgroups) and the largest basket together are about 0.65 MB, so the profile's `PUT` now takes up to **1 MiB** (was 256 KiB) |
| **First sign-in** (ADR 0009) | The basket goes with the set as one profile. An account holding neither a set nor a basket gets the browser's; one holding the same set and basket is matched; anything else brings the one prompt, which now names how many calls each basket holds. Nothing is merged |
| **Saving** | Every change is saved with the set, debounced (ADR 0009). A page from before M3 sends no basket, and the server keeps the account's; adopting a profile with none keeps the browser's |
| **Clearing** (R110) | **Clear saved settings** removes the attribute, basket and all. The browser keeps its own, as it keeps its set |
| **What a call shows** | The call details (`POST /api/v1/calls`, R67), 500 IDs a request: asked when the page opens, at once for a call just put in, every 5 s while a call has no recording, else every minute |
| **Unavailable** | Deleted, hidden since it was put in, or anything else the gate doesn't give: **No longer available** and Remove (or Take out, in a conversation), one markup for every reason, naming nothing (P1, R88). Left out of downloads by the browser, and by the server anyway (ADR 0015) |
| **The countdown** | `deletes_at` on each call the call details and recent calls give: the nightly run after its start plus the period in force (`retention.Job.DeletesAt`, through `gate.WithRetention`). Shown as "deleted in N days" (whole days left, rounded down), then "deleted in N hours", then "deleted at the next nightly run" |
| **Downloads** | The whole basket or one conversation, by GET (ADR 0015), the browser's encoder checked against ADR 0015's example. Only calls available and recorded. Split in "part *n* of *N*" by the URL budget (7,680) and 500 calls, a conversation kept whole in a part where it fits, else split with its name and note in each part |
| **New since last time** | Each scope (the basket, each conversation) has a `last`: when it was last downloaded. **Download new since last time** takes the calls put in it since: loose calls by their `added`, a conversation's calls by when they were put in that conversation. A whole download is each conversation's too. The full re-download is always offered once there's a `last` |
| **"Last time"** | Set when a download (or any of its parts) is pressed: the browser can't tell when a download finishes |
| **Order** | A call put in a conversation goes by its start among the calls already there, so a conversation plays, and its playlist lists, the calls as they happened. Loose calls are in the order put in |
| **Play all** | A conversation's recorded calls replay one after another in the dock (ADR 0016's replay, `Replay.playAll`), "Structure fire: call 2 of 3"; Stop replay stops them all |
| **Add to basket** | `web/src/lib/basket/AddToBasket.svelte`, beside Share: on every recent-calls row (ADR 0016's `actions`), on the call card, and in the dock for the live call playing and for a replay. Pressed (`aria-pressed`) while the call is in the basket; pressing again takes it out. A live call goes in at once and downloads once its recording is in |
| **Page** | `/basket`: **Basket** in the side menu, with how many calls it holds as a quiet pill, and in the phone's bottom bar (R84) |
| **Fixture** | `webfixture call-dated` (a call recorded a given age ago), `retention <days>`, and `nightly <dir> <ahead>` (the nightly run with the job's clock ahead), for A19 |
| **Migration** | None. The server keeps nothing new (P4) |

## What the basket keeps, and why only IDs

P1 says a hidden talkgroup's name, ID or audio never reach the browser, nor anything that says one exists. A basket that kept each call's talkgroup name would go on showing it after the talkgroup was hidden, from the browser's own storage or from the profile. So the basket keeps only the call's ID, which the listener already had and which says nothing (R60), and the listener's own words. Every time the page shows a call it asks the call details, which go through the gate (ADR 0004): a call hidden since it was put in isn't in the answer, exactly as a deleted one isn't, and the page renders the two the same way. The A10 harness probes the call details as the basket asks them (`a basket's check`: the basket's calls, a never-stored one among them), and `basket-live.spec.ts` hides 1001 after its call is put in and compares the item's markup with a deleted call's.

The account's answer (`GET /api/v1/account`) gives the basket as saved. Its call IDs are the listener's own; unlike the set's talkgroup IDs (ADR 0009's `shown`) they name nothing, so none are left out.

## Where it's kept

R64 says the basket is "saved like the listening set" (R41), and ADR 0009 said it joins the profile in M3. So it follows the set's rules exactly: `localStorage` by default, the profile when signed in, and the first sign-in's rule treats the set, the look, the playback settings and the basket as one profile: matched, saved, or offered in the one prompt, never merged. A listener with an empty set but a full basket has something to lose, so "the account holds nothing" now means no set and no basket.

Two things keep a basket from being lost across the change. A browser running a page from before M3 sends a profile with no basket, and the server then keeps the account's (`kept`). And a browser that adopts a profile from before M3, which has no basket, keeps its own and saves it at the next change.

**How large.** A listener collects a few calls an incident; 1,000 calls is three GET parts and far beyond that. 50 conversations and 2,000 places in them bound what one listener can make Authentik hold to well under a megabyte, as ADR 0009's limits bound the set. The browser stops adding at the limits (Add to basket says "The basket is full") and the server refuses a profile past them.

## The countdown

R64 wants each item to say when it goes ("deleted in 3 days"); ADR 0005 deletes a call at the first nightly run whose cut-off, the run's time less the period, is after the call's start. That moment depends on the period (a setting), the nightly time (`NIGHTLY_AT`) and the zone (`TZ`), none of which the browser has. So the server computes it: `retention.Job.DeletesAt(days, start)` is `NextRun(start + days × 24 h)`, the gate reads the period once per answer (`Gate.Deletion`), and the call details and recent calls give it as `deletes_at`. Recent calls' rows are the call details' shape (ADR 0013), so they carry it too.

It tells nothing of a hidden talkgroup: it's a function of the call's start and the deployment's settings, the same for every call that started at the same moment, and it's given only for calls the gate gives. It does make the retention period public, to within a day; anyone could tell it already from the oldest calls listed. Early deletion for low disk (R88) can come sooner, and isn't predicted: when it happens the call is "No longer available" at the next check.

A19's call, recorded 29 days ago with 30 days' retention, is deleted at the first nightly run more than a day from now: between one and two days away, which rounds down to "deleted in 1 day".

## New since last time

R64 offers **Download new since last time** and a full re-download, and leaves where "last time" is kept to the slice. Kept here per scope, in the basket itself, so it goes to the profile and the listener's other browsers with the rest:

- the whole basket's `last`, and each conversation's;
- each call's `added`, and each conversation's own `added` for each of its calls, so a call put in a conversation since its last download is new to it, even if the call was in the basket before.

A whole download sets each conversation's `last` too: their calls were in it. A conversation's own download doesn't move the basket's. "Last time" is when the listener pressed the download: the page can't know whether the browser finished saving it, and the full re-download is there for one that didn't.

Considered and not chosen:

- **One `last` for the whole basket.** A conversation downloaded on its own would leave the basket's "new" wrong, or the other way round.
- **Marking each call downloaded.** It can't say "since": a call taken out of a conversation and put back would never be new again, and a call in two conversations needs a mark for each anyway.
- **Keeping the IDs of the last download.** As large as the basket again, in the profile.

## Downloads

ADR 0015 left the split to the browser. `zipParts` adds the scope's calls in order, loose first, and starts a new part before the next call would take the part past 500 calls or its URL past 7,680 characters, as the server checks them. A conversation that fits in a part of its own but not in what's left of this one starts the next part; one too big for any part is split call by call with its name and note in each. `basket-rules.spec.ts` checks the encoder against ADR 0015's example, 359 loose calls in one part and 360 in two, three conversations with 2,000-byte notes in two, and a 400-call conversation split; `basket.spec.ts` downloads each part of such a basket and checks every available call is in one.

The basket leaves out what can't be downloaded (unavailable, or not recorded yet) before it packs, so a part's budget isn't spent on calls the server would leave out. One part downloads from a link with `download`; several show as a list of links, "Part 1 of 2", so each is the browser's own download that it can resume (R65).

## Open points for the owner

1. **"Last time" is when the download was pressed.** A download that failed and was never retried still counts; the full re-download covers it. Marking it when finished would need the page to fetch the zip itself, which loses the browser's own resume (R65).
2. **Clear saved settings leaves the browser's basket**, as it leaves the browser's set (ADR 0009): it empties the account. The basket's own **Clear** empties the browser's, and, signed in, the account's with it. Say if Clear saved settings should empty this browser's basket too.
3. **How large a basket the profile holds**: 1,000 calls, 50 conversations and 2,000 places in conversations. Say if listeners need more.
4. **`deletes_at` makes the retention period public**, to within a day, on every call's details. The alternative is a countdown only in days left, with the same effect.
5. **Conversations are in the order the calls happened**, with no way to reorder them by hand. A conversation's playlist and Play all follow that order.

## How each choice is verified

| Choice | Verification |
|---|---|
| `deletes_at` is the nightly run after start plus the period, and follows the setting | `TestDeletesAt` (`internal/retention`), `TestIntegrationCallDetails` |
| A10: the basket's check names unavailable calls among public ones and answers as for a deleted one; `deletes_at` the same whatever else is named | `a10Probes`'s `a basket's check`; `e2e-a10.sh` (a list with 1003's calls answers as the public call alone, with `deletes_at`) |
| P4: the basket page, its check, zips and the profile with a basket keep nothing | `cmd/radioarchive/p4_test.go` |
| The profile holds a basket, keeps it from a page that sends none, refuses one that isn't one or is too large, takes the largest | `TestProfileBasket` (`internal/account`) |
| The first sign-in rule with the basket | `tests/account-profile.spec.ts` |
| The encoder, the split, new since last time, the order, the countdown, reading a kept basket | `tests/basket-rules.spec.ts` |
| Add to basket on a row and the card, the menu's count, A18's zip from the browser, the same bytes twice, new since last time, the full re-download, Clear, a download in parts, an unavailable call, axe in both looks and themes at 375 and 1,280 px | `tests/basket.spec.ts` |
| A19, a call hidden since it was put in, Add to basket in now playing for a live call, Play all | `tests/basket-live.spec.ts` (`@live`) |
| Signed in: saved to the account, there in another browser, cleared with Clear saved settings | `web/signin-tests/a33.spec.ts` |

## Consequences

- #82 shares a conversation from the basket, packing it as ADR 0015 says.
- #83's unit aliases show in the basket's items as they do in recent calls' rows.
- The fixture's `call-dated`, `retention` and `nightly` go with the others when the binary serves the admin API (M4).
