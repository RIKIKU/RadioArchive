# How much disk a month of recordings needs

Research for [#21](https://github.com/RIKIKU/RadioArchive/issues/21). Terms follow `GLOSSARY.md`: **call**, **transmission**, **recording**, **talkgroup**, **recorder**, **recorder client**. The stack is the one in `docs/adr/0001-technology-stack.md`: the recorder client encodes trunk-recorder's 8 kHz P25 audio to narrowband Opus and the server keeps each recording as one Ogg Opus file on a volume, with a row per call in PostgreSQL 18.

Sources were read on 2026-11-16. Where a figure is my own arithmetic rather than a quote, it says so.

## Answer in brief

- **Encode at 12 kbit/s VBR, narrowband, `OPUS_APPLICATION_VOIP`, 20 ms frames.** RFC 6716 puts the narrowband speech sweet spot at 8–12 kbit/s, and libopus's own automatic choice for 8 kHz mono at 20 ms is 11 kbit/s. Going higher buys nothing: the source is 8 kHz audio decoded from a P25 vocoder.
- **12 kbit/s is 5.4 MB of Opus per hour of audio, and about 5.7 MB per hour once Ogg pages are added.** Each recording file also costs about 2.1 KB of fixed overhead (headers plus the half-empty last filesystem block), so short calls cost more per hour than long ones.
- **The sizing formula:** `disk ≈ call-hours × 5.7 MB + calls × 2.1 KB`, then add headroom. For a typical 10 s average call that comes to about **6.5 MB per call-hour**.
- **A busy month is single-digit gigabytes per recorder.** 300,000 calls averaging 10 s is about 833 call-hours, or **about 5.4 GB**. Two such recorders hear different calls, so their sizes add, giving about 11 GB. The same audio kept as trunk-recorder's WAV would be about 48 GB per recorder.
- **Call-hours come from trunk-recorder's call JSON.** `call_length` is the sum of the call's transmission audio in seconds, which is what the recording holds. So call-hours = Σ `call_length` / 3600, or calls × mean `call_length` / 3600.
- **The database is small next to the audio.** A call row plus its indexes is about 300 bytes, or about 500 bytes with a few transmission rows per call. 300,000 calls is about 90–150 MB, roughly 2–3 % of the audio.

## 1. Bitrate

### What the specs and libopus say

- RFC 6716 §2.1.1 lists the sweet spots for 20 ms frames: "8-12 kbit/s for NB speech, 16-20 kbit/s for WB speech, 28-40 kbit/s for FB speech". Narrowband (NB) is defined in Table 1 as 4 kHz audio bandwidth at an 8 kHz sample rate. Opus as a whole covers 6–510 kbit/s ([RFC 6716](https://www.rfc-editor.org/rfc/rfc6716.html)).
- trunk-recorder records P25 audio at 8 kHz, 16-bit: `wav_sink = gr::blocks::transmission_sink::make(1, 8000, 16);` (`trunk-recorder/recorders/p25_recorder_decode.cc`, master at commit `1aac86c`, 2026-09-27). An 8 kHz source carries nothing above 4 kHz, so wideband or fullband Opus would spend bits on an empty band.
- With `OPUS_AUTO` (the default), libopus picks `60*Fs/frame_size + Fs*channels` bit/s (`user_bitrate_to_bitrate` in `src/opus_encoder.c`, [xiph/opus](https://github.com/xiph/opus/blob/main/src/opus_encoder.c)). At 8 kHz mono and 20 ms frames (160 samples) that is 3,000 + 8,000 = **11,000 bit/s** (my arithmetic).
- Encoder controls ([libopus 1.5 encoder CTLs](https://opus-codec.org/docs/opus_api-1.5/group__opus__encoderctls.html)): VBR is the default ("1: VBR (default)"), and hard CBR "can cause noticeable quality degradation" in LPC/hybrid modes at very low bit-rates. `OPUS_APPLICATION_VOIP` is to "Process signal for improved speech intelligibility." `OPUS_SET_MAX_BANDWIDTH` is the recommended way to cap the band. DTX is off by default and only applies to the LPC layer.

### Recommendation

Set 12,000 bit/s, VBR on, `OPUS_APPLICATION_VOIP`, `OPUS_SIGNAL_VOICE`, max bandwidth narrowband, 20 ms frames, and DTX off. A call has no long silences to save on, because trunk-recorder records only while a unit transmits. This setting is at the top of the RFC's narrowband range and just above libopus's own default. Whether 8 or 16 kbit/s sounds different after a P25 vocoder is a listening test for the first milestone, and the table below shows what each would cost.

## 2. Bytes per hour of audio

### Opus payload

The bitrate counts the whole Opus packet, TOC byte included (RFC 6716 §3.1). So payload bytes per hour = bitrate / 8 × 3,600.

### Ogg page overhead

- An Ogg page header is `number_page_segments + 27` bytes. Packets are cut into 255-byte segments plus a final shorter one, so each packet under 255 bytes costs one lacing byte ([RFC 3533](https://www.rfc-editor.org/rfc/rfc3533.html)).
- At 20 ms frames there are 50 packets a second. At 32 kbit/s or less each packet is 80 bytes or smaller, so that is 50 lacing bytes a second.
- libopusenc's default is `enc->max_ogg_delay = 48000;`, meaning one page per second of 48 kHz granule time (`src/opusenc.c`, [xiph/libopusenc](https://github.com/xiph/libopusenc/blob/master/src/opusenc.c)). Granule positions are always at 48 kHz ([RFC 7845](https://www.rfc-editor.org/rfc/rfc7845.html)). That adds 27 header bytes a second.
- Together that is about 77 bytes a second, or **0.28 MB an hour** (my arithmetic). That matches RFC 7845's own aside of "about 0.4 kbps" for lacing at this packet rate.

| Bitrate (VBR target) | Opus payload per hour | With Ogg pages per hour |
|---|---|---|
| 8 kbit/s | 3.60 MB | 3.88 MB |
| 11 kbit/s (libopus auto) | 4.95 MB | 5.23 MB |
| **12 kbit/s (recommended)** | **5.40 MB** | **5.68 MB** |
| 16 kbit/s | 7.20 MB | 7.48 MB |
| 24 kbit/s | 10.80 MB | 11.08 MB |
| 32 kbit/s | 14.40 MB | 14.68 MB |
| *trunk-recorder WAV, for comparison* | *57.6 MB (8,000 × 2 B × 3,600)* | |

MB here means 10⁶ bytes. VBR averages close to its target over a long run, so treat these figures as the expected size rather than a hard cap.

### Per-file overhead

Every recording is its own file, so each call also pays a fixed cost:

- **Headers.** The ID header is 19 bytes for mono with no channel mapping table, alone on its own page (RFC 7845 §5.1 and Figure 2), so about 47 bytes with its page. The comment header holds an 8-byte magic signature, the vendor string and the tags. libopusenc then pads it with `enc->comment_padding = 512;` by default, so the two header pages come to about **650 bytes**. With `OPE_SET_COMMENT_PADDING(0)` (or a hand-written muxer that adds no padding) they come to about **100 bytes**. The recorder client never edits tags afterwards, so the padding is wasted space and should be set to 0.
- **Filesystem blocks.** ext4's default block size is 4 KiB ([kernel.org ext4 overview](https://www.kernel.org/doc/html/latest/filesystems/ext4/overview.html)). A file fills whole blocks, so on average half a block (about 2 KB) at the end of each file is unused (my estimate).

That is about **2.1 KB per call** with padding off, or 2.7 KB with libopusenc's default padding. Here is what it adds per hour of audio for different average call lengths:

| Average call | Files per call-hour | Added per call-hour (padding 0) | Total per call-hour at 12 kbit/s |
|---|---|---|---|
| 5 s | 720 | 1.55 MB | 7.2 MB |
| 8 s | 450 | 0.97 MB | 6.7 MB |
| 10 s | 360 | 0.77 MB | 6.5 MB |
| 15 s | 240 | 0.52 MB | 6.2 MB |

## 3. Estimating call-hours from an existing deployment

trunk-recorder writes a JSON file per call when `callLog` is `true` (the default): "Should a json file with the call details be kept after successful uploads?" ([docs/CONFIGURE.md](https://github.com/TrunkRecorder/trunk-recorder/blob/master/docs/CONFIGURE.md)). That JSON includes `start_time`, `stop_time`, `talkgroup`, `freqList`, `srcList` and `call_length` (`call_concluder.cc`).

`call_length` is the audio actually recorded, not the wall-clock span of the call. `call_concluder.cc` sums each transmission's segment length (`audio_sum_ms += seg_ms`) and sets `call_info.length = audio_sum_ms / 1000.0`. The JSON stores that value rounded to whole seconds. Gaps between transmissions therefore don't count, and the figure matches the length of the recording.

Method:

1. Take a **busy month** of call JSON files from the recorder you want to size, or the busiest month you have.
2. Call-hours **H** = Σ `call_length` / 3,600. Without the JSON files, use calls × mean `call_length` / 3,600. Rounding to whole seconds averages out over many calls.
3. Call count **N** = the number of JSON files. Calls shorter than `minDuration` are deleted by trunk-recorder ("calls below this number will have recordings deleted") and never reach the archive. Leave them out.
4. If only WAV files are kept, their duration is (file bytes − 44) / 16,000 seconds (8 kHz × 2 bytes, standard 44-byte header). The default `compressWav: true` converts recordings to `.m4a`, whose size does not give duration this way, so use the JSON for those.
5. **Upper bound:** a recorder can't record more calls at once than it has digital recorders (`digitalRecorders`, "The number of Digital Recorders to have attached to this source."). So H ≤ Σ `digitalRecorders` × 720 h a month. This is a ceiling, not an estimate.

Then:

```
month_disk ≈ H × 5.68 MB  +  N × 2.1 KB            (12 kbit/s, comment padding 0)
volume     ≈ month_disk × (retention_days + 1) / 30 × headroom
```

The `+ 1` day covers the nightly retention job, which deletes each recording up to a day after it expires. Headroom of 1.5× covers a major incident month and filesystem reserve. ext4 also reserves 5 % of blocks for root by default.

### Worked examples (one recorder, 12 kbit/s, one month)

| Calls (N) | Mean `call_length` | Call-hours (H) | Audio (H × 5.68 MB) | Per-file (N × 2.1 KB) | Month on disk | × 1.5 headroom |
|---|---|---|---|---|---|---|
| 100,000 | 8 s | 222 | 1.26 GB | 0.21 GB | **1.5 GB** | 2.2 GB |
| 300,000 | 10 s | 833 | 4.73 GB | 0.63 GB | **5.4 GB** | 8.0 GB |
| 600,000 | 12 s | 2,000 | 11.36 GB | 1.26 GB | **12.6 GB** | 19 GB |
| Ceiling, 20 digital recorders | — | 14,400 | 81.8 GB | — | ≥ 82 GB | — |

**Two recorders.** Each recorder hears different calls (GLOSSARY: Recorder), so run the method once per recorder and add the results. Two recorders at the middle row need about 11 GB a month, or 16 GB with headroom.

## 4. PostgreSQL size of call rows

### Overheads from the PostgreSQL 18 docs

From [Database Page Layout](https://www.postgresql.org/docs/18/storage-page-layout.html):

- Pages are "usually 8 kB", and "The first 24 bytes of each page consists of a page header".
- Each row has an item identifier, "each requiring four bytes".
- Each row header is "a fixed-size header (occupying 23 bytes on most machines), followed by an optional null bitmap". The user data starts at an offset that "must always be a multiple of the MAXALIGN distance" (8 bytes on 64-bit), so the header takes 24 bytes, or 32 if a null bitmap is needed.
- B-tree index tuples have an 8-byte `IndexTupleData` header (`src/include/access/itup.h`), are padded to MAXALIGN, and also take a 4-byte item identifier. B-tree leaf pages are filled to 90 % by default ([CREATE INDEX, fillfactor](https://www.postgresql.org/docs/18/sql-createindex.html)). Heap tables are filled to 100 % ([CREATE TABLE](https://www.postgresql.org/docs/18/sql-createtable.html)).

### Estimate (assumed schema; my arithmetic)

The schema isn't designed yet. This assumes a call row holding id (`bigint`), recorder client (`int`), `sys_num` (`smallint`), `call_num` (`bigint`), start and stop (`timestamptz` × 2), talkgroup (`int`), unit (`int`), length in ms (`int`), file size (`int`) and a relative path (`text`, about 50 bytes).

| Part | Bytes per call |
|---|---|
| Heap row: 24–32 header + about 100 data, aligned, + 4 item ID | about 135 |
| PK `(id)`: 8 + 8 key, + 4, at 90 % fill | about 22 |
| Unique identity `(recorder, sys_num, call_num, start_time)`: 8 + 24, + 4 | about 40 |
| `(talkgroup, start_time)` for browsing: 8 + 16, + 4 | about 31 |
| `(start_time)` for the retention job: 8 + 8, + 4 | about 22 |
| **Call row with indexes** | **about 250, so plan for 300** |
| Optional transmission rows (unit, offset, length): about 60 B each with index, ≈ 3 per call | + about 180–200 |

That gives **about 300 bytes per call**, or about 500 with transmissions. 300,000 calls is 90–150 MB a month, roughly 2–3 % of the audio. Retention deletes leave dead rows that autovacuum makes reusable, so in steady state the tables stay near one retention period's worth plus some bloat. Check the estimate on the first real data with `pg_total_relation_size('calls')`.

## 5. What this means for the design

- **The recorder client** should set the encoder to 12 kbit/s VBR, VOIP, narrowband, 20 ms frames, and write Ogg with no comment padding (`OPE_SET_COMMENT_PADDING(0)` if it uses libopusenc). One page per second (the libopusenc default) is fine.
- **The admin docs** can give one rule of thumb: **about 6.5 MB per call-hour, or about 2 KB per call plus 5.7 MB per audio hour**, with the trunk-recorder JSON method above for getting call-hours.
- **Disk, not the database, decides the retention period.** At the volumes above, a month of recordings fits easily on a small volume, and a year of the middle example is about 65 GB.

## Sources

- RFC 6716, Definition of the Opus Audio Codec: https://www.rfc-editor.org/rfc/rfc6716.html
- RFC 7845, Ogg Encapsulation for the Opus Audio Codec: https://www.rfc-editor.org/rfc/rfc7845.html
- RFC 3533, The Ogg Encapsulation Format Version 0: https://www.rfc-editor.org/rfc/rfc3533.html
- libopus 1.5 encoder CTLs: https://opus-codec.org/docs/opus_api-1.5/group__opus__encoderctls.html
- libopus `src/opus_encoder.c` (`user_bitrate_to_bitrate`): https://github.com/xiph/opus/blob/main/src/opus_encoder.c
- libopusenc `src/opusenc.c` (defaults): https://github.com/xiph/libopusenc/blob/master/src/opusenc.c
- trunk-recorder `docs/CONFIGURE.md`, `trunk-recorder/call_concluder/call_concluder.cc`, `trunk-recorder/recorders/p25_recorder_decode.cc` (master, commit `1aac86c`, 2026-09-27): https://github.com/TrunkRecorder/trunk-recorder
- Linux kernel ext4 overview: https://www.kernel.org/doc/html/latest/filesystems/ext4/overview.html
- PostgreSQL 18, Database Page Layout: https://www.postgresql.org/docs/18/storage-page-layout.html
- PostgreSQL 18, CREATE INDEX and CREATE TABLE (fillfactor): https://www.postgresql.org/docs/18/sql-createindex.html, https://www.postgresql.org/docs/18/sql-createtable.html
- PostgreSQL `src/include/access/itup.h` (`IndexTupleData`): https://github.com/postgres/postgres/blob/master/src/include/access/itup.h
