# RadioArchive

Self-hosted live listening and archive for trunked radio systems recorded by trunk-recorder; a companion to PagerArchive.

## Language

**Call**:
An exchange on a talkgroup from its first key-up until the talkgroup goes idle; it may hold transmissions from several units.
_Avoid_: Recording, clip, conversation

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

**Fireground channel**:
A talkgroup used for firefighting operations at an incident, as opposed to a dispatch channel; the term a page uses when it names one.
_Avoid_: Fireground talkgroup, ops channel

**Listening set**:
The talkgroups a listener keeps on to hear live calls from; saved per listener, and added to for a session by a fireground link.
_Avoid_: Selection, favourites, scan list

## Deployment

**Combined deployment**:
RadioArchive and PagerArchive deployed together so a user moves between them as if they were one app, while each remains deployable on its own.
_Avoid_: Suite, merged app
