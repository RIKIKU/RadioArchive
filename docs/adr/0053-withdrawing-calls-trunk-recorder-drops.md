---
status: proposed
---

# 53. Withdrawing calls trunk-recorder drops

Date: 2026-11-28

trunk-recorder sometimes starts a recorder for a call that then decodes no voice. When it concludes the call it logs `No Transmissions were recorded!` and writes no WAV. By then the recorder client may have started the call on the server from the call's first audio or transmission, so the server holds a call whose recording never comes. It shows in the history, and 10 minutes after it ends `radioarchive_recordings_missing` counts it (R19, R91). Production saw 22 in 24 hours, every one 0-6 s long, and the alert fired with nothing wrong ([issue #185](https://github.com/RIKIKU/RadioArchive/issues/185)). Such a call should be **withdrawn**, as R31 withdraws a call from listeners, and only a recording that genuinely failed to arrive should count as missing.

trunk-recorder references are to the pinned release, v5.2.1, as in ADR 0002.

## Summary

| Part | Choice |
|---|---|
| **Where** | The recorder plugin. Only it can see how trunk-recorder concluded a call; the client and the server can only guess from silence |
| **The hook** | `calls_active`. No plugin callback runs on the paths that drop a call, but `calls_active` runs on the main thread right after `manage_calls` concludes and deletes calls, and after a grant. A call the plugin saw `RECORDING` that is gone from the list, or listed but superseded, has been concluded |
| **How it was concluded** | The plugin replays `Call_Concluder::conclude_call`'s checks on what that function read: superseded, then encrypted, then the recorder's transmission list filtered by `minTransmissionDuration`, then the total against `minDuration`. The list is the recorder's, still intact because the recorder has been stopped and not yet given another call. The checks are `conclusion.h`, with no trunk-recorder types |
| **Plugin → client** | A new control message, `call_withdraw` (type `0x05`): the identity, `talkgroup`, `time_ms` and a `reason` (`no_transmissions`, `too_short`, `superseded` or `encrypted`). The envelope stays version 1. Plugin 0.2.0 |
| **Client → server** | `call_end` with `"withdrawn": true`. The live socket's protocol stays 1 |
| **Server** | Deletes the call unless a recording is stored, finding it by its identity, so a call already ended (on this socket or an earlier one) is withdrawn too. The live feed reads the call again, finds it gone, and sends listeners `call_withdrawn` (R31). Nothing new in the web or Android apps |
| **The alert** | `radioarchive_recordings_missing` is unchanged: a withdrawn call has no row to count. A call whose recording was rendered but never uploaded still counts |
| **Counted** | `radioarchive_client_calls_withdrawn_total{reason}` on the client, `radioarchive_recorder_protocol_events_total{kind="calls_withdrawn"}` on the server, and an info line in trunk-recorder's log and the client's |

## What trunk-recorder does

`Call_impl::conclude_call` stops the recorder, copies its transmission list into the call and calls `Call_Concluder::conclude_call` (`trunk-recorder/call_impl.cc:133-195`). That function returns early, with no recording and no plugin callback, for a call that was superseded, an encrypted one, one with no transmissions left once those shorter than `minTransmissionDuration` are removed, and one whose transmissions add up to `minDuration` or less (`trunk-recorder/call_concluder/call_concluder.cc:1199-1235`, the filter at `:1109-1124`). Only otherwise does it start a worker that renders the WAV and runs every plugin's `call_end` (`:1238`, `:900-959`). Every plugin callback (`trunk-recorder/plugin_manager/plugin_api.h:35-63`) was checked: none runs on the early returns.

`manage_calls` concludes a trunked call when its recorder and the control channel have both been quiet for `callTimeout`, deletes it, and then calls `calls_active` with the calls left (`trunk-recorder/monitor_systems.cc:316-343`). A superseding grant concludes the original call, which stays listed as `MONITORING`/`SUPERSEDED`, and then calls `call_start` and `calls_active` (`:529-555`). The recorder's transmission list is cleared only when it starts another call (`trunk-recorder/gr_blocks/transmission_sink.cc:136-175`), and its `is_active()` is false from `stop()` until then (`trunk-recorder/recorders/p25_recorder_impl.cc:265-300`).

## The plugin

- `call_start` remembers, for a `RECORDING` call, its `Recorder *`, talkgroup and `get_encrypted()`; `calls_active` refreshes them for every call still `RECORDING`.
- In `calls_active`, for each call the plugin holds that isn't still `RECORDING`:
  - listed and `SUPERSEDED`: withdrawn;
  - listed otherwise: not concluded yet;
  - gone, with its recorder idle and no listed call using it: the recorder's transmission list goes through `conclude()`. `kCallEnd` marks the call concluded, and its `call_end` comes as before; anything else sends `call_withdraw` and forgets the call;
  - gone, with its recorder already busy again: can't tell, so it waits for `call_end`, as before this ADR.
