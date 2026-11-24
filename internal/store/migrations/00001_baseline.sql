-- The baseline. It creates nothing: it gives goose a first version, so the
-- API's start-up migrations run and record version 1 from the first build.
-- Later slices add their tables as 00002 onwards.

-- +goose Up
SELECT 1;

-- +goose Down
SELECT 1;
