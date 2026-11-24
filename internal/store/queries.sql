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
INSERT INTO systems (id, label, name, auto_create)
VALUES (sqlc.arg(id), sqlc.arg(label), sqlc.arg(name), sqlc.arg(auto_create))
RETURNING id, label, name, auto_create, created_at, updated_at;

-- name: ListSystems :many
SELECT id, label, name, auto_create, created_at, updated_at FROM systems ORDER BY id;

-- name: GetSystem :one
SELECT id, label, name, auto_create, created_at, updated_at FROM systems WHERE id = sqlc.arg(id);

-- name: UpdateSystem :one
-- Changes only the fields given.
UPDATE systems
SET label = COALESCE(sqlc.narg(label), label),
    name = COALESCE(sqlc.narg(name), name),
    auto_create = COALESCE(sqlc.narg(auto_create), auto_create),
    updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING id, label, name, auto_create, created_at, updated_at;

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
       t.created_at, t.updated_at
FROM talkgroups t LEFT JOIN tags g ON g.id = t.tag_id
WHERE t.system_id = sqlc.arg(system_id)
ORDER BY t.talkgroup_id;

-- name: GetTalkgroup :one
SELECT t.system_id, t.talkgroup_id, t.label, t.name, g.name AS tag, t.state, t.unreviewed,
       t.created_at, t.updated_at
FROM talkgroups t LEFT JOIN tags g ON g.id = t.tag_id
WHERE t.system_id = sqlc.arg(system_id) AND t.talkgroup_id = sqlc.arg(talkgroup_id);

-- name: EnsureTag :one
INSERT INTO tags (name) VALUES (sqlc.arg(name))
ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
RETURNING id;

-- name: PutTalkgroup :exec
-- An admin's add or full edit. It marks the talkgroup reviewed.
INSERT INTO talkgroups (system_id, talkgroup_id, label, name, tag_id, state)
VALUES (sqlc.arg(system_id), sqlc.arg(talkgroup_id), sqlc.arg(label), sqlc.arg(name),
        sqlc.arg(tag_id), sqlc.arg(state))
ON CONFLICT (system_id, talkgroup_id) DO UPDATE
SET label = EXCLUDED.label, name = EXCLUDED.name, tag_id = EXCLUDED.tag_id, state = EXCLUDED.state,
    unreviewed = false, updated_at = now();

-- name: PatchTalkgroup :execrows
-- An admin's partial edit, of the fields given. It marks the talkgroup
-- reviewed, so it needs a tag by then (the table's check).
UPDATE talkgroups
SET label = COALESCE(sqlc.narg(label), label),
    name = COALESCE(sqlc.narg(name), name),
    tag_id = COALESCE(sqlc.narg(tag_id), tag_id),
    state = COALESCE(sqlc.narg(state)::talkgroup_state, state),
    unreviewed = false,
    updated_at = now()
WHERE system_id = sqlc.arg(system_id) AND talkgroup_id = sqlc.arg(talkgroup_id);

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
-- Ignored once the recording has stored the call's transmissions.
INSERT INTO transmissions (call_id, offset_ms, unit)
SELECT c.id, sqlc.arg(offset_ms), sqlc.arg(unit)
FROM calls c
WHERE c.id = sqlc.arg(call_id) AND c.recording_path IS NULL
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

-- name: GatePublicRecording :one
-- A public call's recording. No row: no such call, not public, or not
-- recorded yet, which the gate answers alike.
SELECT c.recording_path::text AS recording_path, c.recording_size::bigint AS recording_size,
       c.recording_at::timestamptz AS recording_at
FROM public_calls c
WHERE c.id = sqlc.arg(id) AND c.recording_path IS NOT NULL;

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
-- Low disk (R88): the oldest call goes first. No row: no calls left.
DELETE FROM calls
WHERE id = (SELECT c.id FROM calls c ORDER BY c.started_at, c.id LIMIT 1)
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
