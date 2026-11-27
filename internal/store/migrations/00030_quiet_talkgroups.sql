-- Quiet talkgroups (SPEC.md R114-R117; issue #188, ADR 0054): a public
-- talkgroup its system's recorders haven't heard for the quiet window
-- (a setting, quiet_window_days, 7 by default), counting only the time
-- they were receiving, drops out of listeners' lists until its next call.
-- What that needs kept:
--
--  1. "Always show" on a talkgroup and on a tag, which an admin sets so a
--     channel that sits unused and then goes busy, such as a fireground
--     channel, is never quiet. Columns of the setup, so changing one moves
--     the catalogue's generation (00007's triggers), as any edit does.
--  2. When each talkgroup was last heard on a call the public may have:
--     last_heard_at also counts calls patched with a hidden talkgroup and
--     calls refused as ignored, and a talkgroup coming back on one of
--     those would tell listeners such a call happened (P1).
--  3. When each system's recorders were receiving: connected, trunk-
--     recorder up and the system's control channel decoding (R14's
--     signals, which are the API's, in memory). An outage stops the
--     window's clock, so it never empties the lists (R115).
--
-- 00030: the next free number on main.

-- +goose Up
ALTER TABLE talkgroups ADD COLUMN always_show boolean NOT NULL DEFAULT false;
ALTER TABLE tags ADD COLUMN always_show boolean NOT NULL DEFAULT false;

-- The catalogue reads it through the view, at the end of its columns.
CREATE OR REPLACE VIEW public_talkgroups AS
SELECT system_id, talkgroup_id, label, name, tag_id, delay_minutes, alert, led_colour,
       sort_order, fireground_channel, always_show
FROM talkgroups
WHERE call_state(system_id, talkgroup_id, '{}'::integer[]) = 'public';

-- Moved by ingest with last_heard_at, never back, and only by a call whose
-- patch is wholly public when it's heard. NULL: not heard so since this
-- column was added, nor on a call still kept.
ALTER TABLE talkgroup_last_heard ADD COLUMN public_heard_at timestamptz;

-- What the public calls kept so far say, as their own talkgroup or in
-- their patch.
UPDATE talkgroup_last_heard l
SET public_heard_at = least(h.at, now())
FROM (SELECT p.system_id, p.talkgroup_id, max(p.started_at) AS at
      FROM (SELECT c.system_id, c.talkgroup AS talkgroup_id, c.started_at FROM public_calls c
            UNION ALL
            SELECT c.system_id, x, c.started_at FROM public_calls c CROSS JOIN LATERAL unnest(c.patched_talkgroups) AS x) p
      GROUP BY p.system_id, p.talkgroup_id) h
WHERE l.system_id = h.system_id AND l.talkgroup_id = h.talkgroup_id;

-- Spans of time a system was receiving. The API that holds a recorder's
-- socket extends the system's latest span every minute while it receives,
-- or starts one after a gap; the spans overlapping is harmless (several
-- API servers, several recorders), as they're merged when read. A
-- deployment with none has received nothing yet: nothing is quiet until
-- a system has received for a whole window (R116). Kept for 400 days.
CREATE TABLE system_receiving (
    system_id  bigint NOT NULL REFERENCES systems (id) ON DELETE CASCADE,
    started_at timestamptz NOT NULL,
    ended_at   timestamptz NOT NULL,
    PRIMARY KEY (system_id, started_at),
    CHECK (ended_at >= started_at)
);
CREATE INDEX system_receiving_ended_at ON system_receiving (system_id, ended_at);

-- +goose Down
DROP TABLE system_receiving;
ALTER TABLE talkgroup_last_heard DROP COLUMN public_heard_at;
DROP VIEW public_talkgroups;
CREATE VIEW public_talkgroups AS
SELECT system_id, talkgroup_id, label, name, tag_id, delay_minutes, alert, led_colour,
       sort_order, fireground_channel
FROM talkgroups
WHERE call_state(system_id, talkgroup_id, '{}'::integer[]) = 'public';
ALTER TABLE tags DROP COLUMN always_show;
ALTER TABLE talkgroups DROP COLUMN always_show;
