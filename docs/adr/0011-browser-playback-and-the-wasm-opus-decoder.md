---
status: proposed
---

# 11. Browser playback and the WASM Opus decoder

Date: 2026-11-24

The Listen page's player opens the live feed (ADR 0007) when the listener presses **Start listening**, and decodes each call's Opus frames with WebCodecs' `AudioDecoder` where the browser has it with Opus support, or otherwise with **opus-decoder** 0.7.12, a WASM build of libopus. Both feed one Web Audio output, and a finished call's recording (Ogg/Opus) is read in the page and played through the same decoder and output. The WASM decoder is part of the app's own JavaScript, embedded in the binary like the rest of the app, loaded only by a browser that needs it, and never fetched from anywhere else. These are the implementer's choices where `SPEC.md` P2, R38, R39 and R44 leave room (spec "How to use this spec", item 6). Built in [Browser playback: WebCodecs, the WASM fallback and the delay budget](https://github.com/RIKIKU/RadioArchive/issues/58).

## Summary

| Part | Choice |
|---|---|
| **Decoder, first choice** | WebCodecs `AudioDecoder`, `{codec: "opus", sampleRate: 48000, numberOfChannels: 1}`, chosen when `AudioDecoder.isConfigSupported` says yes. One decoder per call |
| **Decoder, fallback** | [opus-decoder](https://github.com/eshaz/wasm-audio-decoders/tree/master/src/opus-decoder) **0.7.12** (npm, pinned exactly in `web/package.json`), its `OpusDecoder` on the page's main thread. The WASM is inside the package's JavaScript, so Vite bundles it into one chunk of the app (`_app/immutable/chunks/`), which only a browser without WebCodecs' Opus loads |
| **Licences** | opus-decoder and its `@wasm-audio-decoders/common` and `simple-yenc`: **MIT**. The WASM is libopus: **BSD-3-Clause**. Both are compatible with RadioArchive's MIT licence. Its web-worker helper, `@eshaz/web-worker` (Apache-2.0), is installed with it but never bundled: the player doesn't use the worker class |
| **Content Security Policy** | `script-src` gains `'wasm-unsafe-eval'`, which lets the page compile WebAssembly and allows no `eval` |
| **Output** | One `AudioContext`, made inside the Start listening click (R44). Each decoded 20 ms frame is an `AudioBufferSourceNode` scheduled after the call's last, through a gain node and an analyser to the speakers. A call's first frame, and any after a gap, starts 150 ms ahead, room for the network's jitter |
| **Recordings** | `GET /api/v1/calls/{call}/recording`, read whole, split into packets by a small Ogg reader in the page (`web/src/lib/audio/ogg.ts`), and decoded and played like the live frames. No `<audio>` element, so all three browsers play it the same way |
| **One call at a time** | Calls play in the order they started; one that starts while another plays waits, with its frames. A call that has ended by its turn with nothing heard of it plays from its recording (R43). Priority, skip, pause, hold and the dock are #60's |
| **Feed** | `web/src/lib/live/feed.ts`: the `set` message from the listening set (ADR 0008's `sendListeningSet`, which now hands each set to the open feed), the highest event ID kept, and a new socket after a drop (0.5 s doubling to 10 s) that resumes from it |

## Why opus-decoder

R39 asks for a WASM Opus decoder for a browser without WebCodecs' Opus, served from the binary, and the project is MIT, so the decoder must be under a licence compatible with it. What was weighed:

- **opus-decoder** (chosen): libopus itself, built with Emscripten, about 85 KB of JavaScript (66 KB gzipped) with the WASM inside it. It decodes one Opus packet at a time (`decodeFrame`), which is what the live feed carries, with no container. MIT, with libopus under BSD-3-Clause. Maintained (0.7.12, 2026-08), and its sibling `ogg-opus-decoder` is used widely in browsers. Because the WASM is in the JavaScript, the build needs no loader for `.wasm` files and the server no new route or MIME type: it's a file of the app like any other.
- **Building libopus to WASM in this repo**: the same code, but an Emscripten toolchain in CI and a second build to keep up, for nothing opus-decoder doesn't already give.
- **A decoder that only reads Ogg files** (such as `ogg-opus-decoder`): the live feed has raw packets, not Ogg pages, and playing recordings through the same packet decoder keeps one path.
- **Decoders under the GPL or LGPL, or ones loaded from a CDN**: ruled out by R39 and the MIT licence.

The decoder runs on the main thread. A 20 ms mono narrowband frame decodes in well under a millisecond, so a worker would add a hop to every frame for no gain.

## What the browser tests found (A7, 2026-11-24)

Playwright 1.64's Chromium, Firefox and WebKit all have `AudioDecoder` with Opus, so each takes the WebCodecs path, and each takes the WASM path with `AudioDecoder` removed (`delete window.AudioDecoder` before the page loads). WebKit's is Playwright's Linux build; Safari on macOS isn't run in CI.

Headless Firefox on Linux plays Web Audio only to a sound server: with none, its `AudioContext` never leaves `suspended`. The CI job installs PulseAudio, and `scripts/ci/e2e-web.sh` starts it (it plays to nothing). A listener's Firefox has one already.

## The delay (R38, A6)

Measured from the fake recorder writing a call's first PCM datagram to the browser's first decoded frame of that call (the `AudioDecoder` output callback, or `decodeFrame` returning), both by the same machine's clock, over 50 calls each in Chromium, Firefox and WebKit, through WebCodecs. The tests print p50 and p95 in the job's output and fail at a p95 of 2 s.

On the devbox, in CI's Playwright image (2026-11-24), the whole stack on one machine:

| Browser | p50 | p95 | max |
|---|---|---|---|
| Chromium | 7 ms | 8 ms | 13 ms |
| Firefox | 10 ms | 20 ms | 24 ms |
| WebKit | 28 ms | 83 ms | 123 ms |

Nothing in the path waits for more audio than one 20 ms frame: the recorder client encodes and forwards each frame as soon as it has its samples, the server forwards it as it arrives, and the page decodes it at once. The budget is spent on the network between a recorder, the server and a listener, which a test on one machine doesn't have, and on the 150 ms the player schedules ahead, which comes after the first decoded frame and so outside what R38 measures. P2's "about a second behind the radio" is a ceiling, not a target to buffer up to.

## Consequences

- The page announces each call's first decoded frame (`radioarchive:first-frame`) and the end of each call it played (`radioarchive:played`, with the decoder, the frames and the loudest sample that reached the output) as window events, for the browser tests and for R90's delay metric later.
- The browser tests now run the recorder client beside the binary: the web job builds it from source with libopus, since it runs inside Playwright's image with no Docker. The image the client ships in is built and run end to end by the image job (A1, A10).
- #60 builds the queue's controls and the dock on this player: it orders the calls, and the decoding, the output and the recordings stay as they are.
- R44's "switching away to PagerArchive stops the audio" waits for the combined deployment (M7).
