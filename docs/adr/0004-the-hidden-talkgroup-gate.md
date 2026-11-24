---
status: accepted (2026-11-24, by the owner)
---

# 4. The hidden-talkgroup gate

Date: 2026-11-24

Every public surface reads the deployment through one Go package, `internal/gate`, which reads calls only through one SQL view, `public_calls`, whose one condition is ADR 0003's `call_state`. The public endpoints live in one package, `internal/public`, which may import the gate but not the store, the database driver or the file system, and the server mounts no other public route. A test harness exercises every public route with a hidden talkgroup's calls, and fails while any route is missing from it. These are the implementer's choices where `SPEC.md` P1, R16, R20, R30, R32-R35, R61, R67 and R74 leave room (spec "How to use this spec", item 6). Built in [Stored recordings, range playback and the hidden-talkgroup gate](https://github.com/RIKIKU/RadioArchive/issues/35).

## Summary

| Part | Choice |
|---|---|
| **The rule** | A call is public when `call_state(system, talkgroup, patched) = 'public'`, read on every request. Hidden, ignored, and patched with either, are alike: not there (R16, R30) |
| **Where the rule is written** | Once, in the view `public_calls` (migration 00005). The gate's queries read `public_calls`, never `calls` |
| **Who may read** | `internal/public` sees only `internal/gate`. A test parses its imports and refuses the store, `pgx`, `os`, `io/fs`, `path/filepath`, `database/sql`, `reflect`, `unsafe` and any third-party module |
| **Who may serve** | `server.API` sorts every route by path: `/healthz`, the recorder client's (`/api/v1/recorder/`), the admin API's (`/api/v1/admin/`) or public. A public route can only come from `public.API`'s list; anything else outside those prefixes panics at start |
| **Unavailable** | Deleted, never stored, hidden, ignored, patched with hidden or ignored, not a call ID, or (for a recording) not recorded yet or its file gone: one answer, the same status, headers and body |
| **Harness** | `internal/server/a10_test.go`: probes per public route. It fails if the mounted public routes and the probed ones differ |
| **Admin playback** | `GET /api/v1/admin/calls/{call}/recording`, behind the admin authenticator, any state, not audited, `Cache-Control: no-store` |

## Why a view, a package boundary and a route table

P1 is the spec's first principle, and R32 lists a dozen surfaces, most of which don't exist yet. A check written into each handler would be as good as the next handler's author. So the design makes the safe path the only one that compiles and passes the tests:

1. **The data.** `public_calls` is the calls whose patch is public now. It has no state column to forget to filter on. Hiding a talkgroup takes its calls, and every call patched with it, out of the view at the next query; making it public puts them back. Nothing is deleted or rewritten (R30). The gate's two queries (`GatePublicCalls`, `GatePublicRecording`) read the view; a future search, recent-calls list or catalogue query is written the same way. The talkgroup it joins is the call's own, which the view has already cleared.
2. **The code.** `internal/public` can't open the database or a file. Its handlers get a `gate.Call` or a `gate.Recording` (an open file that can only be served), or `gate.ErrUnavailable`, which carries no reason.
3. **The routes.** The server's mux is wrapped in `server.API`, which records each route's kind and checks its path belongs to that kind. The public kind is mounted only from `server.WithPublic(*public.API)`. The binary's test checks it serves exactly the public API's routes and no admin route.
4. **The tests.** The A10 harness compares the public routes `server.API` mounts with the routes it has probes for. A new surface must add probes to exist.

Considered and not chosen: a middleware in front of public routes (it can't know which talkgroup a response is about without the handler's help), a `state` column on calls (R30 forbids it: calls carry no state), and row-level security in PostgreSQL (a second definition of the state, in policies, beside `call_state`, and the API connects as one role anyway).

## Answering like a call that never existed

R33 and R61 ask that a hidden call look exactly like a deleted one. Every path that refuses ends at the same `writeJSON(404, {"error": "call_unavailable", "message": "This call isn't available."})`, with the same `Content-Type`, `Cache-Control: no-store` and `Content-Length`. A malformed ID gets it too, so even "this looks like an ID" isn't told apart. The call list (R67) leaves unavailable IDs out, as R64 and R66 want, so a list naming a hidden call is byte for byte the list naming a deleted one.

Timing: a hidden call costs one indexed lookup that finds a row the view's condition then drops; a deleted call costs one that finds none. No file is opened, no second query is made, and nothing is logged for either. The difference is a function call on one row, well under the noise of a network round trip. A10 doesn't measure time.

A public call whose recording hasn't arrived, or whose file has gone while retention deletes its row (R88), answers the same 404 for its recording: nothing hidden can be learnt from it, and the call list says whether a recording exists.

## Playback (R20, R74)

`GET /api/v1/calls/{call}/recording` (and `HEAD`) serves the stored Ogg/Opus file byte for byte through `http.ServeContent`: `Accept-Ranges: bytes`, single and suffix ranges, `416` past the end, `Last-Modified` (when it was stored) with `If-Modified-Since` and `If-Range`. `Content-Type: audio/ogg; codecs=opus` and `Cache-Control: private, max-age=31536000, immutable`, because a recording never changes. No ETag: `Last-Modified` is enough for an immutable file, and a hash in a header is one more thing the leak check would have to excuse. The file is opened only after the gate has passed the call, by its path from the database, checked to be local to the volume. `internal/recordings` holds the path rule (`<id[:2]>/<id>.opus`) and the serving; the upload uses the same rule.

A consequence of `immutable`: a listener's browser keeps a recording it has already played after its talkgroup is hidden. That is the spec's choice (R74); the server serves nothing new.

## Call details (R61, R67)

`QUERY /api/v1/calls` (RFC 10008) and `POST /api/v1/calls` take `{"ids": [...]}`, at most 500 (R73; `400 too_many_calls` over it), and answer `{"calls": [...]}`: the available ones, each once, in the order asked. A call gives its system (ID, label, name), its talkgroup (ID, label, tag, and `name` as R24 shows it: the name, else the label, else the ID), its patched talkgroups, start and end (UTC), duration, whether it's in progress, its transmissions (unit ID, offset, duration) and its recording's URL, type and size once it has one. Unit aliases come with units, in M4. Every answer is `Cache-Control: no-store`, so hiding takes effect at the next request. Errors are R78's `{"error", "message"}`.

## Admin playback (R34)

The admin API plays any stored call's recording, hidden and ignored ones until retention deletes them (R30, R35), with the same range handling. It's behind the `AdminAuthenticator` fence of ADR 0003, so the binary doesn't serve it yet. It isn't audited or logged per request (R34, R54). It's `no-store`, because an admin's browser may be one listeners also use.

## Consequences

- The live feed (M2) must take its frames and events through the gate too. `server.Recorders` hands every accepted frame to a `LiveSink`; the listener feed will be a public surface in `internal/public`, and the gate will need a way to ask a live call's state at each frame or event, using `call_state` on the call's row.
- `public_calls` evaluates `call_state` per row. For one call or 500 that's nothing; search over a month of calls (M3) should be checked with `EXPLAIN` then, and an index or a rewrite of the view's condition considered there, still as one definition.
- A new public surface means: a gate method over `public_calls`, a handler in `internal/public`, and probes in the A10 harness.
