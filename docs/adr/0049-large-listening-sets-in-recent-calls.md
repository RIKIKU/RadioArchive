---
status: proposed
---

# 49. Large listening sets in recent calls

Date: 2026-11-27

The owner tried the Recent page on the cld01 demo with a listening set of 1,212 talkgroups. Their real set has 1,214, and Listen's All on puts every public talkgroup in it. The filter refused: "Name at most 1000 talkgroups at once." ADR 0013 set that cap at "the deployment's size", about 1,000 talkgroups (`SPEC.md` section 2), and a real deployment has more. A listener whose set is large must still be able to filter by it.

This amends ADR 0013's `talkgroups` bound and ADR 0016's body bound. Nothing else in the route changes: the same parameters, the same refusals, and the same answers.

## Decision

| Part | Choice |
|---|---|
| **The cap** | `talkgroups` names at most **10,000** talkgroups (`public.MaxTalkgroups`), ten times the deployment's size. More is refused as before (`too_many_talkgroups`, naming the number) |
| **The body** | A search's JSON body may hold **256 KiB** (`public.MaxSearchBody`, was 64 KiB). That's 10,000 talkgroups each with a 5-digit system and a 10-digit ID, about 190 KB, with room. A set this long goes as a body, which the page already does past 7,680 bytes of URL (ADR 0016) |
| **Every talkgroup named** | The gate treats a set naming every public talkgroup in the catalogue as **no filter** (`Catalogue.filterOf`), whatever else the set names. It finds exactly what no filter finds. Only public talkgroups' calls are listed (`public_calls`), and a call on a talkgroup the catalogue doesn't hold yet is held back either way. So All on costs what no filter costs, however long the set is |
| **The page** | A listening set holding every public talkgroup in the catalogue sends no `talkgroups` at all (`searchOf`), so All on is a short GET. A smaller set goes as before: a GET while it fits, else a POST body. The summary still says "Your listening set (N talkgroups)" |

## Why the cap can rise without making a search dearer

The query never sees the names as sent. The gate reads them against the catalogue first (`resolve`): a talkgroup that isn't public, or doesn't exist, names nothing. Only the public ones' IDs go to `talkgroup = ANY (...) OR patched_talkgroups && ...`. The array the database gets is at most the deployment's public talkgroups, whatever the cap. With every one named it's no array at all, so the largest one a search can send is all of them but one. The cap bounds only what a request may hold, which is the body's size and the work of reading it in Go: a split and a map lookup per name, with no query.

The request limits are unchanged and still mean the same thing. The 120 requests a minute (R73, `internal/limits`) count requests, and a long set is one request, as it was. Every search still has R73's timeout. What a search costs is set by its range and how rare its matches are, not by the length of its set (measured below). The body and the cap bound how much a single request can make the server read.

## Measured

Measured on cld01 with PostgreSQL 18's defaults, from an empty database migrated by the API's own start-up and then filled. That's the spec's month (600,000 calls over 31 days, 1.8 million transmissions, 5% patched) with the owner's talkgroup count: 1,300 talkgroups, of which 65 are hidden and 7 ignored, so 1,228 are public. Calls are spread unevenly, as a real radio's are: a few talkgroups are busy and most are rare. Each search is one page of 50 (51 rows asked), newest first, over the whole 31 days, as the query runs with custom plans (ADR 0013). "Cold" is after a restart of PostgreSQL and `drop_caches` (cld01's VM host may still cache some of it).

Execution time in ms. "After the load" is one cold run of each, on a second database filled the same way, before anything had read it. Cold is the lowest and highest of 3 rounds; warm is the lowest and highest of 5. Planning is cold / warm:

| Set | After the load (cold, before ANALYZE) | Cold | Warm | Planning |
|---|---|---|---|---|
| No filter (also every public talkgroup, now) | 1,540 | 98-109 | 1.1-1.9 | 13 / 1 |
| The 1,000 busiest (allowed before) | 1,506 | 103-160 | 0.8-0.9 | 16 / 1.5 |
| All public but one, 1,227 (the largest array now possible) | 1,509 | 101-128 | 0.7-1.0 | 16-19 / 1.5-2.1 |
| The 1,000 rarest (allowed before) | 1,537 | 120-181 | 1.1-1.4 | 14 / 1.5 |
| The 1,200 rarest | 1,562 | 104-107 | 0.8-1.2 | 16 / 1.5 |
| For scale: the 20 rarest | 3,714 | 2,061-2,630 | 8-10 | 13 / 0.4 |

Straight after the load, every search took about 1.5 s whatever its set. The first reads of freshly written pages write their hint bits (the plans show them "dirtied"), and every search pays that once, filter or none. After that, every plan reads the `(started_at, id)` index backwards, checks each call against the array, and stops at the page's end. A set of 1,227 costs what one of 1,000 costs, and both cost what no filter costs. A large set matches most calls, so a page fills fast. Planning grows with the array, by about 1 ms warm. The searches that read far are small sets of rare talkgroups, which have always been allowed. A long set isn't one of them.

## Considered

- **Raise the cap only.** This alone fixes the refusal, but All on would still send every talkgroup's ID, and the query would test each call against all of them, to find what no filter finds.
- **"Every talkgroup" only.** This fixes All on, but not a large set that isn't everything, like the owner's 1,212 of 1,228.
- **No cap.** Then the only bound would be the body's size. A cap gives a refusal that names the limit (R78), where the body's size gives only "invalid body".
- **The deployment's own count as the cap.** ADR 0013 makes every refusal depend only on the request's form, never on what the deployment holds, so that a refusal never reveals how many talkgroups are hidden.

## Tests

| What | Where |
|---|---|
| A set naming every public talkgroup is no filter; all but one is that filter; none public is nothing | `TestFilterOf` (`internal/gate`) |
| Every public talkgroup named, plus unknown and hidden ones: the same calls as no filter | `TestIntegrationRecentCalls` |
| 10,000 talkgroups in several systems by QUERY and POST; 10,001 refused naming the limit | `TestIntegrationRecentCallsBody` |
| The page: a set holding every public talkgroup names none; 1,212 go as a POST body | `recent-rules.spec.ts` |
| In the browser at 375 px: a set of 1,212 filters, as a POST, with no error; All on asks with no filter | `recent-phone.spec.ts` |
