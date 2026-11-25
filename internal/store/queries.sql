-- name: Ping :one
-- The health check's query: a round trip to the database.
SELECT 1::int AS ok;

-- Recorders (SPEC.md R10). Keys are stored as their SHA-256 only.

-- name: CreateRecorder :one
INSERT INTO recorders (name, key_hash)
VALUES (sqlc.arg(name), sqlc.arg(key_hash))
RETURNING id;

-- name: ListRecorders :many
SELECT id, name, created_at, key_rotated_at, retired_at
FROM recorders
ORDER BY name;

-- name: SetRecorderKey :one
-- Rotates an active recorder's key. No row: unknown or retired.
UPDATE recorders
SET key_hash = sqlc.arg(key_hash), key_rotated_at = now()
WHERE name = sqlc.arg(name) AND retired_at IS NULL
RETURNING id;

-- name: RetireRecorder :one
-- No row: unknown or already retired.
UPDATE recorders
SET key_hash = NULL, retired_at = now()
WHERE name = sqlc.arg(name) AND retired_at IS NULL
RETURNING id;

-- name: NotifyRecorderKeyChanged :exec
-- Tells every running API, at commit, to recheck this recorder's live
-- connections (the old key stops at once).
SELECT pg_notify('radioarchive_recorder_key', sqlc.arg(recorder_id)::bigint::text);

-- name: RecorderByKeyHash :one
SELECT id, name
FROM recorders
WHERE key_hash = sqlc.arg(key_hash) AND retired_at IS NULL;

-- name: RecorderKeyIsCurrent :one
SELECT EXISTS (
    SELECT 1 FROM recorders
    WHERE id = sqlc.arg(id) AND key_hash = sqlc.arg(key_hash) AND retired_at IS NULL
) AS current;

-- name: RecorderByName :one
SELECT id, retired_at FROM recorders WHERE name = sqlc.arg(name);

-- Systems (R11). The ID is the admin's.

-- name: CreateSystem :one
INSERT INTO systems (id, label, name, auto_create, alert, led_colour, sort_order)
VALUES (sqlc.arg(id), sqlc.arg(label), sqlc.arg(name), sqlc.arg(auto_create),
        sqlc.narg(alert), sqlc.narg(led_colour), sqlc.arg(sort_order))
RETURNING id, label, name, auto_create, alert, led_colour, sort_order, created_at, updated_at;

-- name: ListSystems :many
SELECT id, label, name, auto_create, alert, led_colour, sort_order, created_at, updated_at
FROM systems ORDER BY id;

-- name: GetSystem :one
SELECT id, label, name, auto_create, alert, led_colour, sort_order, created_at, updated_at
FROM systems WHERE id = sqlc.arg(id);

-- name: UpdateSystem :one
-- Changes only the fields given. Alert and LED colour may be cleared (NULL:
-- none of its own), so each has a flag saying it's given.
UPDATE systems
SET label = COALESCE(sqlc.narg(label), label),
    name = COALESCE(sqlc.narg(name), name),
    auto_create = COALESCE(sqlc.narg(auto_create), auto_create),
    sort_order = COALESCE(sqlc.narg(sort_order)::integer, sort_order),
    alert = CASE WHEN sqlc.arg(set_alert)::bool THEN sqlc.narg(alert)::text ELSE alert END,
    led_colour = CASE WHEN sqlc.arg(set_led_colour)::bool THEN sqlc.narg(led_colour)::text ELSE led_colour END,
    updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING id, label, name, auto_create, alert, led_colour, sort_order, created_at, updated_at;

-- name: SystemInUse :one
-- A system that still has talkgroups or calls can't be deleted (R11).
SELECT EXISTS (SELECT 1 FROM talkgroups t WHERE t.system_id = sqlc.arg(id))::bool AS talkgroups,
       EXISTS (SELECT 1 FROM calls c WHERE c.system_id = sqlc.arg(id))::bool AS calls;

-- name: DeleteSystem :execrows
DELETE FROM systems WHERE id = sqlc.arg(id);

-- Attachments (R12): each recorder + shortName pair to one system.

-- name: ListAttachments :many
SELECT r.name AS recorder, a.short_name, a.system_id, s.label AS system_label, a.attached_at
FROM attachments a
JOIN recorders r ON r.id = a.recorder_id
JOIN systems s ON s.id = a.system_id
ORDER BY r.name, a.short_name;

-- name: Attach :one
INSERT INTO attachments (recorder_id, short_name, system_id)
VALUES (sqlc.arg(recorder_id), sqlc.arg(short_name), sqlc.arg(system_id))
ON CONFLICT (recorder_id, short_name) DO UPDATE
SET system_id = EXCLUDED.system_id, attached_at = now()
RETURNING attached_at;

-- name: Detach :execrows
DELETE FROM attachments WHERE recorder_id = sqlc.arg(recorder_id) AND short_name = sqlc.arg(short_name);

-- Talkgroups (R21, R22).

-- name: ListTalkgroups :many
SELECT t.system_id, t.talkgroup_id, t.label, t.name, g.name AS tag, t.state, t.unreviewed,
       ARRAY(SELECT x.name FROM talkgroup_groups m JOIN groups x ON x.id = m.group_id
             WHERE m.system_id = t.system_id AND m.talkgroup_id = t.talkgroup_id
             ORDER BY x.sort_order, x.name)::text[] AS groups,
       t.delay_minutes, t.alert, t.led_colour, t.sort_order, t.fireground_channel, t.type, t.frequency,
       t.created_at, t.updated_at
FROM talkgroups t LEFT JOIN tags g ON g.id = t.tag_id
WHERE t.system_id = sqlc.arg(system_id)
ORDER BY t.talkgroup_id;

-- name: GetTalkgroup :one
SELECT t.system_id, t.talkgroup_id, t.label, t.name, g.name AS tag, t.state, t.unreviewed,
       ARRAY(SELECT x.name FROM talkgroup_groups m JOIN groups x ON x.id = m.group_id
             WHERE m.system_id = t.system_id AND m.talkgroup_id = t.talkgroup_id
             ORDER BY x.sort_order, x.name)::text[] AS groups,
       t.delay_minutes, t.alert, t.led_colour, t.sort_order, t.fireground_channel, t.type, t.frequency,
       t.created_at, t.updated_at
FROM talkgroups t LEFT JOIN tags g ON g.id = t.tag_id
WHERE t.system_id = sqlc.arg(system_id) AND t.talkgroup_id = sqlc.arg(talkgroup_id);

-- name: EnsureTag :one
INSERT INTO tags (name) VALUES (sqlc.arg(name))
ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
RETURNING id;

-- name: PutTalkgroup :exec
-- An admin's add or full edit. It marks the talkgroup reviewed. Its groups
-- are set apart (ClearTalkgroupGroups, AddTalkgroupGroups).
INSERT INTO talkgroups (system_id, talkgroup_id, label, name, tag_id, state,
                        delay_minutes, alert, led_colour, sort_order, fireground_channel, type, frequency)
VALUES (sqlc.arg(system_id), sqlc.arg(talkgroup_id), sqlc.arg(label), sqlc.arg(name),
        sqlc.arg(tag_id), sqlc.arg(state), sqlc.arg(delay_minutes), sqlc.narg(alert),
        sqlc.narg(led_colour), sqlc.arg(sort_order), sqlc.narg(fireground_channel),
        sqlc.arg(type), sqlc.narg(frequency))
ON CONFLICT (system_id, talkgroup_id) DO UPDATE
SET label = EXCLUDED.label, name = EXCLUDED.name, tag_id = EXCLUDED.tag_id, state = EXCLUDED.state,
    delay_minutes = EXCLUDED.delay_minutes, alert = EXCLUDED.alert, led_colour = EXCLUDED.led_colour,
    sort_order = EXCLUDED.sort_order, fireground_channel = EXCLUDED.fireground_channel,
    type = EXCLUDED.type, frequency = EXCLUDED.frequency,
    unreviewed = false, updated_at = now();

