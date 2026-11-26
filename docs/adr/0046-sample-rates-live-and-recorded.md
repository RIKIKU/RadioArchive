---
status: proposed
---

# 46. Sample rates, live and recorded

Date: 2026-11-26

ADR 0002 has the recorder client take 8 kHz PCM, and until now the client refused anything else, live or recorded (issue #31). A real trunk-recorder, the pinned v5.2.1 image with a P25 system and no sample-rate setting in its config, writes every call's WAV at **16 kHz**, so the client refused every recording it made (`recording is 16000 Hz; v1 takes 8000 Hz (P25) only`), and nothing reached the server. This ADR records why, and what the client now takes.

## Why the recordings are 16 kHz

trunk-recorder v5.2.1 has two rates for one P25 call:

- **Live audio, 8 kHz.** The plugin's `audio_stream` gets the P25 decoder's output, at `Recorder::get_wav_hz()`, which is 8,000 for P25 and DMR (`trunk-recorder/recorders/recorder.h:97`). Each transmission's own WAV is 8 kHz too (`trunk-recorder/recorders/p25_recorder_decode.cc:85`).
- **The call's WAV, 16 kHz.** When the call ends, the call concluder renders the call's transmissions into the one WAV that `call_end` names, with ffmpeg, and always passes `-ar 16000` (`trunk-recorder/call_concluder/call_concluder.cc:405-417`, used at `:640-662`). No config option changes it.

So the WAV holds the same audio as the live stream, resampled to 16 kHz, with nothing above 4 kHz. The audio-postprocess filters (`audio_postprocess`, `trunk-recorder/call_concluder/call_concluder.cc:345`) run in the same ffmpeg pass and don't change the rate. (That pass also runs `loudnorm`, on by default, so the WAV is louder than the live stream: ADR 0047.)

CI's plugin test ran this same trunk-recorder, and its recordings were 16 kHz too. The test only checked that each WAV existed and was longer than a header, and the fake recorder wrote its WAVs at the live rate, 8 kHz, so no test ever put a real recording through the client's reader.

## Summary

| Part | Choice |
|---|---|
| **Rates the client takes** | 8,000 and 16,000 Hz, live and recorded. Anything else is dropped and counted as malformed, as before (`radioarchive_client_udp_malformed_total{reason="sample_rate"}`) |
| **Recordings** | Encoded at the WAV's own rate, never resampled. An 8 kHz WAV makes an 8 kHz encoder, a 16 kHz WAV a 16 kHz one |
| **Live audio** | The rate is in every audio datagram's header (`sample_rate`, ADR 0002), so the client learns it from the call's first datagram and makes the call's encoder at that rate. Frames are 20 ms at that rate: 160 samples at 8 kHz, 320 at 16 kHz. A later datagram for the same call at another rate is dropped as malformed, because its PCM would play at the wrong speed through that encoder |
| **The plugin** | Unchanged. It still sends only 8 kHz audio, the only rate a P25 recorder gives it, and logs and drops any other. A P25 system's live audio is 8 kHz and its recordings 16 kHz |
| **Opus settings** | R4's, at either rate: 12 kbit/s VBR, narrowband forced, `OPUS_APPLICATION_VOIP`, 20 ms frames. Narrowband keeps only what's below 4 kHz, which is all the 16 kHz WAV holds, so both rates give the same kind of stream (SILK narrowband, 20 ms) at the same bitrate |
| **The Ogg file** | `OpusHead`'s input rate is the WAV's rate. The pre-skip and every granule position count 48 kHz samples whatever the input rate (RFC 7845), so the recording's length is right at either rate |
| **The server** | Unchanged. It never transcodes or reads the rate: an upload must start `OggS`, and a live frame is an Opus packet of 1 to `MaxOpusPacket` bytes |
| **The browser** | Unchanged. Both decoders (WebCodecs and the WASM fallback, ADR 0011) decode at 48 kHz whatever the input rate, and the Ogg reader takes only the channel count and pre-skip from `OpusHead` |
| **Tests** | The plugin's CI test reads each real recording with the client's own reader (`recorderclient.ReadRecording`) and checks it is 16 kHz and as long as the call's 8 kHz live audio. The fake recorder writes its WAVs at 16 kHz, as trunk-recorder does, so the end-to-end and browser tests upload 16 kHz recordings. Unit tests cover both rates on both paths |

## Considered options

| Option | Why not |
|---|---|
| **Resample the 16 kHz WAV to 8 kHz in the client** | More code (a filter to keep aliasing out) for nothing: the encoder's narrowband setting already drops everything above 4 kHz, and the stream and its size are the same either way |
| **Set trunk-recorder's output rate** | v5.2.1 has no setting for it; `-ar 16000` is hard-coded. Patching trunk-recorder is out of the question: the image is the official one, pinned by digest (ADR 0001, `recorder/plugin/Dockerfile`) |
| **Read the transmissions' own 8 kHz WAVs instead** | `call_end` names only the rendered file, and trunk-recorder deletes the transmissions' files as soon as every plugin's `call_end` returns (`trunk-recorder/call_concluder/call_concluder.cc:862-888`), before the client reads anything. It would also lose the audio-postprocess filters a deployment chose |
| **Change the plugin to send 16 kHz live audio** | It would have to resample the decoder's 8 kHz output, which adds nothing, and a new plugin needs trunk-recorder restarted. The client takes 16 kHz live audio anyway, so an analog recorder needs no client change if it's ever supported |
| **A sample-rate field in the call events or the upload's metadata** | Not needed. The datagram header already carries the live rate, the WAV header carries the recording's, and an Opus stream needs neither to be decoded |

## Consequences

- **Recordings from a real trunk-recorder upload.** A client from before this change refuses them all; it must be upgraded. The server needs no change.
- **ADR 0002's "8 kHz only" is gone.** Its open question 1 asked whether to take analog's 16 kHz; the client now takes 16 kHz for a different reason, and analog systems stay out of scope for v1 (`SPEC.md`, Out of scope): the plugin sends nothing from a system that isn't P25, and the import skips them.
- **A trunk-recorder upgrade** must recheck the rate its call concluder renders at. The plugin test now fails if it isn't 16 kHz, so the change can't go unnoticed.
