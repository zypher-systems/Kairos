#!/usr/bin/env bash
# Kairos backup: Postgres dump + file library + manifest -> timestamped tarball.
# Usage: bash scripts/backup.sh [output-dir]
set -euo pipefail

OUT_DIR="${1:-.}"
PROJECT="${COMPOSE_PROJECT_NAME:-kairos}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

echo ">> dumping postgres"
docker compose -p "$PROJECT" exec -T postgres \
  pg_dump -U kairos -Fc kairos > "$WORK/kairos.dump"

echo ">> archiving file library"
docker compose -p "$PROJECT" exec -T server \
  tar -czf - -C /var/lib/kairos files 2>/dev/null > "$WORK/files.tar.gz" || echo "(empty library)" > "$WORK/files.EMPTY"

cat > "$WORK/manifest.json" <<EOF
{
  "app": "kairos",
  "created_at": "$STAMP",
  "components": ["postgres", "files"],
  "note": "Restore with scripts/restore.sh. Job/SSH secrets stay decryptable only with the same KAIROS_SECRET."
}
EOF

OUT="$OUT_DIR/kairos-backup-$STAMP.tar.gz"
tar -czf "$OUT" -C "$WORK" .
echo ">> backup written: $OUT"
