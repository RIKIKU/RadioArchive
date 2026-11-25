---
status: proposed
---

# 37. Live listening limits and "the radio is full"

Date: 2026-11-26

The listener's WebSocket, `GET /api/v1/live` ([ADR 0007](0007-the-live-feed-to-listeners.md)), is capped at **4 open per client and 50 in all** (R71), on ADR 0036's client and exemptions. A cap only refuses new sockets. A refused handshake is answered over HTTP, which the phone app reads (R75). The browser's WebSocket can't read it, so the same path answers a plain GET with the same verdict. These are the implementer's choices where `SPEC.md` R36, R70, R71, R75 and R78 leave room (spec "How to use this spec", item 6). Built in [Live listening limits and "the radio is full" (server)](https://github.com/RIKIKU/RadioArchive/issues/140).

## Summary

| Part | Choice |
|---|---|
| **What counts** | A listener socket counts from when its upgrade is let through until its handler returns, which is when the socket has closed. A handshake that fails, or is refused, never counts. Release happens once, however the socket ends |
| **The client's cap** (`LIMIT_LIVE_PER_CLIENT`, 4) | The 5th: `429`, `Retry-After: 30`, `{"error": "too_many_live", "message": "Too many live connections from here: at most 4 at once. Try again in 30 seconds.", "retry_after": 30}` |
| **The radio's cap** (`LIMIT_LIVE_TOTAL`, 50) | A new socket when 50 are open: `503`, `Retry-After: 30`, `{"error": "radio_full", "message": "The radio is full right now. Try again in 30 seconds.", "retry_after": 30}`. When both caps are reached, the client's is the answer, since closing one of its own sockets is what helps |
| **Existing listeners** | Never closed by a cap (R71) |
| **The browser's check** | `GET /api/v1/live` without `Upgrade: websocket` opens and counts nothing. It answers the refusal a handshake would get now, or the WebSocket library's `426 Upgrade Required` when there's room |
| **Retry** | 30 s, R71's, both as `Retry-After` and as the browser's wait (#142). It's a constant (`limits.LiveRetry`), question 1 on #138 |
| **Admins** (R70) | A socket that would be refused is let in when a signed-in admin opens it, by session cookie or the module's token. It isn't counted, so an admin can go over either cap. Their sockets under the caps count like anyone's |
| **The recorder client** | Its live connection is a recorder route (`/api/v1/recorder/live`), never counted. A full radio still takes calls and sends them to every open listener |
| **Telemetry** | `Limits.Hits(Live)` counts refusals of either kind. `Limits.LiveOpen` is the number counted open, for M6's metrics and R91's "cap full for 10 minutes" alert |

## Why the browser asks again with a GET

A browser's `WebSocket` reports a refused handshake only as a `close` event with code 1006, whatever the status was. There were three ways to let it tell "you have 4 open" or "the radio is full" from a network fault:

1. **Accept the socket, send a refusal message, and close it.** The browser can read that. But the handshake then says `101`, not R73's `429`, so the phone app and A20's check would have to read a message instead of a status, and every refused socket would cost an upgrade.
2. **A separate status route**, such as `GET /api/v1/live/status`. That's another public route for the A10 harness, and its answer could drift from the handshake's.
3. **The same path, asked without the upgrade** (chosen). The limiter answers it exactly as it would answer the handshake, through the same code, so the two can't drift. When there's room, the WebSocket library's own `426` answers, and the browser goes back to its usual reconnecting. The check opens nothing, so it can't use up a slot, and it's cheap: no database is read. It's asked only after a socket closes before it opens.

The check and the next handshake are separate moments, so a slot can come or go in between. The browser treats the check as a hint: a `426` is followed by its ordinary reconnect, and a refusal by waiting `Retry-After`.

## "The radio is full" is the browser's notice

ADR 0007 kept `radio_full` as a feed notice. A refused listener has no socket for the server to send one on, so the browser raises the notice itself when its check answers `radio_full`, and clears it once a socket is let in (#142). The server never sends a `radio_full` notice. ADR 0007's `notice` message is unchanged, and the recorder notice still comes from the server.

## 503 for the radio's cap

R73's `429` is "this client has made too many requests" (RFC 6585). When the radio is full, the client hasn't done anything too often: the server has no room for anyone. RFC 9110 gives that `503` with `Retry-After`. A20 asks for `429` only for one client's 5th socket. The stable codes, `too_many_live` and `radio_full`, are what a client matches on (R78).

## Considered options

- **Counting an admin's socket even when it's let in over a cap.** Then 50 open sockets would include admins', and a listener could be refused because an admin is listening. The cap is for the public.
- **Dropping the oldest socket at the cap.** R71 forbids it.
- **A shorter retry for the client's cap.** One of its own sockets may never close, so waiting longer doesn't help. A fixed 30 s keeps one rule for both caps.

## How each choice is verified

- `internal/limits`: `TestLive` covers 4 per client and 50 in all, both refusals' status, code, message and wait, the check counting nothing, a release making room once and only once, and `LiveOpen`.
- `internal/server`: `TestIntegrationA20Live` is A20's socket half through the real handler. It opens 4 sockets, refuses the 5th with `429`, and refuses the browser's check the same way. The 4 still take sets. Another client's check is `426`. An admin is let in, a signed-in listener isn't, and 50 from 13 clients refuse a 51st with `503 radio_full` in both forms. The module's token gets in. None of the 50 drops. Closing one lets a new listener in, and the radio is full again. A recorder connecting while the radio is full has its call reach the listeners.

## Consequences

- The browser test stack raises both caps (`e2e-web.sh`, `scripts/dev-env.sh`), as it does for ADR 0036's limits.
- #142 adds the browser's half: the check after a socket closes before opening, the notices and the 30 s retry.
