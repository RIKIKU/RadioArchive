---
status: proposed
---

# 38. Zip limits and speeds

Date: 2026-11-26

Zips ([ADR 0015](0015-zips-their-url-packing-parts-and-resuming.md)) are capped at **2 at once per client and 6 in all**, and sent at **2 MB/s each and 8 MB/s together** (R72), on ADR 0036's client and exemptions. These are the implementer's choices where `SPEC.md` R65, R70, R72, R73 and R78 leave room (spec "How to use this spec", item 6). Built in [Zip limits and speeds (server)](https://github.com/RIKIKU/RadioArchive/issues/141).

## Summary

| Part | Choice |
|---|---|
| **What counts** | Every request to `GET /api/v1/zip/{packed}` and the QUERY/POST list, from the moment it's let through until its handler returns. A resumed download (a range request) is a new zip (R72), and so is a HEAD |
| **The client's cap** (`LIMIT_ZIPS_PER_CLIENT`, 2) | The 3rd: `429`, `{"error": "too_many_downloads", "message": "Too many downloads at once: at most 2 from here. Try again in 2 minutes.", "retry_after": 96}` |
| **The radio's cap** (`LIMIT_ZIPS_TOTAL`, 6) | The 7th: `503`, `{"error": "downloads_busy", "message": "The radio is busy with other downloads. Try again in 2 minutes.", …}`. When both caps are reached, the client's is the answer. 503 for the same reason as ADR 0037's full radio |
| **`Retry-After`** | When the soonest of the zips in the way (the client's own for 429, all for 503) should be done: its `Content-Length` less what's been sent, at the per-zip speed, rounded up to whole seconds, at least 1. A minute when none of them has a size yet |
| **Speed** (`LIMIT_ZIP_SPEED` 2, `LIMIT_ZIP_SPEED_TOTAL` 8, in MB/s of a million bytes, decimals allowed) | The answer is written in pieces of at most 32 KB. Each piece reserves its time on the zip's own pace and on one shared pace for all zips (a GCRA: each reservation follows the last), then waits for the later of the two, with 50 ms of slack. A wait ends at once if the listener goes |
| **What's paced** | The whole answer, headers aside. A18's bytes and resuming are untouched: pacing only delays writes |
| **The page's check** | The browser's download manager can't show why a download was refused, so the page asks first for the zip's first byte: the same URL with `Range: bytes=0-0`. It's a zip like any other, refused as the download would be, or a `206` of one byte that gives its place back at once (#142) |
| **Admins** (R70) | A zip that would be refused is let through when a signed-in admin asks, and isn't counted. It's paced like every zip, the total included, so an admin's downloads can't crowd out the public's |
| **Telemetry** | `Limits.Hits(Zip)` counts refusals of either kind. `Limits.ZipsOpen` is the number counted, for M6 |

## Why a first byte, not HEAD

A HEAD would answer the same `429` or `503`, but a browser can't read a HEAD answer's body, so the page couldn't show the message naming the limit. A route of its own would be one more public surface for the A10 harness, and its answer could drift from the download's. A one-byte range is the download itself, refused by the same code. When it's let in, it costs what a resumed zip costs: the gate's read of the calls, and one byte. The page checks once per click, so the check and the download happen within a moment of each other. A zip that ends in that moment can only make room, and one that starts in it is a race the page loses gracefully: the download manager shows its own failure.

## The estimate

The refusal says when to come back, and R73's example names minutes ("Try again in 2 minutes"). The server knows each zip's size, since it's built before it streams (ADR 0015), and how much it has sent, so the earliest that a place could free up is the smallest time left at the per-zip speed. Under the total cap a zip can go slower than 2 MB/s, so the estimate can be early. An early estimate costs a listener one more refusal. A late one would keep them waiting for nothing.

## Considered options

- **Pacing with a token bucket per zip only**, and dividing 8 MB/s by the zips in flight. That changes each zip's speed as others start and finish, and needs a scheduler. The shared GCRA gives the same bound with one more reservation per piece.
- **`golang.org/x/time/rate`.** It does the same thing as a few lines here, and would be another module for the server.
- **Admins at full speed.** An admin downloading a month's audio would take the bandwidth R72 keeps for everyone, live listening included.
- **Counting a zip only while bytes are flowing.** A listener's paused download manager would then hold no place, but it would also not count towards "2 at once", which is what R72 says.

## How each choice is verified

- `internal/limits`: `TestZipCaps` covers both caps, their status, code, message and estimate, an unknown size waiting a minute, a release making room once, and an admin not counted. `TestZipSpeed` measures 1 MB at 2 MB/s in about half a second, and two zips under a 3 MB/s total taking about a second. `TestZipGone` checks that a waiting write gives up when the listener goes.
- `internal/server`: `TestIntegrationA20Zips` runs through the real handler at the default limits with two 3 MB recordings. A zip takes about 3 s. While one client has 2 being sent, its 3rd (GET, POST, the first-byte check, and a signed-in listener's) is refused with `429` and `Retry-After`, while an admin's check and an admin's zip get in. With 6 being sent from 3 clients, a 7th from a 4th client gets `503 downloads_busy`. Every zip that was let in is byte-identical to the first, and a resume afterwards matches byte for byte. ADR 0015's zip tests, A18 included, pass unchanged.

## Consequences

- The browser test stack raises every zip limit, as for ADR 0036's.
- A zip's handler now runs for as long as the zip takes to send, which is about 45 minutes for a busy month (#22). The nginx example's long timeouts for zips (R85) are needed, and are M6's docs.
