---
status: proposed
---

# 7. The live feed to listeners

Date: 2026-11-24

Each listener opens one WebSocket, `GET /api/v1/live`, with no sign-in, and sends its listening set as a JSON `set` message, again whenever it changes. The server sends call events as JSON text and Opus frames as binary messages. Every event carries an event ID that rises across restarts, and a listener reconnecting with its last one gets the events of the last 5 minutes it missed. The gate's live feed (`gate.Live`) decides each call's state at every event from the call's row in `public_calls` and the catalogue, holds the answer in memory, and reads it again for every call it holds whenever the catalogue's generation moves, so no frame costs a query and hiding a talkgroup withdraws its calls within a tick. Each listener is a bounded queue: a full one drops frames, and one too far behind for an event is closed. These are the implementer's choices where `SPEC.md` P1, P2, R16, R23, R31, R32, R36, R37, R75, R76 and section 13 ("how a listening set is sent over the socket") leave room (spec "How to use this spec", item 6). Built in [Live feed to listeners behind the gate](https://github.com/RIKIKU/RadioArchive/issues/56).

## Summary

| Part | Choice |
|---|---|
| **Route** | `GET /api/v1/live`, a WebSocket, no sign-in, in `internal/public`, reading only through `internal/gate` (ADR 0004). No compression |
| **Listener → server** | Text JSON. `{"type": "set", "talkgroups": [{"id": 1001}, {"system": 2, "id": 1001}], "last_event_id": N}`. Unknown types are ignored |
| **Server → listener, text** | `welcome`, `listening`, `call_start`, `transmission`, `call_end`, `call_withdrawn`, `call_delayed`, `replay`, `notice` |
| **Server → listener, binary** | One Opus frame: a 24-byte header (kind, call ID, frame number) then the packet as the recorder client sent it |
| **Event IDs** | Integers, rising: the server's start time in microseconds, plus one per event. Below 2^53, so a JavaScript number holds one exactly |
| **Resuming** | `last_event_id` in the first `set`: the events after it from the last 5 minutes, then `{"type": "replay", "complete": bool}`. Frames are never replayed |
| **How the gate sees a live call** | At every event, one read of the call's row through `public_calls` and the catalogue. Every tick (200 ms), one read of the catalogue's generation; when it has moved, one read of every call held. A frame is a map lookup |
| **Hiding mid-call** | The call is withdrawn: `{"type": "call_withdrawn", "event_id", "call_id"}` and nothing more, and its frames stop. Sticky: it isn't sent again if made public |
| **Delay** | A delayed call is never sent live and nothing is buffered. N minutes after it ends, by the gate's clock, `call_delayed` with the recording to play |
| **Slow listeners** | A queue of 1,024 messages. Frames stop being queued at 768 (dropped and counted); an event that doesn't fit closes the socket with 1013, to resume |

## The listening set (section 13)

```json
{"type": "set", "talkgroups": [{"id": 1001}, {"id": 1002}, {"system": 2, "id": 1001}], "last_event_id": 1764000000123456}
```

- `talkgroups` is the whole set, the saved set plus any added for the session (R47): the server keeps no union of its own. A message replaces the last. At most 5,000 entries. It's what the browser's `sendListeningSet` (`web/src/lib/live/listening-set.ts`, ADR 0008) already builds.
- `{"id": T}` is that talkgroup ID in any system. With one system, which is every deployment whose catalogue gives no `system` (R15, ADR 0006), that's simply the talkgroup. `{"system": S, "id": T}` names one system's talkgroup, for a deployment with several; the catalogue gives each talkgroup's `system` then. A bare number `T` is taken as `{"id": T}`, for a client that has no more to say.
- `last_event_id` is read only in the first `set` of a socket. Later ones change the set and nothing else.
- The server answers every `set` with `{"type": "listening"}` once it has taken it, after any replay, so a client knows that what follows is by the new set.
- A listener sends `set` after the welcome; until then it gets nothing. Sending `{"type": "set", "talkgroups": []}` is valid and hears nothing.
- A bad message closes the socket, as the recorder protocol does for a broken client (ADR 0002): binary `1003`, not a JSON object `1007`, an invalid `set` `1008` with the reason.

A set is a filter **after** the gate (R36): a call reaches a listener once if any talkgroup in its patch is in the set (R16), but only a call the gate has passed. Naming 1003 in a set changes nothing for a hidden 1003.

When a set newly names a call that is live, the listener is sent that call's `call_start` and `transmission` events so far (their original IDs and contents) and then its frames from that moment. When a set stops naming a call, its frames and later events stop; the client drops the call itself.

## Server → listener

**Welcome**, first: `{"type": "welcome", "protocol": 1, "last_event_id": N}`. `last_event_id` is the latest event when the socket opened, so a client has an ID to resume from before its first event.

**Call events** share one shape (R36, R76). Times are UTC, ISO 8601 with milliseconds.

