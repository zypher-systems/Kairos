#!/usr/bin/env bash
# Kairos restore from a backup tarball written by scripts/backup.sh.
# WARNING: overwrites current database and file library.
# Usage: bash scripts/restore.sh <backup.tar.gz>
set -euo pipefail

ARCHIVE="${1:?usage: restore.sh <backup.tar.gz>}"
PROJECT="${COMPOSE_PROJECT_NAME:-kairos}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

tar -xzf "$ARCHIVE" -C "$WORK"
[ -f "$WORK/kairos.dump" ] || { echo "no kairos.dump in archive" >&2; exit 1; }

echo ">> restoring postgres (drops and recreates the kairos database)"
docker compose -p "$PROJECT" exec -T postgres \
  psql -U kairos -d postgres -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname='kairos' AND pid <> pg_backend_pid();" >/dev/null
docker compose -p "$PROJECT" exec -T postgres \
  psql -U kairos -d postgres -c "DROP DATABASE IF EXISTS kairos; CREATE DATABASE kairos;" >/dev/null
docker compose -p "$PROJECT" exec -T -i postgres \
  pg_restore -U kairos -d kairos --no-owner < "$WORK/kairos.dump"

if [ -f "$WORK/files.tar.gz" ]; then
  echo ">> restoring file library"
  docker compose -p "$PROJECT" exec -T -i server \
    sh -c 'rm -rf /var/lib/kairos/files && mkdir -p /var/lib/kairos && tar -xzf - -C /var/lib/kairos' < "$WORK/files.tar.gz"
fi

echo ">> restart the server to re-read state: docker compose restart server"
