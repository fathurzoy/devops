# DevOps di VPS — Platform Ringan + Example App Golang

Repo bahan belajar DevOps dari perjalanan membangun platform mirip OpenShift (versi ringan) di sebuah VPS Ubuntu + Docker, lengkap dengan contoh aplikasi Golang: **2 frontend yang saling berhubungan dua arah**, API Gateway, backend service (user, order, notification, **scheduler terpusat**), PostgreSQL, Redis, dan RabbitMQ — plus logging terpusat, distributed tracing (`trace_id`), monitoring, **tiga environment (dev/staging/prod)**, contoh **HA (replica + blue-green)** dan **CI/CD**.

## Isi Repo

| Folder | Isi |
|---|---|
| [`docs/`](docs/) | Semua tutorial (lihat indeks di bawah) |
| [`example-app/`](example-app/) | Source aplikasi contoh (Go) + `docker-compose.yml` terparameterisasi untuk semua env + `env/` (konfigurasi per env, non-rahasia) + `scripts/` (backup/restore, rolling-update blue-green, deploy-dev) |
| [`platform/`](platform/) | Docker Compose platform: Portainer, Grafana, Prometheus, Loki, Promtail, Node Exporter, cAdvisor + konfigurasinya (termasuk job scrape per env & dashboard “Example App per Environment”) |
| [`.github/workflows/`](.github/workflows/) | CI GitHub Actions (vet + build + docker build) untuk `example-app/**` |

## Environment

Satu codebase, tiga lingkungan yang terisolasi penuh (DB/broker/password sendiri-sendiri):

| Env | FE1 / FE2 | Gateway | Interval scheduler | Catatan |
|---|---|---|---|---|
| development | :3400 / :3401 | :18100 | 20 dtk | **CI/CD otomatis** (pull-based dari repo ini) |
| staging | :3300 / :3301 | :18090 | 30 dtk | promosi manual dari dev |
| production | :3200 / :3201 | :18080 | 60 dtk | promosi manual, order-service **2 replica** |

Menjalankan satu env:

```bash
cd example-app
cp env/dev.env app.env            # pilih env: dev / staging / prod
cp .env.example secrets.env       # isi password & token sendiri (JANGAN commit)
docker compose --env-file app.env --env-file secrets.env up -d --build
```

## Arsitektur Example App

```
FE1 ──┐
      ├──> API Gateway ──> user-service ────────> PostgreSQL
FE2 ──┘        │         ├─> order-service (replica, blue-green) ─> PostgreSQL + RabbitMQ
               │         ├─> notification-service <── RabbitMQ (event FE1<->FE2)
               │         └─> scheduler-service ── lock Redis ──> POST /jobs/* order-service
               └──────── Redis (cache) • semua log membawa env + trace_id • /metrics per env
```

- FE tidak pernah bicara langsung ke FE lain — interaksi FE1 ↔ FE2 lewat backend + event RabbitMQ.
- **Scheduler terpusat**: satu-satunya penjadwal; menembak API service lain dengan lock Redis anti-dobel (`SKIPPED_LOCKED` bila job sebelumnya masih jalan).
- **HA**: gateway memilih upstream order secara round-robin; update versi memakai blue-green (yang baru sehat dulu, yang lama mati terakhir) — lihat `example-app/scripts/rolling-update.sh`.
- Semua service menulis log JSON terstruktur (`env`, `trace_id`, ...) dan metrik Prometheus berlabel `env`.

## CI/CD

- **CI**: GitHub Actions memeriksa setiap perubahan `example-app/**` (`go vet`, `go build`, `docker build`).
- **CD**: hanya **dev** yang otomatis — VPS menarik repo ini sendiri tiap menit (`deploy-dev.sh` + cron) dan men-deploy bila example-app berubah. Staging & prod naik lewat **prosedur promosi manual** (checklist di tutorial deploy).

## Indeks Tutorial

1. [Tutorial utama — dari nol sampai platform + example app](docs/tutorial-devops-vps-lengkap.md)
2. [Environment dev / staging / prod & grouping](docs/tutorial-environment-dev-staging-prod.md)
3. [Deploy, promosi & CI/CD](docs/tutorial-deploy-cicd.md)
4. [Backup, restore & menyalin data antar env](docs/tutorial-backup-restore-copy.md)
5. [Menambah service baru & mendaftarkannya ke gateway](docs/tutorial-tambah-service-baru.md)
6. [HA (replica & blue-green) dan scheduler terpusat](docs/tutorial-ha-dan-scheduler.md)

## Cara Menjalankan Platform

```bash
cd platform
cp .env.example .env   # isi password admin Grafana & Portainer
docker compose up -d
```

- Portainer: https://localhost:9443 — Grafana: http://localhost:3100

## Keamanan

- File rahasia (`secrets.env` / `.env`) **tidak pernah** masuk repo — yang masuk Git hanya `.env.example` (placeholder) dan `env/*.env` (konfigurasi non-rahasia per env).
- Semua password di Compose dibaca dari variabel (`${POSTGRES_PASSWORD}`, `${RABBITMQ_PASSWORD}`, `${GATEWAY_ADMIN_TOKEN}`, ...), unik per environment.

## Mulai Belajar Dari Mana?

Baca [tutorial utama](docs/tutorial-devops-vps-lengkap.md) berurutan, lalu perdalam per topik lewat indeks di atas.
