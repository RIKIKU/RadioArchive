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

**Talkgroup**:
A P25 trunked-radio group that calls are made on; the unit of what a listener chooses to hear.
_Avoid_: Channel, frequency

**Hidden talkgroup**:
A talkgroup whose calls are recorded but never streamed or played to the public.
_Avoid_: Private talkgroup, blocked talkgroup

**Ignored talkgroup**:
A talkgroup whose calls are not recorded at all.
_Avoid_: Blacklisted talkgroup, rejected talkgroup

**Unreviewed talkgroup**:
A talkgroup created automatically the first time a call is heard on it, which no admin has named or checked yet.
_Avoid_: Unknown talkgroup, placeholder

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

**Listening set**:
The talkgroups a listener keeps on to hear live calls from; saved per listener, and added to for a session by a fireground link.
_Avoid_: Selection, favourites, scan list

**Priority talkgroup**:
A talkgroup in a listener's listening set whose calls go ahead of others waiting to be heard, without cutting off the call already playing.
_Avoid_: Favourite, preempt, emergency talkgroup

**Hold**:
A listener's temporary narrowing to only the talkgroup of the call playing now, lasting until they release it; it leaves the listening set unchanged.
_Avoid_: Lock, pin, solo

**Download basket**:
The calls a listener has marked, and the conversations they've grouped them into, kept to download later.
_Avoid_: Cart, queue, playlist

## Deployment

**Combined deployment**:
RadioArchive and PagerArchive deployed together so a user moves between them as if they were one app, while each remains deployable on its own.
_Avoid_: Suite, merged app

**Recorder**:
One trunk-recorder installation capturing a radio system; a deployment may have several, each hearing different calls.
_Avoid_: Scanner, receiver, site

**Recorder client**:
The RadioArchive program that runs beside a recorder and sends its live calls and recordings to the server.
_Avoid_: Uploader, agent, feeder

**Recorder plugin**:
The small part of RadioArchive that runs inside trunk-recorder and hands each call to the recorder client.
_Avoid_: Streamer, simplestream
