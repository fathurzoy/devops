#!/usr/bin/env bash
# rolling-update.sh <versi-baru> — BLUE-GREEN zero-downtime untuk order-service
# di env DEV. Prinsipnya:
#   1. Versi BARU (green) dinyalakan DULU di samping versi lama (blue).
#   2. Green di-health-check sampai terbukti sehat & versinya benar.
#   3. Trafik dialihkan bertahap: blue -> blue+green -> green.
#   4. Blue hanya dimatikan SETELAH green melayani trafik sendirian.
#   5. Terakhir green "dipromosikan" menjadi blue (APP_VERSION di app.env
#      diperbarui), supaya kondisi akhir konsisten dengan konfigurasi.
# Selama seluruh proses, sebuah checker memukul /api/orders/whoami lewat
# gateway terus-menerus — TIDAK BOLEH ada satu pun respons non-200.
#
# Rollback kapan pun sebelum langkah 5: arahkan upstream kembali ke blue
# (POST /admin/upstreams {"upstreams":["http://<prefix-env>order-service:8080"]}).
set -uo pipefail

TARGET="${1:-}"
[ -n "$TARGET" ] || { echo "Pakai: $0 <versi-baru>  (contoh: $0 v1.1.0)"; exit 1; }

DEV_DIR="$HOME/example-app-dev"
cd "$DEV_DIR"
COMPOSE="docker compose --env-file app.env --env-file secrets.env"
TOKEN="$(grep -E '^GATEWAY_ADMIN_TOKEN=' secrets.env | cut -d= -f2-)"
PREFIX="$(grep -E '^NAME_PREFIX=' app.env | cut -d= -f2-)"
GW="http://127.0.0.1:18100"
# Alamat upstream WAJIB memakai nama unik per env (container name / alias unik),
# jangan nama service polos: nama polos bertabrakan antar env di jaringan bersama.
BLUE="http://${PREFIX}order-service:8080"
GREEN="http://${PREFIX}order-green:8080"
log() { echo "[$(date +%T)] $*"; }

# --- Checker latar: catat semua kode status selama proses ---
CHECK_FILE="$(mktemp)"
( while true; do
    code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 "$GW/api/orders/whoami" 2>/dev/null || echo 000)"
    echo "$code" >> "$CHECK_FILE"
    sleep 0.5
  done ) &
CHECK_PID=$!
trap 'kill $CHECK_PID 2>/dev/null' EXIT

set_upstreams() {
  curl -s -X POST "$GW/admin/upstreams" -H "X-Admin-Token: $TOKEN" -H 'Content-Type: application/json' \
    -d "{\"upstreams\":[$1]}" >/dev/null
}
probe_version() { # $1=url -> versi yang menjawab (kosong bila gagal)
  curl -s "$GW/admin/probe?url=$1" -H "X-Admin-Token: $TOKEN" | grep -o '"version":"[^"]*"' | head -1 | cut -d'"' -f4
}

log "=== BLUE-GREEN order-service DEV -> $TARGET ==="
log "Upstream awal: $(curl -s "$GW/admin/upstreams" -H "X-Admin-Token: $TOKEN")"

log "Fase 1: menyalakan GREEN versi $TARGET di samping blue..."
GREEN_APP_VERSION="$TARGET" $COMPOSE --profile bluegreen up -d order-green

log "Fase 2: health-check green sampai sehat & versi $TARGET..."
ok=""
for i in $(seq 1 40); do
  v="$(probe_version "$GREEN")"
  if [ "$v" = "$TARGET" ]; then ok=1; log "  green sehat, versi $v (percobaan $i)"; break; fi
  sleep 2
done
[ -n "$ok" ] || { log "GAGAL: green tidak sehat — proses dibatalkan, blue tetap melayani."; exit 1; }

log "Fase 3: trafik dibagi blue+green (transisi)..."
set_upstreams "\"$BLUE\",\"$GREEN\""
sleep 4

log "Fase 4: trafik 100% ke green..."
set_upstreams "\"$GREEN\""
sleep 4

log "Fase 5: mematikan blue (versi lama) — green sudah terbukti melayani..."
$COMPOSE stop order-service
sleep 3

log "Fase 6: promosi green menjadi blue (APP_VERSION app.env -> $TARGET)..."
sed -i "s/^APP_VERSION=.*/APP_VERSION=$TARGET/" app.env
$COMPOSE up -d order-service
ok=""
for i in $(seq 1 40); do
  v="$(probe_version "$BLUE")"
  if [ "$v" = "$TARGET" ]; then ok=1; log "  blue baru sehat, versi $v (percobaan $i)"; break; fi
  sleep 2
done
[ -n "$ok" ] || { log "GAGAL di fase promosi — green masih aktif melayani, periksa manual."; exit 1; }
set_upstreams "\"$BLUE\",\"$GREEN\""
sleep 3
set_upstreams "\"$BLUE\""
$COMPOSE --profile bluegreen rm -sf order-green >/dev/null 2>&1

kill $CHECK_PID 2>/dev/null; trap - EXIT
TOTAL="$(wc -l < "$CHECK_FILE")"
GAGAL="$(grep -cv '^200$' "$CHECK_FILE" || true)"
log "=== SELESAI ==="
log "Checker: $TOTAL request ke /api/orders/whoami selama proses, gagal/non-200: $GAGAL"
log "Sampel jawaban akhir:"
for i in 1 2 3; do curl -s "$GW/api/orders/whoami"; echo; done
rm -f "$CHECK_FILE"
if [ "$GAGAL" -gt 0 ]; then echo "ADA REQUEST GAGAL — zero-downtime TIDAK tercapai"; exit 1; fi
log "Zero-downtime TERCAPAI: tidak ada request gagal selama rolling update ke $TARGET."
