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

-- Systems: one, for now (issue #34 brings attachments).

-- name: EnsureSystem :one
INSERT INTO systems (name) VALUES (sqlc.arg(name))
ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
RETURNING id;

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

-- name: EndLiveCall :exec
-- The recording's end time and patched talkgroups win over the live ones.
UPDATE calls
SET ended_at = CASE WHEN recording_path IS NULL THEN sqlc.arg(ended_at) ELSE ended_at END,
    patched_talkgroups = CASE WHEN recording_path IS NULL THEN sqlc.arg(patched_talkgroups)::integer[]
                              ELSE patched_talkgroups END,
    live_frames = live_frames + sqlc.arg(frames)
WHERE id = sqlc.arg(id);

-- name: AddLiveFrames :exec
UPDATE calls SET live_frames = live_frames + sqlc.arg(frames) WHERE id = sqlc.arg(id);

-- Recordings (R19, R20). In one transaction: lock or create the call, then
-- store the recording on it once.

-- name: LockCallForRecording :one
-- Creates the call if the live call never reached the server, and locks
-- its row either way.
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
SET system_id = sqlc.arg(system_id),
    short_name = sqlc.arg(short_name),
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
