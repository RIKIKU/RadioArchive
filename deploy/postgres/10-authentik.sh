#!/bin/sh
# Creates Authentik's role and database in RadioArchive's PostgreSQL
# container (SPEC.md section 5, ADR 0009), as PagerArchive's does. Runs
# once, when the data volume is new. A development database made before
# this script existed has neither: drop its volume once
# (docker compose down --volumes).
set -eu
psql -v ON_ERROR_STOP=1 -v pw="$AUTHENTIK_DB_PASSWORD" --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<'SQL'
CREATE ROLE authentik LOGIN PASSWORD :'pw';
CREATE DATABASE authentik OWNER authentik;
SQL
