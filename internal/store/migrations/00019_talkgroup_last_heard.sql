-- The admin table's last heard (SPEC.md R50): when each talkgroup was
-- last heard, kept by ingest so retention doesn't take it with the calls.
-- Issue #110; ADR 0026.

-- +goose Up

-- One row per talkgroup once it's been heard: on a stored call, as the
-- call's own talkgroup or in its patch, or on a call refused because it's
-- ignored (ADR 0003's rejected_calls names it). Moved by ingest after each
-- call is decided, never back, so a recording uploaded late from a client's
-- queue doesn't undo a newer call. It's a table of its own, not a column on
-- talkgroups, so ingest's writes don't move the catalogue's generation
-- (ADR 0006), which every change to a talkgroups row does.
CREATE TABLE talkgroup_last_heard (
    system_id     bigint NOT NULL,
    talkgroup_id  integer NOT NULL,
    last_heard_at timestamptz NOT NULL,
    PRIMARY KEY (system_id, talkgroup_id),
    FOREIGN KEY (system_id, talkgroup_id) REFERENCES talkgroups (system_id, talkgroup_id) ON DELETE CASCADE
);

-- What the calls and rejected calls kept so far say, by the same rule.
INSERT INTO talkgroup_last_heard (system_id, talkgroup_id, last_heard_at)
SELECT h.system_id, h.talkgroup_id, least(max(h.at), now())
FROM (SELECT c.system_id, c.talkgroup AS talkgroup_id, c.started_at AS at FROM calls c
      UNION ALL
      SELECT c.system_id, p, c.started_at FROM calls c CROSS JOIN LATERAL unnest(c.patched_talkgroups) AS p
      UNION ALL
      SELECT j.system_id, j.talkgroup, to_timestamp(j.start_time) FROM rejected_calls j
      WHERE j.reason = 'ignored_talkgroup') h
JOIN talkgroups t ON t.system_id = h.system_id AND t.talkgroup_id = h.talkgroup_id
GROUP BY h.system_id, h.talkgroup_id;

-- +goose Down
DROP TABLE talkgroup_last_heard;