-- name: PatchTalkgroup :execrows
-- An admin's partial edit, of the fields given. It marks the talkgroup
-- reviewed, so it needs a tag by then (the table's check). Alert, LED
-- colour and fireground channel number may be cleared, so each has a flag
-- saying it's given.
UPDATE talkgroups
SET label = COALESCE(sqlc.narg(label), label),
    name = COALESCE(sqlc.narg(name), name),
    tag_id = COALESCE(sqlc.narg(tag_id), tag_id),
    state = COALESCE(sqlc.narg(state)::talkgroup_state, state),
    delay_minutes = COALESCE(sqlc.narg(delay_minutes)::integer, delay_minutes),
    sort_order = COALESCE(sqlc.narg(sort_order)::integer, sort_order),
    alert = CASE WHEN sqlc.arg(set_alert)::bool THEN sqlc.narg(alert)::text ELSE alert END,
    led_colour = CASE WHEN sqlc.arg(set_led_colour)::bool THEN sqlc.narg(led_colour)::text ELSE led_colour END,
    fireground_channel = CASE WHEN sqlc.arg(set_fireground_channel)::bool
        THEN sqlc.narg(fireground_channel)::integer ELSE fireground_channel END,
    type = COALESCE(sqlc.narg(type), type),
    frequency = CASE WHEN sqlc.arg(set_frequency)::bool THEN sqlc.narg(frequency)::bigint ELSE frequency END,
    unreviewed = false,
    updated_at = now()
WHERE system_id = sqlc.arg(system_id) AND talkgroup_id = sqlc.arg(talkgroup_id);

-- The setup's import and export (R52, R53; issue #115, ADR 0030).

-- name: ImportTalkgroup :exec
-- A talkgroup the import creates: every field the file gives, state and
-- unreviewed included (owner, 2026-11-25). Its groups are set apart.
INSERT INTO talkgroups (system_id, talkgroup_id, label, name, tag_id, state, unreviewed,
                        delay_minutes, alert, led_colour, sort_order, fireground_channel, type, frequency)
VALUES (sqlc.arg(system_id), sqlc.arg(talkgroup_id), sqlc.arg(label), sqlc.arg(name),
        sqlc.narg(tag_id), sqlc.arg(state), sqlc.arg(unreviewed), sqlc.arg(delay_minutes), sqlc.narg(alert),
        sqlc.narg(led_colour), sqlc.arg(sort_order), sqlc.narg(fireground_channel),
        sqlc.arg(type), sqlc.narg(frequency));

-- name: ImportUpdateTalkgroup :exec
-- A talkgroup the import changes: only R52's fields (label, name, tag,
-- fireground channel number, and sort order when given; groups apart).
-- Never state, unreviewed, delay, alert, LED colour, type or frequency.
UPDATE talkgroups
SET label = sqlc.arg(label),
    name = sqlc.arg(name),
    tag_id = COALESCE(sqlc.narg(tag_id), tag_id),
    sort_order = sqlc.arg(sort_order),
    fireground_channel = sqlc.narg(fireground_channel),
    updated_at = now()
WHERE system_id = sqlc.arg(system_id) AND talkgroup_id = sqlc.arg(talkgroup_id);

-- name: ClearFireground :exec
-- Frees a talkgroup's fireground channel number before an import gives it
-- to another, so two talkgroups can swap numbers in one import.
UPDATE talkgroups SET fireground_channel = NULL
WHERE system_id = sqlc.arg(system_id) AND talkgroup_id = sqlc.arg(talkgroup_id);

-- name: ExportTalkgroups :many
-- Every talkgroup of every system, by system and ID, as GetTalkgroup reads
-- one.
SELECT t.system_id, t.talkgroup_id, t.label, t.name, g.name AS tag, t.state, t.unreviewed,
       ARRAY(SELECT x.name FROM talkgroup_groups m JOIN groups x ON x.id = m.group_id
             WHERE m.system_id = t.system_id AND m.talkgroup_id = t.talkgroup_id
             ORDER BY x.sort_order, x.name)::text[] AS groups,
       t.delay_minutes, t.alert, t.led_colour, t.sort_order, t.fireground_channel, t.type, t.frequency,
       t.created_at, t.updated_at
FROM talkgroups t LEFT JOIN tags g ON g.id = t.tag_id
ORDER BY t.system_id, t.talkgroup_id;

-- name: ExportUnits :many
SELECT system_id, first_id, last_id, alias, created_at, updated_at
FROM units ORDER BY system_id, first_id, last_id;

-- name: ImportUnit :exec
-- A unit the import creates. Not an upsert: a unit created meanwhile is a
-- change since the preview (import_changed).
INSERT INTO units (system_id, first_id, last_id, alias)
VALUES (sqlc.arg(system_id), sqlc.arg(first_id), sqlc.arg(last_id), sqlc.arg(alias));

-- name: CreateTag :one
INSERT INTO tags (name, alert, led_colour, sort_order)
VALUES (sqlc.arg(name), sqlc.narg(alert), sqlc.narg(led_colour), sqlc.arg(sort_order))
RETURNING id, name, alert, led_colour, sort_order;

-- name: LockImport :exec
-- One import at a time. The two-key form keeps clear of the audit log's
-- and the first admin's keys.
SELECT pg_advisory_xact_lock(115, 0);

-- name: FiregroundHolder :one
-- The talkgroup, other than this one, that has a fireground channel number
-- (R100), to name it in the refusal.
SELECT t.system_id, s.label AS system_label, t.talkgroup_id, t.label, t.name
FROM talkgroups t JOIN systems s ON s.id = t.system_id
WHERE t.fireground_channel = sqlc.arg(channel)::integer
  AND NOT (t.system_id = sqlc.arg(system_id)::bigint AND t.talkgroup_id = sqlc.arg(talkgroup_id)::integer);

-- name: ClearTalkgroupGroups :exec
DELETE FROM talkgroup_groups
WHERE system_id = sqlc.arg(system_id) AND talkgroup_id = sqlc.arg(talkgroup_id);

-- name: AddTalkgroupGroups :exec
INSERT INTO talkgroup_groups (system_id, talkgroup_id, group_id)
SELECT sqlc.arg(system_id)::bigint, sqlc.arg(talkgroup_id)::integer, g
FROM unnest(sqlc.arg(group_ids)::bigint[]) AS g
ON CONFLICT DO NOTHING;

-- Groups and tags (R22): names, with alert, LED colour and sort order.

-- name: EnsureGroup :one
-- A group named on a talkgroup is created on first use, as a tag is.
INSERT INTO groups (name) VALUES (sqlc.arg(name))
ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
RETURNING id;

-- name: CreateGroup :one
INSERT INTO groups (name, alert, led_colour, sort_order)
VALUES (sqlc.arg(name), sqlc.narg(alert), sqlc.narg(led_colour), sqlc.arg(sort_order))
RETURNING id, name, alert, led_colour, sort_order;

-- name: ListGroups :many
SELECT id, name, alert, led_colour, sort_order FROM groups ORDER BY sort_order, name;

-- name: UpdateGroup :one
UPDATE groups
SET name = COALESCE(sqlc.narg(name), name),
    sort_order = COALESCE(sqlc.narg(sort_order)::integer, sort_order),
    alert = CASE WHEN sqlc.arg(set_alert)::bool THEN sqlc.narg(alert)::text ELSE alert END,
    led_colour = CASE WHEN sqlc.arg(set_led_colour)::bool THEN sqlc.narg(led_colour)::text ELSE led_colour END
WHERE id = sqlc.arg(id)
RETURNING id, name, alert, led_colour, sort_order;