```json
{"type": "call_start", "event_id": 1764000000123457, "call_id": "6f1d2c3b-4a59-4e8f-9d7c-0b1a2c3d4e5f",
 "talkgroup": {"id": 1001, "label": "DISP N", "name": "Dispatch North", "tag": "dispatch", "groups": ["North"],
               "led_colour": "blue", "alert": null},
 "patched_talkgroups": [1002], "unit": {"id": 5001, "name": "5001"}, "started_at": "2026-11-24T01:00:00.120Z"}
```

- `talkgroup` is the call's own, as the catalogue shows it: `name` is R24's, and `led_colour` and `alert` are the effective ones (ADR 0006), `null` for none. `groups` is every group, in the groups' order; the issue's "group" is the first.
- `system` (`{"id", "label", "name"}`) is present only when more than one system has public talkgroups (R15), as in the catalogue.
- `unit` is the transmitting unit: `name` is its alias once units have aliases (M4) and its ID until then (R27); `null` when no unit is known.
- `transmission` adds `offset_ms` from the call's start, and its `unit` is the new one. `call_end` adds `ended_at` and `duration_ms`; for a call the recorder lost (nothing heard of it for a minute), both are missing.
- Each event carries the call's details again, so an app that joins mid-call or replays (R75) needs nothing else.

**`call_withdrawn`** is `{"type": "call_withdrawn", "event_id", "call_id"}`, nothing more (R31). The client ends the call, playing or queued, and forgets it. It's sent to every listener that has been sent the call: live, ended within the last 5 minutes, or released from a delay.

**`call_delayed`** is a delayed talkgroup's call (R23), finished, with the call event's fields and `ended_at`, `duration_ms`, `delay_minutes` and `recording`: `{"url": "/api/v1/calls/{id}/recording", "type": "audio/ogg", "size": N}`. It's the call's first and only event; the client queues it and plays it from its recording, labelled "delayed N min" (R45).

**`replay`** ends a resume: `{"type": "replay", "complete": true}` when every event after the given ID is still held; `false` when some have gone, because they're older than 5 minutes or from before the server started. A client that gets `false` may have missed calls (recent calls, M3, shows them).

**`notice`** is a feed notice: `{"type": "notice", "notice": "recorders" | "radio_full", "active": bool}`. The type is reserved here; the recorder notice (R15) and "the radio is full" (R71) are later slices'.

**Binary: one Opus frame**, little-endian:

| Offset | Size | Field | Notes |
|---|---|---|---|
| 0 | 1 | `kind` | `0x01` Opus frame |
| 1 | 3 | `reserved` | `0` |
| 4 | 16 | `call_id` | the call's ID as 16 bytes, in the order of its text form |
| 20 | 4 | `frame_seq` | uint32, the recorder client's: 0 for the call's first frame, +1 per frame. A gap means frames were lost or dropped |
| 24 | rest | Opus packet | 20 ms, R4's settings, 1-1,275 bytes |

A frame arrives only after its call's `call_start` on the same socket. Frames go out as they arrive, about a second behind the radio (P2, R38).

## Event IDs and replay (R37, R87)

An event ID is the server's start time in Unix microseconds plus a count, so IDs rise across restarts (the clock moves on faster than events are made) and stay exact in a JavaScript number. A client keeps the highest it has seen; an event resent because a set newly names a live call has its original, lower ID.

The server holds every event of the last 5 minutes in memory (at most 50,000), with the call it's about. A resume sends, in order, those after the given ID whose call the new set names, leaving out every event of a call that has since been withdrawn or was never sent. A withdrawal is replayed to a set that named the call while it was public, so a listener who had a call before it dropped learns it's withdrawn. Missed frames are never replayed (R37): a call that ended while the listener was away is queued from its `call_end` and played from its recording (R43).

After an upgrade (R87) the new server holds no events: a resume from before it starts gets `complete: false` and nothing replayed, and the client carries on.

## How the gate sees a live call's state

ADR 0004 left the live feed needing "a way to ask a live call's state at each frame or event, using `call_state` on the call's row". The answer is the catalogue's pattern (ADR 0006) applied to calls:

1. **At every event** (start, transmission, end), the live feed reads the catalogue (one read of its generation, and a rebuild if it has moved) and then the call's row through `GatePublicCalls`, which reads `public_calls`, whose condition is `call_state` on the row. Present, with its talkgroup in the catalogue, is public; absent is not, whatever the reason. The talkgroup's settings (groups, colour, alert, delay) come from the catalogue, so the rules stay in one place (`gate.catalogueOf`).
2. **The answer is held** on the call in memory, with the catalogue generation it was read at. A frame is one lookup in a map and a phase check: no query.
3. **Every tick** (200 ms), one read of the generation, one row by primary key. When it's newer than any held call's, every such call (live, delayed, or ended within 5 minutes) is read again in one query, and any that is no longer public is withdrawn. The generation moves at the commit of any talkgroup, group, tag or system change by any writer (migration 00007), so an admin's edit, an import, auto-create and `psql` all count, on every API server.
4. **A patch that grows** comes as a `transmission` or `call_end`, an event, so the call's row is read again then, on the recorder's goroutine, before its next frame is handled. A patch grown into an ignored talkgroup also refuses the rest of the call at the recorder endpoint (ADR 0003).

