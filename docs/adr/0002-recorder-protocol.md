---
status: accepted (2026-11-24, by the owner)
---

# 2. Recorder protocol

Date: 2026-11-24

The audio path from ADR 0001 has two legs, and this ADR fixes the wire format of both. The **recorder plugin** sends the **recorder client** one UDP datagram per message on localhost: raw PCM with a small binary header that carries the call's full identity, plus JSON control messages (transmission, `call_end`, heartbeat) in the same envelope. The recorder client sends the server live Opus frames and call events over one WebSocket, and uploads each **recording** with a separate HTTPS POST. Both use the same client key. Live audio is best effort and the recording is the source of truth, so nothing is retransmitted. Decided with the owner in [Recorder protocol ADR and a fake recorder](https://github.com/RIKIKU/RadioArchive/issues/31) (`SPEC.md` R1-R5, R16, R19).

trunk-recorder references are to the pinned release, [v5.2.1](https://github.com/TrunkRecorder/trunk-recorder/tree/v5.2.1) (commit `f0ace2c`), as `file:line`.

## Summary

| Part | Choice |
|---|---|
| **Plugin → client transport** | UDP to `127.0.0.1`, one datagram = one message, never more than 8,192 bytes. No fragmentation, no reassembly |
| **Envelope** | 16-byte binary header: magic, protocol version, message type, boot ID, sequence number, payload length. Little-endian |
| **Audio** | Binary payload: call identity, `shortName`, talkgroup, unit, frequency, TDMA slot, sample rate, then raw PCM (signed 16-bit little-endian, mono) |
| **Control** | JSON payload in the same envelope: `transmission`, `call_end`, `heartbeat` |
| **Loss** | Best effort. Large `SO_RCVBUF`, gaps counted from sequence numbers, no retransmission. The recording fills in what live audio missed |
| **Client → server, live** | One WebSocket, `/api/v1/recorder/live`. Binary messages are Opus frames, text messages are JSON events and heartbeats |
| **Client → server, recordings** | `POST /api/v1/recorder/recordings` over HTTPS, multipart (metadata JSON + Ogg/Opus). Idempotent by call identity, acknowledged with the server's SHA-256 of the file |
| **Auth and versions** | `Authorization: Bearer <client key>` on the WebSocket upgrade and on every upload. Client and protocol versions in headers. An unsupported version is refused before the upgrade with an error naming both versions |
| **Malformed input** | Plugin → client: drop, count, rate-limited log, never crash. Client → server: unknown types ignored and counted; an undecodable or invalid message closes the WebSocket with 1007 or 1008; the client reconnects with backoff |
| **Patch de-duplication (R16)** | Nothing to de-duplicate in the normal case: unlike simplestream, the plugin sends each recorder's audio once. The client drops a second call only when it overlaps an active call on the same system, frequency and TDMA slot and the two talkgroups are in one patch |
| **Fake recorder** | Go, `testing/fakerecorder`. Scenarios are JSON timelines with millisecond offsets |
| **Fixture audio** | Short `.pcm` files made by a small Go generator (tones and noise bursts), committed, reproducible. No real radio traffic |

## What trunk-recorder gives the plugin

Checked in the v5.2.1 source. These facts shape the formats below.

- **Callbacks.** `call_start(Call *)`, `audio_stream(Call *, Recorder *, int16_t *samples, int sampleCount)`, `call_end(Call_Data_t)` and `system_rates(std::vector<System *>, float timeDiff)` (`trunk-recorder/plugin_manager/plugin_api.h:41-51`). `audio_stream` runs on each recorder's GNU Radio thread (`trunk-recorder/gr_blocks/plugin_wrapper_impl.cc:57-61`, `trunk-recorder/plugin_manager/plugin_manager.cc:138-145`), so several calls arrive concurrently.
- **Audio only flows with `audioStreaming: true`** in trunk-recorder's config (`trunk-recorder/config.cc:216`; the plugin sink is only built when it's set, `trunk-recorder/recorders/p25_recorder_decode.cc:108-118`).
- **Sample format.** `int16_t`, mono, native byte order (little-endian on amd64 and arm64). The rate is the recorder's `get_wav_hz()`: **8,000 Hz for P25 and DMR** (`trunk-recorder/recorders/p25_recorder_decode.cc:85,136`, `trunk-recorder/recorders/recorder.h:97`) but **16,000 Hz for analog recorders** (`trunk-recorder/recorders/analog_recorder.cc:133,225`). `sampleCount` is whatever the GNU Radio block hands over, so its size varies.
- **Call identity.** `get_sys_num()`, `get_call_num()` and `get_start_time()` (seconds; `get_start_time_ms()` also exists) on `Call` (`trunk-recorder/call.h:26,37,69-70`). `call_num` is a process-wide counter that restarts at 0 with trunk-recorder (`trunk-recorder/call_impl.cc:33`), which is why `start_time` is part of the identity. `start_time` is set once when the call is made (`trunk-recorder/call_impl.cc:43`).
- **Unit.** `get_current_source_id()` (`trunk-recorder/call.h:92`), which can be `-1` before a unit is known; simplestream falls back to the last transmission's source (`plugins/simplestream/simplestream.cc:80-90`).
- **Transmission boundaries.** trunk-recorder calls `call_start` again for the same `Call` when the unit talking changes (`trunk-recorder/monitor_systems.cc:479-485`). It also calls `call_start` for calls it isn't recording (encrypted, unknown talkgroup, duplicate grant), because the call is made before the recorder starts (`trunk-recorder/monitor_systems.cc:502-556`). So the plugin can't treat `call_start` as "new call"; it checks `call->get_state() == RECORDING` and the unit.
- **Patches.** The `Call` object has **no patch field**. Patches are kept per system, keyed by supergroup, refreshed by control-channel messages and expired 10 s after their last update (`trunk-recorder/systems/system_impl.cc:772-847`, the 10 s at `:831`). `System::get_talkgroup_patch(tg)` returns every talkgroup in the patch, **including `tg` itself and the supergroup, without saying which is the supergroup** (`trunk-recorder/systems/system_impl.cc:758-770`). `Call_Data_t.patched_talkgroups` at `call_end` is that same lookup made when the call is concluded (`trunk-recorder/call_concluder/call_concluder.cc:1079`), so a patch that expired before then is missing from it. The call JSON treats a list of size 1 as "no patch" (`trunk-recorder/call_concluder/call_concluder.cc:787-789`).
- **Motorola patch grants are on the supergroup.** A patch group voice grant sets the call's talkgroup to the supergroup (`trunk-recorder/systems/p25_parser.cc:433-439`), so a patched call is normally recorded **once**, under the supergroup.
- **`call_end` and the file.** `call_end` runs after trunk-recorder has rendered the call's audio, in a concluder worker (`trunk-recorder/call_concluder/call_concluder.cc:900-959`). `Call_Data_t.filename` is `<stem>.wav` (`trunk-recorder/call_concluder/call_concluder.cc:1027`). **Unless `audioArchive` is on, trunk-recorder deletes that file as soon as the plugins' `call_end` returns** (`trunk-recorder/call_concluder/call_concluder.cc:862-888,961`). `transmission_list` carries each transmission's `source`, `start_time_ms` and `stop_time_ms` (`trunk-recorder/global_structs.h:11-26,173`).
- **`system_rates`** is called about every 3 s from the main loop (`trunk-recorder/monitor_systems.cc:949-952`) with the systems and the seconds since the last check. It runs **before** trunk-recorder updates `get_decode_rate()` for the interval (`trunk-recorder/monitor_systems.cc:770,776-777`), so the current rate is `message_count / timeDiff`, as trunk-recorder's own stats use (`trunk-recorder/systems/system_impl.cc:750-756`).

