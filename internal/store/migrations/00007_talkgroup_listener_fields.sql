-- What a listener needs to know about talkgroups (SPEC.md R22-R24, R42,
-- R77, R100): groups, delay, LED colour, alert, sort order and the
-- fireground channel number; the public talkgroups the catalogue reads; and
-- a generation number the catalogue's cache is checked against on every
-- request (R74). Issue #55; ADR 0006.

-- +goose Up

-- Alert and LED colour are stored as names (the colours become ArchiveUx
-- tokens, the sounds ship with the frontend). NULL takes the setting from
-- the talkgroup's tag, then its groups, then its system; 'none' stops there
-- with no alert or no colour (ADR 0006). internal/store lists the same
-- names (store.Alerts, store.LEDColours).

ALTER TABLE tags
    ADD COLUMN alert text CHECK (alert IN ('none', 'beep', 'chime', 'pip', 'tone')),
    ADD COLUMN led_colour text CHECK (led_colour IN ('none', 'red', 'orange', 'yellow', 'green',
                                                      'cyan', 'blue', 'purple', 'pink')),
    ADD COLUMN sort_order integer NOT NULL DEFAULT 0;

ALTER TABLE systems
    ADD COLUMN alert text CHECK (alert IN ('none', 'beep', 'chime', 'pip', 'tone')),
    ADD COLUMN led_colour text CHECK (led_colour IN ('none', 'red', 'orange', 'yellow', 'green',
                                                      'cyan', 'blue', 'purple', 'pink')),
    ADD COLUMN sort_order integer NOT NULL DEFAULT 0;

-- R22: whose or where a talkgroup is. A talkgroup is in any number of them.
CREATE TABLE groups (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       text NOT NULL UNIQUE CHECK (length(name) BETWEEN 1 AND 100),
    alert      text CHECK (alert IN ('none', 'beep', 'chime', 'pip', 'tone')),
    led_colour text CHECK (led_colour IN ('none', 'red', 'orange', 'yellow', 'green',
                                          'cyan', 'blue', 'purple', 'pink')),
    sort_order integer NOT NULL DEFAULT 0
);

CREATE TABLE talkgroup_groups (
    system_id    bigint NOT NULL,
    talkgroup_id integer NOT NULL,
    group_id     bigint NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    PRIMARY KEY (system_id, talkgroup_id, group_id),
    FOREIGN KEY (system_id, talkgroup_id) REFERENCES talkgroups (system_id, talkgroup_id) ON DELETE CASCADE
);

CREATE INDEX talkgroup_groups_group ON talkgroup_groups (group_id);

-- R22, R23, R100. The delay is the talkgroup's own, in whole minutes, up to
-- a day. The fireground channel number is a whole number, so leading zeros
-- don't count, and unique across the deployment, not per system.
ALTER TABLE talkgroups
    ADD COLUMN delay_minutes integer NOT NULL DEFAULT 0 CHECK (delay_minutes BETWEEN 0 AND 1440),
    ADD COLUMN alert text CHECK (alert IN ('none', 'beep', 'chime', 'pip', 'tone')),
    ADD COLUMN led_colour text CHECK (led_colour IN ('none', 'red', 'orange', 'yellow', 'green',
                                                      'cyan', 'blue', 'purple', 'pink')),
    ADD COLUMN sort_order integer NOT NULL DEFAULT 0,
    ADD COLUMN fireground_channel integer CHECK (fireground_channel >= 0);

CREATE UNIQUE INDEX talkgroups_fireground_channel ON talkgroups (fireground_channel)
    WHERE fireground_channel IS NOT NULL;

-- The talkgroups the public may know of, as public_calls is the calls
-- (ADR 0004): by call_state, the one rule for a state (ADR 0003), for a
-- talkgroup on its own. The catalogue and the fireground lookup read
-- talkgroups through this view and nothing else (internal/gate), so a
-- hidden or ignored talkgroup, and its name, label, groups, tag and
-- fireground channel number, aren't in them.
CREATE VIEW public_talkgroups AS
SELECT system_id, talkgroup_id, label, name, tag_id, delay_minutes, alert, led_colour,
       sort_order, fireground_channel
FROM talkgroups
WHERE call_state(system_id, talkgroup_id, '{}'::integer[]) = 'public';

-- The catalogue is held in the API's memory (R74). Any change to what it's
-- built from, by any writer (the admin API, an import, auto-create, psql),
-- adds one to this number at commit, and the gate reads it on every
-- catalogue request and rebuilds when it has moved, so hiding a talkgroup
-- takes effect at the next request, on every API server. A table, not a
-- sequence: a sequence moves before the commit, and a cache built then
-- would keep the old data under the new number.
CREATE TABLE catalogue_generation (
    one boolean PRIMARY KEY DEFAULT true CHECK (one),
    n   bigint NOT NULL
);
INSERT INTO catalogue_generation (one, n) VALUES (true, 1);

-- +goose StatementBegin
CREATE FUNCTION bump_catalogue_generation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    UPDATE catalogue_generation SET n = n + 1;
    RETURN NULL;
END
$$;
-- +goose StatementEnd

-- Deferred to the commit, so the generation's row is the last lock a
-- transaction takes: two writers can't deadlock over it.
CREATE CONSTRAINT TRIGGER talkgroups_catalogue AFTER INSERT OR UPDATE OR DELETE ON talkgroups
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION bump_catalogue_generation();
CREATE CONSTRAINT TRIGGER talkgroup_groups_catalogue AFTER INSERT OR UPDATE OR DELETE ON talkgroup_groups
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION bump_catalogue_generation();
CREATE CONSTRAINT TRIGGER groups_catalogue AFTER INSERT OR UPDATE OR DELETE ON groups
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION bump_catalogue_generation();
CREATE CONSTRAINT TRIGGER tags_catalogue AFTER INSERT OR UPDATE OR DELETE ON tags
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION bump_catalogue_generation();
CREATE CONSTRAINT TRIGGER systems_catalogue AFTER INSERT OR UPDATE OR DELETE ON systems
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION bump_catalogue_generation();

-- +goose Down
DROP TRIGGER systems_catalogue ON systems;
DROP TRIGGER tags_catalogue ON tags;
DROP TRIGGER groups_catalogue ON groups;
DROP TRIGGER talkgroup_groups_catalogue ON talkgroup_groups;
DROP TRIGGER talkgroups_catalogue ON talkgroups;
DROP FUNCTION bump_catalogue_generation();
DROP TABLE catalogue_generation;
DROP VIEW public_talkgroups;
DROP INDEX talkgroups_fireground_channel;
ALTER TABLE talkgroups
    DROP COLUMN fireground_channel, DROP COLUMN sort_order, DROP COLUMN led_colour,
    DROP COLUMN alert, DROP COLUMN delay_minutes;
DROP TABLE talkgroup_groups;
DROP TABLE groups;
ALTER TABLE systems DROP COLUMN sort_order, DROP COLUMN led_colour, DROP COLUMN alert;
ALTER TABLE tags DROP COLUMN sort_order, DROP COLUMN led_colour, DROP COLUMN alert;