The generation is read before the rows, at an event and at a tick, so a call is never held under a generation newer than what it was read at; a talkgroup change that commits between the two is caught at the next tick. The live feed adds no migration and no query of its own.

A check that can't read the database fails closed: a new call isn't sent, and a live one is withdrawn.

Considered and not chosen: a query per frame (50 a second per call, against a constraint of 512 MB and 5 % idle CPU); `LISTEN`/`NOTIFY` on the generation (faster than a 200 ms tick, but a second mechanism beside the catalogue's, and a lost connection needs the same polling to recover; R31 asks for 1 s); and having the recorder endpoint pass the state it already computes in `store.Admit` (it would put the decision outside the gate).

## Withdrawal (R31)

When a held call stops being public (its talkgroup hidden or ignored, a talkgroup in its patch made so, or its patch grown into one), or a live call's talkgroup becomes delayed, the call is withdrawn: one `call_withdrawn` to every listener that has it, its frames stop at once, and nothing more of it is sent. It stays withdrawn if the talkgroup is made public again before the call ends: the listener was told it ended, and its recording is listed again (R30) by recent calls and call details.

"Queued calls" (R31) are the server's guess at the client's queue: every call it sent within the last 5 minutes. A client may have one queued for longer (R43 drops nothing). When it comes to play it from its recording, the gate answers the recording as unavailable, and the client skips it.

## Delay (R23)

A call whose patch has a delayed talkgroup (the longest delay in the patch, since the call is heard on each of them) is never sent live: no `call_start`, no frames, and nothing buffered. Once its delay has passed since it ended, by the gate's clock (`gate.WithClock`; the test fixture's controllable clock), and its recording is in, the tick sends `call_delayed` to every listener whose set names its patch. A recording that hasn't come 10 minutes after that is given up on (R19 alerts on it). If the talkgroup is hidden meanwhile, nothing is sent.

The same rule now holds in the gate's call details and recording playback (`Gate.Calls`, `Gate.Recording`): a delayed call is unavailable until its delay has passed, as R23 asks of every public surface. Recent calls and search (M3) read the same rule.

Pending releases are held in memory: a delayed call that ends just before a restart isn't released to listeners afterwards. Its recording becomes available on time, and recent calls (M3) list it.

## Slow listeners

The recorder endpoint calls the live feed on its own goroutine, and the live feed only ever does non-blocking sends to listeners' queues, so no listener can hold up a recorder or another listener. Each listener's socket has a queue of 1,024 messages and its own writer goroutine:

- At 768 messages waiting, frames are dropped and counted (`LiveCounters.FramesDropped`, and per listener). That's about 15 s of one call's audio: a listener that far behind wouldn't hear them live anyway.
- The remaining room is for events. An event that doesn't fit closes the socket with `1013` (try again later), counted (`SlowClosed`); the client reconnects and resumes by its last event ID.
- A write that takes 10 s closes the socket. A ping every 30 s keeps idle sockets open through proxies (R85).

## The gate stays the only way in

`internal/public` now imports one third-party module, the WebSocket library `github.com/coder/websocket`, which it needs to speak the protocol and which reaches no data. `imports_test.go` allows exactly that module path and still refuses the database driver, the store and every other module. Everything a listener receives was built by `gate.Live`, which holds only what `public_calls` and the catalogue passed.

The A10 harness probes the live route with listeners (`a10_live_test.go`), not HTTP requests: sets naming hidden 1003 (alone, with 1001, by system), ignored 1004, every talkgroup, one that adds 1003 mid-call, and one that resumes from before every call, around live calls on 1003, on 1001 patched with 1003, on 1004, and on 1001 whose patch grows into 1003. None may receive 1003's ID, name, label or tag, those calls' IDs, or their audio, and each whose set names 1001 must hear the public call.

## Consequences

- `server.LiveSink` gets the call's start, transmissions and end besides its frames, each after the row is written; `gate.Live` is the sink in the binary.
- `gate.New` takes options; `gate.WithClock` is the clock for delays and the replay window.
- The player (#58) opens the socket, sends what `sendListeningSet` is handed as the `set` message above, keeps the highest event ID, resends `set` with it after a reconnect, and ignores message types it doesn't know.
- Limits on listener sockets (R71) and the feed notices are M5's and a later slice's; they attach to this route and the `notice` type.
- The live delay metric (R38, R90) can be measured from `live_first_pcm_at` to the frame's send; not built here.