-- name: ListTags :many
SELECT id, name, alert, led_colour, sort_order FROM tags ORDER BY sort_order, name;

-- name: UpdateTag :one
UPDATE tags
SET name = COALESCE(sqlc.narg(name), name),
    sort_order = COALESCE(sqlc.narg(sort_order)::integer, sort_order),
    alert = CASE WHEN sqlc.arg(set_alert)::bool THEN sqlc.narg(alert)::text ELSE alert END,
    led_colour = CASE WHEN sqlc.arg(set_led_colour)::bool THEN sqlc.narg(led_colour)::text ELSE led_colour END
WHERE id = sqlc.arg(id)
RETURNING id, name, alert, led_colour, sort_order;

-- Units (R26): a radio ID, or a range of them, with an alias in its system
-- (migration 00016, ADR 0019).

-- name: ListUnits :many
SELECT system_id, first_id, last_id, alias, created_at, updated_at
FROM units WHERE system_id = sqlc.arg(system_id)
ORDER BY first_id, last_id;

-- name: GetUnit :one
SELECT system_id, first_id, last_id, alias, created_at, updated_at
FROM units
WHERE system_id = sqlc.arg(system_id) AND first_id = sqlc.arg(first_id) AND last_id = sqlc.arg(last_id);

-- name: PutUnit :one
-- An admin's add or edit. created says which.
INSERT INTO units (system_id, first_id, last_id, alias)
VALUES (sqlc.arg(system_id), sqlc.arg(first_id), sqlc.arg(last_id), sqlc.arg(alias))
ON CONFLICT (system_id, first_id, last_id) DO UPDATE
SET alias = EXCLUDED.alias, updated_at = now()
RETURNING (xmax = 0)::bool AS created;

-- name: DeleteUnit :execrows
DELETE FROM units
WHERE system_id = sqlc.arg(system_id) AND first_id = sqlc.arg(first_id) AND last_id = sqlc.arg(last_id);

-- Ingest's decisions (R12, R17, R18, R21), made in one transaction when the
-- server first hears of a call: store.Admit.

-- name: RejectionFor :one
SELECT reason FROM rejected_calls
WHERE recorder_id = sqlc.arg(recorder_id) AND sys_num = sqlc.arg(sys_num)
  AND call_num = sqlc.arg(call_num) AND start_time = sqlc.arg(start_time);

-- name: CallForAdmission :one
-- Locks the call's row while its recording or live start is decided.
SELECT id, system_id, patched_talkgroups, (recording_path IS NOT NULL)::bool AS recorded
FROM calls
WHERE recorder_id = sqlc.arg(recorder_id) AND sys_num = sqlc.arg(sys_num)
  AND call_num = sqlc.arg(call_num) AND start_time = sqlc.arg(start_time)
FOR UPDATE;

-- name: AttachedSystem :one
SELECT s.id, s.auto_create
FROM attachments a JOIN systems s ON s.id = a.system_id
WHERE a.recorder_id = sqlc.arg(recorder_id) AND a.short_name = sqlc.arg(short_name);

-- name: TalkgroupListed :one
SELECT EXISTS (
    SELECT 1 FROM talkgroups WHERE system_id = sqlc.arg(system_id) AND talkgroup_id = sqlc.arg(talkgroup_id)
)::bool AS listed;

-- name: AutoCreateTalkgroup :exec
-- R21: public and unreviewed, with no label, name or tag yet.
INSERT INTO talkgroups (system_id, talkgroup_id, unreviewed)
VALUES (sqlc.arg(system_id), sqlc.arg(talkgroup_id), true)
ON CONFLICT DO NOTHING;

-- name: CallState :one
-- The state a call on talkgroup, patched with patched, is treated as (R16).
SELECT call_state(sqlc.arg(system_id)::bigint, sqlc.arg(talkgroup)::integer,
                  sqlc.arg(patched)::integer[])::talkgroup_state AS state;

-- name: IgnoredInPatch :one
-- Which talkgroup of an ignored call's patch is ignored: its own first.
SELECT talkgroup_id FROM talkgroups
WHERE system_id = sqlc.arg(system_id) AND state = 'ignored'
  AND (talkgroup_id = sqlc.arg(talkgroup) OR talkgroup_id = ANY (sqlc.arg(patched)::integer[]))
ORDER BY talkgroup_id = sqlc.arg(talkgroup) DESC, talkgroup_id
LIMIT 1;

-- name: RecordRejection :execrows
-- Once per call: no row when the call was already counted.
INSERT INTO rejected_calls (recorder_id, sys_num, call_num, start_time, short_name, system_id, talkgroup, reason)
VALUES (sqlc.arg(recorder_id), sqlc.arg(sys_num), sqlc.arg(call_num), sqlc.arg(start_time),
        sqlc.arg(short_name), sqlc.narg(system_id), sqlc.arg(talkgroup), sqlc.arg(reason))
ON CONFLICT DO NOTHING;

-- name: DeleteUnrecordedCall :exec
-- A live call refused before its recording arrived (its patch became
-- ignored) leaves nothing behind.
DELETE FROM calls WHERE id = sqlc.arg(id) AND recording_path IS NULL;

-- Rejected calls, for admins (R12, R17, R18).

-- name: UnattachedRejections :many
SELECT r.name AS recorder, j.short_name, count(*) AS calls, max(j.rejected_at)::timestamptz AS last_at
FROM rejected_calls j JOIN recorders r ON r.id = j.recorder_id
WHERE j.reason = 'not_attached'
GROUP BY r.name, j.short_name
ORDER BY r.name, j.short_name;

-- name: RecorderOverlaps :many
-- Overlapping calls (R13; ADR 0027): two ended calls from two recorders on
-- the same system and talkgroup (the talkgroup each was recorded on, not
-- its patch) whose times overlap, ends included, both starting in
-- [since, until). Each pair of calls counts once, under the pair of
-- recorders. The longest call in the window bounds how far before a call
-- the other can start, so the other side reads calls (talkgroup,
-- started_at) in a range.
WITH w AS (
    SELECT c.recorder_id, c.system_id, c.talkgroup, c.started_at, c.ended_at
    FROM calls c
    WHERE c.started_at >= sqlc.arg(since)::timestamptz AND c.started_at < sqlc.arg(until)::timestamptz
      AND c.ended_at IS NOT NULL
), span AS (
    SELECT coalesce(max(w.ended_at - w.started_at), interval '0') AS longest FROM w
), a AS MATERIALIZED (
    SELECT w.recorder_id, w.system_id, w.talkgroup, w.started_at, w.ended_at,
           greatest(sqlc.arg(since)::timestamptz, w.started_at - span.longest) AS lo
    FROM w CROSS JOIN span
), o AS (
    SELECT a.recorder_id AS first_id, b.recorder_id AS second_id, a.system_id, a.talkgroup, count(*) AS calls
    FROM a
    JOIN calls b ON b.talkgroup = a.talkgroup AND b.system_id = a.system_id AND b.recorder_id > a.recorder_id
     AND b.ended_at IS NOT NULL
     AND b.started_at >= a.lo AND b.started_at >= sqlc.arg(since)::timestamptz
     AND b.started_at < sqlc.arg(until)::timestamptz
     AND b.started_at <= a.ended_at AND b.ended_at >= a.started_at
    GROUP BY a.recorder_id, b.recorder_id, a.system_id, a.talkgroup
)
SELECT o.system_id::bigint AS system_id, s.label AS system_label, o.talkgroup::integer AS talkgroup,
       ra.name AS first_recorder, rb.name AS second_recorder, o.calls::bigint AS calls
FROM o
JOIN recorders ra ON ra.id = o.first_id
JOIN recorders rb ON rb.id = o.second_id
JOIN systems s ON s.id = o.system_id
ORDER BY o.calls DESC, o.system_id, o.talkgroup, ra.name, rb.name;

