#!/usr/bin/env bash
# setup-telegram-contact-point.sh — memastikan contact point Telegram +
# notification policy Grafana ada & benar, lewat API Grafana (idempoten).
#
# KENAPA LEWAT API, BUKAN FILE PROVISIONING?
# Di Grafana 13.2.4, provisioning file untuk contact point Telegram tidak
# bisa diandalkan: validasinya gagal dengan "telegram: could not find Bot
# Token in settings" (token dari $__env{} maupun $__file{} sama-sama
# ditolak pada tahap pemetaan) dan pada beberapa boot Grafana bahkan masuk
# restart-loop karenanya. Alert RULES tetap di-provision dari file
# (provisioning/alerting/rules.yaml — tidak ada secret di sana, aman);
# khusus contact point + policy yang membawa secret dipakai jalur API
# resmi ini. Contact point yang dibuat script ini ber-provenance "api"
# (header X-Disable-Provenance: true) sehingga bisa juga dikelola dari UI.
#
# Token & chat ID dibaca dari .env platform (diisi user sendiri di server).
# Script ini TIDAK PERNAH mencetak nilai token/chat id.
#
# Pakai (di server, Grafana harus sudah jalan):
#   bash platform/scripts/setup-telegram-contact-point.sh
set -euo pipefail
cd "$(dirname "$0")/.."

export GRAFANA_URL="${GRAFANA_URL:-http://127.0.0.1:3100}"

python3 - <<'PY'
import base64, json, os, sys, urllib.request, urllib.error

env = {}
for line in open('.env'):
    line = line.strip()
    if '=' in line and not line.startswith('#'):
        k, v = line.split('=', 1)
        env[k] = v

token = env.get('TELEGRAM_BOT_TOKEN', '')
chat = env.get('TELEGRAM_CHAT_ID', '')
admin_pw = env.get('GRAFANA_ADMIN_PASSWORD', '')
if not token or not chat:
    sys.exit('TELEGRAM_BOT_TOKEN / TELEGRAM_CHAT_ID masih kosong di .env — isi dulu.')
if not admin_pw:
    sys.exit('GRAFANA_ADMIN_PASSWORD tidak ada di .env')

base = os.environ.get('GRAFANA_URL', 'http://127.0.0.1:3100')
auth = base64.b64encode(('admin:' + admin_pw).encode()).decode()

def call(method, path, payload=None):
    req = urllib.request.Request(base + path, method=method,
        data=json.dumps(payload).encode() if payload is not None else None,
        headers={'Authorization': 'Basic ' + auth, 'Content-Type': 'application/json',
                 'X-Disable-Provenance': 'true'})
    try:
        with urllib.request.urlopen(req, timeout=15) as r:
            body = r.read().decode()
            return r.status, json.loads(body) if body else {}
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()[:300]

# Bentuk contact point di API provisioning Grafana itu FLAT (satu receiver
# per contact point): {uid, name, type, settings, secureSettings}.
st, cps = call('GET', '/api/v1/provisioning/contact-points')
existing = None
if st == 200 and isinstance(cps, list):
    existing = next((c for c in cps if c.get('name') == 'telegram'), None)
if existing:
    print('contact point telegram SUDAH ADA (uid:', existing.get('uid'),
          '| type:', existing.get('type'), '| provenance:', existing.get('provenance'), ') — tidak diubah.')
else:
    payload = {
        'name': 'telegram',
        'type': 'telegram',
        'disableResolveMessage': False,
        'settings': {'chatid': chat, 'parse_mode': 'None'},
        'secureSettings': {'bottoken': token},
    }
    st, resp = call('POST', '/api/v1/provisioning/contact-points', payload)
    print('contact point telegram dibuat (POST):', st, str(resp)[:160] if st >= 300 else '')

policy = {
    'receiver': 'telegram',
    'group_by': ['grafana_folder', 'alertname', 'env'],
    'group_wait': '30s',
    'group_interval': '5m',
    'repeat_interval': '4h',
}
st, resp = call('PUT', '/api/v1/provisioning/policies', policy)
print('notification policy -> telegram:', st, str(resp)[:120] if st >= 300 else '')

st, pol = call('GET', '/api/v1/provisioning/policies')
print('verifikasi policy receiver:', pol.get('receiver') if isinstance(pol, dict) else pol)
PY
echo "Selesai. Bukti pengiriman: jalankan chaos test di FE dev — alert firing akan mengirim pesan ke Telegram."
