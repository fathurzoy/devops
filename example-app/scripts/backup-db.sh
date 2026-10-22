#!/usr/bin/env bash
# backup-db.sh <env> — backup PostgreSQL example-app per environment.
# env: prod | staging | dev
# Hasil: ~/backups/example/<env>/backup-<timestamp>.sql.gz
set -euo pipefail

ENV_NAME_ARG="${1:-}"
case "$ENV_NAME_ARG" in
  prod)    DIR="$HOME/example-app";         PG="example-postgres" ;;
  staging) DIR="$HOME/example-app-staging"; PG="example-staging-postgres" ;;
  dev)     DIR="$HOME/example-app-dev";     PG="example-dev-postgres" ;;
  *) echo "Pakai: $0 <prod|staging|dev>"; exit 1 ;;
esac

[ -d "$DIR" ] || { echo "Folder env tidak ada: $DIR"; exit 1; }
docker ps --format '{{.Names}}' | grep -qx "$PG" || { echo "Container $PG tidak jalan"; exit 1; }

TS="$(date +%Y%m%d-%H%M%S)"
OUT_DIR="$HOME/backups/example/$ENV_NAME_ARG"
mkdir -p "$OUT_DIR"
OUT="$OUT_DIR/backup-$TS.sql.gz"

echo "[$(date +%T)] Backup $ENV_NAME_ARG dari container $PG ..."
docker exec "$PG" pg_dump -U example -d exampledb --clean --if-exists | gzip > "$OUT"
gzip -t "$OUT"
echo "[$(date +%T)] Selesai: $OUT ($(du -h "$OUT" | cut -f1))"
