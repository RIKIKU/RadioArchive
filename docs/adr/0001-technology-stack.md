---
status: accepted (2026-11-15, by the owner)
---

# 1. Technology stack

Date: 2026-11-15

RadioArchive uses PagerArchive's stack (Go, PostgreSQL, SvelteKit, Docker Compose) unchanged wherever nothing gives a reason to differ. Where it differs, it's because of audio. A thin **recorder plugin** inside trunk-recorder hands each call's audio and its exact identity to a Go **recorder client** on the same machine. The client encodes to Opus and forwards live audio and finished recordings to the server. The server never transcodes, and it sends live calls to listeners over one WebSocket per listener. Decided with the owner in [Which technology stack does RadioArchive use?](https://github.com/RIKIKU/RadioArchive/issues/7).

## Summary

| Part | Choice |
|---|---|
| **Server (API)** | Go, as PagerArchive: `net/http`, `pgx` v5, `sqlc`, `goose` migrations, `coreos/go-oidc`, `log/slog`. One **static** binary (distroless `static`, amd64 only) that also serves the frontend and recordings |
| **Frontend** | Svelte 5 + SvelteKit (`adapter-static`, SPA) + TypeScript, embedded in the API binary. The same framework as PagerArchive, so the UI can be shared. *How* it's shared is decided in [What form do the shared UI and the combined deployment take?](https://github.com/RIKIKU/RadioArchive/issues/6) |
| **Database** | Official `postgres:18` image, settings passed in the compose file. No PostGIS, because there's no map or geocoding |
| **Admin sign-in** | Authentik via OIDC, sharing the `db` container. Listeners never sign in |
| **Admin bulk work** | PowerShell module over the admin API (Authentik sign-in, PKCE or device code, no API keys) |
| **Recorder plugin** | C++ trunk-recorder plugin in this repo, replacing simplestream. Sends PCM, call identity and call end (with the recording's file name) over UDP on localhost |
| **Recorder client** | Go, with cgo and libopus. Runs beside trunk-recorder, encodes Opus, buffers, and forwards to the server over one authenticated connection using its own client key |
| **Live feed to listeners** | One WebSocket per listener, filtered by listening set on the server, carrying call events (with IDs, resumable) and Opus frames |
| **Browser playback** | WebCodecs `AudioDecoder` (Opus) into Web Audio, with a WASM Opus decoder as the fallback |
| **Android app (planned, not specified)** | The same WebSocket, played by the app's own Media3 `Player`, which plays silence between calls |
| **Recordings** | Opus/Ogg files on a Docker volume, metadata in PostgreSQL, served with range requests, deleted by the nightly retention job |
| **Deployment and CI** | Docker Compose. GitHub Actions to GHCR. Server images amd64 only. Recorder images amd64 + arm64. MIT |
| **Tests** | Go `testing` (integration against a PostgreSQL service container), Playwright with axe-core |

## Audio path

```
trunk-recorder ──(recorder plugin)──UDP localhost──▶ recorder client ──WebSocket/HTTPS, client key──▶ API ──WebSocket per listener──▶ browser / app
   PCM + call identity                    Opus encode, buffer                 store recording, filter    Opus frames + call events
   call_end + file name                   read finished recording             by listening set
```

### Recorder plugin

simplestream, trunk-recorder's built-in live audio plugin, sends no call identifier, and its TCP mode can crash v5.2.1 ([What can trunk-recorder send while a call is still in progress?](https://github.com/RIKIKU/RadioArchive/issues/2)). The plugin API already exposes `sys_num`, `call_num`, the start time, talkgroup, unit and, at call end, the recording's file name. So the plugin:

- sends each call's PCM as the recorder decodes it, tagged with `sys_num`, `call_num`, `start_time`, talkgroup and unit
- sends `call_end` with the same identity and the recording's file name once the file is written
- sends only to UDP on localhost, never across a network, with no authentication, encoding or buffering of its own

Each recording is then matched exactly to its live call by **(recorder client, `sys_num`, `call_num`, `start_time`)**, with no matching heuristic and no `upload_script`.

The plugin lives in this repo (for example `recorder/plugin/`). CI builds it against a **pinned trunk-recorder release** (v5.2.1 at the time of writing; the pin is part of the build). It's published as an image layered on trunk-recorder's official image (amd64 + arm64), plus the `.so` file for native installs. Upgrading trunk-recorder means bumping the pin and passing CI.

### Recorder client

Modelled on PagerArchive's receiving client (its R13–R15 and R59):

- Runs as a container beside trunk-recorder's, or natively under systemd (amd64 + arm64).
- Encodes live PCM to narrowband Opus with libopus via cgo, and reads the finished recording on `call_end` and encodes it to Opus/Ogg. The arm64 build needs cgo, which `docker buildx` with libopus handles.
- Holds one authenticated connection to the API with **its own client key**, issued from the server shell, never through a sign-in (as PagerArchive's R14/R59). The key identifies the recorder, so a second recorder is a second key.
- Buffers briefly in memory when the link drops. Live audio that arrives too late to be live is dropped, and the recording still arrives.

### Server

The server receives Opus and stores Opus, so it **never transcodes** and stays a pure-Go static binary. It:

- filters live calls per listener connection by listening set, and keeps hidden talkgroups out of every feed
- stores recordings as files on a volume, with call, talkgroup, unit, times and path in PostgreSQL, and serves them with `http.ServeContent` so players can seek
- deletes expired recordings and their rows in a nightly job (goroutine + jobs table + advisory lock, as PagerArchive, with no queue)

### Live feed to listeners

One WebSocket per listener carries both:

- **call events** (start, end, talkgroup and unit names), each with an ID. Reconnecting with the last ID replays missed *events*. Missed *audio* is never replayed.
- **Opus frames**, tagged with their call, about a second behind the radio.

Calls stay separate. Overlapping calls on different talkgroups are queued by the player, so the server needs no mixing, silence fill or stream per listening set.

## Considered options

| Decision | Rejected | Why |
|---|---|---|
| Live transport | WebRTC | ICE/STUN/TURN, DTLS-SRTP and UDP ports, to win milliseconds this use doesn't need |
| Live transport | LL-HLS | About 2–3 s of delay, and a continuous mixed stream per listening set. **Kept as the fallback** if iOS Safari screen-off listening becomes a requirement |
| Android audio | A continuous HTTP Ogg/Opus stream per listening set, which ExoPlayer plays as it is | Needs a server-side mixer and encoder per listener. The app needs custom code for per-call metadata anyway |
| Events and audio | SSE for events plus a WebSocket for audio (PagerArchive uses SSE) | Two connections to filter and keep in step. The app would hold two sockets |
| Getting audio off trunk-recorder | Stock simplestream + `upload_script`, matching on system, talkgroup, frequency and time | A heuristic match can join the wrong recording. The owner chose an exact match from a small plugin |
| Getting audio off trunk-recorder | Plugin does everything (Opus, auth, buffering, upload) in C++ | More C++ to rebuild for each trunk-recorder version. Go already does this in PagerArchive's client |
| Opus encoding | On the server via cgo, or `ffmpeg` as a child process | Gives up the static distroless server image, and puts CPU on the server for every call |
| Recordings | PostgreSQL `bytea` | Bloats backups and vacuum |
| Recordings | S3 / MinIO | One more container for no gain at this scale |
| Database | PagerArchive's PostGIS image | No spatial data here |

## Consequences

- **Three languages:** Go, TypeScript and a small amount of C++. The C++ is kept to a thin shim on purpose.
- **trunk-recorder upgrades are gated** on rebuilding the plugin against the new release.
- **The recorder client isn't a static binary** (cgo with libopus). The server still is.
- **Plain `postgres:18` alone.** If the combined deployment shares one `db` container with PagerArchive, it has to be PagerArchive's PostGIS image. That belongs to [What form do the shared UI and the combined deployment take?](https://github.com/RIKIKU/RadioArchive/issues/6).
- **iOS background playback is unproven.** Web Audio is suspended when the screen is off, even with `audioSession.type = "playback"`, and WebKit has regressed between releases. Safari's Opus support in WebCodecs is also to be confirmed. If iOS screen-off listening becomes a requirement, the fallback is LL-HLS played by an `<audio>` element.
- **Storage sizing** for a month of about 1,000 talkgroups is still open (map fog). This ADR fixes only the kind of storage.

## How each choice will be verified

| Choice | Verification | When |
|---|---|---|
| Plugin builds against the pinned trunk-recorder | CI builds the plugin image for amd64 + arm64 and runs trunk-recorder with it against a recorded I/Q or audio fixture | First milestone |
| Exact match of live call to recording | An integration test: plugin → recorder client → API, asserting every recording links to the live call with the same identity, including overlapping calls on one talkgroup | First milestone |
| Delay under about a second | Playwright with a fake recorder: time from the plugin's first PCM packet to the browser's first decoded frame | First milestone |
| Browser playback | Playwright in Chrome, Firefox and WebKit: WebCodecs path, plus the WASM fallback with `AudioDecoder` removed | First milestone |
| iOS background behaviour | A throwaway prototype on a real iPhone with the screen off and in the background. Its result decides whether LL-HLS is needed | First milestone |
| Hidden talkgroups never leave the server | Integration test: a hidden talkgroup's calls never appear on any listener WebSocket, recording URL or catalogue | First milestone |
| Server stays static | CI builds the API on distroless `static`, and the build fails if cgo is enabled | First milestone |
