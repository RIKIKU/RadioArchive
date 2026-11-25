-- The audit log (SPEC.md R12, R54; issue #112, ADR 0029): every admin
-- change, written in the same transaction as the change by one store path
-- (store.Audited), so a change can't commit without its entry and an entry
-- can't exist without its change.
--
-- Who: the admin's account by issuer and subject, with their name and email
-- as they were at the change, so an entry still reads after the account
-- changes or goes. No foreign key to accounts, for the same reason.
--
-- How: browser (a session cookie), module (a bearer token, issue #116) or
-- import (the import route, issue #115). It comes from how the request was
-- signed in, never from anything the client says.
--
-- What: the kind of thing, which one (thing, a stable key: "1" for system
-- 1, "1/1001" for its talkgroup 1001, "Bench/ex" for an attachment), the
-- name it went by (label), and each changed field before and after, in the
-- admin API's field order. A creation keeps every field after, a deletion
-- every field before.
--
-- Kept a long time (R54): retention never touches it, and the table refuses
-- UPDATE, DELETE and TRUNCATE, so nothing in the API can rewrite history.
-- It holds names because only admins read it; it isn't telemetry (R90,
-- A32): logs, metrics and traces still never hold them. Reads and playback
-- aren't in it (R34). Anonymous use never adds a row (P4).
--
-- 00023: assigned for issue #112.

-- +goose Up
CREATE TABLE audit_log (
    id          bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    at          timestamptz NOT NULL DEFAULT clock_timestamp(),
    issuer      text        NOT NULL,
    subject     text        NOT NULL CHECK (subject <> ''),
    admin_name  text        NOT NULL DEFAULT '',
    admin_email text        NOT NULL DEFAULT '',
    via         text        NOT NULL CHECK (via IN ('browser', 'module', 'import')),
    kind        text        NOT NULL CHECK (kind IN ('system', 'talkgroup', 'unit', 'group', 'tag', 'attachment',
                                                     'recorder', 'retention', 'account')),
    thing       text        NOT NULL,
    label       text        NOT NULL DEFAULT '',
    action      text        NOT NULL CHECK (action IN ('created', 'changed', 'deleted')),
    -- [{"field": "label", "before": "DISP N", "after": "DISP North"}, ...]
    changes     jsonb       NOT NULL CHECK (jsonb_typeof(changes) = 'array')
);
-- Newest first, paged by (at, id); and the same filtered by who or by thing.
CREATE INDEX audit_log_at_idx ON audit_log (at DESC, id DESC);
CREATE INDEX audit_log_who_idx ON audit_log (subject, at DESC, id DESC);
CREATE INDEX audit_log_thing_idx ON audit_log (kind, thing, at DESC, id DESC);

-- +goose StatementBegin
CREATE FUNCTION audit_log_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'the audit log is append-only' USING ERRCODE = 'insufficient_privilege';
END
$$;
-- +goose StatementEnd
CREATE TRIGGER audit_log_no_change BEFORE UPDATE OR DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION audit_log_append_only();
CREATE TRIGGER audit_log_no_truncate BEFORE TRUNCATE ON audit_log
    FOR EACH STATEMENT EXECUTE FUNCTION audit_log_append_only();

-- +goose Down
DROP TABLE audit_log;
DROP FUNCTION audit_log_append_only();
