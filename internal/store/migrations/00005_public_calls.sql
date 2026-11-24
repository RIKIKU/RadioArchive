-- The hidden-talkgroup gate's one view of the calls (SPEC.md P1, R16, R30,
-- R32). Issue #35; ADR 0004.

-- +goose Up

-- The calls the public may know of: those whose patch is public now, by
-- call_state, the one rule for a call's state (ADR 0003). Hidden and ignored
-- talkgroups' calls, and calls patched with one, aren't in it. Every public
-- query reads calls through this view and nothing else (internal/gate), so
-- hiding a talkgroup takes its calls out of every surface at once, and
-- making it public again puts them back. Nothing is deleted.
CREATE VIEW public_calls AS
SELECT id, system_id, talkgroup, patched_talkgroups, started_at, ended_at,
       recording_path, recording_size, recording_at
FROM calls
WHERE call_state(system_id, talkgroup, patched_talkgroups) = 'public';

-- +goose Down
DROP VIEW public_calls;
