---
status: proposed
---

# 47. Live audio at the recordings' loudness

Date: 2026-11-26

Live calls played much quieter than the recordings of the same calls. The live stream and the recording come from different points in trunk-recorder, and only the recording is loudness-normalised. This ADR records the measurements and why the recorder client now levels live audio to the recordings' loudness.

## Why they differ

In trunk-recorder v5.2.1 the P25 decoder's audio goes through one `levels` block (gain 1 unless `digitalLevels` is set) and then to two sinks: the plugin's audio stream and the transmission WAVs (`trunk-recorder/recorders/p25_recorder_decode.cc:106-119`). Up to that point the two paths are the same samples.

When the call ends, the call concluder renders the transmission WAVs into the call's WAV with ffmpeg (ADR 0046). That render also runs ffmpeg's **`loudnorm`**, which is **on by default** for every system, separately from `audio_postprocess.enabled`. The defaults are `loudnorm_i` -16 LUFS, `loudnorm_tp` -0.1 dBTP and `loudnorm_lra` 11, in two passes when the first pass can measure the call (`trunk-recorder/config.cc:384-404`, `trunk-recorder/global_structs.h:69-73`, `trunk-recorder/call_concluder/call_concluder.cc:596-634`, `docs/CONFIGURE.md` "Audio Post-Processing"). The live stream never goes through it. ADR 0046 missed this: the WAV isn't just the live audio at 16 kHz, it's that audio loudness-normalised.

The recorder client, the server and the browser don't change the level of either path. The client passes the plugin's int16 PCM to libopus unchanged on both paths, and the browser's live player plays through a gain node left at 1.

## Measurements

These were measured on the cld01 demo, which is fed live by a real recorder (trunk-recorder v5.2.1, one P25 system, no `audio_postprocess` in its config; its log at startup says `Audio Loudnorm: true`, `I: -16`, `TP: -0.1`). A probe joined `/api/v1/live` as a browser does and decoded each call's Opus frames with libopus at 48 kHz. When the call's recording arrived, the probe decoded that too. Loudness is BS.1770 integrated loudness (`recorderclient.Loudness`).

| 47 calls, 1-31 s | Live, as the browser gets it | Recording |
|---|---|---|
| Integrated loudness | -34.1 LUFS on average | -19.5 LUFS on average (-17.1 to -22.5) |
| Peak | -5 to -23 dBFS | -4.3 to 0.0 dBFS |
| Recording louder by | | **14.5 LU on average (4.8 to 23.1)** |

The live audio's level varies from call to call, depending on the transmitter, so no fixed gain could close the gap. The recordings come out under loudnorm's -16 target because P25 voice peaks about 20 dB above its loudness, and loudnorm keeps the true peak under -0.1 dBTP.

The plugin's CI test shows the same gap before Opus. It runs the real trunk-recorder against the synthetic fixture: the first call's live PCM, as the plugin sends it, is -22.6 LUFS and its recording -16.4.

## Decision

| Part | Choice |
|---|---|
| **Where** | The recorder client, on live audio only, just before each 20 ms frame is encoded. Recordings are uploaded as trunk-recorder rendered them, as before |
| **Measure** | BS.1770-4 integrated loudness, as `loudnorm` measures it: K-weighted, 400 ms blocks every 100 ms, gated at -70 LUFS absolute and 10 LU relative |
| **Target** | `LiveLoudness`, -20 LUFS. The gain follows the call's loudness so far, which compresses a little and lands about 1 LU above the target, at the recordings' measured -19.5 |
| **Gain** | Set from the call's first audio above the gate, then following the loudness so far. It rises at most 1 dB per frame and falls at once. It stays between -20 and +30 dB, so a channel's noise floor is never boosted to speech level. Audio below the -70 LUFS gate is left as it is |
| **Peaks** | Each frame's gain is held to keep its peak under -1 dBFS, leaving room for Opus overshoot. The gain ramps across each frame so it never steps |
| **Tests** | Unit tests: the meter's BS.1770 calibration at both rates; speech-like audio from -44 to -13 LUFS levelled to within 2 LU of the target and never over -1 dBFS; silence and sub-gate hiss untouched; the engine encodes levelled PCM. The plugin's CI test levels the real trunk-recorder's live audio with the client's own leveller and checks it against the call's real recording (within 3 LU) |

After the change, on the same 47 calls (the live audio levelled offline with `LevelLive`), the live audio is on average **0.3 LU louder** than the recording. The mean difference either way is 1.1 LU and the range -2.5 to +4.2 LU. Short calls of 2-3 s vary the most, because there's least of the call to measure before it ends.

## Considered options

| Option | Why not |
|---|---|
| **Turn trunk-recorder's `loudnorm` off** (`"audio_postprocess": {"loudnorm": false}` per system) | Recordings would then be as quiet as live audio is now, and as uneven from call to call. It's the deployment's choice, not ours, and on a recorder shared with another uploader (ADR 0045) it changes that uploader's audio too |
| **Level in the browser** (a Web Audio compressor or gain on the live path) | Every client implementation would need its own copy. The level would also depend on the player, and the server's live frames would stay quiet for anything else that reads them |
| **Level in the plugin** | C++ inside trunk-recorder's GNU Radio threads (ADR 0001). Any change would need a trunk-recorder restart, and it's harder to test than Go |
| **A fixed gain** | The measured gap ranges from 5 to 23 LU from call to call |
| **Level the recordings as well**, so both paths go through one leveller | It would change audio that's already at the target, and the stored recording would no longer be what trunk-recorder rendered |
| **Copy `loudnorm` exactly**, with one gain from the call's true peak | That needs the whole call. Live audio can't wait for it, and one click at the start of a call would hold the rest of the live audio down, as it does to the fixture's second recording |

## Consequences

- **The recorder client must be upgraded** for live audio to be levelled. The plugin, server and browser don't change, and recordings don't change.
- **If a deployment turns `loudnorm` off** or changes `loudnorm_i`, live audio still plays at about -19.5 LUFS while recordings follow the new setting. The plugin could report each system's loudnorm settings in its heartbeat, and the client could take its target from that. That's left for later.
- **A trunk-recorder upgrade** must recheck the recordings' loudness. The plugin's CI test fails if the levelled live audio and the recording drift more than 3 LU apart.
