-- What the server keeps of a recorder for its signals (SPEC.md R14): when
-- it last delivered a call, and its quiet threshold in minutes, which is
-- off (NULL) by default. Whether it's connected or deaf is the API's, in
-- memory, as the state of its socket (ADR 0010). Issue #57.
--
-- 00011: issue #61 holds 00009 and 00010.

-- +goose Up
ALTER TABLE recorders
    ADD COLUMN last_call_at        timestamptz,
    ADD COLUMN quiet_after_minutes integer CHECK (quiet_after_minutes BETWEEN 1 AND 10080);

-- +goose Down
ALTER TABLE recorders
    DROP COLUMN quiet_after_minutes,
    DROP COLUMN last_call_at;
