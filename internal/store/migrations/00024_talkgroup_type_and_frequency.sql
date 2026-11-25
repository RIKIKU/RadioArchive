-- A talkgroup's type and frequency (SPEC.md R22), the last of R22's fields
-- the database didn't hold, so the setup export carries the whole setup
-- and an import into an empty deployment rebuilds it (R52, R53, A24;
-- issue #115, ADR 0030).
--
-- type is what the setup says the talkgroup is, as trunk-recorder's
-- talkgroup file's mode column does (D, T, E, ...), or '' for nothing
-- said. It's kept and shown, not interpreted: v1 is P25 trunked only
-- (section 4), and the import skips rows whose type says analog or
-- conventional before they get here. frequency is in hertz, or NULL.
-- Neither is a listener's: the catalogue and the gate don't read them.
--
-- 00024: assigned for issue #115.

-- +goose Up
ALTER TABLE talkgroups
    ADD COLUMN type text NOT NULL DEFAULT '' CHECK (length(type) <= 32),
    ADD COLUMN frequency bigint CHECK (frequency > 0);

-- +goose Down
ALTER TABLE talkgroups DROP COLUMN frequency, DROP COLUMN type;