-- name: TalkgroupRejections :many
SELECT j.system_id::bigint AS system_id, j.talkgroup, j.reason, count(*) AS calls,
       max(j.rejected_at)::timestamptz AS last_at
FROM rejected_calls j
WHERE j.reason <> 'not_attached'
  AND (sqlc.narg(system_id)::bigint IS NULL OR j.system_id = sqlc.narg(system_id))
GROUP BY j.system_id, j.talkgroup, j.reason
ORDER BY j.system_id, j.talkgroup, j.reason;

-- Live calls. A call_start creates the call, or finds the one its recording
-- already created; it never overwrites what a recording stored.

-- name: UpsertLiveCall :one
INSERT INTO calls (recorder_id, sys_num, call_num, start_time, system_id, short_name,
                   talkgroup, patched_talkgroups, started_at, live_first_pcm_at)
VALUES (sqlc.arg(recorder_id), sqlc.arg(sys_num), sqlc.arg(call_num), sqlc.arg(start_time),
        sqlc.arg(system_id), sqlc.arg(short_name), sqlc.arg(talkgroup),
        sqlc.arg(patched_talkgroups)::integer[], sqlc.arg(started_at), sqlc.narg(live_first_pcm_at))
ON CONFLICT (recorder_id, sys_num, call_num, start_time) DO UPDATE
SET live_first_pcm_at = COALESCE(calls.live_first_pcm_at, EXCLUDED.live_first_pcm_at)
RETURNING id;

-- name: AddLiveTransmission :exec
-- Ignored once the recording has stored the call's transmissions. The
-- call's row is locked before the row is inserted, as the recording's
-- upload locks it before storing its transmissions: so one coming during
-- an upload waits for it, then finds the recording and adds nothing, where
-- inserting first deadlocked with the upload (issue #85).
INSERT INTO transmissions (call_id, offset_ms, unit)
SELECT c.id, sqlc.arg(offset_ms), sqlc.arg(unit)
FROM calls c
WHERE c.id = sqlc.arg(call_id) AND c.recording_path IS NULL
FOR KEY SHARE OF c
ON CONFLICT DO NOTHING;

-- name: ExtendLivePatch :one
-- Adds the patched talkgroups a live event reports to the call's (a patch
-- can grow during a call, and can expire before it ends), unless the
-- recording has stored them, and returns the call's state now.
UPDATE calls
SET patched_talkgroups = CASE WHEN recording_path IS NULL
        THEN ARRAY(SELECT DISTINCT p FROM unnest(patched_talkgroups || sqlc.arg(patched)::integer[]) AS p
                   WHERE p <> talkgroup ORDER BY p)
        ELSE patched_talkgroups END
WHERE id = sqlc.arg(id)
RETURNING call_state(system_id, talkgroup, patched_talkgroups)::talkgroup_state AS state;

-- name: EndLiveCall :exec
-- The recording's end time wins over the live one.
UPDATE calls
SET ended_at = CASE WHEN recording_path IS NULL THEN sqlc.arg(ended_at) ELSE ended_at END,
    live_frames = live_frames + sqlc.arg(frames)
WHERE id = sqlc.arg(id);

-- name: AddLiveFrames :exec
UPDATE calls SET live_frames = live_frames + sqlc.arg(frames) WHERE id = sqlc.arg(id);

-- Recordings (R19, R20). In one transaction: lock or create the call, then
-- store the recording on it once.

-- name: LockCallForRecording :one
-- Creates the call if the live call never reached the server, and locks
-- its row either way. An existing call keeps the system it was filed under.
INSERT INTO calls (recorder_id, sys_num, call_num, start_time, system_id, short_name,
                   talkgroup, patched_talkgroups, started_at, ended_at)
VALUES (sqlc.arg(recorder_id), sqlc.arg(sys_num), sqlc.arg(call_num), sqlc.arg(start_time),
        sqlc.arg(system_id), sqlc.arg(short_name), sqlc.arg(talkgroup),
        sqlc.arg(patched_talkgroups)::integer[], sqlc.arg(started_at), sqlc.arg(ended_at))
ON CONFLICT (recorder_id, sys_num, call_num, start_time) DO UPDATE
SET recorder_id = calls.recorder_id
RETURNING id, recording_sha256;

-- name: SetCallRecording :exec
UPDATE calls
SET short_name = sqlc.arg(short_name),
    talkgroup = sqlc.arg(talkgroup),
    patched_talkgroups = sqlc.arg(patched_talkgroups)::integer[],
    started_at = sqlc.arg(started_at),
    ended_at = sqlc.arg(ended_at),
    recording_path = sqlc.arg(recording_path),
    recording_size = sqlc.arg(recording_size),
    recording_sha256 = sqlc.arg(recording_sha256),
    recording_at = now()
WHERE id = sqlc.arg(id);

-- name: DeleteTransmissions :exec
DELETE FROM transmissions WHERE call_id = sqlc.arg(call_id);

-- name: AddTransmission :exec
INSERT INTO transmissions (call_id, offset_ms, unit, duration_ms)
VALUES (sqlc.arg(call_id), sqlc.arg(offset_ms), sqlc.arg(unit), sqlc.arg(duration_ms))
ON CONFLICT (call_id, offset_ms) DO UPDATE
SET unit = EXCLUDED.unit, duration_ms = EXCLUDED.duration_ms;

-- A stored call's state, now (R16, R30).

-- name: CallEffectiveState :one
SELECT call_state(system_id, talkgroup, patched_talkgroups)::talkgroup_state AS state
FROM calls WHERE id = sqlc.arg(id);

-- Recorder client versions (R6).

-- name: SetRecorderClient :exec
-- Records the client version a recorder connected with.
UPDATE recorders
SET client_version = sqlc.arg(client_version),
    client_out_of_date = sqlc.arg(client_out_of_date),
    client_seen_at = now()
WHERE id = sqlc.arg(id);

-- Recorder signals (R14; ADR 0010).

-- name: ListRecorderSignals :many
-- Every active recorder, with what its signals need from the database: the
-- shortNames attached to a system, for the decode rate, and its last call
-- and quiet threshold.
SELECT r.id, r.name, r.client_version, r.client_out_of_date, r.client_seen_at,
       r.last_call_at, r.quiet_after_minutes,
       coalesce((SELECT array_agg(a.short_name ORDER BY a.short_name)
                 FROM attachments a WHERE a.recorder_id = r.id), '{}')::text[] AS attached
FROM recorders r
WHERE r.retired_at IS NULL
ORDER BY r.name;

-- name: TouchRecorderCall :exec
-- Records that a recorder delivered a call heard at the given time. It never
-- moves back: a recording uploaded late, from the client's queue, is an old
-- call.
UPDATE recorders
SET last_call_at = greatest(last_call_at, sqlc.arg(at)::timestamptz)
WHERE id = sqlc.arg(id);

-- name: SetRecorderQuiet :one
-- Sets an active recorder's quiet threshold, in minutes; NULL is off. No
-- row: unknown or retired.
UPDATE recorders
SET quiet_after_minutes = sqlc.narg(quiet_after_minutes)
WHERE name = sqlc.arg(name) AND retired_at IS NULL
RETURNING id;

-- The gate (R30, R32; ADR 0004). Public queries read calls only through the
-- public_calls view (migration 00005), whose one condition is call_state's,
-- and only internal/gate calls them.

-- name: GatePublicCalls :many
-- The public calls among ids, with what a listener may see of each. Its
-- transmissions come in the same query, so in the same snapshot.
SELECT c.id, c.system_id, s.label AS system_label, s.name AS system_name,
       c.talkgroup, t.label AS talkgroup_label, t.name AS talkgroup_name, g.name AS tag,
       c.patched_talkgroups, c.started_at, c.ended_at, c.recording_size,
       COALESCE((SELECT jsonb_agg(jsonb_build_object('unit', x.unit, 'offset_ms', x.offset_ms,
                                                     'duration_ms', x.duration_ms) ORDER BY x.offset_ms)
                 FROM transmissions x WHERE x.call_id = c.id), '[]'::jsonb)::jsonb AS transmissions
