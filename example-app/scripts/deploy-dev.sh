#!/usr/bin/env bash
# deploy-dev.sh — CD pull-based (GitOps-lite) KHUSUS env DEV.
# Diinstall di server pada ~/deploy/deploy-dev.sh dan dijalankan cron tiap
# 1 menit. Cara kerja:
#   1. git fetch clone repo; bandingkan commit origin/main dengan penanda
#      terakhir yang ter-deploy (~/deploy/deployed-dev.commit).
#   2. Bila ada commit baru DAN menyentuh example-app/** -> salin source ke
#      ~/example-app-dev (secrets.env TIDAK disentuh), build + up -d env dev.
#   3. Catat hasil ke ~/deploy/deploy.log + perbarui penanda.
# Env staging & prod TIDAK disentuh script ini — promosinya manual (lihat
# tutorial-deploy-cicd.md), meniru pola kantor: CI/CD otomatis hanya di dev.
set -uo pipefail

CLONE="$HOME/deploy/devops-clone"
MARKER="$HOME/deploy/deployed-dev.commit"
LOG="$HOME/deploy/deploy.log"
DEV_DIR="$HOME/example-app-dev"

log() { echo "[$(date '+%F %T')] $*" | tee -a "$LOG"; }

cd "$CLONE" || { log "GAGAL: clone tidak ada di $CLONE"; exit 1; }
git fetch -q origin main || { log "GAGAL: git fetch"; exit 1; }
NEW="$(git rev-parse origin/main)"
OLD="$(cat "$MARKER" 2>/dev/null || echo none)"
[ "$NEW" = "$OLD" ] && exit 0

CHANGED="$(git diff --name-only "$OLD" "$NEW" 2>/dev/null || echo 'example-app/')"
if ! echo "$CHANGED" | grep -q '^example-app/'; then
  log "commit $NEW tidak menyentuh example-app/** — tidak ada deploy (penanda diperbarui)"
  echo "$NEW" > "$MARKER"
  exit 0
fi

log "commit baru $NEW menyentuh example-app/** — deploy env DEV..."
git reset -q --hard origin/main
# secrets.env & app.env di folder env TIDAK dihapus rsync; app.env sesudahnya
# diperbarui dari env/dev.env milik repo (sumber kebenaran konfigurasi dev).
rsync -a --delete --exclude 'secrets.env' --exclude 'app.env' "$CLONE/example-app/" "$DEV_DIR/"
cp "$DEV_DIR/env/dev.env" "$DEV_DIR/app.env"
cd "$DEV_DIR"
if docker compose --env-file app.env --env-file secrets.env up -d --build >>"$LOG" 2>&1; then
  echo "$NEW" > "$MARKER"
  log "deploy DEV sukses ke commit $NEW"
else
  log "deploy DEV GAGAL di commit $NEW — cek log di atas baris ini"
  exit 1
fi
