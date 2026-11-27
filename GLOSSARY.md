# RadioArchive

Self-hosted live listening and archive for trunked radio systems recorded by trunk-recorder; a companion to PagerArchive.

## Language

**Call**:
An exchange on a talkgroup from its first key-up until the talkgroup goes idle; it may hold transmissions from several units.
_Avoid_: Recording, clip

**Conversation**:
Calls a listener groups together to hear an exchange in order, often because each side of it is a separate call.
_Avoid_: Thread, exchange, call group

**Story**:
Conversations a listener groups together to follow one incident from start to finish, over hours, days or weeks.
_Avoid_: Job, incident, case

**Transmission**:
One unit's key-up within a call, from press to release of its push-to-talk.
_Avoid_: Call, over, burst

**Live call**:
A call heard while it is still in progress, its audio arriving as the radio receives it rather than after the call ends.
_Avoid_: Stream, real-time call

**Recording**:
The finished audio of a call, kept in the archive once the call ends.
_Avoid_: Call file, upload

**Delay budget**:
How far a live call may trail the radio: from the recorder plugin's first PCM packet of a call to the browser's first decoded frame of it, p95 under 2 s.
_Avoid_: Latency, lag

**Retention period**:
How long a call and its recording are kept, in days, before the nightly job deletes them; the same for every talkgroup, hidden ones included.
_Avoid_: TTL, expiry, archive period

**Deletion moment**:
When the nightly job will delete a call: the first nightly run after the call's start plus the retention period. A basket item counts down to it.
_Avoid_: Expiry, expiry date, TTL

**Retention shortened**:
Recordings deleted before their retention period is over, oldest first, because the recordings volume ran low on free space.
_Avoid_: Purge, eviction, early expiry

**Talkgroup**:
A P25 trunked-radio group that calls are made on; the unit of what a listener chooses to hear.
_Avoid_: Channel, frequency

**Patch**:
Talkgroups joined for a time so that one call is heard on all of them at once.
_Avoid_: Link, merge, simulselect

**Hidden talkgroup**:
A talkgroup whose calls are recorded but never streamed or played to the public.
_Avoid_: Private talkgroup, blocked talkgroup

**Ignored talkgroup**:
A talkgroup whose calls are not recorded at all.
_Avoid_: Blacklisted talkgroup, rejected talkgroup

**Unreviewed talkgroup**:
A talkgroup created automatically the first time a call is heard on it, which no admin has named or checked yet.
_Avoid_: Unknown talkgroup, placeholder

**Last heard**:
When a call was last heard on a talkgroup, as its own talkgroup or in its patch, including calls refused because the talkgroup is ignored; kept after retention deletes the calls.
_Avoid_: Last seen, last active

**Tag**:
The one label a talkgroup carries for the kind of traffic on it, such as dispatch or tactical.
_Avoid_: Category, type

**Group**:
A label for whose or where a talkgroup is, such as an agency or region; a talkgroup can be in several groups.
_Avoid_: Folder, category

**Unit**:
A radio heard on calls, identified by its radio ID and named by an alias.
_Avoid_: Radio, user, source

**Fireground channel**:
A talkgroup used for firefighting operations at an incident, as opposed to a dispatch channel; the term a page uses when it names one, by its number.
_Avoid_: Fireground talkgroup, ops channel

**Fireground link**:
A link shown with a page that names a fireground channel, which opens the radio and adds that channel's talkgroup to the listener's listening set for the session.
_Avoid_: Listen link, radio link

**Call link**:
A link to one call, shared from recent calls or now playing, which opens the radio at that call's call card; a link to a call that isn't available looks the same whatever the reason.
_Avoid_: Permalink, call URL

**Call card**:
What a call link opens: the call's talkgroup, start, duration and units, with Play, Download and Share, or live listening while the call is in progress.
_Avoid_: Call details, call page

**Conversation link**:
A link to a conversation, shared from the basket, carrying its name, note and calls, which opens them as call cards in order with Play all; calls that aren't available are left out without a word.
_Avoid_: Shared basket, playlist link

**Listening set**:
The talkgroups a listener keeps on to hear live calls from; saved per listener, and added to for a session by a fireground link.
_Avoid_: Selection, favourites, scan list

**Priority talkgroup**:
A talkgroup in a listener's listening set whose calls go ahead of others waiting to be heard, without cutting off the call already playing.
_Avoid_: Favourite, preempt, emergency talkgroup

**Hold**:
A listener's temporary narrowing to only the talkgroup of the call playing now, lasting until they release it; it leaves the listening set unchanged.
_Avoid_: Lock, pin, solo

