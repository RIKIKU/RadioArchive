-- name: Ping :one
-- The health check's query: a round trip to the database.
SELECT 1::int AS ok;