FROM public_calls c
JOIN systems s ON s.id = c.system_id
JOIN talkgroups t ON t.system_id = c.system_id AND t.talkgroup_id = c.talkgroup
LEFT JOIN tags g ON g.id = t.tag_id
WHERE c.id = ANY (sqlc.arg(ids)::uuid[]);

-- name: GateUnitCalls :many
-- The calls the radio IDs in [first_unit, last_unit] were heard on, one row
-- a transmission, up to row_limit, for a search's unit filter: one radio
-- (first_unit = last_unit) or a range of them (ADR 0019). It reads only the
-- transmissions_unit index, in its order, and stops at row_limit, so a
-- range costs what its first row_limit transmissions do, however wide it
-- is; the gate drops the repeats.
SELECT call_id FROM transmissions
WHERE unit >= sqlc.arg(first_unit)::bigint AND unit <= sqlc.arg(last_unit)::bigint
LIMIT sqlc.arg(row_limit);

-- name: GateRecentCallsNewest :many
-- Recent calls and search (R46, R73; ADR 0013), newest first: the public
-- calls that started in [started_from, started_to) and come after the
-- cursor (before it in time; with no cursor the gate passes started_to and
-- the smallest ID). talkgroups, when given, are a superset of the filter's
-- public talkgroups by ID, matched against the call's own or its patch's
-- (R16); the gate checks each row's system. A unit filter (in unit_system
-- when that's given) comes as call_ids, the calls the unit was heard on
-- (GateUnitCalls), or, for a unit heard on more calls than that list
-- takes, as unit_first and unit_last, the radio IDs one of which must be
-- heard on the call: one radio, or a range (ADR 0013, ADR 0019). The gate runs it
-- with custom plans, so a NULL filter costs nothing. It joins no talkgroup
-- or system: the gate names them from the catalogue, which every listed
-- call's talkgroup must be in, and the joins' estimates made the planner
-- sort a month of a busy talkgroup's calls rather than read the newest
-- hundred (ADR 0013).
SELECT c.id, c.system_id, c.talkgroup, c.patched_talkgroups, c.started_at, c.ended_at, c.recording_size,
       COALESCE((SELECT jsonb_agg(jsonb_build_object('unit', x.unit, 'offset_ms', x.offset_ms,
                                                     'duration_ms', x.duration_ms) ORDER BY x.offset_ms)
                 FROM transmissions x WHERE x.call_id = c.id), '[]'::jsonb)::jsonb AS transmissions
FROM public_calls c
WHERE c.started_at >= sqlc.arg(started_from) AND c.started_at < sqlc.arg(started_to)
  AND (c.started_at, c.id) < (sqlc.arg(cursor_at)::timestamptz, sqlc.arg(cursor_id)::uuid)
  AND (sqlc.narg(talkgroups)::integer[] IS NULL
       OR c.talkgroup = ANY (sqlc.narg(talkgroups)::integer[])
       OR c.patched_talkgroups && sqlc.narg(talkgroups)::integer[])
  AND (sqlc.narg(unit_first)::bigint IS NULL
       OR EXISTS (SELECT 1 FROM transmissions u WHERE u.call_id = c.id
                  AND u.unit >= sqlc.narg(unit_first)::bigint AND u.unit <= sqlc.narg(unit_last)::bigint))
  AND (sqlc.narg(call_ids)::uuid[] IS NULL OR c.id = ANY (sqlc.narg(call_ids)::uuid[]))
  AND (sqlc.narg(unit_system)::bigint IS NULL OR c.system_id = sqlc.narg(unit_system)::bigint)
ORDER BY c.started_at DESC, c.id DESC
LIMIT sqlc.arg(row_limit);

-- name: GateRecentCallsOldest :many
-- As GateRecentCallsNewest, oldest first: the cursor is the last row given,
-- or, with none, a microsecond before started_from and the largest ID.
SELECT c.id, c.system_id, c.talkgroup, c.patched_talkgroups, c.started_at, c.ended_at, c.recording_size,
       COALESCE((SELECT jsonb_agg(jsonb_build_object('unit', x.unit, 'offset_ms', x.offset_ms,
                                                     'duration_ms', x.duration_ms) ORDER BY x.offset_ms)
                 FROM transmissions x WHERE x.call_id = c.id), '[]'::jsonb)::jsonb AS transmissions
FROM public_calls c
WHERE c.started_at >= sqlc.arg(started_from) AND c.started_at < sqlc.arg(started_to)
  AND (c.started_at, c.id) > (sqlc.arg(cursor_at)::timestamptz, sqlc.arg(cursor_id)::uuid)
  AND (sqlc.narg(talkgroups)::integer[] IS NULL
       OR c.talkgroup = ANY (sqlc.narg(talkgroups)::integer[])
       OR c.patched_talkgroups && sqlc.narg(talkgroups)::integer[])
  AND (sqlc.narg(unit_first)::bigint IS NULL
       OR EXISTS (SELECT 1 FROM transmissions u WHERE u.call_id = c.id
                  AND u.unit >= sqlc.narg(unit_first)::bigint AND u.unit <= sqlc.narg(unit_last)::bigint))
  AND (sqlc.narg(call_ids)::uuid[] IS NULL OR c.id = ANY (sqlc.narg(call_ids)::uuid[]))
  AND (sqlc.narg(unit_system)::bigint IS NULL OR c.system_id = sqlc.narg(unit_system)::bigint)
ORDER BY c.started_at, c.id
LIMIT sqlc.arg(row_limit);

-- name: GateEndedSince :many
-- The public calls that ended at or after ended_since, for the live feed to
-- find, when it starts, the delayed calls it has still to release (R23).
-- started_since only lets the started_at index bound the scan.
SELECT c.id, c.system_id, c.talkgroup, c.patched_talkgroups, c.ended_at::timestamptz AS ended_at
FROM public_calls c
WHERE c.started_at >= sqlc.arg(started_since) AND c.ended_at >= sqlc.arg(ended_since);

-- name: GatePublicRecording :one
-- A public call's recording. No row: no such call, not public, or not
-- recorded yet, which the gate answers alike.
SELECT c.recording_path::text AS recording_path, c.recording_size::bigint AS recording_size,
       c.recording_at::timestamptz AS recording_at
FROM public_calls c
WHERE c.id = sqlc.arg(id) AND c.recording_path IS NOT NULL;

-- The catalogue (R15, R22, R24, R42, R77, R101; ADR 0006). It reads
-- talkgroups only through the public_talkgroups view (migration 00007), as
-- the call queries read public_calls, and only internal/gate calls it.

-- name: GateCatalogueGeneration :one
-- Moves at the commit of any change the catalogue is built from.
SELECT n FROM catalogue_generation;

-- name: GateCatalogue :many
-- Every public talkgroup, with what its settings are taken from: its own,
-- its tag's, its groups' (in their order) and its system's.
SELECT p.system_id, s.label AS system_label, s.name AS system_name,
       s.alert AS system_alert, s.led_colour AS system_led_colour,
       p.talkgroup_id, p.label, p.name, p.delay_minutes, p.alert, p.led_colour, p.fireground_channel,
       g.name AS tag, g.sort_order AS tag_sort_order, g.alert AS tag_alert, g.led_colour AS tag_led_colour,
       COALESCE((SELECT jsonb_agg(jsonb_build_object('name', x.name, 'sort_order', x.sort_order,
                                                     'alert', x.alert, 'led_colour', x.led_colour)
                                  ORDER BY x.sort_order, x.name)
                 FROM talkgroup_groups m JOIN groups x ON x.id = m.group_id
                 WHERE m.system_id = p.system_id AND m.talkgroup_id = p.talkgroup_id),
                '[]'::jsonb)::jsonb AS groups
