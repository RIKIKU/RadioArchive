-- Recorders and their client keys (SPEC.md R10), and calls with their
-- transmissions and recordings (R2, R19, R20, R60). Issue #32.

-- +goose Up

-- A recorder is managed only from the server shell. Only the SHA-256 of its
-- client key is kept. Retiring a recorder clears its key, so it can't
-- connect, and keeps the row, so its calls stay.
CREATE TABLE recorders (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name           text NOT NULL UNIQUE CHECK (length(name) BETWEEN 1 AND 100),
    key_hash       bytea UNIQUE CHECK (length(key_hash) = 32),
    created_at     timestamptz NOT NULL DEFAULT now(),
    key_rotated_at timestamptz,
    retired_at     timestamptz,
    CHECK ((retired_at IS NULL) = (key_hash IS NOT NULL))
);

-- Systems get their own slice (issue #34: admin-created systems and
-- recorder + shortName attachments). Until then the server files every call
-- under one system.
CREATE TABLE systems (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       text NOT NULL UNIQUE CHECK (length(name) BETWEEN 1 AND 100),
    created_at timestamptz NOT NULL DEFAULT now()
);

-- One trunk-recorder call. (recorder_id, sys_num, call_num, start_time) is
-- R2's identity: the live call and its recording meet on it. The ID is a
-- random UUID, so it's opaque and gaps reveal nothing (R60).
CREATE TABLE calls (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    recorder_id        bigint NOT NULL REFERENCES recorders (id),
    sys_num            integer NOT NULL CHECK (sys_num BETWEEN 0 AND 65535),
    call_num           bigint NOT NULL,
    start_time         bigint NOT NULL, -- trunk-recorder's start_time, Unix seconds
    system_id          bigint NOT NULL REFERENCES systems (id),
    short_name         text NOT NULL,
    talkgroup          integer NOT NULL,
    patched_talkgroups integer[] NOT NULL DEFAULT '{}',
    started_at         timestamptz NOT NULL,
    ended_at           timestamptz,
    -- The live call: when the client got its first PCM (R38's clock) and how
    -- many Opus frames the server received for it.
    live_first_pcm_at  timestamptz,
    live_frames        bigint NOT NULL DEFAULT 0,
    -- The recording: a file on the recordings volume (R20), its path relative
    -- to RECORDINGS_DIR. All four are set together, once.
    recording_path     text,
    recording_size     bigint,
    recording_sha256   bytea,
    recording_at       timestamptz,
    UNIQUE (recorder_id, sys_num, call_num, start_time),
    CHECK ((recording_path IS NULL) = (recording_sha256 IS NULL)
       AND (recording_path IS NULL) = (recording_size IS NULL)
       AND (recording_path IS NULL) = (recording_at IS NULL))
);

CREATE INDEX calls_started_at ON calls (started_at);

-- One unit's key-up within a call. offset_ms is from the call's started_at.
-- Live transmission events add rows as they come; the recording, the source
-- of truth, replaces them all.
CREATE TABLE transmissions (
    call_id     uuid NOT NULL REFERENCES calls (id) ON DELETE CASCADE,
    offset_ms   integer NOT NULL CHECK (offset_ms >= 0),
    unit        bigint NOT NULL,
    duration_ms integer CHECK (duration_ms >= 0),
    PRIMARY KEY (call_id, offset_ms)
);

-- +goose Down
DROP TABLE transmissions;
DROP TABLE calls;
DROP TABLE systems;
DROP TABLE recorders;
