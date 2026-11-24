-- The recorder client version each recorder last connected with, and
-- whether the server counted it out of date: one minor version behind its
-- own (SPEC.md R6). For the Recorders page (R14). Issue #36.
--
-- 00004, not 00003: issue #34 (systems and attachments) holds 00003.

-- +goose Up
ALTER TABLE recorders
    ADD COLUMN client_version     text CHECK (length(client_version) <= 100),
    ADD COLUMN client_out_of_date boolean NOT NULL DEFAULT false,
    ADD COLUMN client_seen_at     timestamptz;

-- +goose Down
ALTER TABLE recorders
    DROP COLUMN client_seen_at,
    DROP COLUMN client_out_of_date,
    DROP COLUMN client_version;