FROM public_talkgroups p
JOIN systems s ON s.id = p.system_id
LEFT JOIN tags g ON g.id = p.tag_id
ORDER BY s.sort_order, s.id, p.sort_order, p.talkgroup_id;

-- name: GateUnits :many
-- Every unit's alias (R26, R27), held with the catalogue at its generation
-- (ADR 0019). The gate names with them only the units of calls it gives,
-- and unit search finds only units heard on those.
SELECT system_id, first_id, last_id, alias FROM units ORDER BY system_id, first_id, last_id;

-- name: GateUnitsIn :many
-- Unit search (ADR 0019): the radio IDs in [first_id, last_id] heard on any
-- stored call, lowest first, up to row_limit. A skip scan of the
-- transmissions_unit index, one probe per radio ID, so a busy radio costs
-- what a quiet one does. Whether each is heard on a public call is
-- GateUnitsHeard's to say.
WITH RECURSIVE heard (unit) AS (
    (SELECT x.unit FROM transmissions x
     WHERE x.unit >= sqlc.arg(first_id)::bigint AND x.unit <= sqlc.arg(last_id)::bigint
     ORDER BY x.unit LIMIT 1)
    UNION ALL
    SELECT (SELECT x.unit FROM transmissions x
            WHERE x.unit > h.unit AND x.unit <= sqlc.arg(last_id)::bigint
            ORDER BY x.unit LIMIT 1)
    FROM heard h WHERE h.unit IS NOT NULL
)
SELECT h.unit::bigint AS unit FROM heard h WHERE h.unit IS NOT NULL LIMIT sqlc.arg(row_limit);

-- name: GateUnitsHeard :many
-- Unit search (ADR 0019): for each radio ID, up to per_unit of the public
-- calls it's heard on, from at most scan_limit of its transmissions, for the
-- gate to check against the catalogue and the delay (R23). Bounded, so a
-- radio heard on many hidden calls costs no more than scan_limit probes; it
-- may then be missed, which finds less, never more.
SELECT u.unit::bigint AS unit, c.system_id, c.talkgroup, c.patched_talkgroups, c.ended_at
FROM unnest(sqlc.arg(units)::bigint[]) AS u (unit)
CROSS JOIN LATERAL (
    SELECT p.system_id, p.talkgroup, p.patched_talkgroups, p.ended_at
    FROM (SELECT x.call_id FROM transmissions x WHERE x.unit = u.unit LIMIT sqlc.arg(scan_limit)) t
    JOIN public_calls p ON p.id = t.call_id
    LIMIT sqlc.arg(per_unit)
) c;

-- The admin table (R50; ADR 0026): every talkgroup, last heard and calls
-- in the last 30 days, and a talkgroup's calls whatever their state (R34).

-- name: TouchTalkgroupsHeard :exec
-- Records that these talkgroups of the system were heard at the given
-- time. IDs the system doesn't list are skipped; it never moves back.
-- In ID order, so two calls touching the same talkgroups lock them in the
-- same order.
INSERT INTO talkgroup_last_heard (system_id, talkgroup_id, last_heard_at)
SELECT t.system_id, t.talkgroup_id, sqlc.arg(at)::timestamptz
FROM talkgroups t
WHERE t.system_id = sqlc.arg(system_id) AND t.talkgroup_id = ANY (sqlc.arg(talkgroups)::integer[])
ORDER BY t.talkgroup_id
ON CONFLICT (system_id, talkgroup_id) DO UPDATE
SET last_heard_at = EXCLUDED.last_heard_at
WHERE talkgroup_last_heard.last_heard_at < EXCLUDED.last_heard_at;

-- name: AdminTalkgroupTable :many
-- Every talkgroup of every system, with its last heard and how often it
-- was heard since `since`: a stored call that started then or later, as
-- its own talkgroup or in its patch, or a call refused because it's
-- ignored. Hidden and ignored talkgroups alike: this is the admin's.
-- Each kind is counted on its own and the counts added, so the planner
-- aggregates a month of calls in parallel (ADR 0026).
WITH counts AS (
    SELECT n.system_id, n.talkgroup_id, sum(n.calls)::bigint AS calls
    FROM (SELECT c.system_id, c.talkgroup AS talkgroup_id, count(*) AS calls
          FROM calls c
          WHERE c.started_at >= sqlc.arg(since)
          GROUP BY c.system_id, c.talkgroup
          UNION ALL
          SELECT c.system_id, p.p, count(*)
          FROM calls c
          CROSS JOIN LATERAL (SELECT DISTINCT x AS p FROM unnest(c.patched_talkgroups) AS x
                              WHERE x <> c.talkgroup) p
          WHERE c.started_at >= sqlc.arg(since) AND c.patched_talkgroups <> '{}'
          GROUP BY c.system_id, p.p
          UNION ALL
          SELECT j.system_id, j.talkgroup, count(*)
          FROM rejected_calls j
          WHERE j.reason = 'ignored_talkgroup' AND j.start_time >= sqlc.arg(since_unix)::bigint
          GROUP BY j.system_id, j.talkgroup) n
    GROUP BY n.system_id, n.talkgroup_id
)
SELECT t.system_id, t.talkgroup_id, t.label, t.name, g.name AS tag, t.state, t.unreviewed,
       ARRAY(SELECT x.name FROM talkgroup_groups m JOIN groups x ON x.id = m.group_id
             WHERE m.system_id = t.system_id AND m.talkgroup_id = t.talkgroup_id
             ORDER BY x.sort_order, x.name)::text[] AS groups,
       t.delay_minutes, t.alert, t.led_colour, t.sort_order, t.fireground_channel, t.type, t.frequency,
       t.created_at, t.updated_at,
       l.last_heard_at, COALESCE(n.calls, 0)::bigint AS calls_since
FROM talkgroups t
LEFT JOIN tags g ON g.id = t.tag_id
LEFT JOIN talkgroup_last_heard l ON l.system_id = t.system_id AND l.talkgroup_id = t.talkgroup_id
LEFT JOIN counts n ON n.system_id = t.system_id AND n.talkgroup_id = t.talkgroup_id
ORDER BY t.system_id, t.talkgroup_id;

-- name: AdminTalkgroupCalls :many
-- A talkgroup's stored calls, newest first, whatever their state (R34):
-- those on it and those patched with it (R16), started before the cursor
-- (with the call ID breaking ties). Each half reads its own index and stops
-- at the page; state is call_state's, now.
SELECT c.id, c.talkgroup, c.patched_talkgroups, c.started_at, c.ended_at, c.recording_size,
       call_state(sqlc.arg(system_id)::bigint, c.talkgroup, c.patched_talkgroups)::talkgroup_state AS state
FROM ((SELECT o.id, o.talkgroup, o.patched_talkgroups, o.started_at, o.ended_at, o.recording_size
       FROM calls o
       WHERE o.talkgroup = sqlc.arg(talkgroup)::integer AND o.system_id = sqlc.arg(system_id)::bigint
         AND (o.started_at, o.id) < (sqlc.arg(cursor_at)::timestamptz, sqlc.arg(cursor_id)::uuid)
       ORDER BY o.started_at DESC, o.id DESC
       LIMIT sqlc.arg(row_limit))
      UNION ALL
      (SELECT p.id, p.talkgroup, p.patched_talkgroups, p.started_at, p.ended_at, p.recording_size
       FROM calls p
       WHERE p.patched_talkgroups @> ARRAY[sqlc.arg(talkgroup)::integer]
         AND p.talkgroup <> sqlc.arg(talkgroup)::integer AND p.system_id = sqlc.arg(system_id)::bigint
         AND (p.started_at, p.id) < (sqlc.arg(cursor_at)::timestamptz, sqlc.arg(cursor_id)::uuid)
       ORDER BY p.started_at DESC, p.id DESC
       LIMIT sqlc.arg(row_limit))) c
