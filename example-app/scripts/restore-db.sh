#!/usr/bin/env bash
# restore-db.sh <env> <file-backup.sql.gz> — restore PostgreSQL example-app.
# PERINGATAN: restore MENIMPA database env tujuan (dump dibuat dengan
# --clean --if-exists, jadi tabel lama di-drop dulu). Ada konfirmasi manual.
set -euo pipefail

ENV_NAME_ARG="${1:-}"
FILE="${2:-}"
case "$ENV_NAME_ARG" in
  prod)    PG="example-postgres" ;;
  staging) PG="example-staging-postgres" ;;
  dev)     PG="example-dev-postgres" ;;
  *) echo "Pakai: $0 <prod|staging|dev> <file.sql.gz>"; exit 1 ;;
esac
[ -f "$FILE" ] || { echo "File backup tidak ditemukan: $FILE"; exit 1; }
gzip -t "$FILE" || { echo "File bukan gzip valid: $FILE"; exit 1; }

echo "PERINGATAN: restore akan MENIMPA database exampledb di env '$ENV_NAME_ARG' (container $PG)."
read -r -p "Ketik 'restore-$ENV_NAME_ARG' untuk melanjutkan: " JAWAB
[ "$JAWAB" = "restore-$ENV_NAME_ARG" ] || { echo "Dibatalkan."; exit 1; }

echo "[$(date +%T)] Restore $FILE -> $PG ..."
gunzip -c "$FILE" | docker exec -i "$PG" psql -U example -d exampledb -v ON_ERROR_STOP=1 >/dev/null
echo "[$(date +%T)] Restore selesai. Verifikasi cepat:"
docker exec "$PG" psql -U example -d exampledb -c '\dt'
