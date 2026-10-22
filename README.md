# DevOps di VPS — Platform Ringan + Example App Golang

Repo bahan belajar DevOps dari perjalanan membangun platform mirip OpenShift (versi ringan) di sebuah VPS Ubuntu + Docker, lengkap dengan contoh aplikasi Golang: **2 frontend yang saling berhubungan dua arah**, API Gateway, 3 backend service, PostgreSQL, Redis, dan RabbitMQ — plus logging terpusat, distributed tracing (`trace_id`), dan monitoring.

## Isi Repo

| Folder | Isi |
|---|---|
| [`docs/`](docs/) | Tutorial lengkap langkah demi langkah: [tutorial-devops-vps-lengkap.md](docs/tutorial-devops-vps-lengkap.md) |
| [`example-app/`](example-app/) | Source code aplikasi contoh (Go): FE1, FE2, API Gateway, user-service, order-service, notification-service + `docker-compose.yml` (PostgreSQL, Redis, RabbitMQ) |
| [`platform/`](platform/) | Docker Compose platform monitoring & manajemen: Portainer, Grafana, Prometheus, Loki, Promtail, Node Exporter, cAdvisor + file konfigurasinya |

## Arsitektur Example App

```
FE1 (port 3200) ──┐
                  ├──> API Gateway (18080) ──> user-service ──────> PostgreSQL
FE2 (port 3201) ──┘        │                 ├─> order-service ───> PostgreSQL + RabbitMQ
                           │                 └─> notification-service <── RabbitMQ
                           └──── Redis (cache user) + event FE1 <-> FE2 lewat RabbitMQ
```

- FE tidak pernah bicara langsung ke FE lain — semua interaksi FE1 ↔ FE2 lewat backend + event RabbitMQ (`fe.events`, `order.created`, `order.status.changed`).
- Semua service menulis log JSON terstruktur dengan satu `trace_id` yang sama per request, bisa ditelusuri end-to-end di Loki/Grafana.
- Setiap service mengekspos `/health` dan `/metrics` (format Prometheus).

## Cara Menjalankan Example App

```bash
cd example-app
cp .env.example .env   # lalu isi password-nya sendiri
docker compose up -d --build
```

Lalu buka:
- FE1: http://localhost:3200
- FE2: http://localhost:3201
- API Gateway: http://localhost:18080/health
- RabbitMQ Management: http://localhost:15673

## Cara Menjalankan Platform

```bash
cd platform
cp .env.example .env   # lalu isi password admin Grafana & Portainer
docker compose up -d
```

- Portainer: https://localhost:9443
- Grafana: http://localhost:3100

## Keamanan

- File `.env` asli **tidak pernah** masuk repo (sudah di-`.gitignore`). Di repo hanya ada `.env.example` berisi placeholder.
- Semua password di `docker-compose.yml` dibaca dari variabel `.env` (`${POSTGRES_PASSWORD}`, `${RABBITMQ_PASSWORD}`, `${GRAFANA_ADMIN_PASSWORD}`, dll), bukan ditulis langsung di file.

## Mulai Belajar Dari Mana?

Baca tutorialnya berurutan di [`docs/tutorial-devops-vps-lengkap.md`](docs/tutorial-devops-vps-lengkap.md) — dari cara memberi akses SSH yang aman, audit server, keputusan arsitektur, instalasi platform, sampai panduan memakai semua tombol case di FE dan latihan-latihan praktiknya.
