# Research: delivering live calls to browsers and Android phones with seconds of delay

Ticket: [#3](https://github.com/RIKIKU/RadioArchive/issues/3). Terms follow `GLOSSARY.md`: **call**, **live call**, **recording**, **talkgroup**, **listening set**.

## Question

How can a server deliver a live call to a browser (including iOS Safari) and to an Android phone with a delay of a few seconds or less? Compare WebSocket with Web Audio or Media Source Extensions (MSE), WebRTC, and Low-Latency HLS (LL-HLS) on delay, codecs, browser support, behaviour with the screen off or the app in the background, and server cost at about 6 listeners.

## The shape of the traffic

This shapes the comparison more than any single protocol feature:

- A live call is a short burst of narrowband voice: 8 kHz audio, a few seconds long, with silence between calls.
- Several talkgroups can carry calls at once. Each listener hears only the talkgroups in their listening set, and the player has to show which call is playing now (talkgroup and unit).
- There are about 6 listeners at peak. Bandwidth and CPU are negligible for every option (see "Server cost"). What actually separates the options is complexity and client behaviour.

Continuous-stream protocols (HLS, and WebRTC tracks) expect an unbroken media timeline. A call-by-call feed therefore has to be turned into a continuous stream on the server: fill the gaps with silence, and either mix or queue overlapping calls for each listener. WebSocket delivery can send each call as its own tagged unit and let the client queue the calls.

## Options compared

| | WebSocket + WebCodecs/Web Audio | WebSocket + MSE / ManagedMediaSource | WebRTC | LL-HLS |
|---|---|---|---|---|
| Typical delay | Under 1 s (network plus a small client buffer you choose) | About 1 s (plus the browser's own buffering) | Under 1 s, designed for real-time | About 2-3 s with short parts; standard HLS is far longer |
| Codecs | Whatever AudioDecoder supports (Opus, AAC; check at runtime), or raw PCM | Container plus codec the browser's MSE accepts; AAC in fragmented MP4 is the common baseline | Opus and G.711 are mandatory | AAC, MP3, AC-3 or E-AC-3 as packed audio; fragmented MP4 or MPEG-TS segments |
| Chrome/Edge, Firefox, Android Chrome | Yes (AudioDecoder Chrome 94, Firefox 130) | Yes (MediaSource) | Yes | Not native: needs a JS player (hls.js) over MSE |
| iOS Safari | AudioDecoder only from Safari 26; Web Audio with PCM or JS decoding works earlier | No `MediaSource` on iPhone; `ManagedMediaSource` from iOS 17.1, only with remote playback disabled or an AirPlay alternative | Yes | Native, in `<audio>` |
| iOS screen off or Safari in background | Web Audio is suspended; fixes have regressed between releases | Unclear; no primary source found | Suspended | Best chance: a plain `<audio>` element is how normal media keeps playing |
| Android Chrome screen off | Plays while the tab keeps running; lock-screen controls need Media Session and a media element of at least 5 s | Same | Same | Same |
| Per-call metadata (talkgroup, unit) | Natural: each message carries its call | Natural over the same socket | Needs a data channel beside the track | Needs timed metadata (ID3) or a side channel |
| Server complexity | Low: one socket per listener, server filters by listening set | Low to medium: the server has to package fMP4 fragments | High: ICE/STUN, maybe TURN, DTLS-SRTP, UDP ports through the firewall | Medium: segmenter, playlist with blocking reload, silence fill, a stream per listening set or per talkgroup |

### WebSocket with WebCodecs or Web Audio

The server pushes each call's audio frames to every listener whose listening set includes that talkgroup, as soon as the recorder produces them. Each message is tagged with the call. The client decodes the frames with `AudioDecoder` (WebCodecs) and plays them through Web Audio, holding overlapping calls in a queue.

- Support: `AudioDecoder` arrived in Chrome 94, Firefox 130 and Safari 26 (iOS 26) ([BCD AudioDecoder](https://raw.githubusercontent.com/mdn/browser-compat-data/main/api/AudioDecoder.json)). On older iOS, send PCM or decode in JavaScript or WebAssembly. Narrowband PCM at 8 kHz and 16-bit is 128 kbps per call, which is still trivial at this scale.
- Codec: Opus is the natural fit. For narrowband speech in 20 ms frames, the recommended rate is 8 to 12 kbps ([MDN WebRTC codecs](https://developer.mozilla.org/en-US/docs/Web/Media/Guides/Formats/WebRTC_codecs)).
- Background on iOS: `AudioContext` can enter an `interrupted` state "by an occurrence outside the control of the web app" ([MDN BaseAudioContext.state](https://developer.mozilla.org/en-US/docs/Web/API/BaseAudioContext/state)). Setting `navigator.audioSession.type = "playback"` (Safari 16.4+, [BCD AudioSession](https://raw.githubusercontent.com/mdn/browser-compat-data/main/api/AudioSession.json); [W3C Audio Session draft](https://w3c.github.io/audio-session/)) is meant to keep audio playing. WebKit bug [261554](https://bugs.webkit.org/show_bug.cgi?id=261554) shows `AudioContext` still being suspended in the background even with that setting. The bug regressed in iOS 17.2-17.3 and was fixed in 2024, and a reporter confirmed iOS 17.5. Treat this as fragile.
- Delay: set by the client's jitter buffer (a few hundred ms is plenty) plus the time the recorder takes to emit frames.

### WebSocket with MSE / ManagedMediaSource

The transport is the same, but the client appends fragmented MP4 (or WebM) chunks to a `SourceBuffer` behind an `<audio>` element.

- MSE is in Chrome 31, Firefox 42 and Safari 8, but on iOS it is "Exposed in Mobile Safari on iPad but not on iPhone" ([BCD MediaSource](https://raw.githubusercontent.com/mdn/browser-compat-data/main/api/MediaSource.json)).
- `ManagedMediaSource` covers the iPhone from Safari 17.1 and is Safari-only: Chrome and Firefox lack it ([BCD ManagedMediaSource](https://raw.githubusercontent.com/mdn/browser-compat-data/main/api/ManagedMediaSource.json); [WebKit: Safari 17.1](https://webkit.org/blog/14735/webkit-features-in-safari-17-1/)). On Safari it "only activates when remote playback is explicitly disabled ... or when an AirPlay source alternative is provided" ([MDN ManagedMediaSource](https://developer.mozilla.org/en-US/docs/Web/API/ManagedMediaSource)). The user agent can also evict buffered content at any time.
- "H.264 video codec, AAC audio codec, and MP4 container format is a common baseline" ([MDN MSE](https://developer.mozilla.org/en-US/docs/Web/API/Media_Source_Extensions_API)). Opus support in each browser's MSE varies. Probe it with `isTypeSupported` at runtime.
- The benefit over Web Audio is that playback goes through a real media element, so Media Session and lock-screen handling follow the normal media path. Whether that keeps playing with the screen off on iOS is not documented in a primary source.

### WebRTC

- Delay is the lowest of all the options. Opus and G.711 (A-law and µ-law) are mandatory, and Opus runs anywhere from 6 to 510 kbps ([MDN WebRTC codecs](https://developer.mozilla.org/en-US/docs/Web/Media/Guides/Formats/WebRTC_codecs)).
- It is supported in every target browser.
- Background on iOS: suspended when the screen locks or Safari goes to the background. That claim comes from a developer request with no Apple reply ([Apple forum 774239](https://developer.apple.com/forums/thread/774239)), so it is low-trust but matches the Web Audio bug above.
- Cost: each listener needs its own peer connection with DTLS-SRTP, plus ICE/STUN, a TURN server for some networks, and UDP ports open on the server. Mixing or switching several talkgroups onto one track happens on the server, and call metadata needs a data channel. All of this exists to win a few hundred milliseconds that this use doesn't need. It is built for two-way calls.

### Low-Latency HLS

- The spec defines partial segments (`EXT-X-PART`), blocking playlist reload (`CAN-BLOCK-RELOAD`) and preload hints. `PART-HOLD-BACK` "MUST be at least twice the Part Target Duration" and "SHOULD be at least three times" ([draft-pantos-hls-rfc8216bis](https://datatracker.ietf.org/doc/html/draft-pantos-hls-rfc8216bis)). With 0.5 s parts that is at least 1.5 s of hold-back before network and encoding time, so about 2-3 s in practice. Apple's own LL-HLS page would not render for fetching, so this figure is derived from the spec and not quoted from Apple.
- Audio formats: packed AAC (ADTS), MP3, AC-3 or E-AC-3 with an ID3 timestamp, or fragmented MP4 or MPEG-TS segments (same draft).
- Support: native in Safari, including iOS. Other browsers need hls.js over MSE (`lowLatencyMode` defaults to true, and `preferManagedMediaSource` is opt-in) ([hls.js API](https://raw.githubusercontent.com/video-dev/hls.js/refs/heads/master/docs/API.md)). On Android apps, Media3 ExoPlayer supports "Low-latency HLS (Apple)", blocking playlist reload and preload hints ([Android: ExoPlayer HLS](https://developer.android.com/media/media3/exoplayer/hls)).
- Background: a native `<audio>` element playing HLS is the ordinary media path on iOS, so it is the most likely of the browser options to keep playing with the screen off. No primary source states this for live LL-HLS specifically.
- Fit: the server must produce a continuous stream, filling silence between calls. Overlapping calls on different talkgroups must be mixed, or queued and delayed, for each listening set. The stream is either one per listener (6 at peak, cheap) or one per talkgroup with the client switching streams (awkward with several active at once). Call metadata goes in ID3 timed metadata or a side channel.

## Android phone app (planned, not specified)

- Background playback requires the player inside a `MediaSessionService` running as a foreground service of type `mediaPlayback`, with the `FOREGROUND_SERVICE` and `FOREGROUND_SERVICE_MEDIA_PLAYBACK` permissions. The service publishes a MediaStyle notification. The same session is what Android Auto connects to ([Android: background playback](https://developer.android.com/media/media3/session/background-playback)). The call playing now (talkgroup and unit) goes in `MediaMetadata` and is updated with `Player.replaceMediaItem`, which doesn't interrupt playback (same page).
- The app can consume either the WebSocket feed, decoding it itself into a custom `Player` or audio sink, or an LL-HLS stream, which ExoPlayer plays as-is. "Listening only while switched on" maps directly onto the foreground service's lifetime.
- In Chrome on Android, the Media Session API gives the web app a notification and lock-screen controls. Chrome only requests the full audio focus that the notification needs for media at least 5 s long ([Chrome: Media Session](https://developer.chrome.com/blog/media-session)), so a short call played on its own won't get one. A long-running media element (an MSE stream) is better than one element per call.

## Server cost at about 6 listeners

Opus narrowband runs at about 12 kbps per call. Even with 5 concurrent calls sent to all 6 listeners, that is about 360 kbps. PCM would be about 4 Mbps. Every option fits on the same small host as the rest of the server. The cost differences are operational:

- WebSocket: one HTTP(S) port, which the server stack already has.
- LL-HLS: a segmenter, a playlist with blocking reload (HTTP/2 helps with the held requests), and silence fill.
- WebRTC: UDP ports, STUN/TURN, and DTLS. This is the heaviest to run.

## Answer

Use a **WebSocket feed of call audio frames, filtered on the server by listening set and tagged with call metadata**, as the one live-call protocol for the web app and the future Android app.

- **Delay:** under a second.
- **Fit with the traffic:** each call is a discrete burst on one talkgroup, and overlapping calls are queued on the client. No silence fill or mixing is needed.
- **Metadata:** the call playing now comes for free in each message.
- **Browsers:** decode with WebCodecs `AudioDecoder` where available, and fall back to PCM or a WebAssembly decoder on older iOS. MSE/ManagedMediaSource is a second playback path worth prototyping for better lock-screen behaviour.
- **Android app:** a Media3 `MediaSessionService` foreground service consuming the same feed covers screen-off listening and Android Auto.

WebRTC is rejected: the setup and running cost is much higher for a gain this use doesn't need. LL-HLS (about 2-3 s) stays as the fallback if screen-off listening in iOS Safari becomes a requirement. It is the only browser path likely to survive the screen being off on iOS, but it needs a mixed or queued continuous stream per listening set.

## Open points to verify by prototype

- Screen-off and background playback on current iOS for (a) Web Audio with `audioSession.type = "playback"`, (b) ManagedMediaSource, and (c) native LL-HLS. No primary source settles this.
- Opus in `AudioDecoder` and in MSE on Safari 26: probe with `isConfigSupported` and `isTypeSupported`.
- How soon the recorder can hand over audio while a call is in progress. That is a separate ticket, and it bounds the delay of every option here.
