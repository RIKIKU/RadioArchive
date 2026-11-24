-- Systems as admin records, recorder + shortName attachments, talkgroups
-- with their state, rejected calls, and the effective state of a patched
-- call (SPEC.md R11, R12, R16-R18, R21, R22). Issue #34; ADR 0003.

-- +goose Up

-- A system's ID is the admin's (the fixture's EX is ID 1), fixed once set,
-- and what an import merges on (R11, R52). It was an identity column while
-- every call went to one system.
ALTER TABLE systems ALTER COLUMN id DROP IDENTITY;
ALTER TABLE systems ADD CHECK (id > 0);
-- The short label (EX) and the name ("Example network"), as a talkgroup has.
ALTER TABLE systems RENAME COLUMN name TO label;
ALTER TABLE systems ADD COLUMN name text NOT NULL DEFAULT '' CHECK (length(name) <= 200);
-- R21: a talkgroup first heard on a call is created only with this on.
ALTER TABLE systems ADD COLUMN auto_create boolean NOT NULL DEFAULT false;
ALTER TABLE systems ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

-- R12: each recorder + shortName pair feeds exactly one system. The
-- decision is made when the server first hears of a call and kept on the
-- call, so a change affects only calls heard after it.
CREATE TABLE attachments (
    recorder_id bigint NOT NULL REFERENCES recorders (id),
    short_name  text NOT NULL CHECK (length(short_name) BETWEEN 1 AND 64),
    system_id   bigint NOT NULL REFERENCES systems (id) ON DELETE CASCADE,
    attached_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (recorder_id, short_name)
);

-- Tags carry alert, LED colour and sort order later (R22). For now a name.
CREATE TABLE tags (
    id   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name text NOT NULL UNIQUE CHECK (length(name) BETWEEN 1 AND 100)
);

-- Public, hidden or ignored (R22, R30). The order matters: a patched call
-- takes the strictest state in its patch (R16), which is the max.
CREATE TYPE talkgroup_state AS ENUM ('public', 'hidden', 'ignored');

-- R22, as much as ingest needs. ID and system are the key, fixed once set.
-- Only auto-create makes an unreviewed talkgroup, and only one of those may
-- lack a tag.
CREATE TABLE talkgroups (
    system_id    bigint NOT NULL REFERENCES systems (id),
    talkgroup_id integer NOT NULL CHECK (talkgroup_id > 0),
    label        text NOT NULL DEFAULT '' CHECK (length(label) <= 100),
    name         text NOT NULL DEFAULT '' CHECK (length(name) <= 200),
    tag_id       bigint REFERENCES tags (id),
    state        talkgroup_state NOT NULL DEFAULT 'public',
    unreviewed   boolean NOT NULL DEFAULT false,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (system_id, talkgroup_id),
    CHECK (tag_id IS NOT NULL OR unreviewed)
);

-- Calls the server refused (R12, R17, R18), one row per call by R2's
-- identity, so the live call and its recording count it once, and a retried
-- upload gets the same answer. `talkgroup` is the one the rejection is about:
-- the call's own, or the ignored talkgroup in its patch.
CREATE TYPE rejection_reason AS ENUM ('not_attached', 'unknown_talkgroup', 'ignored_talkgroup');

CREATE TABLE rejected_calls (
    recorder_id bigint NOT NULL REFERENCES recorders (id),
    sys_num     integer NOT NULL,
    call_num    bigint NOT NULL,
    start_time  bigint NOT NULL,
    short_name  text NOT NULL,
    system_id   bigint REFERENCES systems (id) ON DELETE CASCADE, -- NULL when not attached
    talkgroup   integer NOT NULL,
    reason      rejection_reason NOT NULL,
    rejected_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (recorder_id, sys_num, call_num, start_time),
    CHECK ((system_id IS NULL) = (reason = 'not_attached'))
);

CREATE INDEX rejected_calls_pair ON rejected_calls (recorder_id, short_name) WHERE reason = 'not_attached';
CREATE INDEX rejected_calls_talkgroup ON rejected_calls (system_id, talkgroup) WHERE reason <> 'not_attached';

-- R16 and R30: the state a call on `talkgroup`, patched with `patched`, is
-- treated as: the strictest of the patch's talkgroups (ignored, then hidden,
-- then public), read from the talkgroups now, since calls carry no state.
-- A patched ID the system doesn't list has no state of its own. The gate
-- (issue #35) and ingest both use this one rule.
-- +goose StatementBegin
CREATE FUNCTION call_state(p_system bigint, p_talkgroup integer, p_patched integer[])
RETURNS talkgroup_state
LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT COALESCE(max(t.state), 'public')
    FROM talkgroups t
    WHERE t.system_id = p_system
      AND (t.talkgroup_id = p_talkgroup OR t.talkgroup_id = ANY (p_patched))
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION call_state(bigint, integer, integer[]);
DROP TABLE rejected_calls;
DROP TYPE rejection_reason;
DROP TABLE talkgroups;
DROP TYPE talkgroup_state;
DROP TABLE tags;
DROP TABLE attachments;
ALTER TABLE systems DROP COLUMN updated_at;
ALTER TABLE systems DROP COLUMN auto_create;
ALTER TABLE systems DROP COLUMN name;
ALTER TABLE systems RENAME COLUMN label TO name;
ALTER TABLE systems DROP CONSTRAINT systems_id_check;
ALTER TABLE systems ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY;
