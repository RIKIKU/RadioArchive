---
status: accepted (2026-11-25, by the owner)
---

# 10. Recorder signals and the recorder notice

Date: 2026-11-24

The API watches each active recorder for three signals: **disconnected** (it has connected before and has no live socket now), **deaf or down** (its client relays no systems from the recorder plugin, or an attached trunked system's decode rate is below 1 message a second) and **quiet** (no call for its quiet threshold, off by default). Connection and heartbeat state is held in memory by the process that has the socket; the last call and the quiet threshold are columns on `recorders` (migration 00011). Once any recorder has been disconnected or deaf for `RECORDER_NOTICE_AFTER` (default 2 min), the live feed sends every listener one fixed message, `{"type":"notice","notice":"recorders","active":true}`, and `active:false` when none is any more. The admin API's `GET /api/v1/admin/recorders` is the Recorders page's data. These are the implementer's choices where `SPEC.md` R14, R15 and R90 leave room (spec "How to use this spec", item 6). Built in [Recorder signals and the listeners' recorder notice](https://github.com/RIKIKU/RadioArchive/issues/57).

## Summary

| Part | Choice |
|---|---|
| **Disconnected** | No live socket on this server, for a recorder whose client has connected at least once (`client_seen_at` set). Since the socket closed, or since this server started if it hasn't connected to it since. A recorder that has never connected isn't counted: it was never receiving anything |
| **Deaf: down** | The client's heartbeat relays no systems (`systems: []`, ADR 0002: the plugin silent for 10 s) |
| **Deaf: decode rate** | An **attached** shortName whose relayed `decode_rate` is below `DecodeRateNearZero` (1.0 message/s; a healthy P25 control channel decodes about 40). Conventional system types (`conventional*`) have no control channel and are never watched. A shortName that isn't attached, or that's attached but not in the heartbeat, is never deaf |
| **Deaf since** | By the server's clock, at the first heartbeat that showed it. Until a connection's first heartbeat, a recorder is neither deaf nor well |
| **Quiet** | `quiet_after_minutes` set (1 to 10,080), and no call since then: `last_call_at`, or this server's start if it has none |
| **Last call** | `recorders.last_call_at`, moved on (never back) by every admitted live call and every stored recording, to the call's start or now, whichever is earlier. A recording uploaded late from the client's queue doesn't make a recorder look heard |
| **The notice** | On once any recorder has been disconnected or deaf for `RECORDER_NOTICE_AFTER`; off as soon as none is. Quiet never counts (R15) |
| **Checks** | Every second, and at once on every connection, disconnection, heartbeat and quiet-threshold change. One query of the active recorders and their attached shortNames each time |
| **On the feed** | `gate.Live.RecorderNotice(active)` sends one of two fixed byte strings to every listener, whatever its set, from the welcome on. A listener who connects while it's on gets it straight after the welcome; one who connects while it's off gets nothing. Not replayed, and no event ID |
| **Admin API** | `GET /recorders`, `GET /recorders/{recorder}`, `PATCH /recorders/{recorder}` with `{"quiet_after_minutes": N or null}`, under `/api/v1/admin` |
| **Metrics** | `Recorders.Sig` (`SignalCounters`): trips per signal, the notice raised and active, failed checks; per-recorder gauges come from `Recorders.Status`. Labels are recorder names and shortNames only, never a talkgroup or unit name (P1). Exported in M6 |

## The notice names nothing (R15)

The notice is a constant. Which recorder, which signal and which system tripped it are never in the message, and two different causes send the same bytes, which a test asserts. Its text, "Some talkgroups aren't being received right now", is the browser's (#60): the feed carries only the kind and whether it's on.

The notice is not by listening set. A notice only for listeners whose talkgroups a dead recorder carries would say which talkgroups those are, and a recorder's coverage is the admin's, never the listener's (R15: "Never which recorder captured a call").

## The admin API

`GET /api/v1/admin/recorders` lists every active recorder, by name; a retired one isn't listed.

```json
{"name": "Bench", "connected": false,
 "connected_since": null, "disconnected_since": "2026-11-24T01:00:00Z",
 "last_connected_at": "2026-11-24T00:10:00Z", "last_call_at": "2026-11-24T00:59:41Z",
 "client_version": "0.4.1", "client_out_of_date": false, "quiet_after_minutes": null,
 "systems": [{"short_name": "ex", "system_id": 1, "system_label": "EX",
              "system_type": "p25", "decode_rate": 38.7, "deaf": false}],
 "signals": {"disconnected": true, "deaf": false, "quiet": false}, "deaf_reason": null,
 "warning": true, "warnings": ["Disconnected for 3 min"]}
```

- `systems` is every attached shortName, then any the plugin reports that isn't attached (`system_id` null). `system_type` and `decode_rate` are the last heartbeat's on this connection, null until one comes.
- `deaf_reason` is `no_heartbeat` or `decode_rate`.
- `warning` is the page's badge: any signal tripped. `warnings` are the page's words for each. Disconnected and deaf show at once; the notice waits for `RECORDER_NOTICE_AFTER`, and R91's alerts wait for their own 2 minutes.
- `PATCH /api/v1/admin/recorders/{recorder}` takes `{"quiet_after_minutes": N}` (1-10,080) or `null` for off, logs the admin and the change, and answers the recorder's state. Keys stay the shell's (R10).

The admin API is served only by tests until admin sign-in exists (M4), as before. The Recorders page is M4's (issue #53, question 2).

## Consequences

- A server holds only its own sockets' state. With several API servers, a recorder connected to one looks disconnected to the others, which would raise the notice there. A deployment has one (SPEC.md, "Scale it is built for"); several would need the connection state in the database.
- A server restart counts every recorder that has connected before as disconnected from the start, until its client reconnects, normally within seconds; the notice waits 2 minutes, so a restart doesn't raise it.
- A recorder whose attached system is missing from its plugin's systems (renamed in trunk-recorder's config, say) isn't deaf. The page shows that system with no decode rate.
- Moving from deaf-down to deaf-decode-rate (the plugin comes back with a rate of 0) starts the 2 minutes again.
