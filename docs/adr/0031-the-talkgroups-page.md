---
status: accepted (2026-11-25, by the owner)
---

# 31. The Talkgroups page

Date: 2026-11-25

The web admin's **Talkgroups** page shows every talkgroup of every system in one ArchiveUx `DataTable`, from one request to [ADR 0026](0026-the-admin-talkgroup-table-last-heard-and-calls-in-30-days.md)'s table. Search and filters run in the browser over every row as they're typed; every column sorts. One talkgroup at a time opens for editing in a panel above the table, which names it and marks its row; it saves with `PATCH` as only what changed, which marks it reviewed. A row's state changes from the row's More actions: hiding at once, ignoring and making public after asking in a dialog. A row's recordings play in the page's own player. The talkgroups' rejected calls sit at the top. Type and frequency (R22, ADR 0030) are in the form, the frequency in MHz. These are the implementer's choices where `SPEC.md` R17, R21-R24, R30, R31, R34, R50, R80, R83 and R100 leave room (spec "How to use this spec", item 6). Built in [The Talkgroups page](https://github.com/RIKIKU/RadioArchive/issues/113).

## Summary

| Part | Choice |
|---|---|
| **The page** | `/admin/talkgroups`, the first entry of the Admin section (R84's order), inside `AdminPage.svelte` ([ADR 0027](0027-the-admin-frame-the-overlap-query-and-a10s-scope.md)): the guard, the heading, the disk warning |
| **Its data** | `GET /admin/talkgroups` once, with `/systems`, `/tags`, `/groups` and `/rejections` beside it, when the page opens. No polling: a search must ask nothing (A23). A save merges the answer into its row |
| **Rows** | Every row is loaded, searched, filtered and sorted in the browser (R50, A23). The table draws the first 100 of the answer in its order, with **Show 100 more** and **Show all** at its foot (see "Speed"); `DataTable` is given the rows already sorted (`sortRows={false}`, ArchiveUx's own `sortRows`) |
| **Columns** | TG (the row's name), System (only with several), Label, Name (R24: the label beside the name), Tag, Groups, State (a `StateBadge`: Public, Hidden, Ignored, and Unreviewed beside it), Last heard ("4 min ago", or Never), Calls (30 days). Each sorts; Last heard and Calls start most recent or most first; empty values go last either way (ArchiveUx's `sortRows`) |
| **Search** | ID, label and name, ignoring case, a substring of each, as typed. "Showing 12 of 1,000 talkgroups" says what's left |
| **Filters** | System (with several), Group, Tag, State, "Not heard in (days)" (whole days; never heard counts), "Unreviewed only"; all together; Clear. The choices are the values the rows have |
| **Unreviewed** (R21) | An info `Banner` counts them and shows them in one click; the State column's Unreviewed badge, and its sort, put them together |
| **Editing** | Edit opens the talkgroup in a panel above the table (its heading takes the focus; its row gets the open row's ring). One at a time: opening another closes it. Cancel and Save give the focus back to the row's Edit |
| **The form** | Label, Name, Tag (Required, the tags as suggestions), Groups (comma-separated), State, Type, Frequency (MHz), Sort order (Required), Delay (minutes, Required), Alert, LED colour, Fireground channel number. ArchiveUx section 5: problems under their fields, the focus on the first, the top line counting them; the server's refusals the same way, by their code |
| **Taken from elsewhere** (ADR 0006) | Alert and LED colour's first choice is null, written as where it comes from now: "Taken from its tag admin: chime", "…its group North…", "…its system EX…", or "Taken from elsewhere: none set". It follows the tag and groups as typed. Choosing it sets the talkgroup back to null |
| **Saving** | `PATCH` with only the fields that differ from the row, so another admin's change to other fields isn't overwritten. Every `PATCH` marks the talkgroup reviewed (ADR 0003), so an unreviewed talkgroup is reviewed by saving it unchanged; a reviewed one unchanged sends nothing ("Nothing to change") |
| **A25** | The server's `409 fireground_channel_taken` shows under the fireground field, in its words: `Fireground channel 41 is already talkgroup 1002 "Fireground 41" in system EX.` |
| **State from the row** (R30, R31) | More actions has Recordings and the other two states. **Hide** saves at once; **Ignore…** and **Make public…** ask first, in a modal `Confirm`. The edit form's state asks the same, inline under the form. The change reaches listeners through the catalogue's generation (ADR 0006), as any writer's does |
| **Recordings** (R34, A13) | Recordings opens the talkgroup's calls (ADR 0026's list: newest first, a hundred a page, "Show older calls") in the panel, each with its state, length and Play. It plays through a `Replay` of the page's own (ADR 0016's output), never the layout's player, queue or dock |
| **Rejected calls** (R17) | `GET /rejections`' talkgroup counts in ADR 0003's words, at the top of the page ("TG 4711: 38 calls rejected, not in the list"), the system's label first with several |
| **Type and frequency** (R22) | ADR 0030's `type` and `frequency` (hertz). The form shows MHz to six places; saved with `PATCH` only, as `PUT` clears what it isn't given |

## Editing in a panel, not in the row

R50 asks for inline editing, one talkgroup at a time. ArchiveUx 0.5.0's `DataTable` has no row that opens under another, and its rows are its own (keyed, sorted and stacked inside the component), so a form can't be put between two of them from the page. Twelve fields in one table row also don't fit 1,280 px, and on a phone the row is a card already.

So the form opens in a panel on the page, above the table and its filters' answer, never in a dialog: the table stays as it was behind it, sorted and filtered, and the row being edited carries ArchiveUx's open-row ring (`--border-width-strong`, `--accent`), so it's clear which talkgroup the form is. Opening scrolls to the panel and puts the focus on its heading ("Edit 1001 Dispatch North"); Save and Cancel put it back on the row's Edit, which scrolls the row back into view. A `DataTable` with an expandable row (a snippet drawn under a row it names) would let the form sit under its row; that's ArchiveUx's to add if the owner wants it (open point 1).

## Asking first

ArchiveUx asks before a destructive action and not before one undone in a click. The three states are each a click from the others, but two of them can't be taken back:

- **Ignoring** refuses the talkgroup's calls from then on (R18): what's said while it's ignored is never recorded.
- **Making public** a hidden or ignored talkgroup shows its recordings, and the calls patched with it, to everyone at once (R30). Once heard, it can't be unheard.
- **Hiding** only takes away, and making it public again is the case above, which asks. So hiding saves at once: an admin hiding a talkgroup in a hurry (something said that shouldn't be public) isn't slowed.

From a row, the question is a modal `Confirm`: the table is long at every width, so a panel under it or above it would be off-screen (ArchiveUx section 4, "Confirming"). Cancel takes the focus; afterwards it returns to the row's More actions. In the edit form, the question is inline under the form, where the Save was pressed.

## Playing a row's recordings

R34 lets an admin play a hidden talkgroup's recordings from its row; R34 and A13 also say an admin never hears one in the listener. The listener's player is the layout's (ADR 0012): its queue, its dock and its replay. Playing through it would put a hidden talkgroup's call in the dock, under now playing, on every page. So the page makes a `Replay` of its own (ADR 0016's output: one `AudioContext` of its own, the recording read whole and decoded by ADR 0011's decoder), started from the press, stopped when the panel closes or another talkgroup's opens. The dock never knows: if the admin was listening live, the live calls play on beside it (open point 2). It plays ADR 0026's `recording.url`, the admin route, so nothing public is asked. Playback isn't audited (R34, R54).

## Type and frequency

R22's type and frequency came with the import and export ([ADR 0030](0030-the-setup-export-and-import.md), migration 00024): the admin API's `type` (free text up to 32 bytes, such as trunk-recorder's mode `D` or `T`) and `frequency` (whole hertz, 1 Hz to 100 GHz, or null). Both are the admin's notes; nothing public shows them. The page saves them, as every field, with `PATCH`, never `PUT`, which clears what it isn't given.

The form shows the frequency in MHz, as admins write it, with up to six decimal places, converted as text and not as a float, so 851.0125 MHz is exactly 851012500 Hz both ways. The audit log records both with no more code (ADR 0029: the entry is the admin API's shape).

## Speed

Measured on cld01 (8 cores) in the browser tests' Chromium at 1,280 px, with 1,000 talkgroups (the A23 test's rows), while other test runs shared the machine (load average 10 to 39), so these are a busy machine's numbers, from the test's own clock:

| What | Drawing all 1,000 rows | Drawing 100, more when asked |
|---|---|---|
| Opening the page to its rows drawn (sign-in done) | 5-26 s; the test's 5 s wait failed under load | 1.7-2.6 s |
| Each letter of "north" to the count and the table updated | | 0.26-0.93 s |

All 1,000 drawn, each row with Edit and ArchiveUx's More actions (a button, its tooltip, its list and three window listeners), took seconds, and again at every sort and at every letter that widened the match. So every row is loaded, searched, filtered and sorted in the browser, and the table draws the first hundred of the result in its order; **Show 100 more** and **Show all** at its foot draw the rest (the issue leaves "whether rows are rendered as they scroll" to the slice). The count above the table always says how many match ("Showing 1,000 of 1,000 talkgroups"), and the foot how many are drawn.

## Considered options

| Decision | Rejected | Why |
|---|---|---|
| Rows | Drawing all 1,000 at once | Seconds to draw on a busy machine (above), and again at every sort and every letter typed that widens the match |
| Rows | Drawing rows as they scroll into view | Adds rows the keyboard and the tests can't predict; a button at the foot is plain and does the same |
| Rows | Paging, or a search asked of the server | R50 and A23: no paging, no further request |
| Search | Matching words' starts only, as the catalogue's search ranks them | An admin looks for a part of an ID or a label ("41" in "FG 41"); a substring finds both |
| Search | Groups and tags too | Each has its filter; R50 names ID, label and name |
| Editing | A row that becomes a form | Twelve fields don't fit a row; `DataTable` doesn't draw a form under a row |
| Editing | A dialog | Covers the table, and the form is long on a phone |
| Saving | Every field, every time | Overwrites what another admin changed meanwhile in another field |
| State | Asking before hiding too | Hiding is the safe direction, often wanted fast |
| State | Never asking | Ignoring loses calls; making public can't be taken back |
| Playback | The listener's player and dock | A hidden talkgroup would be in now playing, on every page (R34) |
| Groups | A list of checkboxes | A deployment's groups are many; a new one is made by naming it, as the API does |

## Consequences

- Every later admin page, Units (issue #114) first, can follow it: one `DataTable`, filters in a search form, one thing open in a panel above the table.
- Opening the page reads the whole table once (ADR 0026: about a tenth of a second warm, under a second and a half cold). Nothing more is asked until a save; last heard and the counts are as they were when it opened, until it's opened again.
- The admin menu's group heading now links to Talkgroups.

## Open points

1. **Editing under the row.** ArchiveUx's `DataTable` could take a snippet drawn under one row it names, so the form sits under its row (R50's "inline" read literally). It's ArchiveUx's change, then one line here.
2. **Live listening during an admin's playback.** The page's player plays beside live listening; pausing live listening for it, as replaying in recent calls does (ADR 0016), would reach into the listener's player. Left as is.
3. **Groups' separator.** A group name with a comma can't be typed in the form (the API takes it). Rare; a chip input would fix it.
4. **Rows drawn a hundred at a time** (Speed). R50's "1,000 rows load at once" holds for the data; the browser's own find sees only the rows drawn. If the owner wants all drawn, a lighter row (one actions button whose list is drawn once, for the row it's opened on) is the change, in ArchiveUx's `MoreActions` or here.

## How each choice is verified

| Choice | Verification |
|---|---|
| A23: 1,000 rows from one request with no parameters; "north" filters as typed with no request; Show more and Show all draw every row, still with no request | `web/tests/talkgroups.spec.ts` (counts requests) |
| Each filter, together and cleared; each column's sort both ways over all 1,000 before the first hundred are drawn (the 100th row checked), empty values last; all 1,000 drawn in order | `web/tests/talkgroups.spec.ts` |
| A problem under its field and the focus on it; A25's refusal under the fireground field, naming "Fireground 41", nothing saved; Cancel's focus | `web/tests/talkgroups.spec.ts` |
| Type and frequency saved; an alert taken from the tag, shown, set and set back to null; an unchanged save sends nothing | `web/tests/talkgroups.spec.ts` |
| Ignoring and making public ask in a dialog; cancelled, nothing changes | `web/tests/talkgroups.spec.ts` |
| A13, the browser's half: a 1003 recording played from its row, not in the dock; the Listen page signed in as that admin has no 1003 | `web/tests/talkgroups.spec.ts` |
| Hiding 1001 from its row withdraws its live call (A11's path) and takes it out of the catalogue at once; making it public asks | `web/tests/talkgroups-live.spec.ts` |
| The page refuses signed out and a listener, as the API does; the disk warning shows on it | `web/tests/talkgroups.spec.ts`, `web/tests/admin.spec.ts` |
| A36: axe in both looks and themes at 375 px and 1,280 px, with a row open for editing and its problems, the recordings playing, More actions open and the dialog | `web/tests/talkgroups.spec.ts` |
| Every change is audited (the `PATCH` route's probe) | `cmd/radioarchive`, `TestIntegrationAuditHarness` |
| Tokens only (R80) | `npm run check:tokens` |