ORDER BY c.started_at DESC, c.id DESC
LIMIT sqlc.arg(row_limit);

-- Admin playback (R34): any stored call's recording, whatever its state.

-- name: AdminRecording :one
SELECT recording_path::text AS recording_path, recording_size::bigint AS recording_size,
       recording_at::timestamptz AS recording_at
FROM calls
WHERE id = sqlc.arg(id) AND recording_path IS NOT NULL;

-- Settings (R84, R88).

-- name: GetSetting :one
SELECT value FROM settings WHERE key = sqlc.arg(key);

-- name: SetSetting :exec
INSERT INTO settings (key, value) VALUES (sqlc.arg(key), sqlc.arg(value))
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now();

-- Retention (R88, P5, R35; ADR 0005). Calls go by when they started, hidden
-- and ignored ones alike, and their transmissions with them.

-- name: CountRecordingsBefore :one
-- How many recordings a retention period ending at cutoff would delete.
SELECT count(*) FROM calls
WHERE started_at < sqlc.arg(cutoff) AND recording_path IS NOT NULL;

-- name: DeleteCallsBefore :many
-- A batch of the calls that started before cutoff, oldest first. Their
-- recordings' files are the caller's to remove, after the commit.
DELETE FROM calls
WHERE id IN (SELECT c.id FROM calls c WHERE c.started_at < sqlc.arg(cutoff)
             ORDER BY c.started_at, c.id LIMIT sqlc.arg(batch))
RETURNING recording_path;

-- name: DeleteOldestCall :one
-- Low disk (R88): the oldest call goes first, if it started before the
-- cut-off (the early-delete floor). No row: none is left before it.
DELETE FROM calls
WHERE id = (SELECT c.id FROM calls c WHERE c.started_at < sqlc.arg(cutoff)
            ORDER BY c.started_at, c.id LIMIT 1)
RETURNING recording_path, started_at;

-- name: OldestCallStart :one
-- When the oldest call kept started: retention, once shortened.
SELECT min(started_at)::timestamptz AS started_at FROM calls;

-- name: PruneRejectedCalls :execrows
-- Rejected calls are counted over the retention period (ADR 0003).
DELETE FROM rejected_calls WHERE rejected_at < sqlc.arg(cutoff);

-- name: StoredRecordingPaths :many
-- The stored recordings among these calls': a file on the volume whose
-- path isn't among them has no row.
SELECT recording_path::text AS recording_path FROM calls
WHERE id = ANY (sqlc.arg(ids)::uuid[]) AND recording_path IS NOT NULL;

-- Background jobs' last runs (ADR 0005).

-- name: GetJob :one
SELECT name, ran_for, started_at, finished_at, ok, error, result FROM jobs WHERE name = sqlc.arg(name);

-- name: SaveJob :exec
INSERT INTO jobs (name, ran_for, started_at, finished_at, ok, error, result)
VALUES (sqlc.arg(name), sqlc.arg(ran_for), sqlc.arg(started_at), sqlc.arg(finished_at),
        sqlc.arg(ok), sqlc.arg(error), sqlc.arg(result))
ON CONFLICT (name) DO UPDATE
SET ran_for = EXCLUDED.ran_for, started_at = EXCLUDED.started_at, finished_at = EXCLUDED.finished_at,
    ok = EXCLUDED.ok, error = EXCLUDED.error, result = EXCLUDED.result;

-- Signed-in listeners' saved profiles (SPEC.md R110, ADR 0021): one row an
-- account, keyed by its OIDC issuer and subject.

-- name: GetListenerProfile :one
SELECT profile FROM listener_profiles WHERE issuer = sqlc.arg(issuer) AND subject = sqlc.arg(subject);

-- name: UpsertListenerProfile :exec
INSERT INTO listener_profiles (issuer, subject, profile, updated_at)
VALUES (sqlc.arg(issuer), sqlc.arg(subject), sqlc.arg(profile), now())
ON CONFLICT (issuer, subject) DO UPDATE SET profile = EXCLUDED.profile, updated_at = EXCLUDED.updated_at;

-- name: DeleteListenerProfile :exec
DELETE FROM listener_profiles WHERE issuer = sqlc.arg(issuer) AND subject = sqlc.arg(subject);

-- Signed-in listeners' sessions (ADR 0022): one row a signed-in browser,
-- keyed by the SHA-256 of the session ID its cookie holds. Times come from
-- the API's clock, so a session's life is the API's to decide.

-- name: InsertListenerSession :exec
INSERT INTO listener_sessions (id_hash, issuer, subject, name, email, id_token, last_used_at, expires_at)
VALUES (sqlc.arg(id_hash), sqlc.arg(issuer), sqlc.arg(subject), sqlc.arg(name), sqlc.arg(email),
        sqlc.narg(id_token), sqlc.arg(used_at), sqlc.arg(expires_at));

-- name: GetListenerSession :one
-- With its account's role and status (ADR 0025): a session whose account
-- has no row isn't one.
SELECT s.issuer, s.subject, s.name, s.email, s.last_used_at, s.expires_at, a.role, a.status
FROM listener_sessions s JOIN accounts a ON a.issuer = s.issuer AND a.subject = s.subject
WHERE s.id_hash = sqlc.arg(id_hash) AND s.expires_at > sqlc.arg(at);

-- name: TouchListenerSession :exec
UPDATE listener_sessions SET last_used_at = sqlc.arg(used_at), expires_at = sqlc.arg(expires_at)
WHERE id_hash = sqlc.arg(id_hash);

-- name: DeleteListenerSession :many
-- Signing out: the session's ID token, for Authentik's end-session
-- endpoint. No row when the session had already ended.
DELETE FROM listener_sessions WHERE id_hash = sqlc.arg(id_hash) RETURNING id_token;

-- name: DeleteAccountListenerSessions :execrows
DELETE FROM listener_sessions WHERE issuer = sqlc.arg(issuer) AND subject = sqlc.arg(subject);

-- name: DeleteExpiredListenerSessions :execrows
DELETE FROM listener_sessions WHERE expires_at <= sqlc.arg(at);

-- Accounts (ADR 0025): everyone who has finished a sign-in, keyed by the
-- issuer and subject they sign in with, with their role and status. Times
-- come from the API's clock.

-- name: LockFirstAdmin :exec
-- Two sign-ins that could each make the first admin wait for each other
-- here until their transactions end, so only one finds no admin. (Row
-- locks can't do it while no admin's row exists to lock.)
SELECT pg_advisory_xact_lock(7300073);

-- name: LockActiveAdmins :many
-- The active admins, locked until the transaction ends: the first-admin
-- check and the last-admin rule read them this way, so two changes at once
-- each see the other's result.
SELECT subject FROM accounts
WHERE issuer = sqlc.arg(issuer) AND role = 'admin' AND status = 'active'
ORDER BY subject
FOR UPDATE;

-- name: UpsertSignedInAccount :one
-- A finished sign-in: the account's row, made or brought up to date with
-- the name and email it signed in with. promote makes it admin (the first
-- admin) unless it's disabled. A disabled account's sign-in is refused, so
-- its last sign-in stays as it was.
INSERT INTO accounts (issuer, subject, name, email, role, first_signed_in_at, last_signed_in_at)
VALUES (sqlc.arg(issuer), sqlc.arg(subject), sqlc.arg(name), sqlc.arg(email),
        CASE WHEN sqlc.arg(promote)::boolean THEN 'admin' ELSE 'listener' END, sqlc.arg(at), sqlc.arg(at))
