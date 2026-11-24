---
status: accepted (2026-11-25, by the owner)
---

# 14. Single-call download and the file-safe rule

Date: 2026-11-24

A listener downloads one call from `GET /api/v1/calls/{call}/download` (and `HEAD`), with no sign-in: the stored Ogg/Opus file byte for byte, saved under R63's file name, with the talkgroup's label reduced by a file-safe rule this ADR defines (`SPEC.md` R62, R63, A17). The rule lives in one package, `internal/filename`, which the zip (R65, #78) reuses for its files and conversation folders. Built in [Single-call download](https://github.com/RIKIKU/RadioArchive/issues/77).

## Summary

| Part | Choice |
|---|---|
| **Route** | `GET /api/v1/calls/{call}/download`, in `internal/public`, through the gate (`Gate.Download`). A route of its own, not a query parameter on the recording, so the recording's answer and its browser cache stay as they are |
| **Body** | The recording's own answer (ADR 0004's playback): `http.ServeContent` over the stored file, so range requests, `If-Range` and `Last-Modified` resume a broken download; `Content-Type: audio/ogg; codecs=opus` (since ADR 0016, `application/octet-stream`, so Firefox and Safari keep `.opus`); `Cache-Control: private, max-age=31536000, immutable` (R74). Nothing is transcoded or written into the file (R62) |
| **File name** | `Content-Disposition: attachment; filename="..."`, with `filename*=UTF-8''...` (RFC 6266, RFC 8187) only when the name has a character outside printable ASCII |
| **Name** | `<YYYY-MM-DD>_<HHMMSS>_<talkgroup label>_<talkgroup ID>.opus`, at the call's start, in the deployment's time zone. The label is the talkgroup's label (R63), not its name. A label with nothing left once safe is left out with its underscore: `<YYYY-MM-DD>_<HHMMSS>_<talkgroup ID>.opus` |
| **Time zone** | `TZ`, as Go reads it into `time.Local` (the retention job's too, ADR 0005). `public.WithLocation` sets it in tests. The compose file passes `TZ`, UTC by default |
| **Unavailable** | Deleted, never stored, hidden, ignored, patched with either, within its delay, not recorded yet, or not a call ID: ADR 0004's one `404 call_unavailable`, the same status, headers and body as the recording's |
| **A10** | The route's probes are whole, a range (`bytes=100-`) and `HEAD`. The harness's world gains a call on 1006, delayed 5 minutes and still within it, which every route must answer as a deleted call |

## The file-safe rule

`filename.Safe` reduces a name to characters a file may hold on Linux, macOS and Windows:

1. Invalid UTF-8, control characters (Unicode `Cc`, tab and newline among them), the path separators `/` and `\`, and `: * ? " < > |` each become a hyphen (`-`).
2. Every other white space (no-break and em spaces, say) becomes a space, and runs of spaces become one.
3. Dots and spaces are trimmed from both ends: a leading dot hides a file on Linux and macOS, and Windows drops trailing ones.
4. It's cut to 100 bytes at a character boundary, then trimmed again.
5. A name Windows keeps for a device (`CON`, `PRN`, `AUX`, `NUL`, `COM1`-`COM9`, `LPT1`-`LPT9`, in any case, before any extension) gets an underscore after that word: `con` becomes `con_`, `LPT1.txt` becomes `LPT1_.txt`.

Everything else is kept, non-ASCII letters among it: file systems take UTF-8 names, and `filename*` carries them to the browser. The plain `filename` beside it, for a browser that doesn't read `filename*`, has a hyphen for each non-ASCII character. The rule is idempotent, and the result may be empty; each caller says what stands in for an empty one (a call's file leaves the label out). `DISP N` stays `DISP N`, so A17's name is `2026-03-04_050607_DISP N_1001.opus`.

A hyphen, not an underscore, stands in for a refused character, because the underscore separates the name's parts. Nothing is transliterated or normalised (no Unicode NFC): the label is the admin's, and the name should look like it.

## Why a second read

`Gate.Download` reads the call through `Calls` (which holds back a delayed call, R23, and a talkgroup not yet in the catalogue, and gives the label and start the name needs), then opens the file through `public_calls` again, as the recording does. A call hidden between the two reads is still refused at the second. Both reads are by primary key.

## Consequences

- `internal/public` may import `internal/filename` beside the gate. It reads nothing: a test in that package keeps its imports to `fmt`, `strings`, `time`, `unicode` and `unicode/utf8`.
- A download names its talkgroup's label in a header, so the A10 leak check now covers `Content-Disposition` for every unavailable call; it's absent from the 404.
- The call details (`QUERY /api/v1/calls`) don't carry a download URL yet: the Download buttons (#79, #80) build it with the call's ID, as `public.DownloadURL` does.
- A changed label renames later downloads of the same call; the bytes don't change, and `If-Range` still resumes.
