---
status: proposed
---

# 27. The admin frame, the overlap query and A10's scope

Date: 2026-11-25

The web admin gets its frame and its first two pages. The **Admin** section of ArchiveUx's menu shows only while `GET /api/v1/account` says the account is an admin; its pages are SvelteKit routes under `/admin/`, and each follows the admin API's answer to its own data, which is the guard. The **Recorders** page shows each active recorder's state and signals (ADR 0010), calls rejected from a recorder and shortName that isn't attached (ADR 0003, R12) and, from a new query, two recorders' **overlapping calls** on one talkgroup in the last 24 hours (R13). The **Settings** page sets the retention period, asking before it shortens it (ADR 0005). The recordings volume's warning shows on every admin page. A10's literal check for 1003's name now passes over the app's own files, the UI chrome every page ships, which carry the menu's "Admin", and still applies to every data answer. These are the implementer's choices where `SPEC.md` R10, R12-R15, R80, R81, R84 and R88 leave room (spec "How to use this spec", item 6). Built in [Admin frame, the Recorders page and the Settings page](https://github.com/RIKIKU/RadioArchive/issues/111).

## Summary

| Part | Choice |
|---|---|
| **The Admin section** (R84) | A group of ArchiveUx's `Menu` on desktops (heading **Admin**, the `shield` icon, open by default), and the same group in `BottomBar`'s **More** sheet on phones. Shown while the account's `admin` is true (ADR 0025); nobody else gets it, signed in or not |
| **Its entries** | One list, `web/src/lib/admin/menu.ts`, in R84's order. Each page adds its entry when it's built (Recorders and Settings now; the audit log, issue #112, next). The group's heading links to the first entry. No entry for a page that isn't there |
| **The pages** | SvelteKit routes `/admin/recorders` and `/admin/settings` in the one app (ADR 0001), inside `AdminPage.svelte`: the heading, the disk warning, then the page |
| **The guard** | The admin API (ADR 0025). Each page reads its own data and follows the answer: `401` shows "This page is for admins" and a Sign in link back to the page, `403` shows "This page is for admins" and nothing else, `503` or no answer says the server couldn't be reached. The menu opens nothing by itself |
| **The dock** | Stays on admin pages: an admin listening on Listen keeps the player while they work (R81 names listener pages; nothing forbids it here) |
| **The disk warning** (R88) | `GET /admin/retention`'s volume state, when it isn't `ok`, as an ArchiveUx alert that stays (the `--warn-bg` fill, no edge) under the heading of every admin page, its words the server's. Read when the page opens and every minute. No listener page asks for it |
| **Recorders page** (R14, A27) | `GET /admin/recorders`, `/admin/rejections` and `/admin/overlaps`, asked every 5 s. Each recorder: a **Warning** or **OK** badge in words, the server's words for each signal tripped, connection (since when), last connected, last call, client version and "Out of date", and each system's shortName, the system it's attached to, its type and decode rate ("Near 0" when deaf). Times on the deployment's clock (section 13) |
| **Quiet threshold** | A form per recorder (ArchiveUx section 5): "Quiet after (minutes)", optional, empty is off, whole minutes 1 to 10,080; a problem shows under the field and takes the focus. `PATCH /admin/recorders/{name}` |
| **Rejected calls** (R12, A2) | `GET /admin/rejections`'s unattached pairs, in its words: "Bench / other: 1 call rejected, not attached to a system". Talkgroups' rejections (R17) are the Talkgroups page's (issue #113) |
| **Keys** (R10) | Never on the page or in its answers: no key, hash or rotation. The page says recorders are added and retired from the server's shell |
| **Overlap** (R13) | `GET /api/v1/admin/overlaps`: for the 24 hours up to now, each pair of recorders and system and talkgroup with overlapping calls, the most first, with R13's words: "North hill and South ridge both recorded TG 1203: 47 overlapping calls in the last 24 h". No migration |
| **What overlaps** | Two **ended** calls, from two recorders, on the same system and the same talkgroup (the one each was recorded on, not its patch), both started in the 24 hours, whose times overlap **ends included** (`a.start <= b.end` and `b.start <= a.end`). Each pair of calls counts once. Every talkgroup state counts: the page is an admin's |
| **Settings page** (R88) | "Retention period (days)", Required. Lengthening saves. Shortening is answered `409 confirm_shorter_retention`; the page shows "Delete N recordings?" naming the count and the next run, with **Shorten to N days** (danger) and **Cancel**. Only confirming saves; cancelling puts the field back and says nothing changed. Also the volume's free space and its two levels |
| **A10's scope** | The literal check (no `1003`, "Admin", "ADMIN", `"admin"`, its audio) skips the app's own files under `_app/`, blanked whole and byte for byte as built. Everything else an answer holds is checked as before: every data answer, every page built from the shell, and any byte added beside a file |
| **The browser tests' seams** | The made-up Authentik signs a browser in as a listener while its own `/authorize` gets the cookie `fakeauthentik_person=listener`. `e2e-web.sh` stops Bench's client while `RA_CLIENT_HELD` exists, and adds a second recorder, Ridge, that recorded 1003's three fixture calls too (`webfixture overlap`) |

## The Admin section and the guard

ArchiveUx's side menu has groups under headings (section 4, "Side menu"), and its bottom bar puts every group in the More sheet. The Admin section is one such group, so it needs nothing ArchiveUx doesn't have. The layout passes it to both while the account's `admin` is true, and an empty list otherwise. A visitor signed out or a listener never gets the group; the app's script holds its words for everyone (A10, below).

Showing the menu is a convenience, never a lock. The admin API reads the session and the account's row at every request (ADR 0025), so a page that showed its data from the account's `admin` alone would be wrong for a moment after a demotion. Each page asks the API for its data and shows what the answer allows. Typing `/admin/settings` signed out offers sign-in, coming back to the same page; a listener gets one plain heading, "This page is for admins", and nothing about what the page holds.

Entries are added page by page. A menu that listed all seven of R84's pages before they exist would link to pages that aren't there. Each slice adds its line to `menu.ts`, so the audit log (issue #112) and the others plug in without touching the frame.

## Overlapping calls

R13 asks the Recorders page to warn when two recorders store the same calls, which means their coverage in trunk-recorder's config overlaps. The server stores both copies and never merges them, so the warning is the admin's only sign.

**The definition.** Two recorders hearing one call each store a call on the same system and talkgroup, starting and ending within moments of each other. The query counts two calls as overlapping when they are on the same system (attachments make two recorders on one network file into one system, R12) and the same talkgroup, from two different recorders, both ended, both started within the 24 hours, and their times overlap with the ends included. Each such pair counts once. For two recorders that hear the same calls, that's the number of calls both stored.

- **The talkgroup a call was recorded on**, not its patch: a patched call is stored once per recorder under the talkgroup trunk-recorder recorded it on (R16), so two copies of one call share it.
- **Ended calls only.** A call in progress has no end yet, and one that never ended (a client stopped mid-call) would overlap everything after it. It counts once it ends.
- **Ends included**, so two copies with the same start and end, or a zero-length one, count. Two calls that only touch, one starting the moment the other ended, count too; on one talkgroup that's rare, and it errs toward the warning.
- **Every state.** A hidden or ignored-later talkgroup's copies are still two recorders' overlap, and the page is an admin's.

**The query** (`RecorderOverlaps`). The 24 hours' ended calls, each with its lower bound (its start less the longest call in the window, and no earlier than the window), joined to the other recorders' calls on its talkgroup that start between that bound and its end. Bounding by the longest call keeps the join a range read of `calls (talkgroup, started_at)` (ADR 0013's index) without missing a long call's overlaps. Each pair is counted under the lower recorder ID, so once.

**Speed**, on cld01, PostgreSQL 18 with its defaults, the query through `psql` (the handler adds a few rows of JSON), a database migrated from empty and then filled with a month in start-time order: 600,000 calls over 31 days from one recorder (60,000 on the busiest talkgroup, the rest over 999), and a second recorder storing a copy of 5 % of them, statistics as autovacuum left them. Cold is PostgreSQL restarted with the page cache dropped, then the one query.

| Database | Warm p50 | Warm p95 | Cold | Answer |
|---|---|---|---|---|
| Empty (migrated, two recorders, no calls) | 0.6 ms | 0.8 ms | | no rows |
| The month (20,000 calls in the 24 hours, 968 overlapping pairs) | 63 ms | 76 ms | 0.28 s (5 rounds, 275-284 ms) | 1 row |

Warm is 200 runs in one session. The plan reads the day's calls once by `calls (started_at, id)`, then for each one probes `calls (talkgroup, started_at)` between its lower bound and its end.

Two things the first version got wrong, found by measuring, and how the query avoids them:

- **The lower bound as an expression.** Written as `greatest(since, a.started_at - span.longest)` in the join, PostgreSQL used it as a filter, not an index bound, and each probe read the talkgroup's calls from the start of the month: 86 s. Computed as a column of a materialized CTE (`a.lo`), it bounds the index range.
- **Generic plans.** With the window's bounds as unknown parameters, the generic plan joins the day's calls to every call of their talkgroups and filters 8.5 million rows. The handler runs the query with `plan_cache_mode = force_custom_plan` in a read-only transaction, as the recent calls (ADR 0013) and a talkgroup's calls (ADR 0026) do. Planning each time is the 0.6 ms on an empty database.

## A10 and the word "Admin"

Hidden talkgroup 1003 is named "Admin" in the fixture (section 8), and A10 checks that no public answer holds that word (issue #53's note). The admin menu's heading is the same word, and it's in the app's script, which the binary sends to everyone at `/_app/…`, signed in or not: the menu is only drawn for admins, but the script is one build.

So the check now passes over the app's own files. A file under `_app/` is the same bytes for every visitor and every path; it holds no data and can't depend on a hidden talkgroup. The harness blanks each such file, whole and byte for byte, before it looks for the secrets. An answer that is one of the files passes; an answer holding a file and anything else is checked on the rest; part of a file isn't blanked, so it's checked. The shell page (`index.html`) isn't blanked: pages built from it (a call's link and its preview, ADR 0017) are checked whole, as before. Data answers, the live feed, recordings and zips are untouched by the change.

The harness's stand-in build (`appBuild`) now has the admin menu's words in its script, so `TestIntegrationA10` exercises the scope at every run, and `TestA10CheckScope` shows the check still fails a data answer, a preview, part of the script, or anything beside it that holds 1003's name or audio.

## What ArchiveUx doesn't have

ArchiveUx 0.4.1 has no badge for a state in words (it has the priority labels and the Live badge), no table, no confirmation dialog, and no class for an alert (section 4 describes its look; the app draws it). The pages use the closest ArchiveUx-consistent approach, from its tokens and allowed pairs only, with no app token:

- the **Warning** badge is the Delayed badge's pairing (`--warn-fg` on `--warn-bg`, `--radius-badge`, the `--badge-*` sizes), and **OK** is a saved message's (`--accent-soft-fg` on `--accent-soft`);
- a recorder's systems are page rows (`--border` dividers), not a table, so they fit 375 px;
- the confirmation is an inline panel (`--surface`, `--border`), as the account menu's "Clear saved settings" and the basket's Clear are, with a danger button;
- alerts are the `--warn-bg` fill with `--warn-fg` text and no edge, as section 4 says.

If these recur (the Talkgroups and Units pages will want a state badge and a table, R83), they belong in ArchiveUx.

**2026-11-25: adopted ArchiveUx 0.5.0** (its ADR 0004), which has them. The recorders' Warning and OK badges and the "Out of date" and "Near 0" flags are its `StateBadge` (warning, or OK); the disk warning is its `Banner` (warning tone, no role, as it is there when the page opens) and "Couldn't reach the server" its error `Banner` with `role="alert"`; the overlap messages are its `.banner` class; the retention question is its `Confirm`, inline, its heading an `h3`. The local `.alert`, `.badge`, `.flag` and `.confirm` styles are gone. A recorder's systems stay page rows, and the recent calls keep their inline More actions (ADR 0023).

## Considered options

| Decision | Rejected | Why |
|---|---|---|
| Who sees the menu | Every signed-in account, with the pages refusing listeners | A listener would be offered pages they can't open |
| The guard | The page trusting the account's `admin` | It's a moment old; the API is the one check (ADR 0025) |
| The menu's entries | All of R84's seven now | Five would lead nowhere until their slices land |
| A listener's page | The page's frame with its data missing | "This page is for admins" says it and shows nothing of the page |
| The dock on admin pages | Leave it out | Playback would go on with no controls |
| What overlaps | Calls starting within N seconds of each other | Long calls' copies can start apart; an overlap in time is what two copies have |
| What overlaps | Patched talkgroups too | One call would count for every talkgroup in its patch |
| What overlaps | Calls in progress | One never ended would overlap the rest of the day |
| Counting | Distinct calls on either side | Two copies are one pair; counting both doubles every overlap |
| The query | A self-join of the window on system and talkgroup | Every pair of a busy talkgroup's day compared; the range read compares neighbours only |
| The query | A table kept by ingest | Ingest's write per call for a page an admin opens now and then |
| The endpoint | `GET /recorders/overlaps` | A recorder named "overlaps" would be unreachable |
| A10's scope | Drop "Admin" from the secrets | Weakens the check for every data answer |
| A10's scope | Skip the check for app routes | A page built from the shell, with a preview, would go unchecked |
| A listener in the browser tests | A second made-up Authentik | The radio has one issuer; a cookie on the fake's own page picks the person per browser, so tests side by side don't race |
| The disk warning in the browser tests | Fill a small volume | Needs a mount (root) on the runner; the warning's state is the server's tests' (ADR 0005), the page's is answered by an intercepted answer |

## Open points

1. **The dock on admin pages.** R81 says the dock is on every listener page; the admin pages keep it too, so an admin listening doesn't lose the controls. Removing it is one line in the layout.
2. **What counts as overlapping** (above): ends included, ended calls only, a pair counted once. A different rule changes one query; the owner may want a minimum overlap (say 1 s) if touching calls turn out to raise the warning.
3. **The overlap metric** (R90) is M6's, from the same query.

## Consequences

- An admin sees the Admin section; nothing else changes for anyone else. The public routes and the live feed answer exactly as before (A10, A13).
- Every later admin page adds one line to `menu.ts` and wraps itself in `AdminPage.svelte`, which gives it the heading, the disk warning and the guard.
- The Recorders page asks the admin API every 5 s while it's open: three queries, the overlap one the largest (about 60 ms warm on a month, above). An admin leaving the page open all day costs the database about 1 % of a core.
- A10's harness checks the app's files only as far as they hold more than the build.

## How each choice is verified

| Choice | Verification |
|---|---|
| Overlap: by 5 s, touching ends, inside a 90-minute call that started an hour before (fails with the longest-call bound shortened), hidden 1003, counted per pair; not a second apart, another talkgroup or a patch, the same recorder, another system, before 24 h, in progress; the words; `401` without an admin | `internal/server/overlaps_test.go`, `TestIntegrationOverlaps` |
| The binary serves the overlap route | `cmd/radioarchive/main_test.go`, `TestIntegrationBinaryRoutes` |
| A10: the app's files pass with the menu's words; a data answer, a preview, part of a file or anything beside one still fails | `internal/server/a10_test.go`, `TestA10CheckScope`; `TestIntegrationA10` with the stand-in build's menu (both fail with the scope taken out) |
| The Admin menu for an admin only, on desktops and under More; its pages open from it | `web/tests/admin.spec.ts` |
| Signed out: sign-in offered, `401`; a listener: "This page is for admins" only, `403` | `web/tests/admin.spec.ts`, signed in through the made-up Authentik from the root URL |
| The Recorders page: state, decode rate, Ridge never connected, the overlap's words, no keys, the quiet form's problem and save | `web/tests/admin.spec.ts` |
| A27, page half: disconnected and deaf show, and clear; the listener's notice on disconnect | `web/tests/admin-live.spec.ts` |
| A2, page half: "Bench / other: 1 call rejected, not attached to a system" | `web/tests/admin-live.spec.ts` |
| R88: shortening names the count, cancelling changes nothing, confirming saves, lengthening just saves | `web/tests/admin.spec.ts`, `web/tests/admin-live.spec.ts` |
| The disk warning on admin pages below 15 %, on no listener page | `web/tests/admin.spec.ts` |
| A36: axe in both looks and both themes at 375 px and 1,280 px, the menu, the Recorders page, Settings with its confirmation, the fields' problems, the refused page | `web/tests/admin.spec.ts` |
| Tokens only, no app tokens (R80) | `npm run check:tokens` |