- A `call_end` that runs before `calls_active` (the worker is quick) has already forgotten the call, so it's never withdrawn.
- `call_withdraw` is sent for every dropped call the plugin held, including one it sent nothing about (no unit, no audio). The client ignores what it can't use.

## The client

- A call it holds: its live audio is finished, nothing is uploaded, and the server gets `call_end` with `withdrawn: true`, stopped at the call's last packet. A call it never started on the server (no audio arrived) is just forgotten.
- A call it doesn't hold (it ended the live audio after 30 s idle, or the client restarted): `call_end` with `withdrawn: true` is still sent, stopped at the plugin's `time_ms`, because the server may hold the call, ended and waiting for a recording.
- A dropped patch copy: forgotten, as its `call_end` would be.

## Compatibility

| Plugin | Client | Server | What happens to a dropped call |
|---|---|---|---|
| new | new | new | Withdrawn |
| new | old | any | `0x05` is an unknown type: dropped and counted (`radioarchive_client_udp_unknown_type_total`), as ADR 0002 says. The call stays as before |
| old | new | any | No `call_withdraw` ever comes. As before |
| new | new | old | `withdrawn` is an unknown field: the call ends as before (or, if it had already ended, the server counts a message for an unstarted call). As before |

No version bump is needed in either protocol, because both ADR 0002 rules hold: an unknown type is dropped and an unknown field ignored.

## Considered options

| Option | Rejected because |
|---|---|
| **A server fallback**: withdraw a live call that ended with zero transmissions | The server can't tell "no voice" from "voice whose live packets were lost" or "a recording still queued on the client" (R5 keeps recordings for days). Guessing from silence would hide real missing recordings, which must still alert |
| **The client**: withdraw a call whose `call_end` hasn't come within N seconds of its last packet | The same guess on a timer. trunk-recorder's worker can take long (an upload script, a slow disk), and a recording withdrawn then uploaded would come back as a new call |
| **`call_end` from the plugin with no filename** | No `call_end` callback runs on these paths, so the plugin would still have to detect the conclusion itself; and an older client would queue an upload of an empty filename |
| **Withdraw only `no_transmissions`** | The other three early returns leave the same false missing recording. Superseded and too-short calls cost one more comparison each; encrypted calls are rarely recorded, but the check is one flag |
| **Keep the row, marked withdrawn** | Every query that lists calls or counts them would need the mark. A call with no audio has nothing to keep, and `DeleteUnrecordedCall` already removes a live call refused before its recording (R12, R17, R18) |
| **A timer in the plugin after the conclusion** | Not needed: the conclusion's outcome is known the moment `calls_active` runs |

## Consequences

- The plugin now mirrors part of `conclude_call`. A trunk-recorder upgrade has to check `conclusion.h` against the new release, as it already checks the headers (`recorder/plugin/Dockerfile`). If they drift, the failure is the one before this ADR (a false missing recording) or, for a call trunk-recorder did hand over, a withdrawal followed by its `call_end`, which recreates the call from its recording (`LockCallForRecording`).
- The plugin CI test's fixture has a third, silent call, so the real trunk-recorder produces `No Transmissions were recorded!` and the test checks the withdrawal. The other reasons can't be made by the fixture without new trunk-recorder settings per call; `recorder/plugin/conclusion_test.cc` covers them, and the plugin image's build runs it.
- A withdrawn call's unit and talkgroup were still heard: `last_heard` and the recorder's last call keep the time from its `call_start`.

## How each choice will be verified

| Choice | Verification |
|---|---|
| The hook and the no-transmissions reason | `recorder/plugin/plugintest`: the real trunk-recorder logs `No Transmissions were recorded!` for the fixture's silent call, and the plugin sends `call_withdraw` with `no_transmissions` and no `call_end` |
| The other reasons and the filters | `conclusion_test.cc`, run by the image build |
| The message | `internal/recorderproto` round trip and malformed-input tests |
| The client | `TestEngineWithdraw`: live audio finished, `call_end` with `withdrawn`, no upload; a call it doesn't hold is still sent |
| The server and the alert | `TestIntegrationWithdrawnCall`: a live call's listeners get `call_withdrawn`; an ended call stops counting as missing while a genuinely missing one still counts; a recorded call is kept |
| End to end | `testing/e2e` `TestWithdrawnCall`: fake recorder, client, server and PostgreSQL |
