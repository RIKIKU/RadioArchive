---
status: proposed
---

# 36. The client and the request limits

Date: 2026-11-26

Heavy-use limits ([#22](https://github.com/RIKIKU/RadioArchive/issues/22)) stand in front of the public API's routes and pages, and nothing else. They live in `internal/limits`, counted in memory, and the server (`server.WithLimits`) puts them in front of each route. This ADR covers who a request's client is (R70), the two per-minute limits (R73), what a refusal says, and who's exempt. The live socket's limits and the zips' limits build on these, in ADRs 0037 and 0038. These are the implementer's choices where `SPEC.md` P3, R70, R73, R78, R90 and R111 leave room (spec "How to use this spec", item 6). Built in [The client's address and the request limits](https://github.com/RIKIKU/RadioArchive/issues/139).

## Summary

| Part | Choice |
|---|---|
| **The client** | One IPv4 address, or one IPv6 /64, as text (`192.0.2.7`, `2001:db8:1:2::/64`). An IPv4-mapped IPv6 address is its IPv4 address; an IPv6 zone is dropped. An address that can't be read, such as a Unix socket's, makes one shared client, `unknown` |
| **`TRUSTED_PROXY`** | One exact address, IPv4 or IPv6. A range (`/`), a host name, a port or a zone is refused at start. Unset, no `X-Forwarded-For` is ever read |
| **Which forwarded address** | From the trusted proxy only: the **last** address of the last `X-Forwarded-For` line, with or without a port. If it's missing or unreadable, the request counts as the proxy's own address. An earlier address in the header is never used |
| **Requests** (`LIMIT_REQUESTS`, 120) | Every public route but playback, zips and the live socket: the catalogue and talkgroup search, fireground, recent calls (GET, QUERY, POST), call details (QUERY, POST), single-call downloads, unit search, and the call link's page. A HEAD counts as a GET |
| **Playback** (`LIMIT_PLAYBACK`, 600) | `GET /api/v1/calls/{call}/recording`, ranges and all. It's counted apart from requests: playing doesn't use up the 120 |
| **The window** | Fixed, per client per limit: it opens at the client's first request and lasts a minute, holding up to the limit. A refused request isn't counted. Windows that have ended are swept once a minute |
| **A refusal** | `429`, `Retry-After` in whole seconds rounded up (at least 1), `Cache-Control: no-store`, and R78's body with the wait: `{"error": "too_many_requests", "message": "Too many requests from here: at most 120 a minute. Try again in 48 seconds.", "retry_after": 48}`. Playback's code is `too_many_plays` |
| **Waits in words** | Under a minute: "1 second", "48 seconds". From a minute on, rounded up to whole minutes: "1 minute", "2 minutes" |
| **Admins** (R70) | A request that would be refused is let through if a signed-in admin made it. The check is the admin API's own (`account.Service.Admin`): the session cookie, or the module's bearer token, then the account's row. It's only asked when the request has a cookie or `Authorization` header and would otherwise be refused, so other requests cost no read |
| **Signed-in listeners** | Limited exactly as everyone (P3, R111) |
| **Not limited** | The recorder client's routes, the admin API, the account routes, the app's files, `robots.txt` and the health check |
| **Which limit a route has** | Declared with the route in `internal/public` (`public.Route.Limit`). The server panics at start on a public route without one, and `TestLimitsOnRoutes` checks each route's |
| **Telemetry** | Nothing is logged. Each limit counts its refusals (`Limits.Hits`) for M6's metrics (R90). No address leaves the process |

## The last forwarded address, not the first

The first address in `X-Forwarded-For` is whatever the listener's browser, or a scraper, chose to send. Only the address the trusted proxy wrote itself can be believed. With R85's nginx example, which overwrites the header with `$remote_addr`, there's only one address, and the first and the last are the same. A proxy that appends (`$proxy_add_x_forwarded_for`) leaves the listener's own claim in front and its real address last. Reading the last address is correct for both, and a listener can't choose which client it counts as.

A chain of proxies has to collapse into one hop before the trusted one: the trusted proxy, using nginx's `real_ip`, works out the listener's address from the proxies in front of it, then writes that address. Otherwise every listener behind the chain counts as the next proxy's address. That's question 2 on the milestone (#138), and M6's install docs say how.

When the trusted proxy sends no usable address, the request counts as the proxy. This is wrong, but in the safe direction: such requests share one client, and a broken proxy setup shows up as refusals rather than going unlimited.

## A fixed window

A sliding log is exact but keeps every request's time. A token bucket allows bursts and refills smoothly, but its `Retry-After` says when the next single request is allowed, not when the listener can carry on. A fixed window, opened by the client's first request, matches "120 a minute" as A20 puts it: the 121st request within a minute is refused, and `Retry-After` is the end of that minute. A client can make up to twice the limit across the edge of two windows. That's within what R73 is for, which is stopping a scraper rather than metering.

## Admins, without a read for everyone

R70 exempts signed-in admins and the module. Public handlers still see no cookie or credential (ADR 0009): the limiter sits in front of the handler, so it reads the session only to decide whether a request is an admin's, and the handler's answer is the same whoever asks. Asking only when a request would be refused means a listener's request never costs a session read. An admin's own requests are counted like anyone's until the limit, and only then let through. Admins on a shared address, such as a household, use up that address's count with everyone else.

## Considered options

- **Exempting by reading every request's session first.** That costs a database read on every signed-in listener's request, for nothing in almost every case.
- **The client's whole IPv6 /56 or /48.** R70 says /64, the usual size of one home or phone network.
- **A `limit` field in the body** beside `error`. The code already names the limit, and R78 asks only for a stable code and a message.
- **Configuring which routes count** as requests. The routes are the SPEC's (R73). A deployment tunes the numbers, not the list.

## How each choice is verified

- `internal/limits`: `TestClientOf` covers the client and A21's rule, including the last address, header lines, ports, IPv6 /64, zones, mapped addresses and an unreadable address. `TestParseTrustedProxy` and `TestFromEnv` cover the settings and their refusals. `TestTake` covers the 121st request, the wait, uncounted refusals, other clients, playback apart and the next window. `TestSweep` and `TestRefusalWrite` cover memory and the answer. `TestWait` covers the wait in words.
- `internal/server`: `TestIntegrationA21` is A21 through the request limit. `TestIntegrationA20Requests` is A20's request half over every kind of request, playback's 601st, the admin, module and listener cases, and the recorder client's uploads beyond 120. `TestLimitsOnRoutes` checks every route's limit.
- `cmd/radioarchive`: `TestIntegrationA20AdminsNotLimited` uses the binary's handler at the default limits with real sign-in. The admin isn't refused in the browser or with the module's token, a listener is, and no address is logged.

## Consequences

- Every test stack raises the limits: `scripts/dev-env.sh` writes `LIMIT_REQUESTS` and `LIMIT_PLAYBACK` for `compose.yaml`, and `e2e-web.sh` sets them on its binary. The tests that check limits use the defaults.
- A restart starts every count again (R70), and so does a second server process: each counts on its own.
- The live socket and zips are marked with their limits (`public.LimitLive`, `public.LimitZip`), but this slice doesn't count them yet. ADRs 0037 and 0038 do.
- The browser already shows a refusal's message on the Recent calls page. #142 shows it everywhere else.
