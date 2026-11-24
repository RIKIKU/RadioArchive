-- Units and their aliases (SPEC.md R26, R27): a radio ID, or a span of
-- them, named in its system. Issue #83; ADR 0019.

-- +goose Up

-- A unit is a radio ID with an alias in its system (R26). first_id and
-- last_id are the same for one radio; a range has last_id above first_id
-- and names every radio in it. An exact unit beats a range that covers it,
-- and of two ranges that cover a radio the narrower wins (ADR 0019), so
-- ranges may overlap and nothing has to be checked across rows. An empty
-- alias is a unit with none: listeners see its ID (R27), even inside a
-- range. No ";": index.csv separates a call's aliases with it (R65).
CREATE TABLE units (
    system_id  bigint NOT NULL REFERENCES systems (id) ON DELETE CASCADE,
    first_id   bigint NOT NULL CHECK (first_id > 0),
    last_id    bigint NOT NULL,
    alias      text NOT NULL DEFAULT '' CHECK (length(alias) <= 100 AND strpos(alias, ';') = 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (system_id, first_id, last_id),
    CHECK (last_id >= first_id)
);

-- The gate holds the aliases in memory with the catalogue (ADR 0019), so a
-- change to a unit moves the catalogue's generation, as a change to a
-- talkgroup does (migration 00007), and the next request sees it.
CREATE CONSTRAINT TRIGGER units_catalogue AFTER INSERT OR UPDATE OR DELETE ON units
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION bump_catalogue_generation();

-- +goose Down
DROP TRIGGER units_catalogue ON units;
DROP TABLE units;
