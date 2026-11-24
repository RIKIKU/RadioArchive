-- Signed-in listeners' sessions (ADR 0022, superseding the sealed session
-- cookie of ADR 0009). The browser holds only a random 256-bit session ID
-- in its host-only cookie; this table holds the ID's SHA-256, never the ID
-- itself, so a copy of the table signs nobody in. Every account request
-- looks its session up here, so deleting a row ends that session at once:
-- signing out deletes the browser's row, and ending an account's sessions
-- (an admin disabling a listener, M4) deletes every row of that account.
--
-- A row is the account it signed in as (the OIDC issuer and subject its
-- profile is keyed by, ADR 0021), the name and email the account menu
-- shows, and the ID token Authentik issued, kept only for the sign-out hop
-- to Authentik's end-session endpoint (id_token_hint). The ID token is
-- never in a cookie or in an answer the browser's script can read, and
-- never logged. A session lasts 30 days from its last use, renewed at most
-- hourly; expired rows are deleted hourly. Nothing is ever stored for a
-- listener who hasn't signed in (P4): only a finished sign-in adds a row.
--
-- 00017: 00016 is another issue's.

-- +goose Up
CREATE TABLE listener_sessions (
    id_hash      bytea       PRIMARY KEY CHECK (length(id_hash) = 32),
    issuer       text        NOT NULL CHECK (issuer <> ''),
    subject      text        NOT NULL CHECK (subject <> ''),
    name         text        NOT NULL DEFAULT '',
    email        text        NOT NULL DEFAULT '',
    id_token     text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz NOT NULL,
    expires_at   timestamptz NOT NULL
);
-- Ending every session of one account, and the hourly sweep.
CREATE INDEX listener_sessions_account_idx ON listener_sessions (issuer, subject);
CREATE INDEX listener_sessions_expires_idx ON listener_sessions (expires_at);

-- +goose Down
DROP TABLE listener_sessions;
