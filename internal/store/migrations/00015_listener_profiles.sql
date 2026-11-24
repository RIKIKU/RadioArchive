-- Signed-in listeners' saved profiles (SPEC.md R41, R64, R110): the
-- listening set with its priority marks, the playback settings, the look
-- and the download basket with its conversations, one row an account.
-- Authentik keeps only who the listener is; the row is keyed by the OIDC
-- issuer and subject the account signs in with (ADR 0021, superseding where
-- ADR 0009 kept it). Nothing is ever stored for a listener who hasn't
-- signed in (P4), and Clear saved settings deletes the row.
--
-- The profile is the server's own JSON (internal/account, Profile), checked
-- and size-limited before it is written; jsonb keeps it readable in a
-- backup. updated_at is when the listener last saved: an account deleted in
-- Authentik leaves its row, which this tells apart from one in use.
--
-- 00015: 00013 and 00014 are other issues'.

-- +goose Up
CREATE TABLE listener_profiles (
    issuer     text        NOT NULL CHECK (issuer <> ''),
    subject    text        NOT NULL CHECK (subject <> ''),
    profile    jsonb       NOT NULL CHECK (jsonb_typeof(profile) = 'object'),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (issuer, subject)
);

-- +goose Down
DROP TABLE listener_profiles;
