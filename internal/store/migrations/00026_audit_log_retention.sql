-- The audit log's retention (owner, 2026-11-25, on issue #118; issue #112,
-- ADR 0032): old entries are deleted. The setting audit_retention_days
-- (AUDIT_RETENTION_DAYS on the Settings page, 90 to 3,650 days, 730 by
-- default) is how long an entry is kept, and the nightly run deletes the
-- entries older than it with audit_log_prune(), which writes one entry of
-- its own saying how many went and from before when.
--
-- 00023's triggers refused every DELETE. Now one path may delete: inside
-- audit_log_prune(), which turns on a transaction-local setting for its own
-- DELETE, and even then only entries older than the period in force, by the
-- database's clock (the clock "at" is written with). UPDATE and TRUNCATE are
-- still refused, as is any other DELETE, so nothing rewrites an entry and
-- nothing deletes one the period still keeps.
--
-- The prune's own entry is the system's: issuer '', subject 'system', how
-- 'nightly', kind 'audit' (the audit log itself). No admin route writes
-- either value: store.Audited refuses an actor that isn't browser, module
-- or import.
--
-- 00026: assigned for issue #112's page half.

-- +goose Up
ALTER TABLE audit_log DROP CONSTRAINT audit_log_via_check;
ALTER TABLE audit_log ADD CONSTRAINT audit_log_via_check
    CHECK (via IN ('browser', 'module', 'import', 'nightly'));
ALTER TABLE audit_log DROP CONSTRAINT audit_log_kind_check;
ALTER TABLE audit_log ADD CONSTRAINT audit_log_kind_check
    CHECK (kind IN ('system', 'talkgroup', 'unit', 'group', 'tag', 'attachment',
                    'recorder', 'retention', 'account', 'audit'));

-- +goose StatementBegin
-- The period in force, in days: the setting when it's a whole number from
-- 90 to 3,650 (retention.MinAuditDays to MaxAuditDays), else 730
-- (DefaultAuditDays). The API saves only such numbers; this is the one
-- reading of it, so the API, the prune and the trigger agree.
CREATE FUNCTION audit_log_retention_days() RETURNS integer LANGUAGE sql STABLE AS $$
    SELECT COALESCE((SELECT v FROM (SELECT CASE WHEN value ~ '^[0-9]{1,5}$' THEN value::integer END AS v
                                    FROM settings WHERE key = 'audit_retention_days') s
                     WHERE v BETWEEN 90 AND 3650), 730)
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Entries from before this are past the period.
CREATE FUNCTION audit_log_cutoff() RETURNS timestamptz LANGUAGE sql STABLE AS $$
    SELECT now() - make_interval(days => audit_log_retention_days())
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION audit_log_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF current_setting('radioarchive.audit_prune', true) = 'on' AND OLD.at < audit_log_cutoff() THEN
            RETURN OLD;
        END IF;
    END IF;
    RAISE EXCEPTION 'the audit log is append-only' USING ERRCODE = 'insufficient_privilege';
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The nightly run's prune: deletes the entries past the period and, when
-- it deleted any, writes one entry saying how many and from before when.
-- It answers what it did; deleted 0 wrote nothing.
CREATE FUNCTION audit_log_prune() RETURNS TABLE (deleted bigint, cutoff timestamptz, days integer)
LANGUAGE plpgsql AS $$
DECLARE
    d integer := audit_log_retention_days();
    c timestamptz := audit_log_cutoff();
    n bigint;
BEGIN
    PERFORM set_config('radioarchive.audit_prune', 'on', true);
    DELETE FROM audit_log WHERE at < c;
    GET DIAGNOSTICS n = ROW_COUNT;
    PERFORM set_config('radioarchive.audit_prune', '', true);
    IF n > 0 THEN
        INSERT INTO audit_log (issuer, subject, admin_name, admin_email, via, kind, thing, label, action, changes)
        VALUES ('', 'system', 'System', '', 'nightly', 'audit', '', 'Audit log', 'deleted',
                jsonb_build_array(
                    jsonb_build_object('field', 'entries', 'before', n, 'after', NULL),
                    jsonb_build_object('field', 'before', 'before',
                                       to_char(c AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), 'after', NULL),
                    jsonb_build_object('field', 'days', 'before', d, 'after', NULL)));
    END IF;
    RETURN QUERY SELECT n, c, d;
END
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION audit_log_prune();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION audit_log_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'the audit log is append-only' USING ERRCODE = 'insufficient_privilege';
END
$$;
-- +goose StatementEnd
DROP FUNCTION audit_log_cutoff();
DROP FUNCTION audit_log_retention_days();
ALTER TABLE audit_log DISABLE TRIGGER audit_log_no_change;
DELETE FROM audit_log WHERE via = 'nightly' OR kind = 'audit';
ALTER TABLE audit_log ENABLE TRIGGER audit_log_no_change;
ALTER TABLE audit_log DROP CONSTRAINT audit_log_kind_check;
ALTER TABLE audit_log ADD CONSTRAINT audit_log_kind_check
    CHECK (kind IN ('system', 'talkgroup', 'unit', 'group', 'tag', 'attachment',
                    'recorder', 'retention', 'account'));
ALTER TABLE audit_log DROP CONSTRAINT audit_log_via_check;
ALTER TABLE audit_log ADD CONSTRAINT audit_log_via_check
    CHECK (via IN ('browser', 'module', 'import'));