**Now-playing display**:
The fixed panel at the top of the Listen page that shows the call playing, or the last one heard, in fields that never move; on other pages it shrinks to one line while listening. The player's controls are in the dock, apart from it.
_Avoid_: Now playing bar, LCD, header (the frame's top bar)

**Replay**:
Playing a call from the archive because a listener chose it, apart from live listening, which waits meanwhile and carries on by itself afterwards.
_Avoid_: Playback (that's any call's audio), rewind, catch-up

**Play from here**:
A replay of a call and then the calls that started after it, in time order, until the moment it was pressed.
_Avoid_: Play next, autoplay, continue

**Download basket**:
The calls a listener has marked, and the conversations they've grouped them into, kept to download later.
_Avoid_: Cart, queue, playlist

**Account**:
Someone who has signed in to the radio at least once, as the radio records them: who they are in Authentik, their role and their status.
_Avoid_: User, profile, login

**Admin**:
An account whose role is admin and whose status is active, in the radio's own records; being an admin of anything else, PagerArchive or Authentik included, doesn't make one.
_Avoid_: Superuser, operator, moderator

**First admin**:
The account that signs in with the deployment's configured initial admin address, proven by Authentik, while no admin exists.
_Avoid_: Owner, root, bootstrap admin

**Disabled account**:
An account an admin has switched off: signed out of the radio at once, refused at sign-in, and switched off in Authentik too, keeping everything it had until an admin enables it again. Listening without signing in is the same for it as for anyone.
_Avoid_: Banned, blocked, suspended (PagerArchive's word for the same is deactivated)

**Deleting an account**:
An admin removing an account for good: its record, its saved settings, its sessions and its Authentik user. The audit log keeps who it was.
_Avoid_: Purging, erasing

**Audit log**:
The radio's record of every change an admin makes: who made it, how (browser, module or import), when, and each changed field before and after. Kept for its own period (two years unless an admin changes it), never rewritten, and read only by admins; the nightly run deletes older entries and records how many went.
_Avoid_: History, activity log, changelog

**Setup**:
Everything an admin has set up: systems, their talkgroups and units, groups and tags, with every setting they hold. Not recorders, attachments, accounts or calls. The export writes it whole; the import merges it in.
_Avoid_: Config, catalogue (the catalogue is the public's part of it)

**Import preview**:
What an import would do, row by row (new, changed with the fields, unchanged, kept, skipped and why), worked out without changing anything, and fingerprinted as its **plan**. An import applies only with the plan of a preview that still holds.
_Avoid_: Dry-run report, diff

**Update (an import's)**:
An import asked to make what exists take every field the file gives otherwise, not just a talkgroup's label, name, tag, groups, fireground channel number and sort order. Even then a talkgroup's state and unreviewed never change: an old file can't make a hidden talkgroup public.
_Avoid_: Overwrite, sync

**Placeholder row**:
A row in an imported file whose label is just its talkgroup ID, as trunk-recorder's files carry for talkgroups nobody named. The import skips it.
_Avoid_: Unreviewed talkgroup (that's a talkgroup the radio made itself)

## Deployment

**Combined deployment**:
RadioArchive and PagerArchive deployed together so a user moves between them as if they were one app, while each remains deployable on its own.
_Avoid_: Suite, merged app

**System**:
One trunked radio network whose talkgroups and units are numbered together; recorders feed calls into it.
_Avoid_: Site, network

**Recorder**:
One trunk-recorder installation capturing one or more systems; a deployment may have several, each hearing different calls.
_Avoid_: Scanner, receiver, site

**Recorder client**:
The RadioArchive program that runs beside a recorder and sends its live calls and recordings to the server.
_Avoid_: Uploader, agent, feeder

**Recorder plugin**:
The small part of RadioArchive that runs inside trunk-recorder and hands each call to the recorder client.
_Avoid_: Streamer, simplestream

**Recorder signal**:
One of three ways the server notices a recorder has stopped: disconnected (its client's connection has dropped), deaf (trunk-recorder is down, or an attached system's control channel is lost) and quiet (no calls for an admin-set time).
_Avoid_: Alarm, health check, status

**Recorder notice**:
What every listener sees once a recorder has been disconnected or deaf for a while: "Some talkgroups aren't being received right now". It names no recorder or talkgroup, and quiet never raises it. A listener can put it away, and it goes by itself after 10 s; it shows again at the next outage.
_Avoid_: Outage banner, recorder alert

**Attachment**:
An admin's link from one recorder's system, as trunk-recorder names it (its `shortName`), to the system its calls are filed under.
_Avoid_: Mapping, binding, assignment

**Overlapping calls**:
Two recorders' calls on the same system and talkgroup whose times overlap: both recorded the same call, so their coverage overlaps. The Recorders page counts them over the last 24 hours; both copies are kept.
_Avoid_: Duplicates, double calls
