---
status: proposed
---

# 16. The Recent calls page: its address, its default, the search by body, the time zone and replay

Date: 2026-11-25

The Recent calls page lists the listening set's calls, with the talkgroups added for this session, newest first, on ADR 0013's route. Its filters live in its address as the listener chose them, never as the talkgroups they expand to. A search whose talkgroups make a URL too long for a default nginx goes as a `POST` (or `QUERY`) body to the same route, through the same code. The catalogue names the deployment's time zone, so the page shows times on its clock. Pressing a call replays its recording in the dock through an output of its own, pausing live listening, which stays paused once the replay ends until the listener resumes it. Play from here waits on #75's open question 3. These are the implementer's choices where `SPEC.md` R23, R24, R33, R46, R47, R62, R63, R73, R83, R85 and section 13 leave room (spec "How to use this spec", item 6). Built in [Recent calls page: list, filters, play and play from here](https://github.com/RIKIKU/RadioArchive/issues/79).

## Summary

| Part | Choice |
|---|---|
| **Page** | `/recent`: **Recent calls** in the side menu, **Recent** in the phone's bottom bar (R84). `web/src/routes/recent/+page.svelte` |
| **The list** | `web/src/lib/recent/RecentList.svelte`, a component the call card (#80) reuses: a search in, its calls a page (50) at a time with **Load more**, each row the call details' call (ADR 0013) |
| **Default** | The listening set **and the session's additions** (R47), as the live feed hears them; an empty one is every public talkgroup (ADR 0013) |
| **Address** | `talkgroups` (none: the default; `all`; or the share link's items, ADR 0008), `from`, `to` (`YYYY-MM-DD`), `from_time`, `to_time` (`HH:MM`), all on the deployment's clock as ArchiveUx's `DateRange` gives them, `unit`, `order=oldest`. Only what was chosen: the listening set and the instants it stands for are never written there |
| **Search by body** | `QUERY` and `POST /api/v1/recent-calls`, the same parameters as a JSON object (`talkgroups` a list of strings, `limit` a number, the rest strings, `null` for not given). Read into the query string's form and answered by the GET's code. The page sends a POST when the GET would pass **7,680** characters (ADR 0015's budget) |
| **Time zone** | The catalogue's `time_zone`: TZ by its IANA name (`Australia/Melbourne`), `UTC` when TZ is unset. The binary carries Go's zone database (`time/tzdata`) |
| **Errors** | A refusal about a field (`range_too_long`, `invalid_range`, `invalid_time`, `invalid_unit`, `invalid_talkgroups`, `too_many_talkgroups`) under that field, with the summary at the top of the form (ArchiveUx section 5); `search_too_slow` and failures under the form |
| **Replay** | Pressing a call plays its recording (R20) in the dock, from a second `AudioContext`; live listening pauses (`suspend()`, ADR 0012) and keeps queuing. One call at a time; **Stop replay** stops it; Resume, Jump to live and Start listening stop it too. When it ends, live listening stays paused until Resume |
| **In progress** | A call still in progress has no recording (ADR 0013): its row offers **Listen live**, which adds its talkgroup for the session if it isn't in the set (R47) and starts or resumes listening |
| **Row actions** | **Download** (`/api/v1/calls/{id}/download`, R63's name, ADR 0014); Share (#80) and Add to basket (#81) go after it, through the list's `actions` snippet |

## The default includes the session's additions

R46's default is "the listening set". The page lists what the listener hears: a talkgroup added by a fireground link is one they're following now, and leaving it out of recent calls while its live calls play in the dock would be surprising. It's also what `sendListeningSet` already hands the live feed, so the page and the feed agree. The share link, which is about the saved set, still leaves them out (ADR 0008).

## The address holds choices, not the set

The filters are in the address so a filtered list can be linked to and #80 can open the list at a talkgroup and a time (`/recent?talkgroups=1001&from=2026-11-24&from_time=14:00&order=oldest`). The default (the listening set) is written as no `talkgroups` at all, so a link opens with the reader's own set, and a set of 1,000 talkgroups never reaches the address. Chosen talkgroups use the share link's items, so a range stays a range and is read against the catalogue, never expanded; one that isn't public is dropped without a word (R33), and a link naming only hidden talkgroups lists nothing without asking the server, exactly as one naming talkgroups that don't exist. Days and times are DateRange's own fields, on the deployment's clock, so a link means the same moment whoever opens it; the page turns them into instants with ArchiveUx's `rangeBounds`.

## Why a body search, and why the same route

ADR 0013 noted that a listening set of 1,000 talkgroups in several systems may pass nginx's default 8 KB request line (R85). The set lives in the browser (P4), so the browser has to send it. As R67 does for call lists and ADR 0015 for zips, the same parameters go in a JSON body: `QUERY`, and `POST` while browsers' support for `QUERY` is unsettled. It's the same path, read into the query string's form (`url.Values`) and handed to the same function, so every bound, refusal and answer is the GET's byte for byte (`TestIntegrationRecentCallsBody`), and A10's probes run against both (`recentProbes`). The page uses GET whenever it fits, so most searches stay a URL a browser can retry. Considered and not chosen: sending the set as "every public talkgroup" when it's large (the list would show calls the listener doesn't follow), and a separate path (one more route to keep in step).

## The time zone

Section 13 shows times in `TZ`. The browser can't learn it on its own, and the app shell carries no deployment data (ADR 0004), so the public catalogue, which the page loads anyway, names it. Go calls the zone it reads from `TZ` "Local", so the binary loads `TZ` by name; a zone still called Local is one no `TZ` named, which is UTC in the image. Embedding the zone database (about 450 KB) makes `TZ` work wherever the binary runs, as the browser tests' does in Playwright's image.

## Replay

R46: "Replaying pauses live listening; live calls keep queuing meanwhile." ADR 0012's pause is `AudioContext.suspend()`, which stops the clock everything live is scheduled on, so the replay can't share that context: it has its own, made from the press (browsers start audio only from a gesture), with the same decoder. Live listening is paused with the player's own Pause, so the queue, its order, priority and hold are untouched and calls keep queuing; the dock says "Paused. Calls keep queuing." beside the replay. Nothing replays over live audio: Resume, Jump to live and Start listening stop the replay first. A replay works with live listening never started, too.

When the replay ends, live listening stays paused until the listener presses Resume. That's the half of #75's open question 3 this page meets first: carrying on by itself would start whatever queued meanwhile, at once and possibly loud, after a call the listener chose. The follow-up issue for play from here carries the question, and this choice may change with its answer.

The replay announces `radioarchive:first-frame`, `radioarchive:played` and `radioarchive:ended` (`how: "stopped"`) with `replay: true`, through a meter of its own, so the browser tests measure what reached the speakers as they do for live calls.

## How each choice is verified

| Choice | Verification |
|---|---|
| The body search answers as GET, refusals included; 1,000 talkgroups in several systems in one request | `TestIntegrationRecentCallsBody` |
| A10: QUERY and POST probed as GET is | `a10Probes`'s `recentProbes("QUERY")`, `recentProbes("POST")`; `e2e-a10.sh` |
| The catalogue's time zone | `TestIntegrationCatalogue` |
| The address, the default, the range on the deployment's clock, ranges never expanded, the POST past 7,680 characters | `web/tests/recent-rules.spec.ts` |
| The list, Load more, filters, the calendar, A22's error under the form, no 1003 whatever the filters, A17's download | `web/tests/recent.spec.ts`, in Chromium, Firefox and WebKit against the binary with `TZ=Australia/Melbourne`, the browsers in UTC |
| Replay, live calls queuing meanwhile, staying paused; A11's and A14's recent-calls halves | `web/tests/recent-live.spec.ts` (`@live`) |
| axe in both looks and themes at 375 and 1,280 px: the list, the filters open, an error, an empty list | `recent.spec.ts` |

## Consequences

- The browser tests' archive is made up by the fixture's command (`webfixture calls`), stored as Bench's with silent Ogg/Opus recordings, and the API in `e2e-web.sh` runs in `TZ=Australia/Melbourne`.
- #80 puts `RecentList` under the call card with a search for the call's talkgroup from its time, oldest first, and adds Share through `actions`; #81 adds Add to basket there too.
- #83 adds unit aliases to the rows and unit search to the filters.
- Play from here is the follow-up issue's, blocked on #75's open question 3.
