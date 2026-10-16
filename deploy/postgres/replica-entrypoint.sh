#!/bin/bash
# Starts a streaming-replication hot standby of the primary.
# On first start it clones the primary with pg_basebackup; afterwards it simply
# resumes streaming from the existing data directory.
set -euo pipefail

PGDATA="${PGDATA:-/var/lib/postgresql/data}"
PRIMARY_PORT="${PRIMARY_PORT:-5432}"

if [ ! -s "$PGDATA/PG_VERSION" ]; then
  echo "replica: waiting for primary at ${PRIMARY_HOST}..."
  until pg_isready -h "$PRIMARY_HOST" -p "$PRIMARY_PORT" -U "$POSTGRES_USER" -d "$POSTGRES_DB" >/dev/null 2>&1; do
    sleep 1
  done

  # The running standby re-authenticates to the primary on every reconnect, so
  # the password must live in ~/.pgpass, not only in this shell's environment.
  PGPASS_FILE="${HOME:-/var/lib/postgresql}/.pgpass"
  echo "${PRIMARY_HOST}:${PRIMARY_PORT}:*:replicator:${POSTGRES_REPLICATION_PASSWORD}" > "$PGPASS_FILE"
  chmod 600 "$PGPASS_FILE"

  echo "replica: cloning primary with pg_basebackup"
  rm -rf "${PGDATA:?}"/*
  PGPASSWORD="$POSTGRES_REPLICATION_PASSWORD" pg_basebackup \
    -h "$PRIMARY_HOST" -p "$PRIMARY_PORT" -U replicator \
    -D "$PGDATA" -Fp -Xs -P -R
  chmod 700 "$PGDATA"
fi

exec postgres -c hot_standby=on
