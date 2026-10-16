#!/bin/bash
# Runs once, on first start of the primary (docker-entrypoint-initdb.d).
# Creates the replication role and allows it to connect from the Docker network.
set -euo pipefail

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<SQL
CREATE ROLE replicator WITH REPLICATION LOGIN PASSWORD '${POSTGRES_REPLICATION_PASSWORD}';
SQL

echo "host replication replicator all scram-sha-256" >> "$PGDATA/pg_hba.conf"
