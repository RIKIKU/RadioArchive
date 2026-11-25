-- Disabling and enabling an account reaches Authentik (ADR 0033, issue
-- #105; owner, 2026-11-25). The radio changes its own row first, in one
-- transaction with the account's sessions and the audit entry, and then
-- sets the user's is_active in Authentik through Authentik's API. If
-- Authentik can't be reached or refuses, the radio's change stands (a
-- disabled account is locked out of the radio at once) and the admin
-- repeats the request, which tries Authentik again.
--
-- authentik_pending says the radio's status hasn't reached Authentik yet:
-- set with every status change, cleared when Authentik has taken it. The
-- Accounts page shows it ("Disabled here, not yet in Authentik"). It holds
-- no address and nothing of Authentik's; false for every existing row,
-- which no admin has disabled or enabled before this.
--
-- 00027: assigned for issue #105.

-- +goose Up
ALTER TABLE accounts ADD COLUMN authentik_pending boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE accounts DROP COLUMN authentik_pending;
