-- The audit log's retention period may be as short as 30 days (owner,
-- 2026-11-25; ADR 0032). 00026's audit_log_retention_days() read only 90
-- to 3,650 and fell back to 730 below that; it now reads 30 to 3,650
-- (retention.MinAuditDays to MaxAuditDays), else 730 (DefaultAuditDays).
-- The API, the prune and the trigger all use this one reading, so they
-- still agree. Nothing stored changes: the API saved only 90 to 3,650.
--
-- 00029: 00028 is assigned for issue #113.

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION audit_log_retention_days() RETURNS integer LANGUAGE sql STABLE AS $$
    SELECT COALESCE((SELECT v FROM (SELECT CASE WHEN value ~ '^[0-9]{1,5}$' THEN value::integer END AS v
                                    FROM settings WHERE key = 'audit_retention_days') s
                     WHERE v BETWEEN 30 AND 3650), 730)
$$;
-- +goose StatementEnd

-- +goose Down
-- A period of 30 to 89 days, saved since, reads as 730 again.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION audit_log_retention_days() RETURNS integer LANGUAGE sql STABLE AS $$
    SELECT COALESCE((SELECT v FROM (SELECT CASE WHEN value ~ '^[0-9]{1,5}$' THEN value::integer END AS v
                                    FROM settings WHERE key = 'audit_retention_days') s
                     WHERE v BETWEEN 90 AND 3650), 730)
$$;
-- +goose StatementEnd