## Plugin → client (UDP, localhost)

The recorder plugin sends to `127.0.0.1:<port>` (default `9850`, set in the plugin's config) and never binds a non-loopback address. Each callback builds one or more datagrams and calls a non-blocking `sendto`. A failed send is counted and forgotten; the plugin never blocks, buffers or retries.

### Envelope

Every datagram starts with this header. All integers are **little-endian**.

| Offset | Size | Field | Value |
|---|---|---|---|
| 0 | 2 | `magic` | `0x52 0x41` (ASCII `RA`) |
| 2 | 1 | `version` | Protocol version, `1` |
| 3 | 1 | `type` | `0x01` audio, `0x02` transmission, `0x03` call_end, `0x04` heartbeat |
| 4 | 4 | `boot_id` | uint32, random, chosen when the plugin starts |
| 8 | 4 | `seq` | uint32, one counter for every message the plugin sends, starting at 0, wrapping at 2³² |
| 12 | 2 | `length` | uint16, payload length in bytes. `16 + length` must equal the datagram size |
| 14 | 2 | `reserved` | `0`. Ignored by the client |
| 16 | `length` | payload | By `type` |

The sequence counter is shared by all of the plugin's threads (an atomic increment), so loss of any message, audio or control, shows as a gap. A new `boot_id` means trunk-recorder restarted: the client resets its sequence tracking instead of counting a gap.

### Audio (`type` 0x01)

Sent from `audio_stream`. Every packet carries the call's full identity, so a client that restarts mid-call picks the call up from the next packet.

| Offset | Size | Field | Notes |
|---|---|---|---|
| 0 | 8 | `call_num` | int64 |
| 8 | 8 | `start_time` | int64, Unix seconds (`Call::get_start_time()`) |
| 16 | 4 | `talkgroup` | int32 (`Call::get_talkgroup()`) |
| 20 | 4 | `unit` | uint32, the unit talking now. `0` when unknown |
| 24 | 4 | `freq` | uint32, Hz |
| 28 | 2 | `sys_num` | uint16 |
| 30 | 2 | `sample_rate` | uint16, Hz. `8000` for P25 and DMR, `16000` for analog |
| 32 | 1 | `tdma_slot` | int8, `-1` when not TDMA |
| 33 | 1 | `short_name_len` | uint8, 1-64 |
| 34 | `short_name_len` | `short_name` | UTF-8, the system's `shortName` |
| 34 + n | rest | PCM | Signed 16-bit little-endian, mono. Even length, at least 2 bytes |

The plugin splits a callback's samples into packets of at most **4,000 samples (8,000 bytes)**, so a datagram is at most 16 + 98 + 8,000 = 8,114 bytes. Loopback's MTU is 64 KB, so nothing fragments. A system whose `shortName` is longer than 64 bytes is logged at startup and its audio isn't sent.

Patched talkgroups are **not** in audio packets: they change rarely, and they go in `transmission` and `call_end`. Frequency and TDMA slot are, because the client needs them to spot a patched copy (below) even after a restart.

### Control messages (`type` 0x02-0x04)

The payload is one UTF-8 JSON object, at most 8,176 bytes. Unknown fields are ignored. Every call message carries the identity fields `sys_num`, `call_num`, `start_time` and `short_name`, written as `…identity` below.

**`transmission` (0x02).** Sent when the plugin sees a recorded call's unit change: on a repeated `call_start` for a call in `RECORDING`, or in `audio_stream` when `get_current_source_id()` differs from the last unit sent for that call. The first one for a call marks its first transmission.

```json
{
  "sys_num": 0, "call_num": 4182, "start_time": 1764000000, "short_name": "ex",
  "talkgroup": 1001,
  "unit": 5001,
  "time_ms": 1764000000412,
  "freq": 851012500, "tdma_slot": -1,
  "patched_talkgroups": [1002]
}
```

`patched_talkgroups` is `System::get_talkgroup_patch(talkgroup)` with the call's own talkgroup removed: `[]` when there's no patch.

**`call_end` (0x03).** Sent from `call_end`, once the recording is written.

```json
{
  "sys_num": 0, "call_num": 4182, "start_time": 1764000000, "short_name": "ex",
  "talkgroup": 1001,
  "start_time_ms": 1764000000120,
  "stop_time_ms": 1764000009870,
  "freq": 851012500, "tdma_slot": -1,
  "patched_talkgroups": [1002],
  "filename": "/app/media/ex/2026/11/24/1001-1764000000_851012500.0-call_4182.wav",
  "transmissions": [
    {"unit": 5001, "start_time_ms": 1764000000412, "stop_time_ms": 1764000004200},
    {"unit": 5123, "start_time_ms": 1764000004950, "stop_time_ms": 1764000009870}
  ]
}
```

`filename` is `Call_Data_t.filename`, the WAV trunk-recorder wrote. `transmissions` comes from `transmission_list`, so the recording's transmissions are right even when live `transmission` messages were lost.

**`heartbeat` (0x04).** Sent from `system_rates`, about every 3 s.

```json
{
  "time_ms": 1764000003000,
  "plugin_version": "0.1.0",
  "systems": [
    {"sys_num": 0, "short_name": "ex", "system_type": "p25", "decode_rate": 38.7}
  ]
}
```

`decode_rate` is control-channel messages per second over the last interval (`message_count / timeDiff`). `system_type` is trunk-recorder's (`p25`, `smartnet`, `conventional`, …) so the server can ignore the rate of a conventional system, which has no control channel (R14).

### What the client does with plugin messages

- **Receive buffer.** The client asks for a large `SO_RCVBUF` (4 MB; the container sets `net.core.rmem_max` to allow it) and logs the size it actually got.
- **Malformed datagram** (wrong magic, unsupported version, `length` not matching the datagram, audio shorter than its fixed fields, odd PCM length, `short_name_len` 0 or over 64, unsupported `sample_rate`, invalid UTF-8 or JSON, missing identity fields): dropped, counted in `radioarchive_client_udp_malformed_total{reason}`, and logged at most once a minute per reason. It never crashes the client or affects other calls.
- **Unknown `type`** with a valid envelope: dropped and counted, for forward compatibility.
- **Gaps.** A forward jump in `seq` within one `boot_id` adds the missing count to `radioarchive_client_udp_lost_total`. A packet with a lower `seq` is late: an audio packet is dropped (it's no longer live), a control message is still used.
- **Supported sample rates** are 8,000 and 16,000 Hz. The Opus encoder for a call is created at the rate of its first packet, and libopus encodes narrowband from either.
- **A new call** begins with the first audio packet or `transmission` for an identity the client hasn't seen. The client never needs a separate "call start" from the plugin.
- **A lost `call_end`** isn't recovered: the call's recording never uploads, and the server's R19 alert (recording missing 10 minutes after the call ended) reports it. The client also ends a call's live audio after 30 s with no packets, without uploading anything.

## Patched calls (R16)

**Finding.** The copies the spec refers to come from **simplestream**, not from trunk-recorder's recording. simplestream loops over every talkgroup in the patch and, with `TGID: 0`, sends the same audio once per patched talkgroup (`plugins/simplestream/simplestream.cc:73,101-102`), which is what [What can trunk-recorder send while a call is still in progress?](https://github.com/RIKIKU/RadioArchive/issues/2) observed. Our plugin sends each `audio_stream` callback once, under `Call::get_talkgroup()`, so this source of copies goes away. trunk-recorder itself records a Motorola patched call once, on the supergroup (`trunk-recorder/systems/p25_parser.cc:433-439`).

**What can still double.** trunk-recorder doesn't stop a recording call when a grant for a *different* talkgroup arrives on the same frequency and TDMA slot. It only logs it (`trunk-recorder/monitor_systems.cc:489-497`) and starts a second recorder for the new talkgroup (`:502-557`). If a system grants a patch's member talkgroup on the voice channel that its supergroup is already using, trunk-recorder records the same audio twice, as two calls with different `call_num` and talkgroup. The source can't tell us how often real systems do this.

**Rule.** The client treats a new call as a copy of an active call when all of these hold:

1. same `sys_num` and `short_name`;
2. same `freq` and `tdma_slot`;
3. the active call is still live (audio within the last 2 s) when the new call's first packet arrives; and
4. the new call's talkgroup is in the active call's patched talkgroups, or the active call's talkgroup is in the new call's.

The first call (earlier `start_time_ms`, then lower `call_num`) is kept. The copy's live frames, events and recording are never sent, and copies are counted in `radioarchive_client_patch_copies_total`. The kept call's patched talkgroups are the union of every `patched_talkgroups` seen in its `transmission` messages and its `call_end`, minus its own talkgroup, because a patch can expire before trunk-recorder concludes the call. Same frequency without a shared patch isn't a copy: that's ordinary channel reuse, and both calls stay.

**Confidence.** High that the plugin produces no per-talkgroup copies (read in the source). Medium that the rule above catches the remaining case, and low on how often that case happens. That needs a real patched system or a recording of one. Until it's seen, the rule is cheap and never merges calls on different channels.

## Client → server

### Live: one WebSocket

`GET /api/v1/recorder/live`, upgraded to a WebSocket, with these request headers:

| Header | Example |
|---|---|
| `Authorization` | `Bearer <client key>` |
| `RadioArchive-Client-Version` | `0.4.1` |
| `RadioArchive-Protocol` | `1` |

Before upgrading, the server answers:

- `401` with `{"error": "invalid_client_key"}` for a missing, unknown, rotated or retired key (R10).
- `426` with `{"error": "client_too_old", "client_version": "0.2.0", "server_version": "0.4.0"}` for a client more than one minor version behind (R6), counted in a metric.
- `426` with `{"error": "unsupported_protocol", "protocol": 2, "supported": [1]}` for a protocol it doesn't speak.

On success the server's first text message is `{"type": "welcome", "server_version": "0.4.0", "protocol": 1}`. If the key is rotated or retired while connected, the server closes with `1008` and reason `client key revoked`.

**Binary messages: Opus frames.** One message per 20 ms Opus packet (R4). Little-endian:

| Offset | Size | Field | Notes |
|---|---|---|---|
| 0 | 1 | `kind` | `0x01` Opus frame |
| 1 | 1 | `reserved` | `0` |
| 2 | 2 | `sys_num` | uint16 |
| 4 | 8 | `call_num` | int64 |
| 12 | 8 | `start_time` | int64, Unix seconds |
| 20 | 4 | `frame_seq` | uint32, 0 for the call's first frame, +1 per frame. A gap means frames were dropped |
| 24 | rest | Opus packet | 1-1,275 bytes |

Frames carry only the identity. Talkgroup, `short_name` and the rest come from the call's `call_start` event, which the client always sends first on the same socket. The recorder half of R2's identity is the key that authenticated the socket.

**Text messages: JSON events.** Each has a `type`. Times are UTC Unix milliseconds.

```json
{"type": "call_start", "sys_num": 0, "call_num": 4182, "start_time": 1764000000, "short_name": "ex",
 "talkgroup": 1001, "unit": 5001, "patched_talkgroups": [1002], "start_time_ms": 1764000000120,
 "first_pcm_ms": 1764000000415}
```

`first_pcm_ms` is when the client received the call's first audio packet, the start of R38's delay budget. The client sends `call_start` when it starts a call, and again for every call still live after it reconnects, so the server never needs state from before the socket opened.

```json
{"type": "transmission", "sys_num": 0, "call_num": 4182, "start_time": 1764000000,
 "unit": 5123, "offset_ms": 4830, "patched_talkgroups": [1002]}
```

```json
{"type": "call_end", "sys_num": 0, "call_num": 4182, "start_time": 1764000000,
 "stop_time_ms": 1764000009870, "patched_talkgroups": [1002]}
```

`call_end` ends the live call. The recording follows by upload.

```json
{"type": "heartbeat", "client_version": "0.4.1", "time_ms": 1764000005000,
 "plugin": {"last_heartbeat_ms": 1764000003000, "boot_id": 2899123456},
 "systems": [{"sys_num": 0, "short_name": "ex", "system_type": "p25", "decode_rate": 38.7}],
 "queue": {"count": 0, "oldest_age_s": 0},
 "udp": {"lost": 0, "malformed": 0, "patch_copies": 0}}
```

The client sends a heartbeat every 5 s whether or not the plugin's arrived. It relays the latest plugin heartbeat's systems. When the plugin has been silent for 10 s, `systems` is `[]` and `plugin.last_heartbeat_ms` shows since when, which is R14's "no heartbeat (trunk-recorder down)". `queue` is R5's queue (count and age of its oldest entry). The counters are totals since the client started.

**Server → client** sends only `welcome` today. The client ignores and counts any other `type`.

### Recordings: HTTPS upload

`POST /api/v1/recorder/recordings`, `Content-Type: multipart/form-data`, with `Authorization: Bearer <client key>`, `RadioArchive-Client-Version` and `RadioArchive-Protocol`, and two parts in this order:

1. `metadata`, `application/json`:

```json
{
  "sys_num": 0, "call_num": 4182, "start_time": 1764000000, "short_name": "ex",
  "talkgroup": 1001, "patched_talkgroups": [1002],
  "start_time_ms": 1764000000120, "stop_time_ms": 1764000009870,
  "transmissions": [
    {"unit": 5001, "offset_ms": 292, "duration_ms": 3788},
    {"unit": 5123, "offset_ms": 4830, "duration_ms": 4920}
  ],
  "sha256": "9f2c…e1",
  "size": 15342
}
```

2. `audio`, `audio/ogg`: the Ogg/Opus recording (R4), at most 64 MB.

The metadata holds everything R19 stores, so a recording can be stored even if the server never saw the live call. `(recorder, sys_num, call_num, start_time)` makes the upload idempotent.

| Response | Meaning | Client |
|---|---|---|
| `201` `{"status": "stored", "call_id": "…", "sha256": "…", "size": 15342}` | Stored now | Removes the queue entry if `sha256` matches its own, otherwise retries |
| `200` `{"status": "duplicate", "call_id": "…", "sha256": "…"}` | Already stored with the same SHA-256 | Removes the queue entry |
| `200` `{"status": "rejected", "reason": "not_attached" \| "unknown_talkgroup" \| "ignored_talkgroup"}` | Refused by R12, R17 or R18, and counted by the server | Removes the queue entry |
| `409` `{"error": "identity_conflict", "sha256": "…"}` | Same identity already stored with a different file | Removes the entry, counts it, logs it. The server keeps the first |
| `400`, `413`, `415` `{"error": "…"}` | Malformed, too large, wrong type | Removes the entry, counts it, logs it. Retrying won't help |
| `401`, `426` | Key or version refused (as for the WebSocket) | Keeps the entry, pauses uploads until the key or client is fixed |
| `5xx`, timeout, network error | Transient | Keeps the entry and retries with backoff |

The server writes the file to a temporary name, checks its SHA-256 and size against the metadata (a mismatch is `400`), then renames it into place and commits the row in one step. A retry after a lost acknowledgement therefore gets `duplicate`, not a second call.

### Malformed input on the server

- **Unknown `type`** (text) or **`kind`** (binary): ignored and counted, so a newer client works with an older server.
- **Undecodable** (invalid UTF-8, not JSON, binary shorter than 24 bytes): close `1007` (invalid payload data).
- **Decodable but invalid** (a known `type` missing a required field or with a wrong type, an Opus packet over 1,275 bytes, a text message over 64 KB): close `1008` (policy violation), with the reason naming the field.
- **Frames for a call the socket hasn't started** (no `call_start`): dropped and counted. Not a close, because the race is harmless.
- **Any close**: the client reconnects with exponential backoff from 1 s to 60 s with jitter, and resends `call_start` for calls still live. Recordings are safe in the disk queue (R5), and live audio missed meanwhile is lost, as R5 says.

### Why two connections

Spec R3 said "one authenticated connection". A recording upload on the same WebSocket would queue behind (or in front of) live frames, and a large upload after an outage would delay every live call by seconds. A separate HTTPS request avoids that head-of-line blocking, and gives the upload its own status code and acknowledgement. Both still use the recorder's one client key, so `SPEC.md` R3 now reads "one client identity". This also refines ADR 0001's "one authenticated connection" for the recorder client.

## Fake recorder and fixtures

- **Fake recorder**, `testing/fakerecorder`, in Go. It speaks the plugin → client protocol above, from a **scenario**: a JSON timeline of events at millisecond offsets (audio from a fixture file for a call, transmission, `call_end`, heartbeat, a forced sequence gap, a malformed datagram). Scenarios cover the cases the issue lists: overlapping calls on one talkgroup (A1), a patch and a patched copy on the same channel (A4, R16), an unknown talkgroup and an unattached `shortName` (A2), heartbeats with a decode rate of 0 (R14), and gaps. Tests run it against a UDP listener and assert what arrives.
- **Fixture audio.** A small Go generator writes short `.pcm` files (8 kHz, signed 16-bit little-endian, mono): tones and noise bursts, a few seconds each. The files are committed and the generator reproduces them byte for byte. No real radio traffic is ever committed (spec section 7).

## Considered options

| Decision | Rejected | Why |
|---|---|---|
| Plugin framing | Length-prefixed JSON + PCM, as simplestream sends | JSON on every 0.1-0.5 s of audio is parsing work for nothing, and its framing assumes a stream |
| Plugin framing | Protobuf or another schema library | A build dependency in both C++ and Go for a dozen fields |
| Plugin transport | TCP or a Unix socket from the plugin | A blocked or slow client could stall trunk-recorder's recorder threads; simplestream's TCP mode can crash v5.2.1 (issue #2). UDP never blocks |
| Call identity | Sent once at call start, then a short handle per packet | A client restart or one lost datagram would orphan the rest of the call |
| Patched talkgroups | In every audio packet | Variable-length, rarely changes, not needed per packet |
| Loss | Retransmission or acknowledgements from the client | Late audio isn't live, and the recording already carries every sample |
| Client → server | Everything on one WebSocket, recordings in chunks | Head-of-line blocking of live frames behind uploads, and a hand-built acknowledgement and resume protocol |
| Client → server | Live frames over HTTP/2 streaming POST | No gain over a WebSocket, and less certain through home reverse proxies |
| Auth | Key in the first WebSocket message | The server would have to accept the upgrade before knowing who it is. A header refuses bad keys and old versions before the upgrade |
| Upload | `PUT` to a URL built from the identity | Also idempotent, but the metadata would be split between path and body. One POST with a metadata part keeps it in one place |
| Server, malformed client message | Ignore every bad message | A client sending garbage is broken. Closing makes that visible, and the reconnect costs nothing because recordings are queued |
| Patch de-duplication | Keep the supergroup's copy | `get_talkgroup_patch` doesn't say which talkgroup is the supergroup (`trunk-recorder/systems/system_impl.cc:758-770`) |
| Patch de-duplication | Match on unit and time alone | Two talkgroups can share a unit at once without being a patch |
| Fixture audio | Real recordings | Section 7 forbids real traffic in the repo, and generated audio is reproducible |

## Consequences

- **trunk-recorder must run with `audioStreaming: true` and `audioArchive: true`.** Without the first, no live audio. Without the second, trunk-recorder deletes the WAV as soon as `call_end` returns, before the client can read it. Our trunk-recorder image sets both. Nothing removes archived WAVs once uploaded, which is an open question below.
- **`SPEC.md` R3** changes from "one authenticated connection" to "one client identity". ADR 0001's wording is refined the same way.
- **Analog calls** arrive at 16 kHz, not 8 kHz. The protocol carries the rate, and the client accepts both.
- **A lost `call_end`** means a missing recording, reported by R19's alert rather than recovered.
- **Conventional DMR** concludes two slots with the same `call_num` and `start_time` (`trunk-recorder/call_impl.cc:180-189`), which R2's identity can't tell apart. Out of scope for v1 (P25 systems), and a later protocol version can add `tdma_slot` to the identity.
- **The patch-copy rule is unproven** on a real system, and it's counted so a deployment can see whether it ever fires.
- **Two protocol versions to evolve.** The plugin's `version` byte and the client's `RadioArchive-Protocol` header are independent. Both ends ignore unknown fields and types, so most additions need no bump.

## Open questions for the owner

1. **Analog audio.** The approved decision said 8 kHz only. trunk-recorder's analog recorders give 16 kHz. This ADR carries the rate and accepts both. Is that right, or should analog systems be refused for v1?
2. **Archived WAVs on the recorder.** With `audioArchive: true` they pile up. Should the recorder client delete trunk-recorder's WAV once the server acknowledges it (it's the queue entry's file, R5), or leave clean-up to the deployment?
3. **Patch copies.** Does the owner's system ever grant a member talkgroup on its supergroup's channel? A trunk-recorder log line `Should be Stopping RECORDING call` at trace level during a patch would show it.

## How each choice will be verified

| Choice | Verification | When |
|---|---|---|
| Plugin datagram format | Go unit tests encode and decode every message type against the byte tables above. The C++ plugin's own test sends one of each to a Go decoder in CI | First milestone |
| Malformed datagrams don't crash the client | Fuzz test (`go test -fuzz`) of the client's datagram decoder, plus fake recorder scenarios with bad magic, bad length and bad JSON | First milestone |
| Loss counting | Fake recorder scenario with forced sequence gaps and a `boot_id` change; the test asserts the lost count | First milestone |
| Patch copies dropped | Fake recorder scenario: a patched call and a copy on the same frequency and slot. One call reaches the server. A second scenario on the same frequency without a patch keeps both | First milestone |
| Fake recorder | A test replays a scenario and asserts what a UDP listener receives (issue #31 acceptance) | First milestone |
| Upload idempotency | Integration test: the same upload twice gives `stored` then `duplicate`, one row and one file | First milestone |
| Version and key refusal | Integration test: an old client version, a bad protocol and a revoked key each get the documented status, and a revoked key mid-session closes with 1008 | First milestone |
| No head-of-line blocking | Integration test: a large upload during a live call, the live call's frame delay stays within R38 | First milestone |