ON CONFLICT (issuer, subject) DO UPDATE SET
    name = EXCLUDED.name,
    email = EXCLUDED.email,
    role = CASE WHEN sqlc.arg(promote)::boolean AND accounts.status = 'active' THEN 'admin' ELSE accounts.role END,
    last_signed_in_at = CASE WHEN accounts.status = 'active' THEN EXCLUDED.last_signed_in_at ELSE accounts.last_signed_in_at END
RETURNING issuer, subject, name, email, role, status, first_signed_in_at, last_signed_in_at, authentik_pending;

-- name: GetAccount :one
SELECT issuer, subject, name, email, role, status, first_signed_in_at, last_signed_in_at, authentik_pending
FROM accounts WHERE issuer = sqlc.arg(issuer) AND subject = sqlc.arg(subject);

-- name: GetAccountForUpdate :one
SELECT issuer, subject, name, email, role, status, first_signed_in_at, last_signed_in_at, authentik_pending
FROM accounts WHERE issuer = sqlc.arg(issuer) AND subject = sqlc.arg(subject)
FOR UPDATE;

-- name: ListAccounts :many
-- Admins first, then by email.
SELECT issuer, subject, name, email, role, status, first_signed_in_at, last_signed_in_at, authentik_pending
FROM accounts WHERE issuer = sqlc.arg(issuer)
ORDER BY role, lower(email), subject;

-- name: UpdateAccountRole :one
UPDATE accounts SET role = sqlc.arg(role)
WHERE issuer = sqlc.arg(issuer) AND subject = sqlc.arg(subject)
RETURNING issuer, subject, name, email, role, status, first_signed_in_at, last_signed_in_at, authentik_pending;

-- name: UpdateAccountStatus :one
-- Disabling or enabling (ADR 0033): the radio's status, and Authentik
-- still to be told.
UPDATE accounts SET status = sqlc.arg(status), authentik_pending = true
WHERE issuer = sqlc.arg(issuer) AND subject = sqlc.arg(subject)
RETURNING issuer, subject, name, email, role, status, first_signed_in_at, last_signed_in_at, authentik_pending;

-- name: SetAccountAuthentikPending :execrows
-- Whether Authentik has yet to take the status the account had when it
-- was asked: false once it has, true while it hasn't. A status changed
-- again since then is left as that change set it.
UPDATE accounts SET authentik_pending = sqlc.arg(pending)
WHERE issuer = sqlc.arg(issuer) AND subject = sqlc.arg(subject) AND status = sqlc.arg(status);

-- name: DeleteAccount :execrows
DELETE FROM accounts WHERE issuer = sqlc.arg(issuer) AND subject = sqlc.arg(subject);

-- name: DeleteAccountListenerProfile :execrows
DELETE FROM listener_profiles WHERE issuer = sqlc.arg(issuer) AND subject = sqlc.arg(subject);

-- Reads for the audit log's "before" (issue #112, ADR 0029).

-- name: GetGroup :one
SELECT id, name, alert, led_colour, sort_order FROM groups WHERE id = sqlc.arg(id);

-- name: GetTag :one
SELECT id, name, alert, led_colour, sort_order FROM tags WHERE id = sqlc.arg(id);

-- name: GetAttachment :one
SELECT a.system_id, s.label AS system_label
FROM attachments a JOIN systems s ON s.id = a.system_id
WHERE a.recorder_id = sqlc.arg(recorder_id) AND a.short_name = sqlc.arg(short_name);

-- name: RecorderQuiet :one
-- An active recorder's quiet threshold; NULL is off. No row: unknown or
-- retired.
SELECT quiet_after_minutes FROM recorders WHERE name = sqlc.arg(name) AND retired_at IS NULL;

-- The audit log (SPEC.md R54; issue #112, ADR 0029): written only through
-- store.Audited, in the same transaction as the change.

-- name: LockAuditThing :exec
-- One audited change of a thing at a time: the change reads the thing's
-- "before" after this, so two admins changing it at once each record what
-- the other left. The two-key form keeps clear of LockFirstAdmin's key.
SELECT pg_advisory_xact_lock(112, hashtext(sqlc.arg(kind)::text || ':' || sqlc.arg(thing)::text));

-- name: InsertAuditEntry :exec
INSERT INTO audit_log (issuer, subject, admin_name, admin_email, via, kind, thing, label, action, changes)
VALUES (sqlc.arg(issuer), sqlc.arg(subject), sqlc.arg(admin_name), sqlc.arg(admin_email), sqlc.arg(via),
        sqlc.arg(kind), sqlc.arg(thing), sqlc.arg(label), sqlc.arg(action), sqlc.arg(changes));

-- name: ListAuditEntries :many
-- Newest first, after the cursor (the last entry of the page before), with
-- each filter given.
SELECT id, at, issuer, subject, admin_name, admin_email, via, kind, thing, label, action, changes
FROM audit_log
WHERE (sqlc.narg(subject)::text IS NULL OR subject = sqlc.narg(subject)::text)
  AND (sqlc.narg(kind)::text IS NULL OR kind = sqlc.narg(kind)::text)
  AND (sqlc.narg(thing)::text IS NULL OR thing = sqlc.narg(thing)::text)
  AND (sqlc.narg(since)::timestamptz IS NULL OR at >= sqlc.narg(since)::timestamptz)
  AND (sqlc.narg(until)::timestamptz IS NULL OR at < sqlc.narg(until)::timestamptz)
  AND (sqlc.narg(after_at)::timestamptz IS NULL
       OR (at, id) < (sqlc.narg(after_at)::timestamptz, sqlc.narg(after_id)::bigint))
ORDER BY at DESC, id DESC
LIMIT sqlc.arg(page_size);

-- name: ListAuditEntriesOldest :many
-- The same, oldest first (the page's When, ascending).
SELECT id, at, issuer, subject, admin_name, admin_email, via, kind, thing, label, action, changes
FROM audit_log
WHERE (sqlc.narg(subject)::text IS NULL OR subject = sqlc.narg(subject)::text)
  AND (sqlc.narg(kind)::text IS NULL OR kind = sqlc.narg(kind)::text)
  AND (sqlc.narg(thing)::text IS NULL OR thing = sqlc.narg(thing)::text)
  AND (sqlc.narg(since)::timestamptz IS NULL OR at >= sqlc.narg(since)::timestamptz)
  AND (sqlc.narg(until)::timestamptz IS NULL OR at < sqlc.narg(until)::timestamptz)
  AND (sqlc.narg(after_at)::timestamptz IS NULL
       OR (at, id) > (sqlc.narg(after_at)::timestamptz, sqlc.narg(after_id)::bigint))
ORDER BY at, id
LIMIT sqlc.arg(page_size);

-- name: ListAuditAdmins :many
-- Everyone with an entry, by the name and email of their latest, for the
-- Audit log page's "who" filter (ADR 0032). The system's prune is one.
SELECT DISTINCT ON (subject) subject, admin_name, admin_email
FROM audit_log
ORDER BY subject, at DESC, id DESC;

-- The audit log's retention (owner, 2026-11-25; ADR 0032).

-- name: AuditRetentionDays :one
-- The period in force: audit_log_retention_days() is its one reading.
SELECT audit_log_retention_days()::integer AS days;

-- name: CountAuditEntriesPast :one
-- How many entries a period of days would delete now, by the database's
-- clock, as the prune goes.
SELECT count(*) FROM audit_log WHERE at < now() - make_interval(days => sqlc.arg(days)::integer);

-- name: AuditLogSize :one
SELECT count(*) AS entries, min(at)::timestamptz AS oldest FROM audit_log;

-- name: PruneAuditLog :one
-- The nightly run's prune (migration 00026): the only DELETE the table's
-- trigger lets through.
SELECT p.deleted::bigint AS deleted, p.cutoff::timestamptz AS cutoff, p.days::integer AS days
FROM audit_log_prune() p;
