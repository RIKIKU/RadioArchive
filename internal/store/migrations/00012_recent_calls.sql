-- Recent calls and search (SPEC.md R46, R73): the indexes a month's search
-- needs, and public_calls's condition rewritten so it costs an index probe
-- a call rather than a function call. Issue #76; ADR 0013.

-- +goose Up

-- The same rule as call_state's (ADR 0003): a call is public when no
-- talkgroup of its patch is listed as anything but public, which is when
-- the strictest state in its patch is public. call_state is a SQL function
-- the planner can't inline, so the view called it once a call, a query of
-- its own each time: about 90 µs, or 80 s for a month's 600,000 calls when
-- a search reads them all. Written as two NOT EXISTS, the call's own
-- talkgroup is a hash anti-join over the few non-public talkgroups and the
-- patch is one index probe, only for a patched call; the month reads in
-- 0.2 s. The planner can also estimate it, which it couldn't for a
-- function's result, so a newest-first page reads a hundred rows of the
-- started_at index and stops. It's still the one definition every public
-- read goes through; a store test checks it agrees with call_state for
-- every combination of states.
CREATE OR REPLACE VIEW public_calls AS
SELECT c.id, c.system_id, c.talkgroup, c.patched_talkgroups, c.started_at, c.ended_at,
       c.recording_path, c.recording_size, c.recording_at
FROM calls c
WHERE NOT EXISTS (SELECT 1 FROM talkgroups t
                  WHERE t.system_id = c.system_id AND t.talkgroup_id = c.talkgroup
                    AND t.state <> 'public')
  AND (c.patched_talkgroups = '{}'
       OR NOT EXISTS (SELECT 1 FROM talkgroups t
                      WHERE t.system_id = c.system_id AND t.talkgroup_id = ANY (c.patched_talkgroups)
                        AND t.state <> 'public'));

-- The talkgroups that aren't public: a few, read by the view's probes.
CREATE INDEX talkgroups_not_public ON talkgroups (system_id, talkgroup_id) WHERE state <> 'public';

-- Paging by start time (R73), with the call ID breaking ties, so load
-- more neither repeats nor skips calls that share a start time. It does
-- what calls_started_at did for retention too.
CREATE INDEX calls_started_at_id ON calls (started_at, id);
DROP INDEX calls_started_at;

-- A talkgroup filter (R46, R16): a call on one of the talkgroups, or
-- patched with one.
CREATE INDEX calls_talkgroup_started_at ON calls (talkgroup, started_at);
CREATE INDEX calls_patched_talkgroups ON calls USING gin (patched_talkgroups);

-- A unit filter (R46): the calls a radio ID was heard on.
CREATE INDEX transmissions_unit ON transmissions (unit);

-- +goose Down
DROP INDEX transmissions_unit;
DROP INDEX calls_patched_talkgroups;
DROP INDEX calls_talkgroup_started_at;
CREATE INDEX calls_started_at ON calls (started_at);
DROP INDEX calls_started_at_id;
DROP INDEX talkgroups_not_public;
CREATE OR REPLACE VIEW public_calls AS
SELECT id, system_id, talkgroup, patched_talkgroups, started_at, ended_at,
       recording_path, recording_size, recording_at
FROM calls
WHERE call_state(system_id, talkgroup, patched_talkgroups) = 'public';
