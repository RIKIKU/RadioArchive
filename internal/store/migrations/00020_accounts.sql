-- Accounts: everyone who has finished a sign-in to the radio, with their
-- role and status (ADR 0025, owner, 2026-11-25). Roles and status live here,
-- in RadioArchive's own database, not in Authentik: an admin is an account
-- whose role is admin and whose status is active (SPEC.md R55, R111).
-- Nothing an Authentik token can change, group memberships included, makes
-- anyone a radio admin.
--
-- A row is keyed by the OIDC issuer and subject it signs in with, as the
-- account's profile (ADR 0021) and sessions (ADR 0022) are. Every finished
-- sign-in upserts its row: the name and email as Authentik gave them at
-- that sign-in, and when. No row is made for anyone who hasn't signed in
-- (P4), and a row holds no address or user agent.
--
-- The first admin is the account whose proven email matches
-- INITIAL_ADMIN_EMAIL, ignoring case, at a sign-in while no active admin
-- exists; the check and the promotion are one transaction, with the active
-- admins locked. Demoting (and, with issue #105, disabling) the last active
-- admin is refused in the same way.
--
-- Accounts already signed in (sessions kept before this migration) get
-- their row here, as listeners, so each live session still has one.
--
-- 00020: assigned for issue #73 (00018 is unused; 00019 is issue #110's).

-- +goose Up
CREATE TABLE accounts (
    issuer             text        NOT NULL CHECK (issuer <> ''),
    subject            text        NOT NULL CHECK (subject <> ''),
    name               text        NOT NULL DEFAULT '',
    email              text        NOT NULL DEFAULT '',
    role               text        NOT NULL DEFAULT 'listener' CHECK (role IN ('listener', 'admin')),
    status             text        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    first_signed_in_at timestamptz NOT NULL,
    last_signed_in_at  timestamptz NOT NULL,
    PRIMARY KEY (issuer, subject)
);
-- The active admins, which the first-admin check and the last-admin rule
-- lock.
CREATE INDEX accounts_active_admins_idx ON accounts (issuer, subject) WHERE role = 'admin' AND status = 'active';

INSERT INTO accounts (issuer, subject, name, email, first_signed_in_at, last_signed_in_at)
SELECT DISTINCT ON (issuer, subject) issuer, subject, name, email,
       min(created_at) OVER (PARTITION BY issuer, subject), max(created_at) OVER (PARTITION BY issuer, subject)
FROM listener_sessions
ORDER BY issuer, subject, created_at DESC;

-- +goose Down
DROP TABLE accounts;
