# RadioArchive: Specification

**Status:** Draft 0.1 (2026-11-24), not yet approved for implementation
**Owner:** RIKIKU

## How to use this spec (implementing agent: read first)

1. Read this whole document, `CLAUDE.md` and `GLOSSARY.md` before doing anything.
2. **Principles (section 1) override every other section.** Where two principles conflict, the lower-numbered one wins.
3. **`OPEN` means undecided.** Don't guess. Stop and ask the owner. Work that doesn't depend on an OPEN item can continue.
4. **The stack is decided**: `docs/adr/0001-technology-stack.md` (accepted 2026-11-15). Implementation may start once this spec is approved (merged with its status set to Approved).
5. "Done" means every acceptance check in section 8 passes, plus the end-to-end verification in section 9, with evidence (commands run and their output).
6. **Implementer discretion.** Where this spec is silent, choose the simplest option consistent with the principles, constraints and requirements, and record it in an ADR (`docs/adr/`). Ask the owner only when the choice changes what listeners see, what data is kept, privacy or security. UI presentation details are the implementer's choice within the requirements **and ArchiveUx's rules** (R80). The defaults in section 12 apply.
7. **ADR approval:** an ADR is approved when its status reads `accepted`, set by the owner or by Claude at the owner's explicit instruction, and committed.
8. **Repos:** this repo is `github.com/RIKIKU/RadioArchive`. Its companion is `github.com/RIKIKU/PagerArchive`; the shared design system is `github.com/RIKIKU/ArchiveUx`. Each deployment's own setup (systems, talkgroups, units) is private configuration, never committed here.
9. **Where each decision came from.** Every section names the decision ticket on the planning map ([RadioArchive: live radio listening and archive, combined with PagerArchive](https://github.com/RIKIKU/RadioArchive/issues/11)) that holds its reasons. The spec states the decision; the ticket holds why.

---

## 1. Principles

- **P1. Hidden talkgroups never leave the server.** A browser, phone app or link preview never receives a hidden or ignored talkgroup's ID, name or audio, nor anything that reveals one exists. One gate on the server enforces it for every public surface (section 6.6). Telemetry carries talkgroup IDs but never talkgroup or unit names.
- **P2. Live means while the call is still in progress.** A listener hears a call about a second behind the radio, not after it ends. The one exception is a talkgroup an admin delays (R23).
- **P3. Listening needs no sign-in, and sign-in never unlocks anything.** Every listener, signed in or not, hears the same public talkgroups with the same features and the same limits. Signing in only saves preferences. Admins are the only people with more, and only in the admin.
- **P4. The server keeps nothing about anonymous listeners.** No accounts, listening sets, baskets or IP addresses are stored for a listener who hasn't signed in. Heavy-use counters live in memory only (R70).
- **P5. Retention always wins.** No recording is kept past its retention period for any reason: not a basket, a share link, a conversation, or a hidden state.

## 2. Problem and users

People who follow a P25 trunked radio network run **trunk-recorder** to record it. They want to hear calls **live**, choose which talkgroups they hear, replay and download recent calls, and share them, all from a site that is public but unadvertised, without signing in. Admins need to manage about 1,000 talkgroups without fighting their tools.

RadioArchive is a self-hosted, resource-light live listener and archive for trunk-recorder. It is a companion to PagerArchive: alone it is a complete product; in the **combined deployment** a user moves between the two as if they were one app, and a page naming a fireground channel links straight to it on the radio.

Scale it is built for: about 1,000 talkgroups; one recorder now and a second planned that hears different calls; about 6 listeners at peak; recordings kept for a month by default.

## 3. Glossary

The vocabulary is in [`GLOSSARY.md`](GLOSSARY.md), and this spec uses it exactly: call, transmission, live call, recording, talkgroup, patch, hidden, ignored and unreviewed talkgroup, tag, group, unit, system, recorder, recorder client, recorder plugin, listening set, priority talkgroup, hold, fireground channel, fireground link, conversation, story, download basket, combined deployment.

Two terms used here that are UI, not domain:
- **Listener**: the web page (and, later, the phone app) that plays live calls.
- **Admin**: the signed-in pages, API and PowerShell module for managing the deployment.

## 4. Scope

### In scope for v1

The recorder plugin and recorder client; ingest, recordings and retention; the live feed; the web listener with recent calls, search, share links, downloads and the download basket; talkgroup, unit and system admin with import, export and an audit log; the PowerShell module; heavy-use limits; install, upgrades, backups and monitoring; the combined deployment with PagerArchive, including fireground links. The server's API is built so a future phone app (Android, with Android Auto) needs nothing more from it (section 6.12).

### Out of scope

- **The phone app itself**, including Android Auto. The server plans for it (section 6.12); the app gets its own spec.
- **Notifications** of any kind. A talkgroup's alert sound (R22) plays only in an open listener.
- **Importing past recordings** from another system.
- **Merging duplicate calls** from several recorders. Two recorders hearing one call store two calls (R13).
- **Conventional and analog systems** (owner, 2026-11-24). v1 is P25 trunked, digital audio at 8 kHz. The import skips such rows and reports them (R52).
- **Doing PagerArchive's or ArchiveUx's work.** This repo lists what they need (section 11) and depends on their releases.
- **TLS, DDoS protection, firewalling, blocking by country.** Left to the deployment (R74).
- **iOS screen-off listening** until the first-milestone prototype says what it takes (R40). If it becomes a requirement, the fallback is LL-HLS (ADR 0001).

## 5. Architecture (decided)

From ADR 0001 ([Which technology stack does RadioArchive use?](https://github.com/RIKIKU/RadioArchive/issues/7)):

```
trunk-recorder ──(recorder plugin)──UDP localhost──▶ recorder client ──WebSocket/HTTPS, client key──▶ API ──WebSocket per listener──▶ browser / app
   PCM + call identity                    Opus encode, disk queue             store, gate, filter       Opus frames + call events
   call_end + file name                   read finished recording             by listening set
                                                                               │
                                                     PostgreSQL ◀──────────────┤──▶ recordings volume (Opus/Ogg files)
                                                     Authentik (OIDC) ◀────────┘
```

- **Recorder host:** trunk-recorder (our image, with the recorder plugin) beside the recorder client. amd64 or arm64.
- **Server:** one static Go binary (API, embedded SvelteKit frontend, recordings), PostgreSQL 18, Authentik. amd64.
- The server **never transcodes**. It receives Opus and stores Opus.
- It sits behind the deployment's own reverse proxy, which handles TLS. The docs include a working nginx example (R85).

## 6. Requirements

### 6.1 Recorder plugin and recorder client

Decided in [What can trunk-recorder send while a call is still in progress?](https://github.com/RIKIKU/RadioArchive/issues/2), [Which technology stack does RadioArchive use?](https://github.com/RIKIKU/RadioArchive/issues/7), [How much disk does a month of recordings need?](https://github.com/RIKIKU/RadioArchive/issues/21) and [How is RadioArchive deployed, upgraded, backed up and monitored?](https://github.com/RIKIKU/RadioArchive/issues/24).

- **R1. Recorder plugin.** A thin C++ trunk-recorder plugin in this repo (`recorder/plugin/`), built in CI against a **pinned trunk-recorder release** (v5.2.1 at the time of writing). For each call it sends, to UDP on localhost only:
  - the PCM audio as trunk-recorder decodes it, tagged with `sys_num`, `call_num`, `start_time`, the system's `shortName`, talkgroup, patched talkgroups and the transmitting unit;
  - a transmission boundary whenever the unit talking changes;
  - `call_end` with the same identity and the recording's file name, once trunk-recorder has written it;
  - a heartbeat carrying trunk-recorder's control-channel decode rate per system (`system_rates`).
  It does no authentication, encoding or buffering of its own. It replaces trunk-recorder's simplestream.
- **R2. One call.** One trunk-recorder call is one **call**, identified by (recorder, `sys_num`, `call_num`, `start_time`). Each change of unit within it is a **transmission**. If trunk-recorder splits a long exchange into two calls, they stay two calls; nothing merges them (owner, 2026-11-24).
- **R3. Recorder client.** Go with cgo and libopus, beside trunk-recorder (container or systemd; amd64 + arm64). It:
  - encodes live PCM to Opus as it arrives (R4) and forwards the frames at once;
  - on `call_end`, reads the finished recording and encodes it to Opus/Ogg (R4), then uploads it;
  - holds **one authenticated connection** to the server using its own **client key** (R10);
  - relays the plugin's heartbeat;
  - de-duplicates the copies trunk-recorder sends of a patched call, so each call is sent once (R16).
- **R4. Encoding.** 12 kbit/s VBR, narrowband, `OPUS_APPLICATION_VOIP`, 20 ms frames, for both live frames and recordings. Recordings are Ogg/Opus with no comment padding.
- **R5. Disk-backed queue.** While the server can't be reached, the client keeps a **queue of unsent recordings on disk**. Each entry points at trunk-recorder's own file and is removed only after the server acknowledges the upload. On reconnect the oldest go first. Entries older than `QUEUE_MAX_AGE` (default 24 h) are dropped and counted. Live audio missed during an outage is lost: frames that arrive too late to be live are dropped, and the recording still arrives.
- **R6. Versions.** The server accepts recorder clients up to **one minor version** behind its own. An older client is refused with an error naming both versions, and counted in a metric.

### 6.2 Recorders and systems

Decided in [What does adding a second recorder take?](https://github.com/RIKIKU/RadioArchive/issues/23).

- **R10. Recorders are managed only from the server shell.** `radioarchive recorder add "<name>"` creates a named recorder and prints its client key once (only its hash is stored). Also `recorder list`, `recorder rotate-key <name>` (the old key stops at once) and `recorder retire <name>` (revokes the key; its calls stay until retention). Nothing about keys passes through the web admin, the API or the PowerShell module. A recorder's name is shown only to admins.
- **R11. Systems.** A system is a record admins create (web admin, admin API, PowerShell module, or import, which merges on system ID). It owns its talkgroups, its units and the auto-create switch (R21). A system that still has talkgroups or calls can't be deleted.
- **R12. Attachments.** An admin attaches each **recorder + `shortName`** pair to exactly one system (web admin and admin API; audited). Two recorders on one network attach to the same system and share its lists. A change affects only future calls. Calls from a pair that isn't attached are rejected and counted per pair ("North hill / `metro`: 312 calls rejected, not attached to a system").
- **R13. Overlap.** Keeping two recorders' coverage apart is the admin's job, in trunk-recorder's config. The server stores both copies and never blocks or merges them. The Recorders page warns about overlapping calls on the same system and talkgroup from two recorders ("North hill and South ridge both recorded TG 1203: 47 overlapping calls in the last 24 h").
- **R14. Noticing a recorder that has stopped.** Three signals per recorder:
  1. **Disconnected:** its client's connection has dropped.
  2. **Deaf or down:** no heartbeat (trunk-recorder down), or a decode rate near 0 for an attached system (control channel lost).
  3. **Quiet:** no calls for N minutes, an optional per-recorder threshold, off by default.
  The **Recorders** page shows each recorder's state (connected, last connected, last call, client version and whether it's out of date, decode rate per attached system) with a warning badge when any signal trips. The same values are metrics (R90).
- **R15. What listeners see of recorders.** Never which recorder captured a call. Once any recorder has been disconnected or deaf for `RECORDER_NOTICE_AFTER` (default 2 min), the listener shows "Some talkgroups aren't being received right now". It names no recorder or talkgroup, clears as soon as the recorder recovers, and quiet never triggers it. The system name is shown to listeners only when the deployment has more than one system.

### 6.3 Ingest and calls

- **R16. Patches** (owner, 2026-11-24). A call on a patch is stored **once**, under the talkgroup trunk-recorder recorded it on, with the other patched talkgroup IDs kept with it. A listener hears it if **any** talkgroup in the patch is in their listening set. If **any** talkgroup in the patch is hidden or ignored, the whole call is treated as that talkgroup's state: ignored wins over hidden, and hidden wins over public. Listeners never see the patched IDs of a hidden talkgroup.
- **R17. Unknown talkgroups.** A call on a talkgroup ID its system doesn't have is recorded only if the system's auto-create is on (R21). Otherwise it's rejected, not stored, and counted per system and talkgroup ID, shown to admins on the talkgroup admin page ("TG 4711: 38 calls rejected, not in the list").
- **R18. Ignored talkgroups** are not recorded: the server refuses their live frames and recordings. Rejections are counted.
- **R19. What a call stores.** Opaque call ID (random, non-sequential, R60), recorder, system, talkgroup, patched talkgroups, start and end time (UTC), duration, each transmission's unit ID and offset, and the recording's path and size. A call's live frames are matched to its recording exactly by R2's identity. A recording that hasn't arrived 10 minutes after its call ended is counted and alerted (R91).
- **R20. Storage.** Recordings are files on a Docker volume, one per call, with metadata in PostgreSQL. They're served with range requests (`http.ServeContent`). They're never stored in the database.

### 6.4 Talkgroups

Decided in [What does an admin need in order to manage about 1,000 talkgroups?](https://github.com/RIKIKU/RadioArchive/issues/13) and [What form do the shared UI and the combined deployment take?](https://github.com/RIKIKU/RadioArchive/issues/6).

- **R21. Where talkgroups come from.** Import (R50), an admin adding one by hand, or **auto-create**: an optional switch per system, off by default. With it on, a talkgroup first heard on a call is created **public** and flagged **unreviewed** until an admin names or checks it.
- **R22. What a talkgroup holds:**
  - talkgroup ID (decimal) and system, fixed once set;
  - **label** (the short alpha tag) and **name** (the description);
  - exactly one **tag** (required) and any number of **groups**;
  - type, frequency, sort order;
  - **delay** (R23);
  - **alert**: one of a few short sounds shipped with the app, or none. It plays in an open listener when a call starts on that talkgroup, only if the listener has switched alerts on in their playback settings (off by default). It is not a notification (owner, 2026-11-24);
  - **LED colour**: one of a fixed set of about 8 named colours, which are ArchiveUx tokens measured as marks (3:1 on `--bg` and `--surface`, every look and theme). It's shown as a mark beside the talkgroup's name when its call plays, and is never the only cue (owner, 2026-11-24);
  - **fireground channel number** (optional): a whole number, unique across the deployment (R100);
  - **state**: public, hidden or ignored (section 6.6). Imported and hand-added talkgroups default to public;
  - **unreviewed** flag, set only by auto-create.
  Groups, tags and systems also carry alert, LED colour and sort order. How they combine for a talkgroup with none of its own is the implementer's choice (section 13).
- **R23. Delay** (owner, 2026-11-24). A delay of N minutes means the talkgroup's calls exist nowhere public until N minutes after each call **ends**: not in the live feed, recent calls, search, share links or downloads. When that time passes, the call enters listeners' queues as a finished call (if its talkgroup is in their listening set) and plays from its recording, labelled "delayed N min". The server buffers no live audio for it. Admins see the call at once.
- **R24. "Talkgroup name"** (owner, 2026-11-24). Wherever this spec says a talkgroup's **name** is shown (now-playing, call cards, recent calls, Android Auto), it is the name, falling back to the label when the name is empty. The label is shown beside it in the admin table and the catalogue. File names use the label (R63).

### 6.5 Units

- **R26.** A unit is a radio ID with an **alias**, in its system. An optional ID range lets one alias cover a span; an exact unit ID beats a range that also covers it. Units have no state, groups or tags. They get the same table, search, import and export as talkgroups.
- **R27.** Listeners see a unit's alias, or its ID when it has none.

### 6.6 Hidden talkgroups: the gate

Decided in [How are hidden talkgroups kept hidden everywhere?](https://github.com/RIKIKU/RadioArchive/issues/19).

- **R30. One state, checked on every request.** Public, hidden or ignored is held on the talkgroup only; calls carry no state. Hiding a talkgroup hides every recording it already has, at once; making it public again exposes them again. Changing it to ignored stops recording, and its existing recordings are treated as hidden until retention deletes them. Nothing is deleted when the state changes.
- **R31. Hiding during a live call.** The server cuts the live audio at once and drops queued calls on that talkgroup from every listener's feed. Listeners see nothing beyond the call ending.
- **R32. One gate on the server** sits in front of every public surface: the catalogue, browse and search, recent calls, unit search results, call details, single-call and share links, link previews, downloads and zips, the live feed and its events, now-playing, and the phone-app endpoints (section 6.12). No hidden talkgroup's ID, name or audio passes it, nor a patched call involving one (R16). Integration tests cover every surface (A10).
- **R33. Fireground and share links to hidden talkgroups.** A fireground link whose number maps to a hidden talkgroup shows "This channel isn't available", exactly as for a number that maps to none (R101). Share links drop hidden talkgroup IDs silently (R44). A call link to a hidden call looks exactly like one to a deleted call (R61).
- **R34. Admins** can play a hidden talkgroup's **recordings** from its row in the web admin and through the admin API and PowerShell module. They never hear hidden talkgroups live or in the listener. Playback isn't audited.
- **R35.** Hidden talkgroups' recordings follow the same retention as public ones.

### 6.7 The live feed

Decided in [How can browsers and Android phones play live audio with only seconds of delay?](https://github.com/RIKIKU/RadioArchive/issues/3) and ADR 0001.

- **R36. One WebSocket per listener** (`/api/v1/live`), no sign-in. The listener sends its listening set (talkgroup IDs, plus any added for the session) and may change it at any time. The server filters by it, after the gate (R32), and sends:
  - **call events**: start, transmission (unit changes), end, and "call withdrawn" (R31), each with an event ID, the call ID, talkgroup ID, name, group, unit alias or ID, system name (when there are several) and start time;
  - **Opus frames**, each tagged with its call, about a second behind the radio;
  - **feed notices**: the recorder notice (R15) and "the radio is full" (R71).
- **R37. Resuming.** Reconnecting with the last event ID replays missed **events** for up to 5 minutes. Missed **audio** is never replayed: an ended call missed this way is played from its recording (R43).
- **R38. Delay budget.** From the recorder plugin's first PCM packet to the browser's first decoded frame: p95 under 2 s, target about 1 s. Measured in CI (A6) and as a metric (R90).
- **R39. Browser playback.** WebCodecs `AudioDecoder` (Opus) into Web Audio, with a WASM Opus decoder when `AudioDecoder` or its Opus support is missing.
- **R40. iOS.** A throwaway prototype on a real iPhone, screen off and in the background, in M1. Its result decides whether LL-HLS is needed. Until then, iOS with the screen on is supported, and screen-off is not promised.

### 6.8 The web listener

Decided in [How does the web listener behave?](https://github.com/RIKIKU/RadioArchive/issues/8).

- **R41. Listening set.**
  - Saved in the browser by default. A **share link** encodes the set (and priority marks) to copy it to another device or a bookmark. Opening one offers to replace the browser's set; hidden IDs in it are dropped silently (R33).
  - Optionally saved in the listener's Authentik profile when signed in (R110), with priority marks and playback settings. First sign-in: if the profile has no set, the browser's is saved to it; if it has one, a one-time prompt offers to replace the browser's set with it. No merge.
  - **Hold** and talkgroups added for the session are never saved.
- **R42. Choosing talkgroups.** Browse by group (switch on a whole group or single talkgroups) and search by name, label, ID or tag. Switching on a group adds its talkgroups as they are at that moment, not a live link to the group. The catalogue groups by system when there are several (R15).
- **R43. Overlapping live calls.**
  - One call plays at a time; the rest queue in order of start time.
  - A listener marks talkgroups as **priority**: a priority call moves to the front of the queue but never cuts off the call playing.
  - A queued call that has ended by its turn plays from its recording.
  - Nothing is dropped automatically; falling behind is the listener's to manage.
- **R44. Controls.**
  - The page opens with a **Start listening** button (browsers need a user gesture). Nothing queues before it's pressed.
  - **Skip**: end the current call and move to the next.
  - **Jump to live**: clear the queue.
  - **Pause** keeps queuing; resume carries on through the queue.
  - **Hold**: hear only the playing call's talkgroup until switched off. Shown clearly; it doesn't change the saved set.
  - Switching away from the radio to PagerArchive (same tab) stops the audio.
- **R45. Now playing** (in the dock, R81): talkgroup name and group; the transmitting unit's alias (else ID), updated per transmission; a **Live** badge or **behind by N s**; "delayed N min" (R23); call duration so far; the number of queued calls; the LED colour mark (R22); the system name only when there are several. **Share** and **Add to basket** (R64) act on the playing call.
- **R46. Recent calls and search.** This is the archive search.
  - A recent calls list, defaulting to the listening set, newest first, with load more.
  - Filters: any public talkgroup(s), date and time range, unit; newest or oldest first. No free text.
  - Pressing a call plays it; **play from here** plays the following calls in order. Replaying pauses live listening; live calls keep queuing meanwhile.
  - Each row has **Share**, **Download** and **Add to basket**.
  - Limits on pages and ranges are in R73.
- **R47. Talkgroups added for the session** (by a fireground link, R101, or an in-progress call link, R61) last until the tab closes, shown apart from the saved set as "added for this session", with one tap to keep. A talkgroup already in the set is unchanged.
- **R48. Playback settings**: alerts on or off (R22, off by default), and the look (R82). Saved like the listening set.

### 6.9 Admin: talkgroups, units, import, export and audit

Decided in [What does an admin need in order to manage about 1,000 talkgroups?](https://github.com/RIKIKU/RadioArchive/issues/13) and [What shape must an imported talkgroup and unit setup take?](https://github.com/RIKIKU/RadioArchive/issues/12).

- **R50. Finding and editing (web admin).** One table of every talkgroup with no paging: 1,000 rows load at once. Instant search across ID, label and name. Filters for system, group, tag, state, unreviewed and "not heard in N days". Every column sorts. Columns include **last heard** and **calls in the last 30 days**. Editing is inline, one talkgroup at a time; there's no bulk editing in the browser. Rejected calls (R17) show beside it. A hidden talkgroup's row can play its recordings (R34).
- **R51. Admin API and PowerShell module.** The admin HTTP API does everything the web admin does. A PowerShell module in this repo wraps it (for example `Get-Talkgroup -Group X | Set-Talkgroup -State Hidden`). Sign-in as in PagerArchive: Authentik via OIDC, with PKCE in a browser or device code without one. Admins only; **no API keys**.
- **R52. Import** (PowerShell module only). Accepts:
  1. a **whole-setup JSON**: `systems[]`, each with `talkgroups[]` and `units[]`, plus top-level `groups[]` and `tags[]` (the export's shape, R53);
  2. trunk-recorder's **talkgroup CSV** (Decimal, Alpha Tag, Description, Tag, Group) and a two-column **unit CSV** (ID, alias), for a named system.
  It merges on system + talkgroup ID (and system + unit ID), shows a preview first (new / changed / unchanged, with the changed fields listed), and applies only once the admin confirms (`-WhatIf` / `-Confirm`). It adds new talkgroups and updates label, name, tag, groups and fireground channel number. It **never** changes state and never deletes anything missing from the file. It skips placeholder rows whose label is just the talkgroup ID, conventional or analog rows (section 4), and reports both. Sort order is optional. A duplicate fireground channel number stops the import and changes nothing (R100).
- **R53. Export** from the browser and the module, in the import's JSON shape plus state, unreviewed and fireground channel number, so a setup round-trips.
- **R54. Audit log.** Read-only and kept a long time (not subject to recording retention): who changed what and when, from the browser, the module and imports alike, including system attachments. Playback isn't in it (R34).
- **R55. Admins.** An admin is a member of the Authentik group `radioarchive-admins`. The first admin is `INITIAL_ADMIN_EMAIL`, as in PagerArchive. In the combined deployment, being a PagerArchive admin doesn't make someone a radio admin (owner, 2026-11-24).

### 6.10 Share links, downloads and the download basket

Decided in [Can a listener link to a single call or download its recording?](https://github.com/RIKIKU/RadioArchive/issues/20).

- **R60. Call IDs** are opaque and non-sequential, so gaps reveal nothing.
- **R61. A call link** (from Share on a recent-calls row or now-playing) opens the radio with a **call card**: talkgroup name and group, start date and time, duration, units heard, and **Play**. Live listening doesn't start; **Start listening** stays available. Below the card: recent calls filtered to that talkgroup, scrolled to that time, with play from here.
  - Opened while the call is still in progress: the card says "In progress" and offers live listening, adding the talkgroup for the session (R47). Once the call ends, the same link plays the recording.
  - Deleted, hidden or ignored: one identical "This call isn't available" (same status, same page, no reason, nothing naming the talkgroup).
  - Link previews show talkgroup name and time, with no audio. Unavailable calls get the site's generic preview.
- **R62. Downloading one call.** Anyone, with no sign-in, from the call card and recent-calls rows: the stored Opus file byte for byte. No transcoding, no MP3, nothing written into the file.
- **R63. File name** `<YYYY-MM-DD>_<HHMMSS>_<talkgroup label>_<talkgroup ID>.opus`, in the deployment's time zone (`TZ`), the label reduced to file-safe characters.
- **R64. Download basket.** Listeners mark calls into a basket from recent calls, the call card and now-playing (a live call becomes downloadable once recorded). Saved like the listening set (R41). In the basket:
  - calls are grouped into named **conversations** (optional note); a call can be in several; a conversation can be played in order;
  - each item shows its retention countdown ("deleted in 3 days"); deleted calls and calls hidden since they were added both show "No longer available" and are left out of downloads;
  - downloads take the whole basket or one conversation; **Download new since last time** is offered, and a full re-download while the files still exist; the basket stays after a download, with **Clear**.
- **R65. Zips.** One zip, stored without compression, built while it streams and never kept on the server. Deterministic: the same call list gives the same bytes, so range requests resume a broken download.
  - Layout: `<NN> <conversation>/<call files>` numbered by first start time, one `.m3u` per conversation, loose calls at the top level.
  - Every zip carries `manifest.json` (the conversations → calls tree with names and notes) and `index.csv` (one row per call): call ID, start and end (UTC and local), duration, system, talkgroup ID, name, label, groups and tag, each transmission's unit ID, alias and offset, and file name.
  - The web download is a GET whose URL carries the encoded call IDs, so the browser's download manager can resume it. A basket too big for one URL, or over 500 calls (R72), is split into "part 1 of N".
- **R66. Sharing a conversation.** The link packs the call IDs into the URL (about 11 characters a call); the server stores nothing. It opens as ordered call cards with **Play all**. Unavailable calls are dropped silently; "This conversation isn't available" if none are left.
- **R67. Call lists in the API.** Endpoints taking a list of call IDs (call details, the basket's availability check, zip download) accept **QUERY** (RFC 10008), plus POST while browser support for QUERY is unsettled.

### 6.11 Heavy use

Decided in [How is the open site protected against heavy use?](https://github.com/RIKIKU/RadioArchive/issues/22). Every number below is an environment variable with this default; the test stack raises them.

- **R70. Where limits live.** The server enforces every limit itself, counting in memory (a restart starts the counts again). One client is one IPv4 address or one IPv6 /64. The address comes from `X-Forwarded-For` only when the request arrives from the one proxy address configured (`TRUSTED_PROXY`, an exact address, never a range); otherwise the connecting address counts. The recorder client and signed-in admins (including the module) are exempt. Signed-in listeners get the same limits as everyone (P3).
- **R71. Live listening.** At most **4** listener WebSockets per client and **50** in total. Existing listeners are never dropped. At the overall cap, a new listener sees "The radio is full right now. Trying again…" and retries every 30 s.
- **R72. Zips.** Each sent at up to **2 MB/s**, all together at up to **8 MB/s**. At most **2** at once per client and **6** in total; a resumed download counts as a new one. A part holds at most **500 calls**.
- **R73. Requests.** **120 a minute** per client across the public API (recent calls, search, call details, single-call downloads, the catalogue). Recording playback (range requests) **600 a minute**. Call-list endpoints take at most **500 IDs**. Recent-calls search pages by start time, at most **100 rows** a page, over a time range of at most **31 days**. Every public query runs under a **5 s** database timeout ("That search took too long. Try a narrower time range."). Over a limit: `429` with `Retry-After`, and the UI says which limit and when to try again ("Too many downloads at once. Try again in 2 minutes.").
- **R74. Scraping and caching.** `robots.txt` serves `Disallow: /`; every response carries `X-Robots-Tag: noindex`; call links keep their preview tags. Recordings are served `Cache-Control: private, max-age=31536000, immutable`. The catalogue is cached in server memory and rebuilt on every talkgroup edit, with no HTTP caching, so hiding takes effect at once. TLS, connection floods, DDoS, blocking by country, fail2ban and firewalling are left to the deployment; the docs say so and give an optional nginx `limit_req` example.

### 6.12 For the future phone app (server only)

Decided in [What does the server need to provide for Android Auto and for listening that runs only while switched on?](https://github.com/RIKIKU/RadioArchive/issues/4) and ADR 0001. The app isn't specified here; the server provides what it will need, all behind the gate (R32) and with no sign-in.

- **R75.** The live WebSocket (R36) is the app's audio and event feed. The app plays it through its own Media3 `Player` and plays silence between calls itself; the server sends no continuous stream.
- **R76.** Call events carry talkgroup and unit display names, for now-playing and Android Auto (title: talkgroup name; subtitle: unit).
- **R77.** One fast catalogue endpoint of public talkgroups with their groups, tags and systems, so about 1,000 talkgroups fit a browse tree of at most 4 tabs with no paging; and talkgroup search by name, for voice commands, including an empty query.
- **R78.** Errors are machine-readable (a stable code and a message). The listening set lives on the device; a signed-in listener's basket and conversations come from their profile (R64). Stories exist only in the app.

### 6.13 Look, frame and layout

Decided in [What form do the shared UI and the combined deployment take?](https://github.com/RIKIKU/RadioArchive/issues/6). Rules: ArchiveUx's `docs/design-system.md`.

- **R80. ArchiveUx.** The UI follows ArchiveUx: tokens, base styles, its rules and its components, installed as `"archiveux": "github:RIKIKU/ArchiveUx#vX.Y.Z"`, locked in `package-lock.json` and bumped deliberately. Every style value is a token; app-specific tokens are not allowed (ArchiveUx rule 1.6: only PagerArchive's map tokens live in an app), so any colour RadioArchive needs is added to ArchiveUx (section 11.2). WCAG 2.1 AA in both looks and both themes. Every form follows ArchiveUx section 5 (the Required tag, errors under fields).
- **R81. Frame.** ArchiveUx's frame: brand, side menu (desktops), bottom bar (phones), account menu, app switcher (combined deployment only), and the **dock**, a bar holding the radio's player (now playing and its controls, R44-R45), on every listener page. Notices and toasts sit above the dock.
- **R82. Looks.** Harbour (default) and Console, light and dark, chosen in the account menu, which works signed out too. Alone, the look is saved like playback settings (R48). In the combined deployment it's the shared parent-domain `look` cookie (R104).
- **R83. Components.** From ArchiveUx: the frame components (batch (a)), `Icon`, `ReleaseNotice`, `Suggest` and `DateRange` (batch (b), with times of day). The live player, the listening-set picker, the download basket, the call card and the admin table are RadioArchive's own, built from ArchiveUx's tokens and row pattern only, because only this app uses them (owner, 2026-11-24).
- **R84. Pages** (menu): Listen (the listener, with the listening set), Recent calls, Basket; Admin: Talkgroups, Units, Systems, Recorders, Audit log, Settings (retention). Phones: Listen, Recent, Basket, More.

### 6.14 Install, upgrades, retention, backups and monitoring

Decided in [How is RadioArchive deployed, upgraded, backed up and monitored?](https://github.com/RIKIKU/RadioArchive/issues/24) and [How much disk does a month of recordings need?](https://github.com/RIKIKU/RadioArchive/issues/21).

- **R85. Install (server)** as PagerArchive: release assets (`compose.yaml`, `env.example`, config tarball), each with a GitHub artifact attestation (`gh attestation verify`). Site changes go only in `compose.override.yaml`. Ports bind to `127.0.0.1`. The nginx example turns buffering off and sets long read timeouts for the listener WebSocket and streamed zips, overwrites `X-Forwarded-For` with the client address, and denies `/metrics`.
- **R86. Install (recorder host).** Each release also ships `recorder-compose.yaml` and `recorder-env.example` (server URL, client key), running trunk-recorder (our image with the plugin) beside the client. Without Docker: the plugin `.so` and client binaries (amd64 + arm64) with `SHA256SUMS` and attestations, under systemd. Recorder clients and the plugin are upgraded by hand.
- **R87. Releases and upgrades** as PagerArchive: weekly releases, security releases sooner, the version by its deliberate-release rules, images tagged by major.minor, base images held 3 days by Renovate. goose migrations run at API start and stay quick; backfills are long jobs. An optional **auto-upgrade** script (verify, back up, install, health check, roll back) whose health check requires a **call** to arrive within 30 min, applied only if at least 20 calls arrived in the 2 h before. Listener sockets drop during an upgrade and reconnect with their last event ID.
- **R88. Retention.** An admin sets the retention period in days in Settings (default **30**); shortening it asks for confirmation, naming how many recordings will go. The nightly job deletes recordings and their call rows older than the period, and recording files that have no row after a grace period. Share links and basket items to deleted calls become unavailable (R61, R64). Below `RECORDINGS_MIN_FREE` (default 5 %) free on the recordings volume, the server deletes the **oldest recordings early**, logged, counted and alerted as "retention shortened". A warning shows to admins at 15 % free. The docs give the sizing formula: disk ≈ call-hours × 5.7 MB + calls × 2.1 KB, and the volume at a month's need × (retention days + 1) / 30 × 1.5.
- **R89. Backups.** Recordings are **not** backed up. A nightly `pg_dump` with a restore check covers RadioArchive's database (setup: systems, talkgroups, units, groups, tags, attachments, recorder key hashes, settings, audit log) and Authentik's (admins, and the listening sets and baskets saved in profiles), with **call data excluded** (`--exclude-table-data` for calls, transmissions). The nightly run also writes the setup export (R53) into the backup folder. `.env` and `compose.override.yaml` aren't backed up; the docs list exactly which files the operator must keep safe. Backup and upgrade state show to admins as ArchiveUx alerts.
- **R90. Telemetry.** An allowlist as in PagerArchive: `/metrics` behind `METRICS_TOKEN`. No listener IP addresses in logs, traces or metrics. Talkgroup IDs are allowed; talkgroup and unit names never (P1). Metrics include each recorder's signals (R14), rejected calls (R12, R17, R18), overlapping calls (R13), live delay (R38), the client queue's age and size (R5), refused old clients (R6), recordings disk, and limit hits per limit (R70-R73).
- **R91. Alerts and dashboards** ship as release assets (Alloy scrape snippet, alert rules, dashboards) that drop into **PagerArchive's monitoring stack**; a deployment without PagerArchive downloads that stack from PagerArchive's release. Alert rules (thresholds as `params:`): recorder disconnected 2 min; deaf or down 2 min; quiet (only where set); calls rejected from an unattached recorder + `shortName` (any in 15 min); recording missing 10 min after call end; live delay p95 > 2 s for 5 min; recordings disk (warning, and retention shortened); client queue oldest > 1 h; API unreachable, backup failing, upgrade failed; total live-socket cap full for 10 min. Overlapping calls and other limit hits go on dashboards only.

### 6.15 Combined deployment with PagerArchive

Decided in [What form do the shared UI and the combined deployment take?](https://github.com/RIKIKU/RadioArchive/issues/6), [How does a page's fireground channel become a link to its talkgroup?](https://github.com/RIKIKU/RadioArchive/issues/10) and [How is RadioArchive deployed, upgraded, backed up and monitored?](https://github.com/RIKIKU/RadioArchive/issues/24).

- **R100. Fireground channel number.** An optional whole number on a talkgroup (admin table, import, export), unique across the deployment; a duplicate is rejected with a message naming the talkgroup that has it. Leading zeros don't count.
- **R101. Fireground links.** `<radio URL>/?fireground=<number>` opens the radio (PagerArchive opens it in a new tab) and adds that talkgroup to the listening set for the session (R47), then offers **Start listening**. Unknown number, or its talkgroup hidden or ignored: the radio still opens, shows "This channel isn't available" without naming anything, adds nothing and never plays it. A fireground link always means live listening, never recordings from the page's time.
- **R102. Turning it on.** One setting in each app naming the other's URL: `PAGERARCHIVE_URL` here, `RADIOARCHIVE_URL` there. Unset means standalone: no app switcher. Nothing is discovered at runtime. Each app is on its own subdomain under **one shared parent domain**.
- **R103. Sign-in.**
  - Standalone: listeners may enrol themselves by emailed code (`LISTENER_SIGNUP`, on by default), because an account only saves preferences (owner, 2026-11-24). Sign-in is via Authentik (OIDC, PKCE).
  - Combined: one Authentik, with RadioArchive as a second OIDC client (an Authentik blueprint ships with the release). Radio sign-in is limited to PagerArchive's invited users; no open enrolment.
  - **Silent sign-in:** on the first visit in a browser session, the radio checks Authentik with a top-level `prompt=none` redirect. With an Authentik session, the user is signed in to the radio too; without one, the radio stays signed out without prompting, and listening is never blocked.
  - Each app keeps its own host-only session cookie. Signing out of either app signs out of Authentik and both apps (the mechanism, such as OIDC back-channel logout, is the implementer's, recorded in an ADR).
- **R104. Look.** In the combined deployment the look is one `look` cookie on the parent domain (`LOOK_COOKIE_DOMAIN`), written by either app and read by both servers for first paint. A signed-in profile's look is copied into it at sign-in; a change while signed in saves to both.
- **R105. Where the server runs.** The recorder host stays separate. The radio server runs either:
  - **default:** on the PagerArchive host, in its own compose project joining PagerArchive's network. Its override points `DATABASE_URL` and OIDC at PagerArchive's `db` (the `postgis/postgis` image) and Authentik, and leaves out its own `db`, `authentik-*` and `backup` services. A one-shot `docker compose run --rm api setup-db` creates the `radioarchive` database and role, given PagerArchive's superuser URL once on the command line and never stored;
  - **supported:** on the recorder host, with its own `postgres:18`.
  PagerArchive's `compose.yaml` stays untouched.
- **R106. Shared operations.** PagerArchive's backup service also dumps and checks the `radioarchive` database (same exclusions as R89); its state shows in both apps. Each app's auto-upgrade script takes one **host-wide lock**, so they never upgrade at once; each pre-upgrade backup and rollback restores only its own app's database. RadioArchive ships its own adapted copy of the script.

### 6.16 Accounts

- **R110. What a listener's account holds**, in their Authentik profile and nowhere else: listening set, priority marks, playback settings (R48), the look, and the download basket with its conversations. A listener can clear it from the account menu.
- **R111.** Sign-in never unlocks talkgroups, features or higher limits (P3). Admin pages, the admin API and the module require `radioarchive-admins` (R55).

## 7. Constraints

| Constraint | Because |
|---|---|
| Docker Compose on a home Linux server | Simple installs and upgrades for self-hosters |
| Linux only. **Server** images amd64 only; **recorder** images and binaries amd64 + arm64 | Servers are x86 PCs; recorders may be Raspberry Pis |
| **Resource budget** (owner, 2026-11-24): RadioArchive's containers (API and its own database; Authentik excluded) ≤ **512 MB RAM** together; API ≤ **256 MB** with 50 live sockets and 5 calls at once; idle CPU < 5 % of one core; recorder client ≤ **128 MB** on an arm64 Raspberry Pi 4 | Shares a small host with PagerArchive and other self-hosted apps |
| The server **never transcodes** and stays a static, cgo-free binary | ADR 0001 |
| PostgreSQL; **no message queue**, no object store | ~6 listeners and a few calls at once don't justify them |
| **Authentik** for identity | Shared with PagerArchive |
| **No paid services** | Hobby-scale self-hosting |
| CI on **GitHub Actions**; images to **GHCR** | Free for public repos |
| Accessibility: **WCAG 2.1 AA**, checked by ArchiveUx's contrast CI and Playwright with axe-core | Usable by everyone, including on phones in poor light |
| Runs behind an **external reverse proxy** | Self-hosters already run one |
| Licence: **MIT** | Open source |
| The repo names **no real agency, talkgroup, site or other project** beyond its own companions; test data is made up | Generic tool; each deployment's setup is private |

## 8. Acceptance checks

All checks use the **test fixture**: system `EX` ("Example network", ID 1) with talkgroups 1001 "Dispatch North" (label `DISP N`, tag dispatch, group "North"), 1002 "Fireground 41" (label `FG 41`, tag fireground, fireground channel number 41), 1003 "Admin" (hidden), 1004 (ignored), units 5001 "Engine 1" and the range 5100-5199 "Portables"; one recorder "Bench" attached as `Bench`/`ex` to `EX`; a **fake recorder** replays fixture PCM through the recorder plugin's UDP protocol. "Now" is a controllable clock.

- **A1. Exact match.** **Given** the fake recorder sends two overlapping calls on talkgroup 1001 and their `call_end`s, **when** both recordings arrive, **then** 2 calls are stored, each linked to the live frames with the same (recorder, `sys_num`, `call_num`, `start_time`), each with its own transmissions.
- **A2. Unattached and unknown.** **Given** auto-create is off, **when** calls arrive from `Bench`/`other` and on talkgroup 4711, **then** none are stored, and the Recorders page and talkgroup admin show 1 rejected call for each with the R12/R17 wording. **When** auto-create is switched on and talkgroup 4711 is heard again, **then** it's created public and unreviewed and its call is stored.
- **A3. Ignored.** **When** a call arrives on 1004, **then** nothing is stored and a rejection is counted.
- **A4. Patch.** **Given** a call recorded on 1001 patched with 1002, **when** listener A has only 1002 in their set and B only 1001, **then** each hears it once, and 1 call is stored. **Given** a call on 1001 patched with 1003 (hidden), **then** no listener receives it, and recent calls and call links show nothing of it.
- **A5. Live feed filtered.** **Given** listener A's set is {1001} and B's {1002}, **when** calls arrive on 1001 and 1002, **then** A receives only 1001's events and frames, B only 1002's.
- **A6. Delay.** **Given** the full stack in Playwright with the fake recorder, **when** the plugin sends a call's first PCM packet, **then** the browser decodes its first frame within 2 s (p95 over 50 calls).
- **A7. Browser playback.** In Chrome, Firefox and WebKit: a live call plays through WebCodecs; with `AudioDecoder` removed, it plays through the WASM fallback.
- **A8. Queue and priority.** **Given** a listener with {1001, 1002}, 1002 marked priority, and a call playing on 1001, **when** a call starts on 1001 and then on 1002, **then** the 1001 call keeps playing, the 1002 call plays next, then the queued 1001 call (from its recording if it has ended). **Skip** moves to the next; **Jump to live** empties the queue; **Pause** keeps queuing.
- **A9. Hold.** **Given** a call on 1001 playing with hold on, **when** calls arrive on 1002, **then** they aren't queued while hold is on, and the saved set is unchanged.
- **A10. The gate.** **Given** talkgroup 1003 (hidden) has 3 recordings, **when** every public endpoint and the live feed are exercised (catalogue, search, recent calls, unit search, call details, call link, preview, single download, zip, QUERY call list, live events and frames), **then** no response contains `1003`, "Admin" or its audio, and its call links return the same response as a deleted call's.
- **A11. Hiding mid-call.** **Given** a listener hearing a live call on 1001 and a second 1001 call queued, **when** an admin hides 1001, **then** within 1 s the audio stops, the queued call is withdrawn, and recent calls no longer list 1001. **When** it's made public again, **then** its recordings are listed again.
- **A12. Ignored keeps old recordings hidden.** **Given** 1001 has recordings, **when** it becomes ignored, **then** new calls aren't stored, and its recordings are hidden but not deleted until retention.
- **A13. Admin hears hidden recordings only.** **Given** an admin, **when** they play a 1003 recording from the admin table, **then** it plays; **when** they open the listener signed in, **then** 1003 is absent as for anyone.
- **A14. Delayed talkgroup.** **Given** 1001 has a delay of 5 min, **when** a call on it ends at 12:00, **then** no listener receives it before 12:05 and recent calls don't list it; **at** 12:05, a listener with 1001 gets it queued, labelled "delayed 5 min", playing from its recording.
- **A15. Fireground link.** **Given** a listener with {1001}, **when** they open `/?fireground=041`, **then** 1002 is added for the session, shown as "added for this session", and the saved set is unchanged after the tab closes. **When** they open `/?fireground=99` or the number of a hidden talkgroup, **then** the page shows "This channel isn't available" identically, and nothing is added.
- **A16. Call link.** **Given** a call on 1001 in progress, **when** its link is opened, **then** the card says "In progress" and offers live listening; after it ends the same link plays the recording; after retention deletes it, the link shows "This call isn't available" with the same status and body as a hidden call's.
- **A17. Single download.** **When** a call on 1001 that started 2026-03-04 05:06:07 local is downloaded, **then** the file is the stored bytes and is named `2026-03-04_050607_DISP N_1001.opus` with the label made file-safe as the implementation defines.
- **A18. Zip.** **Given** a basket of 3 calls in conversation "Structure fire" and 1 loose call, **when** it's downloaded twice, **then** both zips are byte-identical, contain `01 Structure fire/` with 3 files and an `.m3u`, the loose call at the top, `manifest.json` and `index.csv` with R65's columns; **when** the second download is interrupted and resumed with a range request, **then** the result is identical.
- **A19. Basket and retention.** **Given** a basket item recorded 29 days ago with retention 30 days, **then** it shows "deleted in 1 day"; after the nightly run on day 31, it shows "No longer available" and is left out of the zip.
- **A20. Limits.** **Given** default limits, **when** one client opens a 5th live socket, a 3rd zip, or a 121st API request in a minute, **then** each is refused with `429` and `Retry-After`, and the UI names the limit. **When** 50 sockets are open in total, **then** a new listener sees "The radio is full right now. Trying again…", and no existing socket drops. A recorder client and a signed-in admin are not limited.
- **A21. Proxy address.** **Given** `TRUSTED_PROXY=10.0.0.2`, **when** requests carry `X-Forwarded-For` from 10.0.0.2 and from 10.0.0.3, **then** only the first's forwarded address is counted as the client.
- **A22. Search bounds.** **When** recent calls are searched over 32 days, **then** it's refused naming the 31-day limit; a page returns at most 100 rows.
- **A23. Admin table.** **Given** 1,000 talkgroups, **when** the admin table opens, **then** every row is loaded with no paging, and a search for "north" filters the table as it's typed, with no further server request.
- **A24. Import preview and round trip.** **Given** the fixture, **when** a JSON import renames 1001, adds 1005, lists 1003 as public, omits 1002, and includes a row labelled `1006`, **then** the preview shows 1 changed (label/name), 1 new, 1003 unchanged in state, 1002 kept, 1006 skipped as a placeholder; nothing changes until confirmed. **When** the setup is exported and imported into an empty deployment, **then** a second export is identical.
- **A25. Duplicate fireground number.** **When** an admin gives 1001 fireground channel number 41, **then** it's refused naming "Fireground 41".
- **A26. Audit.** **When** 1001 is renamed in the browser, hidden with the module and attached systems change, **then** the audit log shows each with who and when.
- **A27. Recorder signals.** **Given** the fake recorder, **when** its connection drops for 2 min, **then** the Recorders page shows it disconnected, the listener shows "Some talkgroups aren't being received right now", and it clears on reconnect. **When** it reports a decode rate of 0 for 2 min, **then** the same. **When** it's merely quiet, **then** the listener shows nothing.
- **A28. Disk queue.** **Given** the server is down for 1 h while 10 calls are recorded, **when** it comes back, **then** all 10 recordings arrive oldest first and the queue is empty; an entry older than `QUEUE_MAX_AGE` is dropped and counted.
- **A29. Old client.** **Given** a server at 1.4, **when** a 1.2 client connects, **then** it's refused with both versions named and the metric counts it; a 1.3 client connects and is flagged out of date.
- **A30. Retention and low disk.** **Given** retention 30 days, **when** the nightly run happens, **then** recordings 31 days old and their rows are deleted and 29-day-old ones kept. **Given** free space below `RECORDINGS_MIN_FREE`, **then** the oldest recordings are deleted until it's above, and "retention shortened" is counted.
- **A31. Backup excludes calls.** **When** the nightly backup is restored into an empty database, **then** setup, the audit log and settings are present and there are no call rows; the setup export JSON sits beside it.
- **A32. No names in telemetry.** **Given** a day of fixture traffic, **then** no log line, metric or trace contains a talkgroup or unit name or a listener IP address.
- **A33. Listening set and sign-in.** **Given** a signed-out listener's set {1001}, **when** they sign in for the first time, **then** the set is saved to their profile; **given** a profile with {1002}, **when** they sign in on a browser with {1001}, **then** one prompt offers to replace it, and nothing is merged.
- **A34. Silent sign-in** (combined test stack). **Given** a user signed in to PagerArchive, **when** they open the radio, **then** they're signed in without a prompt; **given** no Authentik session, **then** the radio opens signed out and listening works. **When** they sign out of either app, **then** both are signed out.
- **A35. Shared look.** (Combined test stack.) **When** the look is changed to Console in the radio, **then** PagerArchive's next page load paints Console first.
- **A36. Accessibility.** Every page passes axe-core in both looks and both themes, at 375 px and 1280 px; every form shows the Required tag on required fields.
- **A37. Resource budget.** **Given** the full stack with 50 live sockets and 5 calls at once for 1 h, **then** the API uses ≤ 256 MB and RadioArchive's containers (Authentik excluded) ≤ 512 MB; the recorder client on an arm64 Pi 4 ≤ 128 MB.
- **A38. Server stays static.** CI builds the API on distroless `static` with cgo off, and fails otherwise.

## 9. End-to-end verification

On a clean Linux host with Docker (a GitHub Actions Ubuntu runner):
1. `docker compose up -d` starts the whole stack (including Authentik) from `env.example`; `recorder-compose.yaml` starts the recorder side with the **fake recorder** in place of a radio.
2. The first admin signs in, creates system `EX`, imports the fixture setup with the module, and attaches the recorder from the shell's `recorder add`.
3. A browser presses **Start listening** with {1001} and hears fixture calls live within 2 s, then replays one from recent calls, shares it and downloads a basket.
4. All acceptance checks pass in CI, A37 once by hand with evidence.
5. Evidence: commands run and their output.

## 10. Delivery plan

Build in milestones (owner, 2026-11-24). Each ends with its acceptance checks passing, plus evidence.

| Milestone | Scope | Checks |
|---|---|---|
| **M0** | Stack ADR approved | done (2026-11-15) |
| **M1** | Recorder plugin, recorder client (with the disk queue), ingest, systems and attachments, recorder shell commands, recordings and retention, the hidden gate, CI; the iOS prototype (R40) | A1-A4, A10 (storage and API), A12, A28-A30, A38 |
| **M2** | Live feed and web listener: listening set, queue, priority, controls, now playing in the dock, recorder notice | A5-A9, A11, A14, A15, A27, A33 |
| **M3** | Recent calls and search, call links, single downloads, the basket, conversations and zips | A10 (all), A16-A19, A22 |
| **M4** | Talkgroup, unit and system admin, import and export, audit log, PowerShell module | A13, A23-A26 |
| **M5** | Heavy-use limits | A20, A21 |
| **M6** | Install, releases, auto-upgrade, backups, telemetry, alerts and dashboards; resource budget | A29, A31, A32, A36, A37, section 9 |
| **M7** | Combined deployment: switcher, silent sign-in, shared sign-out, shared look, `setup-db`, shared backup and upgrade lock. Needs PagerArchive's and ArchiveUx's changes (section 11) released first | A34, A35 |

**Before the first real deployment:** M1-M6 are done.

## 11. Changes other projects need

Kept as one issue in each repo. This repo depends on their releases; it doesn't do their work.

### 11.1 PagerArchive

Tracked in [Changes for the combined deployment with RadioArchive](https://github.com/RIKIKU/PagerArchive/issues/106):
1. Move its look and frame into ArchiveUx and use them from there.
2. A `RADIOARCHIVE_URL` setting, turning on the app switcher and fireground links.
3. `fireground_channel_pattern` in the network profile, numbers stored at parse time, the "Fireground 41" chip under the page text.
4. Authentik loads extra blueprints from a folder.
5. The backup service dumps and checks extra databases (`BACKUP_EXTRA_DATABASES`).
6. The look cookie's domain is a setting, and the shared look is read and written.
7. The silent sign-in check (`prompt=none`).
8. Sign-out reaches the other app.
9. The monitoring stack loads extra Alloy config, alert rules and dashboards from include folders.
10. The auto-upgrade script takes a shared host-wide lock.

### 11.2 ArchiveUx

Tracked in [What RadioArchive needs from ArchiveUx](https://github.com/RIKIKU/ArchiveUx/issues/6):
1. The **app switcher** in the frame (batch (a)).
2. The **dock** in the frame, with notices and toasts placed above it (batch (a)).
3. `AccountMenu` works **signed out** (a Sign in entry, look and theme still choosable) (batch (a)).
4. The look's first-paint rule reads a **parent-domain `look` cookie** when the app names one (batch (a)).
5. **LED colour tokens**: about 8 named colours, measured as marks in every look and theme.
6. `DateRange` takes **times of day** (batch (b)).
7. A **Live / behind** badge pattern for now-playing (the Live dot exists; "behind by N s" needs its pairing).

## 12. Open questions

None at the time of writing.

## 13. Accepted defaults

- English only. Times are shown in the deployment's time zone (`TZ`); APIs speak UTC.
- Event replay on reconnect covers 5 minutes (R37).
- A unit with no alias is shown by its ID. A talkgroup with neither name nor label is shown by its ID (admins only see unreviewed placeholders; listeners see the ID too).
- The alert sounds and the LED colour names are the implementer's choice, within R22, as is how a talkgroup with no alert or LED colour of its own takes its tag's, group's or system's.
- The listener's share link format, and how a listening set is sent over the socket, are the implementer's, recorded in an ADR.
- A retired recorder's name stays on its calls (admins only) until retention.
