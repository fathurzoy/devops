# Tutorial: Environment Development, Staging, Production

Kenapa aplikasi dibagi menjadi beberapa *environment* (lingkungan)? Supaya
kita bisa mencoba-coba dan merusak sesuatu di tempat yang aman, tanpa
mengguncang aplikasi yang dipakai orang beneran. Di setup kita, example app
sekarang punya **tiga environment** yang hidup berdampingan di satu VPS yang
sama:

| | development (dev) | staging | production (prod) |
|---|---|---|---|
| Buat apa | Coba-coba, oprek, CI/CD otomatis | Uji terakhir sebelum naik prod — salinan mirip prod | Yang dipakai "beneran" |
| Folder di VPS | `~/example-app-dev` | `~/example-app-staging` | `~/example-app` |
| Compose project | `example-app-dev` | `example-app-staging` | `example-app` |
| Prefix container | `example-dev-` | `example-staging-` | `example-` |
| Nilai `ENV_NAME` | `development` | `staging` | `production` |
| FE1 | http://43.167.194.197:3400 | http://43.167.194.197:3300 | http://43.167.194.197:3200 |
| FE2 | http://43.167.194.197:3401 | http://43.167.194.197:3301 | http://43.167.194.197:3201 |
| API Gateway | :18100 | :18090 | :18080 |
| RabbitMQ Management | :15693 | :15683 | :15673 |
| PostgreSQL (hanya lokal server) | 127.0.0.1:15452 | 127.0.0.1:15442 | 127.0.0.1:15432 |
| Redis (hanya lokal server) | 127.0.0.1:16399 | 127.0.0.1:16389 | 127.0.0.1:16379 |
| Interval scheduler | 20 detik | 30 detik | 60 detik |
| Replica order-service | 1 | 1 | 2 (HA) |

**Setiap env punya PostgreSQL, Redis, dan RabbitMQ sendiri-sendiri** (volume
dan password berbeda). Data dev tidak akan pernah bocor ke prod, dan
sebaliknya.

```text
                ┌─ dev ────── FE :3400 ─ gateway :18100 ─ services ─ PG/Redis/MQ sendiri
satu VPS ───────┼─ staging ── FE :3300 ─ gateway :18090 ─ services ─ PG/Redis/MQ sendiri
                └─ prod ───── FE :3200 ─ gateway :18080 ─ services ─ PG/Redis/MQ sendiri
```

## Satu File Compose untuk Semua Env

Kuncinya ada di file `example-app/docker-compose.yml`: file ini **sama** untuk
semua env. Yang membedakan hanyalah dua file variabel saat menjalankan:

```bash
docker compose --env-file app.env --env-file secrets.env up -d
```

- **`app.env`** — konfigurasi tidak rahasia: nama project, port, `ENV_NAME`,
  `APP_VERSION`, interval scheduler. File ini **masuk Git** (tersimpan di repo
  sebagai `example-app/env/dev.env`, `staging.env`, `prod.env`), supaya
  konfigurasi tiap env terdokumentasi dan bisa ditinjau.
- **`secrets.env`** — password PostgreSQL, password RabbitMQ, token admin
  gateway. File ini **hanya ada di server** (chmod 600) dan **tidak pernah
  masuk Git**.

Contoh isi `app.env` dev (perhatikan yang berubah antar env cuma nilai di
sebelah kanan):

```ini
PROJECT_NAME=example-app-dev
NAME_PREFIX=example-dev-
ENV_NAME=development
APP_VERSION=v1.0.0
FE1_PORT=3400
GW_PORT=18100
SCHED_INTERVAL_SEC=20
```

## Grouping per Env: Cara Melihatnya di Mana-mana

### 1. Portainer

Container diberi nama berawalan per env (`example-dev-...`,
`example-staging-...`, `example-...`) dan Compose project terpisah.

- Menu **Containers** → ketik `example-dev` di kotak filter untuk melihat
  hanya container dev. Begitu juga `example-staging`.
- Menu **Stacks** menampilkan tiga stack example app yang terpisah — satu per
  env. Inilah "grouping" versi Docker Compose (di Kubernetes nanti padanannya
  adalah *namespace*).

### 2. Log JSON & Loki

Setiap baris log semua service sekarang memuat field `"env"`, contohnya:

```json
{"time":"...","level":"info","service":"order-service","env":"development","trace_id":"...","message":"request selesai","status":201}
```

Di Grafana → Explore → Loki, log juga otomatis ter-group dari label Docker:

```logql
{compose_project="example-app-dev"}                          # semua log env dev
{compose_project="example-app-staging"} |= "error"           # error di staging saja
{container="example-postgres"}                               # database prod
{compose_project=~"example-app.*"} |= "<trace_id>"            # telusuri 1 request
```

### 3. Prometheus & Grafana (label `env`)

Setiap service menulis metrik `http_requests_total` dengan label `env`, dan
Prometheus menambahkan label `env` yang sama dari konfigurasi job scrape-nya
(`example` = production, `example-staging` = staging, `example-dev` =
development). Contoh PromQL di Grafana → Explore → Prometheus:

```promql
sum by (env) (rate(http_requests_total[5m]))                       # traffic per env
sum by (env, service) (rate(http_requests_total{env="development"}[5m]))
sum by (env) (rate(http_requests_total{status=~"5.."}[5m]))         # error per env
```

Sudah disediakan dashboard **“Example App per Environment”** di Grafana yang
membandingkan request rate ketiga env dalam satu layar.

> ⚠️ **Peringatan isolasi (pelajaran nyata dari pembangunan setup ini):**
> jangan pernah memanggil service dengan nama polosnya (`user-service`,
> `order-service`) dari container yang juga terhubung ke jaringan bersama
> `platform-net`. Nama polos itu alias DNS di semua project yang berbagi
> jaringan tersebut, jadi panggilan bisa dijawab container **env sebelah**
> (kasus nyata: data seed dev/staging sempat masuk ke database prod!).
> Semua alamat antar-service di setup ini memakai nama unik per env —
> nama container / alias ber-prefix dari `NAME_PREFIX`
> (mis. `http://example-dev-user-service:8080`). Prometheus juga men-scrape
> memakai nama unik tersebut.

## Menambah Environment Baru (mis. `qa`)

1. Salin folder: `cp -r ~/example-app-dev ~/example-app-qa`
2. Buat `app.env` baru dari `env/staging.env`, ubah: `PROJECT_NAME`,
   `NAME_PREFIX`, `ENV_NAME=qa`, dan **port-port (harus yang masih bebas)**.
3. Buat `secrets.env` baru dengan password yang **berbeda** (`openssl rand`).
4. Tambahkan job scrape di Prometheus meniru job `example-dev`, ubah nama
   container & label `env`-nya; restart container Prometheus saja.
5. `docker compose --env-file app.env --env-file secrets.env up -d`, lalu
   verifikasi `/api/health/all` di gateway env baru.

## Menyalin Konfigurasi Antar Env

Konfigurasi aplikasi (`app.env`) memang dirancang untuk disalin — itulah
gunanya file itu masuk Git. Yang **tidak boleh** disalin adalah `secrets.env`
(password tiap env harus unik) dan **data** (kalau mau menyalin data, itu
urusan backup/restore — lihat `tutorial-backup-restore-copy.md`, dan jangan
pernah mengembalikan data prod ke env lain tanpa dipikir: data prod bisa
berisi data asli pengguna).
