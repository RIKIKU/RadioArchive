-- Retention (SPEC.md R88, P5, R35): the retention period as a setting, and
-- the nightly job's and the low-disk check's last runs. Issue #37; ADR 0005.

-- +goose Up

-- Deployment settings an admin changes (R84's Settings page), by key. Only
-- retention_days so far; missing means the default (30 days). Settings are
-- backed up with the setup (R89).
CREATE TABLE settings (
    key        text PRIMARY KEY CHECK (length(key) BETWEEN 1 AND 100),
    value      text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- The background jobs' last runs, one row per job ('nightly', 'low_disk').
-- A run is serialised by a PostgreSQL advisory lock, so on a deployment with
-- several API servers only one does it; this row is how the others know it's
-- been done. ran_for is the scheduled time the run was for (the nightly
-- job's), or when it ran.
CREATE TABLE jobs (
    name        text PRIMARY KEY CHECK (length(name) BETWEEN 1 AND 100),
    ran_for     timestamptz NOT NULL,
    started_at  timestamptz NOT NULL,
    finished_at timestamptz NOT NULL,
    ok          boolean NOT NULL,
    error       text NOT NULL DEFAULT '',
    result      jsonb NOT NULL DEFAULT '{}'
);

-- The nightly job prunes rejected calls with the recordings (ADR 0003).
CREATE INDEX rejected_calls_rejected_at ON rejected_calls (rejected_at);

-- +goose Down
DROP INDEX rejected_calls_rejected_at;
DROP TABLE jobs;
DROP TABLE settings;
