# What trunk-recorder can send while a call is still in progress

Research for [#2](https://github.com/RIKIKU/RadioArchive/issues/2). Terms follow `GLOSSARY.md`: **call**, **live call**, **recording**, **talkgroup**.

Sources were read on 2026-11-15 from `TrunkRecorder/trunk-recorder`. Version labels used below:

- **v5.2.1** is the latest release (2026-04-08).
- **master** means the default branch as of 2026-09-27, which includes simplestream changes merged after v5.2.1 and not yet released.

## Answer in brief

- **Only one built-in plugin delivers audio during a call: `simplestream`.** It sends raw PCM (signed 16-bit, mono, 8 kHz for digital/P25 and 16 kHz for analog) over UDP or TCP, in small chunks as the recorder decodes them. It needs the global `audioStreaming: true` setting.
- **Metadata per packet (with `sendJSON: true`):** talkgroup, patched talkgroups, unit (`src`, `src_tag`), frequency, system `short_name` and sample rate. Optional `call_start` and `call_end` JSON events can be sent on the same socket.
- **What it does not send:** any call identifier (`call_num`, start time or recording filename). Matching a live call to its recording therefore needs a heuristic, a second channel such as the MQTT Status plugin, or a small custom plugin. The plugin API exposes everything that is needed.
- **No per-talkgroup limit.** One stream entry covers either one talkgroup or all of them (`TGID: 0`). All active calls can share one socket and are told apart by the JSON header.
- **Every other output arrives only after the call ends.** This covers the built-in uploader plugins, `upload_script`, the call JSON file, the MQTT Status `audio` topic and the stat socket.

## 1. Where live audio comes from (plugin API)

The plugin interface (`trunk-recorder/plugin_manager/plugin_api.h`) has these hooks that run during a call:

| Hook | When it fires | What it receives |
| --- | --- | --- |
| `audio_stream(Call*, Recorder*, int16_t* samples, int sampleCount)` | Each time the recorder's audio flowgraph produces samples | Decoded PCM plus the live `Call` object |
| `call_start(Call*)` | When a call is granted and starts recording, and **again** whenever the unit talking changes or the call restarts (see §4) | Live `Call` object |
| `calls_active(std::vector<Call*>)` | About once per second (the MQTT Status plugin documents "updated every second") | All active calls |
| `voice_codec_data(...)` | For every voice codec frame (IMBE/AMBE), before it is turned into audio | Raw codec parameters (master) |
| `call_end(Call_Data_t)` | After the call is concluded **and** its recording has been written | Full call record, including `call_num`, `start_time`, `filename` |

For P25, `p25_recorder_decode.cc` adds a `plugin_wrapper` sink next to the WAV sink, fed from the same `levels` block. This happens only when `get_enable_audio_streaming()` is true. The plugins therefore get the same audio that goes into the recording, at the same 8 kHz rate. The callback runs only while the recorder has a call (`if (d_call)`). `plugman_audio_callback` then calls `audio_stream` on every running plugin. Analog and DMR recorders call it the same way.

From the `Call*`, a plugin can read `get_call_num()`, `get_start_time()`, `get_start_time_ms()`, `get_talkgroup()`, `get_current_source_id()`, `get_freq()`, `get_sys_num()`, `get_short_name()`, `get_tdma_slot()` and `get_transmissions()` (`trunk-recorder/call.h`). `call_impl.cc` builds the call's stats `id` as `<sys_num>_<talkgroup>_<start_time>`.

Config: `audioStreaming` (global, default `false`): "Whether or not to enable the audio streaming callbacks for plugins." (`docs/CONFIGURE.md`)

## 2. simplestream: the built-in live-audio plugin

Source: `plugins/simplestream/simplestream.cc`. The docs are in `docs/CONFIGURE.md`, which is current and documents `sendJSON`/`sendCallStart`/`sendCallEnd`. `docs/Plugins.md` is out of date and still describes only `sendTGID`.

### Audio format

- Raw PCM, signed 16-bit, mono, with no container. The byte order is the host's (little-endian on x86 and ARM).
- 8 kHz for digital (P25) and 16 kHz for analog. The rate is not carried in the audio itself. Since `sendJSON` was added, each packet's JSON includes `audio_sample_rate`.
- Each packet is one `audio_stream` callback's worth of samples (`sampleCount * 2` bytes). The chunk size is whatever the flowgraph hands over and is not configurable.
- Data rate is about 16 KB/s per active digital call (8000 samples/s × 2 bytes). That is fine on a LAN or the same host. The docs say it is "not bandwidth efficient and is intended mostly to send audio to other programs running on the same computer ... or to other computers on the LAN."

### Framing (per packet)

| Option | Packet layout |
| --- | --- |
| neither | `[PCM]` |
| `sendTGID: true` (deprecated) | `[int32 LE talkgroup][PCM]` |
| `sendJSON: true` | `[uint32 LE json_length][JSON][PCM]`, where `sendTGID` is ignored |
| `sendJSON` + `sendCallStart` | additional packet `[uint32 len][JSON event=call_start]` with no PCM |
| `sendJSON` + `sendCallEnd` | additional packet `[uint32 len][JSON event=call_end]` with no PCM |

The `json_length` prefix is in host byte order (`buffer(&json_length,4)`). The docs call it "long integer format (4 bytes, little endian)".

### JSON fields

| Event | Fields (from source) |
| --- | --- |
| `audio` (every packet) | `src`, `src_tag`, `talkgroup`, `patched_talkgroups`, `freq`, `short_name`, `audio_sample_rate`, `event` |
| `call_start` | `src`, `src_tag`, `talkgroup`, `talkgroup_tag`, `patched_talkgroups`, `patched_talkgroup_tags`, `freq`, `short_name`, `event` |
| `call_end` | `talkgroup`, `patched_talkgroups`, `freq`, `short_name`, `event` |

Notes from the source:

- `src` is the current unit. If it is unknown (`-1`), the plugin falls back to the source of the most recent transmission, so the unit can change between packets of one call.
- None of the events include `call_num`, `sys_num`, `start_time`, `tdma_slot`, encryption or emergency flags, or the recording filename.
- **Patched talkgroups produce duplicates.** For each stream entry the plugin loops over the call's patched talkgroups and sends one packet per match. With `TGID: 0`, a call on a patch of N talkgroups is sent N times, each copy carrying a different `talkgroup` in the `audio` JSON. A receiver has to de-duplicate, for example by `(short_name, freq)` and timing.
- Talkgroups can be negative for conventional systems (v5.2.1 changelog, #1039).

### Configuration (stream object)

`TGID` (required; `0` means all), `shortName` (optional system filter; omit for all systems), `sendJSON`, `sendCallStart`, `sendCallEnd`, `sendTGID` (deprecated), and destination:

- **v5.2.1:** `address` + `port` + `useTCP`.
- **master (unreleased):** `url` such as `udp://host:port` or `tcp://host:port`, with hostname resolution. `address`/`port`/`useTCP` still work but log a deprecation warning.

You can configure several stream objects. There is no limit of one stream per talkgroup. With `TGID: 0` and `sendJSON: true`, every concurrent live call is interleaved on one socket and the receiver separates them by `(short_name, talkgroup, freq)`. The docs advise separate ports per system only when JSON is not used, because talkgroup IDs can overlap across systems.

### Transport limits

- **UDP:** fire-and-forget, with no ordering or delivery guarantee. Master logs send errors. v5.2.1 ignores them.
- **TCP:** trunk-recorder is the client and connects **once**, in `start()`. Nothing in the plugin reconnects. If the receiver is not listening when trunk-recorder starts, or the connection later drops, that stream stays dead until trunk-recorder restarts.
- **v5.2.1 risk:** a TCP send error throws `boost::system::system_error` and is not caught, which per the master fix (#1144, 2026-05-30) "would terminate the TR process". Master wraps the sends in try/catch and logs instead. Until a release includes #1144, use UDP or build from master.
- `audio_stream` runs on the recorder's flowgraph thread and sends synchronously, so a slow TCP receiver can hold up the recorder.

## 3. Outputs that only arrive after the call

These all run from `call_concluder.cc` after the call is concluded. `plugman_call_end` is called only after the WAV/M4A files and call JSON are written and any `upload_script` has finished:

- **Built-in uploader plugins** (the three `*_uploader` plugins for third-party services) upload finished recordings only.
- **`upload_script`** and the **call JSON file** (`<stem>-call_<call_num>.json`, containing `call_num`, `start_time`, `stop_time`, `call_length`, `talkgroup`, `freq`, `srcList`, `audio_type`, `short_name` and more).
- **MQTT Status plugin** (`TrunkRecorder/trunk-recorder-mqtt-status`, external). It sends **metadata** live: `call_start`, `calls_active` every second, `recorders`, and unit events. Its `audio` topic (`mqtt_audio`) holds the base64 WAV/M4A of a **completed** call, so it gives no live audio. Its `call_start`/`call_end` messages include `id` (`sys_num_talkgroup_start_time`), `call_num`, `start_time`, `unit` and talkgroup tags, and `call_end` adds `call_filename`. Its `call_start` also fires for calls that are monitored but not recorded (for example `mon_state_type: DUPLICATE`, `rec_num: -1`).
- **`stat_socket`** (source in `plugins/`, but not added in the top-level `CMakeLists.txt`, so it is not built by default) sends call stats over a websocket on `call_start` and `calls_active`. It sends no audio.
- **`streamer`** (gRPC, `plugins/streamer`) defines a `SendStream(stream AudioSample)` RPC. It is written against an older C-style plugin API (`plugin_t*`, `float*` samples), has had no changes since v5.0 (2024-07), is not built by `CMakeLists.txt`, and its `AudioSample` carries only `recorder_num`. Treat it as dead code.

## 4. Call timing and lifecycle facts that affect matching

From `trunk-recorder/monitor_systems.cc`:

- `call_start` fires when a trunked call is created from a grant (line ~560) and when a conventional recorder starts.
- It also fires **again on the same call** when `call->update(message)` reports a new source unit (line ~489). It also fires after a call is concluded and restarted on an idle timeout (`callTimeout`) or `maxDuration` (lines ~271–280). A receiver must treat `call_start` as "call started or its details changed", not as a strictly new call.
- A trunk-recorder call runs until the recorder has been idle for `callTimeout`. One trunk-recorder call can hold several transmissions from different units (`transmission_list`, `srcList`). This may be coarser than the glossary's **call** ("one transmission ... from key-up to the end of its audio"). The spec should decide which one RadioArchive means.
- `call_end` arrives **after** the recording is written, any conversion and `upload_script` are done, and earlier plugins' `call_end` handlers have run. It can also be retried (`RETRY` status, `MAX_RETRY`). Expect a delay of seconds after the audio stops, not a real-time end-of-call signal. The end of live audio packets is the faster signal.

The recording's file name (default `filenameFormat`) is `<captureDir>/<shortName>/<Y>/<M>/<D>/<talkgroup>-<start_epoch>.<ms>_<freq>[.<tdma_slot>]-call_<call_num>.wav` (master `call_concluder.cc`; v5.2.1 differs slightly). A custom `filenameFormat` supports `{talkgroup}`, `{call_num}`, `{epoch}`, `{freq}`, `{sys_num}`, `{tdma_slot}` and other tokens.

## 5. Matching a live call to its recording

Options, from least to most work:

1. **simplestream alone, heuristic.** For as long as a call is live, only one recorder holds a given `(short_name, freq, tdma_slot)` at a time. Key the live call on `(short_name, talkgroup, freq)` from the `audio` JSON. When that system's `call_end` arrives with the same `(short_name, talkgroup, freq)`, attach the recording by finding the call JSON / WAV whose `talkgroup` and `freq` match and whose `start_time` falls in the live window. Weaknesses: `tdma_slot` is not sent, so two Phase 2 calls on one frequency could collide; patch duplicates; `call_end` comes late and has no filename; UDP loss can hide the start.
2. **simplestream + MQTT Status.** MQTT `call_start` gives `id`/`call_num`/`start_time`/`tdma_slot` live, and `call_end` gives `call_filename`. The audio still has no ID, so joining the two is still a `(short_name, talkgroup, freq)` + time heuristic, just with better anchors. It adds a broker.
3. **Small custom plugin (or a simplestream fork / upstream PR).** In `audio_stream` and `call_start`, add `call->get_call_num()`, `call->get_sys_num()`, `call->get_start_time_ms()` and `call->get_tdma_slot()` to the JSON. In `call_end`, send `call_info.call_num`, `start_time` and `filename`. Then `(instance, sys_num, call_num, start_time)` is an exact join key. The start time is needed because `call_counter` restarts when trunk-recorder restarts. The plugin API already exposes all of this (§1). It is a few lines of C++ in a plugin built from `/user_plugins` (`docs/Plugins.md`, "Automatic Building and Development"). The same plugin could also reconnect over TCP and drop patch duplicates.

## 6. Limits and caveats summary

- Needs `audioStreaming: true`. Without it, no audio callbacks are wired into the flowgraph.
- Raw PCM only. Any compression for listeners (Opus and so on) is the receiver's job.
- No per-talkgroup cap. Concurrency is limited only by the number of recorders. Each active call costs about 16 KB/s of PCM.
- No call ID in simplestream. Matching needs a heuristic or a custom plugin (§5).
- Patched talkgroups give duplicate packets with `TGID: 0`.
- TCP: connects once with no reconnect, and on v5.2.1 a send error can crash trunk-recorder (fixed on master, unreleased).
- `docs/Plugins.md` is out of date. Use `docs/CONFIGURE.md` and the source.

## Sources

- `docs/CONFIGURE.md`: `audioStreaming` option and simplestream reference (master) https://github.com/TrunkRecorder/trunk-recorder/blob/master/docs/CONFIGURE.md
- `docs/Plugins.md`: plugin object, simplestream (older text), community plugins, user_plugins build https://github.com/TrunkRecorder/trunk-recorder/blob/master/docs/Plugins.md
- `plugins/simplestream/simplestream.cc` (master, and tag v5.2.1 for comparison) https://github.com/TrunkRecorder/trunk-recorder/blob/master/plugins/simplestream/simplestream.cc
- Commits: #1144 `697a54e` (2026-05-30, TCP try/catch, resolver), #1129 `a7810f3`, #1107 `8db338b`, #1039 `0fb1cd3` (negative TGIDs)
- `trunk-recorder/plugin_manager/plugin_api.h`, `plugin_manager.cc` https://github.com/TrunkRecorder/trunk-recorder/tree/master/trunk-recorder/plugin_manager
- `trunk-recorder/recorders/p25_recorder_decode.cc` (plugin sink wiring)
- `trunk-recorder/monitor_systems.cc` (`call_start` triggers), `trunk-recorder/call.h`, `call_impl.cc` (call stats and `id`)
- `trunk-recorder/call_concluder/call_concluder.cc` (`call_end` ordering, call JSON fields, filename format)
- `trunk-recorder/global_structs.h` (`Call_Data_t`)
- `plugins/streamer/streamer.proto`, `streamer_client_impl.cc`; `plugins/stat_socket/stat_socket.cc`; top-level `CMakeLists.txt` (which plugins are built)
- MQTT Status plugin README and `example_messages.md` https://github.com/TrunkRecorder/trunk-recorder-mqtt-status
