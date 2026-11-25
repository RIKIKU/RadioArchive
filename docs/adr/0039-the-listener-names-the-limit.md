---
status: proposed
---

# 39. The listener names the limit

Date: 2026-11-26

The listener shows what the server's refusals say ([ADR 0036](0036-the-client-and-the-request-limits.md), [ADR 0037](0037-live-listening-limits-and-the-radio-is-full.md), [ADR 0038](0038-zip-limits-and-speeds.md)) where each request was made: the limit and when to try again, in place of the page's own "Couldn't load…". These are the implementer's choices where `SPEC.md` R71, R73 and R78 leave room (spec "How to use this spec", item 6). Built in [The listener names the limit, and "the radio is full" in the browser](https://github.com/RIKIKU/RadioArchive/issues/142).

## Summary

| Part | Choice |
|---|---|
| **One reader** | `web/src/lib/limits.ts`. `limitOf` turns a `429` or `503` whose body has one of the limits' codes into `{error, message, retry_after}`. `refusedBy` makes the error a failed request throws: a `LimitError` for a limit, else the status as before. `limitText` is the message to show, or nothing |
| **Where it shows** | In the place the page already shows that request's failure, as the server's own message: the talkgroups on Listen, Recent calls and Basket; a fireground link; a call's page; a shared conversation; the basket's availability check; a replay in the dock; and Recent calls' search, which already showed refusals. The page's own words stay for every other failure |
| **Downloads** | Every download link (a call card, a recent-calls row, a basket row, the whole basket, a conversation and each part) is a `DownloadLink`. A click first asks for the file's first byte (`Range: bytes=0-0`, ADR 0038). Refused over a limit, no download starts, and the message shows under the link (`role="alert"`). Otherwise the link is clicked again for real, and the download manager takes it from there. If the network fails, the check is skipped |
| **The live feed** | A socket that closes before it opens is followed by the plain GET of ADR 0037. `radio_full` raises the `radio_full` feed notice, "The radio is full right now. Trying again…". `too_many_live` raises a new `too_many_live` notice with the server's message, its "Try again in 30 seconds." said as "Trying again…". Either way the feed tries again after `Retry-After` (30 s, R71), not its usual backoff. Any other answer means the usual reconnect |
| **Clearing** | A socket let in clears both notices: every welcome turns every notice off (ADR 0007). Raising one clears the other. Stopping clears both |
| **Placement** | The feed notices sit with the recorder notice above the dock (ADR 0010), in the same `feed-notice` look, polite live region and all |
| **Not shown** | Unit search's suggestions, which say nothing on any failure and are asked again at the next keystroke, and live playback's fetch of a finished call's recording, which plays from its frames and skips a call it can't get. Both are under limits generous enough that a listener can't reach them without first being refused somewhere that shows it |

## Why the server's words

The server knows the limit's number (a deployment may change it, section 6.11) and how long to wait. Its message is already written for a listener (R73's own example is one), so the browser shows it as it comes. The browser adds words only for the live feed, which tries again by itself. "Try again in 30 seconds." would tell the listener to do something the page is already doing, so it says "Trying again…" (R71's own words for the radio being full).

## Why check before a download

A download link is fetched by the browser's download manager, outside the page. A refused one shows up as a failed download, with nothing to say which limit, or as a saved file holding the JSON refusal. Asking for the first byte costs one more request per click: a request under the request limit for a single call, and a zip that ends at once for a zip (ADR 0038). In exchange, the refusal is shown next to the button that was pressed. "Download new since last time" is only marked done once the check has let the download through, so a refused download doesn't move "last downloaded".

## How each choice is verified

`web/tests/limits.spec.ts` (`@limits`) runs against a binary of its own at the default limits, started by `e2e-web.sh` with `TRUSTED_PROXY=127.0.0.1`. Each test gives itself a client of its own by `X-Forwarded-For`, and zips are sent at 1 KB/s so the test's own zips are still going when the page asks.

- A 121st request in a minute: the Recent calls page shows "Too many requests from here: at most 120 a minute. Try again in N seconds." where the talkgroups would be, and another client's page is unaffected.
- A 3rd zip: with the test's 2 zips of the basket being sent, pressing Download the basket shows "Too many downloads at once: at most 2 from here. Try again in …" under it, and no download starts.
- A 5th live connection: with 4 open from 127.0.0.1, Start listening shows "Too many live connections from here: at most 4 at once. Trying again…". When one closes, the page is listening within the 30 s retry and the notice has gone.
- 50 open from 13 clients: a new listener sees "The radio is full right now. Trying again…" and none of the 50 drops. When one closes, the listener is in within 30 s and the other 49 are still open.
- axe-core passes, in both looks and both themes at 375 px and 1,280 px, with each of the three messages showing, and the PR's screenshots are taken there.
- The existing tests pass with the links checking first, A18's downloads included.

## Consequences

- `e2e-web.sh` runs a third pass (`@limits`, one at a time) against the second binary. The first two passes leave `@limits` out.
- A deployment that changes a limit needs no change in the browser: every number shown comes from the server.
