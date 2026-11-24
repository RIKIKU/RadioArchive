---
status: accepted (2026-11-24, by the coordinator: the owner delegated protocol choices)
---

# 15. Zips: their URL packing, parts and resuming

Date: 2026-11-24

A listener downloads a basket or a conversation as one zip, stored without compression, built while it streams and never kept, the same bytes every time for the same calls, so a range request resumes a broken download (`SPEC.md` P1, P4, P5, R23, R32, R63-R65, R67, R72, R88; A10, A18, A19). The web download is a GET whose URL carries the calls, so the browser's download manager can resume it. This ADR chooses how the URL packs the list, who splits a basket into parts, and how a resumed download behaves when the zip has changed. #82's shared conversation link reuses the packing. Built in [Zips: streamed, deterministic, resumable](https://github.com/RIKIKU/RadioArchive/issues/78).

## Summary

| Part | Choice |
|---|---|
| **Routes** | `GET /api/v1/zip/{packed}` (and `HEAD`), the list packed into the path; `QUERY` and `POST /api/v1/zip` with the same list as JSON (R67). In `internal/public`, reading through the gate |
| **Packing** | One binary list, base64url without padding: format byte, part, calls (16 bytes each, the UUID's bytes), conversations (name, note, and their calls as indexes into the calls). About 21.4 characters a call, plus 1-2 for each conversation it's in |
| **URL budget** | At most **7,680** characters of path and query (`public.MaxZipURL`), inside nginx's default 8 KB `large_client_header_buffers`, with room for a path prefix. Longer: `414 zip_url_too_long` |
| **Part size** | At most **500 calls** (R72; more: `400 too_many_calls`) **and** the URL budget, whichever comes first. 500 calls don't fit in 8 KB (below), so a GET part holds at most **359** loose calls, or 325 in one conversation, with short names and notes |
| **Who splits** | The browser (#81) builds each part's URL, "part *n* of *N*" in it. The server only refuses a part past either limit |
| **Names and notes** | In the URL, as `manifest.json` needs them and the server stores nothing (P4). They are in the reverse proxy's access log (#75's open question 2) |
| **Zip** | Stored entries, no data descriptors, no Zip64 (over 4 GiB: `400 zip_too_big`); sizes known from the database, so `Content-Length` and every offset are known before a byte is read. `internal/storedzip` lays it out and reads it from any offset |
| **Resuming** | `http.ServeContent` over that layout: single and multiple ranges, `If-Range`, `If-None-Match`, with a strong `ETag` naming everything the bytes are made of. No `Last-Modified`. Ranges are GET's: QUERY and POST are answered whole |
| **Changed since** | A call deleted (or hidden, renamed, recorded) between an attempt and its resume changes the `ETag`, so the resume's `If-Range` fails and the whole new zip comes back with `200`: the download starts again |
| **Unavailable calls** | Deleted, never stored, hidden, ignored, patched with either, within their delay, not recorded yet, or not a call ID: left out as if never named (R64). A conversation left with none is left out; none left at all is `404 calls_unavailable` |
| **While streaming** | Each recording is opened through the gate when the zip reaches it. A call hidden or deleted meanwhile is never sent: the zip stops short, and its resume starts again |
| **Memory** | One open file and a 32 KB buffer at a time. A 500-call, 125 MB zip grew the heap by 2 MB, 7 MB allocated in all (`TestIntegrationZipMemory`) |
| **Caching** | `Cache-Control: no-store`: hiding takes effect at the next request |

## The packing

```
version   1 byte: 1
part      uvarint: 0 for a whole basket, else this part's number (1-100)
parts     uvarint: 0 for a whole basket, else how many parts
n         uvarint: how many calls (at most 500)
calls     n × 16 bytes: the calls' UUIDs; first the loose calls in their
          order, then the conversations' calls in the order they first appear
m         uvarint: how many conversations
m × conversation:
  name    uvarint byte length, then UTF-8 (at most 200 bytes)
  note    uvarint byte length, then UTF-8 (at most 2,000 bytes)
  k       uvarint: how many calls, at least 1
  k × uvarint: each call's index in calls, each once, in the conversation's order
```

A call no conversation names is loose. A call in several conversations is listed once and named by index in each, so being in a second conversation costs 1 or 2 bytes, not 16. Every list has exactly one packed form: the server re-packs what it read and refuses anything else (a longer varint than needed, calls out of that order, a call twice, trailing bytes, padding), so one list is one URL. `public.ZipURL` is the reference encoder; #81's browser encoder is checked against this example, one loose call and a conversation "Structure fire" of one call:

```
/api/v1/zip/AQAAAg-OLBprPU5fmnscLT5PWmtvHSw7SllOj518CxosPU5fAQ5TdHJ1Y3R1cmUgZmlyZQABAQ
```

for loose `0f8e2c1a-6b3d-4e5f-9a7b-1c2d3e4f5a6b` and `6f1d2c3b-4a59-4e8f-9d7c-0b1a2c3d4e5f` in the conversation. The JSON body for QUERY and POST is `{"calls": [loose IDs], "conversations": [{"name", "note", "calls": [IDs]}], "part", "parts"}`, read into the same list: strings that aren't call IDs are left out, as in every call list (ADR 0004), and a loose call also in a conversation is in the conversation.

### Why 500 calls don't fit, and the nearest safe limit

R66 estimates "about 11 characters a call", which is a 64-bit ID in base64. Calls' IDs are random 128-bit UUIDs (R60, ADR 0004): 16 bytes, 21.3 characters. 500 of them are 8,000 bytes before any encoding, and 10,687 characters as packed here, past nginx's default 8,192-byte request line however they're encoded. So the part size is set by the URL budget: 359 loose calls, or 325 in one conversation (indexes past 127 take 2 bytes), fewer with long names and notes. A basket of 500 calls is two parts; QUERY and POST still take 500 in one zip, but don't resume.

Considered and not chosen:

- **64-bit ID prefixes** (11 characters, 500 calls in 5,334). The gate would look calls up by prefix, a second kind of call ID would exist in links, and two calls sharing a prefix would need an answer. The URL budget is the simpler bound for baskets this size.
- **Compressing the list.** Random UUIDs don't compress, and an inflater on a public route is one more thing to bound.
- **A larger alphabet.** The characters a path segment may hold unescaped give about 6.3 bits each against base64url's 6: 5 % more calls, against proxies that normalise some of them.
- **The server keeping the list** under a short ID. It would store something about an anonymous listener (P4) and a zip would no longer be "never kept" (R65).
- **Asking deployments to raise `large_client_header_buffers`.** The docs may mention it, but a default proxy must work.

7,680 leaves 512 bytes of the 8 KB line for the method, the protocol and a path prefix a combined deployment's proxy might add. The server refuses a longer URL itself, so a part that works against the binary works behind a default nginx.

### Who splits

The browser builds the parts (#81): it adds calls in the basket's order, keeping a conversation's calls together where it can, and starts a new part before the next call would take the part past 500 calls or the URL past `MaxZipURL`. A conversation split across parts carries its name and note in each, and each part says `part` and `parts`. The server takes each part on its own: it numbers the part's conversations from 01, so a conversation split across parts may have a different number in each. It checks only the bounds, so a browser that splits badly gets a refusal, not a wrong zip.

## The zip

**Layout** (R65): `manifest.json` and `index.csv` first; then each conversation's folder, `<NN> <name>/`, numbered by its first call's start time (ties in the list's order), with its playlist `<NN> <name>.m3u` (`#EXTM3U`, `#PLAYLIST`, an `#EXTINF` for each call in the conversation's order) and its calls; then the loose calls at the top level. Calls in a folder and at the top are by start time, then ID. A call in several conversations is in each folder. `NN` has two digits, more past 99 conversations. Folder names are made file-safe by ADR 0014's rule, and a name with nothing left is just `NN`.

**File names** are R63's (ADR 0014), each once in the zip: a second call on the same talkgroup in the same second is `... (2).opus`, in start and ID order.

**`manifest.json`**: `format` (1), `part` and `parts` when split, `conversations` (number, folder, name, note, and calls in the conversation's order, each with its ID, path in the zip and start) and the loose `calls`.

**`index.csv`** (RFC 4180, CRLF), one row per call by start time: `call_id`, `start_utc`, `end_utc`, `start_local`, `end_local` (the deployment's `TZ`, with its offset), `duration_s`, `system` (its name, else its label), `talkgroup_id`, `talkgroup_name` (R24), `talkgroup_label`, `groups`, `tag`, `unit_ids`, `unit_aliases`, `unit_offsets_s` (one per transmission, `;`-separated, in order; aliases are empty for a unit with none, ADR 0019) and `file`.

**The name** it saves as: the conversation's name when the zip holds one conversation and no loose calls, else `Calls`, then the first call's local date, then ` part n of N` when split: `Structure fire 2026-03-04.zip`. It depends only on what the zip holds, so a call left out changes nothing.

**Determinism.** Every byte comes from the calls and the list: entry times are the calls' starts (a folder's, its playlist's and the metadata's are their first call's), in the deployment's time zone for the MS-DOS time and as an instant in an extended timestamp; nothing is random, counted or dated by the clock. The same list, by GET, QUERY or POST, gives the same bytes and `ETag`. What can change them: a call becoming available or not, a talkgroup's name, label, tag or groups, and `TZ`.

**The ETag** is a hash of each entry's name, time and contents, a recording standing for itself by its call's ID and size, since a stored recording never changes (ADR 0004). It names the bytes without reading a file.

## Resuming

`http.ServeContent` answers ranges over the layout, which `storedzip` reads from any offset: a header is made when asked for, a recording is read at its offset. A browser resuming a download sends `Range` with `If-Range: <ETag>`:

- **Unchanged**: `206` with the rest. A18 checks a download cut in half and resumed is the same bytes, and resumes from inside headers, files and the central directory.
- **Changed** (A19: the nightly run deleted a call between the attempt and the resume): `If-Range` fails, and the answer is `200` with the whole new zip. The download starts again rather than joining the old zip's start to the new one's end. A client resuming with `Range` and no `If-Range` gets the current zip's bytes at that offset, which is that client's risk; browsers send `If-Range` when the first answer had a strong `ETag`.

No `Last-Modified`: a date can't say a talkgroup was renamed, so `If-Range` with one always fails and the zip comes whole.

## Streaming

The list is read through the gate once (`Gate.Calls`, which holds back delayed calls and talkgroups not yet in the catalogue), and the catalogue gives each talkgroup's groups. Each recording is then opened through the gate (`Gate.Recording`, over `public_calls`) only when the zip reaches it, and closed before the next. A call hidden or deleted after the list was read is refused there: the reader fails, the response stops short of its `Content-Length`, the connection closes, and "a zip stopped short" is logged with no name. No byte of that call is sent (`TestIntegrationZipHiddenWhileStreaming`).

An entry's CRC-32 is in its local header, before its data, so each recording is read twice: once for its CRC, then to send it, the second read from the page cache. Considered and not chosen:

- **Data descriptors** (the CRC after the data). Stored entries with descriptors are legal but some readers stumble on them, and a resume past a file still needs its CRC for the central directory.
- **Storing each recording's CRC-32 at upload.** A new column (migration) and a change to the `public_calls` view, which other slices are reworking, plus a fallback for recordings stored before it. Worth it if reading twice shows up in practice.

`internal/storedzip` knows nothing of calls: it lays out entries (in memory, or a `Source` it opens when needed) and reads the archive. `internal/public` may import it beside the gate; it reads nothing itself, and a test keeps its imports to encoding, hashing and `io`.

## Consequences

- #81 writes the browser's encoder and the split, against the example above, and keeps each part's URL within `MaxZipURL`. A basket over 359 calls downloads in parts by GET.
- #82 packs a shared conversation the same way: one conversation, no loose calls, `part` 0. Its name and note go in only if the owner answers #75's open question 2 that way.
- Conversation names and notes reach the reverse proxy's access log with every zip downloaded by GET.
- The A10 harness probes all three routes: alone, between public calls, in a conversation, and the only loose call beside a conversation (all forms), and a range and `HEAD` (GET). `TestIntegrationA10Zip` checks a list naming all of 1003's calls, and every other unavailable kind, gives byte for byte the zip of the list without them. `e2e-a10.sh` checks the same against the image.
- Zip rates and concurrency (R72's 2 MB/s, 8 MB/s, 2 and 6 at once) are M5's; a zip here streams as fast as the client takes it.
