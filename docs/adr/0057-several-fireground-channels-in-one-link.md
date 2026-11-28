---
status: proposed
---

# 57. Several fireground channels in one link

Date: 2026-11-28

PagerArchive showed a radio link per channel a page names. The owner, 2026-11-28: "The radio links are working but I'd prefer it to be simpler. The user should only have to click one button to make it listen to both channels." So PagerArchive shows one **Listen** per page or job, opening the radio with every channel the page names: its dispatch channel and any fireground channels. The format was agreed with PagerArchive: the link's parameter repeated, `/?fireground=508&fireground=540&fireground=542`. This amends R101 and [ADR 0006](0006-talkgroup-settings-and-the-catalogue.md)'s fireground lookup; A49 checks it.

## Summary

| Part | Choice |
|---|---|
| **The link** | `<radio URL>/?fireground=<n>&fireground=<n>…`. A single `?fireground=<n>` is the same link with one value, and reads exactly as before |
| **How many** | The first **10** values are read, in the order given, and the rest ignored. Duplicates count towards the 10, so a link is read the same whatever PagerArchive dedupes |
| **Each value** | Read as `gate.ParseFireground` reads one: decimal digits only, at most 16, leading zeros not counting, within a 32-bit integer. The browser applies the same rule (`web/src/lib/listening/fireground.ts`) and sends only numbers. Two values that are the same number are one channel |
| **The lookup** | `GET /api/v1/fireground?n=508&n=540&n=542`: one request, whatever the count. Always `200 {"talkgroups": [...]}` (and `systems` when there are several, as the catalogue gives them), one talkgroup for each number a public talkgroup has, in the order asked, each once. It reads the first 10 `n` values as the page does. `Cache-Control: no-store`, behind `LimitRequests` |
| **Unavailable numbers** | Left out of the answer, so the answer with a hidden, ignored, unknown or malformed number in it is byte for byte the answer without it. The A10 harness probes it |
| **The page** | Every talkgroup found is added for the session (R47) at once. One notice: "Added for this session: A, B, C." and, for those already in the set, "Already in your listening set: D."; then "Start listening to hear them." A quiet note counts the rest, "1 channel isn't available." / "3 channels aren't available.", naming none. None found: "This channel isn't available", the page a single unknown number shows. One value, or one number given twice: the single link's notice, word for word |
| **Start listening** | The dock's, the page's only one. No autoplay: a browser starts audio only after a tap on the page |
| **The address** | Once the link is read, every `fireground` value leaves the address (`replaceState`), so a reload doesn't read it again; the session's additions stay. A lookup that failed keeps it, so the error's "Reload the page to try again" does |

## Considered

- **One request per number, in parallel, to `/api/v1/fireground/{number}`.** Ten channels would be ten of the listener's public requests a minute (R73's `LimitRequests`, ADR 0036) on top of the catalogue and the live feed's start, and a page with several jobs open in tabs could reach the limit. One request costs one, at any count up to 10. The single-number endpoint stays, unchanged, for anything that uses it.
- **Reading the numbers from the catalogue the page loads anyway.** Its talkgroups carry `fireground_channel`, so the page could match them itself. But the page's lookup works when the catalogue failed to load, as the single link's always has, and a server-side lookup keeps the number's rule in one place.
- **A comma-separated value, `?fireground=508,540`.** Repeated parameters need no new syntax, are what `URLSearchParams.getAll` reads, and make a one-channel link the same as before.
- **Counting the 10 after deduplicating.** Reading the first 10 values as given is simpler to state for PagerArchive and cheaper to check; a page naming more than 10 channels isn't expected.
- **A second Start listening button in the notice.** Two buttons with one name would make the page busier, not calmer, and the dock's is on every page already.

## Consequences

- PagerArchive builds one link per page or job: its dispatch channel's number first, then the fireground channels', at most 10.
- The Android app can use the same endpoint when it handles a fireground link opened by App Links ([ADR 0058](0058-android-app-links.md)).
- Before this, a single link's `?fireground=` stayed in the address; now it leaves it once read.
