# Tutorial DevOps di VPS — Dari Nol Sampai Platform + Example App
### Perjalanan membangun platform mirip OpenShift (versi ringan) lengkap dengan contoh aplikasi Golang, FE ganda yang saling berhubungan, RabbitMQ, Redis, dan PostgreSQL

Ditulis dalam Bahasa Indonesia, berdasarkan pengerjaan nyata di VPS `43.167.194.197` (Tencent Cloud, Ubuntu 24.04) pada 22 Oktober 2026.
Semua password di dokumen ini **tidak ditulis** — password selalu diambil dari file `.env` di server (dijelaskan caranya).

---

## Daftar Isi
1. Gambaran Besar: Apa yang Dibangun
2. Tahap 1 — Memberi Akses VPS dengan Aman (SSH Key)
3. Tahap 2 — Izin Protokol Jaringan di Muse (Deny → Ask)
4. Tahap 3 — Audit Fase 0: Memetakan Isi Server
5. Tahap 4 — Keputusan Besar: Versi Penuh vs Versi Ringan
6. Tahap 5 — Menghapus VoiceVox (Contoh Menghapus Service dengan Aman)
7. Tahap 6 — Instalasi Platform Ringan (Portainer, Grafana, Prometheus, Loki, Promtail, cAdvisor)
8. Tahap 7 — Example App: 2 Frontend + 3 Backend + PostgreSQL + Redis + RabbitMQ
9. Konsep-Konsep Penting (Container vs Pod, Log Terpusat, trace_id, Metrics)
10. Panduan Memakai Semua Tombol Case di FE (Satu per Satu)
11. Interaksi FE1 ↔ FE2: Event-Driven Antar Frontend
12. Latihan Belajar (Langkah demi Langkah)
13. Cheatsheet Perintah
14. Troubleshooting Umum
15. Batasan & Pengembangan Selanjutnya
16. Perkembangan Lanjutan — Environment dev/staging/prod, Scheduler Terpusat, HA & Blue-Green, CI/CD (dokumen pendamping: `tutorial-environment-dev-staging-prod.md`, `tutorial-deploy-cicd.md`, `tutorial-backup-restore-copy.md`, `tutorial-tambah-service-baru.md`, `tutorial-ha-dan-scheduler.md`)

---

## 1. Gambaran Besar: Apa yang Dibangun

Di akhir perjalanan ini, di VPS ada tiga kelompok hal:

| Kelompok | Isi |
|---|---|
| **Aplikasi lama kamu** | 21 container production (tomodachi, ngelmu, gaku-pay, budtech, analyzer, bot, dll) — **tidak diganggu sama sekali** |
| **Platform DevOps (ringan)** | Portainer (konsol manajemen), Grafana (dashboard & log), Prometheus (metrics), Loki + Promtail (log terpusat), Node Exporter + cAdvisor (exporter) — di folder `~/devops-platform` |
| **Example App (untuk belajar)** | FE1 + FE2, API Gateway, user-service, order-service, notification-service, PostgreSQL, Redis, RabbitMQ — di folder `~/example-app` |

### Link Akses (dari mana saja)
| Keperluan | URL | Login |
|---|---|---|
| Portainer (kelola container) | https://43.167.194.197:9443 | user `admin`, password dari `~/devops-platform/.env` |
| Grafana (metrics + log) | http://43.167.194.197:3100 | user `admin`, password dari `~/devops-platform/.env` |
| FE1 — Aplikasi User/Pelanggan | http://43.167.194.197:3200 | tanpa login (untuk belajar) |
| FE2 — Dashboard Admin/Toko | http://43.167.194.197:3201 | tanpa login (untuk belajar) |
| API Gateway | http://43.167.194.197:18080 | tanpa login (untuk belajar) |
| RabbitMQ Management | http://43.167.194.197:15673 | user `example`, password dari `~/example-app/.env` |
| FE1/FE2 **staging** | http://43.167.194.197:3300 / http://43.167.194.197:3301 | gateway :18090, RabbitMQ Mgmt :15683 |
| FE1/FE2 **dev** | http://43.167.194.197:3400 / http://43.167.194.197:3401 | gateway :18100, RabbitMQ Mgmt :15693 |
| Grafana — dashboard per environment | http://43.167.194.197:3100/d/example-per-env/example-app-per-environment | bandingkan traffic dev/staging/prod |
| Prometheus | hanya lokal server `127.0.0.1:9091` | lewat Grafana saja (lihat penjelasan keamanan di Bab 6) |
| Loki | hanya lokal server `127.0.0.1:3101` | lewat Grafana Explore |

> **Cara mengambil password** (jalankan dari terminal **di komputermu sendiri** — bukan dari dalam VPS):
> ```bash
> ssh ubuntu@43.167.194.197
> # setelah masuk:
> grep PASSWORD ~/devops-platform/.env      # password Grafana & Portainer
> grep PASSWORD ~/example-app/.env          # password RabbitMQ example app
> ```
> Kenapa tidak lewat `ssh ubuntu@43.167.194.197 "grep ..."` dari dalam VPS? Karena perintah itu artinya "SSH dari VPS ke VPS", dan VPS tidak punya private key untuk login ke dirinya sendiri — hasilnya `Permission denied (publickey)`. Kamu sebenarnya **sudah berada di dalam VPS**, jadi jalankan `grep`-nya langsung saja tanpa `ssh`.

---

## 2. Tahap 1 — Memberi Akses VPS dengan Aman (SSH Key)

### Konsepnya
Login SSH dengan **key pair** jauh lebih aman daripada password:
- **Private key**: file rahasia, disimpan hanya di mesin yang akan login (di kasus ini: mesin Muse). **Tidak pernah dibagikan ke siapa pun.**
- **Public key**: file yang aman dibagikan dan dipasang di server, di file `~/.ssh/authorized_keys`. Server akan mengizinkan login hanya untuk pemegang private key pasangannya.

### Langkah yang dilakukan
1. User memberikan alamat SSH: `ssh ubuntu@43.167.194.197`.
2. Muse memberikan **public key**-nya (satu baris teks diawali `ssh-ed25519 ...`).
3. User login ke VPS dengan caranya sendiri, lalu menjalankan:
   ```bash
   mkdir -p ~/.ssh && chmod 700 ~/.ssh
   echo "ssh-ed25519 <PUBLIC-KEY> hatch" >> ~/.ssh/authorized_keys
   chmod 600 ~/.ssh/authorized_keys
   ```
   - `700` untuk folder `.ssh` = hanya pemilik yang boleh membuka.
   - `600` untuk `authorized_keys` = hanya pemilik yang boleh membaca/menulis. SSH menolak file yang izinnya terlalu longgar.
4. Setelah itu Muse bisa login tanpa password: `ssh -i <private-key> ubuntu@43.167.194.197`.
5. Host key server dicatat (fingerprint `SHA256:hFvkGfXx...`) dan **dipin** di file known_hosts khusus, supaya setiap koneksi berikutnya memverifikasi bahwa server yang diajak bicara benar-benar VPS yang sama (mencegah man-in-the-middle).

### Pelajaran
- Jangan pernah mengirim password server lewat chat. Public key memang didesain untuk dibagikan; password dan private key tidak.
- Kalau suatu saat ingin mencabut akses seseorang, cukup hapus baris key-nya dari `authorized_keys`.

---

## 3. Tahap 2 — Izin Protokol Jaringan di Muse (Deny → Ask)

Muse sebagai agent punya gerbang keamanan sendiri untuk protokol jaringan langsung (SSH, database, FTP, dll). Secara default setiap protokol berada di posisi **Deny** — koneksi langsung ditolak diam-diam dengan pesan:

```
muse: Outbound SSH is turned off for this assistant. To allow it, ask the user to open
Muse settings -> Permissions -> Direct network protocols and switch ssh from Deny to Ask.
```

Cara membukanya:
1. Buka **Pengaturan (Settings) → Izin (Permissions)**.
2. Di bagian **Kelola izin**, ketuk baris **Protokol jaringan langsung** (di aplikasi Bahasa Indonesia tulisannya persis seperti itu).
3. Cari baris **SSH**, ubah dari **Deny** ke **Ask**.

Catatan penting:
- Di layar itu **hanya ada Deny / Ask, tidak ada Allow**. Itu memang desainnya.
- **Ask** artinya: setiap tujuan baru akan memunculkan kartu persetujuan satu kali. Setelah kamu setujui untuk satu tujuan (misal VPS ini port 22), persetujuan itu tersimpan dan tidak ditanyakan lagi untuk tujuan yang sama.
- Jadi "langsung tanpa tanya-tanya" tetap bisa dicapai: cukup setujui satu kali per tujuan.

Gejala khas kalau tahap ini belum dilakukan: `Connection reset by peer` / banner exchange gagal **sebelum** proses login dimulai — itu bukan salah server, bukan salah key, melainkan gerbang di sisi agent.

---

## 4. Tahap 3 — Audit Fase 0: Memetakan Isi Server

### Prinsipnya
**Audit dulu, instal kemudian. Jangan pernah menginstal di server yang belum kamu petakan.** Tujuannya: tahu apa yang sedang jalan supaya tidak ada yang tertabrak, terhapus, atau kehabisan resource.

### Yang diperiksa (semua read-only)
- Spek: OS, kernel, CPU, RAM, swap, disk (`cat /etc/os-release`, `lscpu`, `free -h`, `df -h`).
- Semua service systemd yang running / tidak running (`systemctl list-units`).
- Semua container Docker & project Compose-nya (`docker ps -a`, `docker compose ls`).
- Semua port yang listen (`ss -tulnp`).
- Tools yang sudah ada (docker, kubernetes, nginx, go, node, dll).
- Proses terberat (`ps aux --sort=-%mem`), isi home, situs nginx, firewall.

### Temuan audit (22 Oktober 2026)
| Hal | Hasil |
|---|---|
| Provider / hostname | Tencent Cloud CVM, `VM-0-11-ubuntu`, uptime 103 hari |
| OS | Ubuntu 24.04.4 LTS, kernel 6.8, x86_64 |
| CPU | 2 vCPU Intel Xeon Platinum 8255C |
| RAM | 3,6 GiB — terpakai ~2,0 GiB, **swap terpakai 2,9 GiB** (tanda RAM ketat) |
| Disk | 60 GB — **terpakai 84%, sisa 9,5 GB** ⚠️ |
| Docker | 29.6.1 aktif; **22 container running dari 15 project Compose**; 1 container Exited |
| Reverse proxy utama | Caddy (di Docker) memegang port 80 & 443 |
| Kubernetes | **belum ada sama sekali** (tidak ada k3s/kubectl/helm) |
| Port terpakai | 22, 80, 443, 3000, 3002, 3003, 5432, 5433, 8080, 8090, 8100, 8110, 9090, dst |
| Firewall host | UFW tidak aktif |

Aplikasi yang ditemukan sedang jalan antara lain: project tomodachi/HanashiRensyu (app + database + VoiceVox + Caddy), ngelmu, gaku-pay (backend Go + frontend + PostgreSQL), budtech + Strapi CMS, budget-battle, qa-engineer, dan sederet analyzer & bot (solana-meme, idx-market, global-us-market, strategy-backtester, crypto-market-intel, hyperliquid, sol-auto-buy, economic-news-bot).
Satu-satunya yang tidak aktif: container `hello-world` yang sudah Exited 3 bulan.

### Pelajaran
- Audit mengubah **asumsi** menjadi **fakta**: rencana awalnya "instal OpenShift-like stack penuh", tapi fakta spek berkata lain (Bab 4).
- Catatan inventaris ini juga menjadi "kontrak": aplikasi aktif harus tetap aktif setelah pekerjaan selesai, dan itu diverifikasi di akhir.

---

## 5. Tahap 4 — Keputusan Besar: Versi Penuh vs Versi Ringan

### Rencana awal (versi penuh)
Meniru pengalaman kerja di BRI: Red Hat OpenShift (konsol web, pod, node) + ELK (Elasticsearch, Logstash, Kibana) + Prometheus/Grafana + Kubernetes.

### Kenapa tidak bisa di VPS ini (hitungan kasar kebutuhan RAM)
| Komponen versi penuh | Kebutuhan RAM kira-kira |
|---|---|
| Rancher (konsol mirip OpenShift) | ± 2 GB |
| Elasticsearch | ± 2 GB (heap + overhead) |
| Kibana | ± 1 GB |
| K3s/Kubernetes + Prometheus + Grafana | ± 1,5 GB |
| **Total platform saja** | **± 6,5 GB** |

Padahal RAM total hanya 3,6 GB dan 2 GB sudah dipakai aplikasi yang ada. Disk pun sisa 9,5 GB, sementara image + data ELK/Kubernetes mudah melewati itu. **Memaksakan instalasi akan membuat VPS tumbang dan justru mematikan 22 aplikasi yang sehat.** Kesimpulan: versi penuh butuh upgrade VPS (disarankan minimal 4 vCPU / 8–16 GB RAM / disk ≥ 120 GB) atau server terpisah.

### Versi ringan yang dipilih (dan trade-off-nya)
| Kebutuhan | Versi penuh | Versi ringan (terpilih) | Trade-off |
|---|---|---|---|
| Konsol manajemen | Rancher / OpenShift Console | **Portainer** | Portainer mengelola **container Docker**, bukan Pod Kubernetes. Fitur hariannya mirip: lihat container, log, terminal, restart, env. Tidak ada konsep namespace/pod/HPA. |
| Orkestrasi | Kubernetes (K3s) | **Docker Compose** | Tidak ada self-healing/scheduling multi-node; cukup untuk 1 server dan jauh lebih ringan. |
| Log terpusat | Elasticsearch + Kibana | **Loki + Grafana** | Loki hanya mengindeks **label**, bukan isi log — jauh lebih hemat RAM/disk, pencarian tetap cepat untuk pola "per service / per trace_id". Analitik full-text kompleks kalah dari Elasticsearch. |
| Pengumpul log | Logstash / Fluent Bit | **Promtail** | Promtail memang pasangan Loki; mengambil log semua container Docker otomatis. |
| Monitoring | Prometheus + Grafana | **Prometheus + Grafana** (sama) | Tidak dikorbankan — tetap versi aslinya, hanya retention & scrape dibuat hemat. |
| Metrics per container | kube-state-metrics + cAdvisor | **cAdvisor + Node Exporter** | Setara untuk kebutuhan CPU/RAM per container. |

Prinsipnya: **fungsi 80% sama, beban bisa 5–10x lebih kecil**, dan arsitekturnya tetap rapi sehingga nanti gampang dimigrasi ke Kubernetes bila server sudah di-upgrade.

---

## 6. Tahap 5 — Menghapus VoiceVox (Contoh Menghapus Service dengan Aman)

VoiceVox (mesin text-to-speech untuk HanashiRensyu) sudah tidak dipakai — rencananya diganti API Gemini — dan image-nya adalah yang terbesar di server (4,67 GB). Menghapusnya adalah contoh prosedur menghapus service yang benar:

1. **Periksa dulu strukturnya**: VoiceVox ternyata service sendiri di `docker-compose.yml` HanashiRensyu, tanpa volume/data, tapi `app` punya `depends_on: voicevox`.
2. **Backup konfigurasi**: `docker-compose.yml` disalin menjadi `docker-compose.yml.bak-20261022-voicevox`.
3. **Cabut dari Compose secara permanen**: blok service `voicevox`, baris `depends_on`-nya, dan env `VOICEVOX_ENGINE_URL` dihapus (diganti komentar penanda "removed — diganti API Gemini nanti"). Tujuannya supaya VoiceVox **tidak hidup lagi** kalau project itu suatu saat di-deploy ulang.
4. **Stop, hapus container, hapus image.**
5. **Verifikasi**: services Compose tinggal `db, app, caddy`; 21 container lain tetap Up; disk bebas naik dari 9,5 GB → **14 GB (84% → 76%)**.

Pelajaran: menghapus container saja tidak cukup kalau definisinya masih ada di Compose — dia akan lahir lagi pada deploy berikutnya. Dan selalu verifikasi tidak ada tetangga yang ikut tumbang.

---

## 7. Tahap 6 — Instalasi Platform Ringan

Semua platform dipasang sebagai **satu project Docker Compose terpisah** di `~/devops-platform` (jadi tidak bercampur dengan aplikasi lain, gampang dirawat/dihapus):

| Service | Fungsi | Port |
|---|---|---|
| Portainer CE | Konsol web kelola container (lihat Bab 9.1) | 9443 (HTTPS, publik) |
| Grafana | Dashboard metrics + penjelajah log | 3100 (publik) |
| Prometheus | Menyimpan & query metrics (scrape) | 127.0.0.1:9091 (lokal) |
| Loki | Menyimpan log terpusat | 127.0.0.1:3101 (lokal) |
| Promtail | Mengumpulkan log semua container Docker → Loki | — |
| Node Exporter | Metrics OS/server (CPU, RAM, disk, network) | internal |
| cAdvisor | Metrics per container | internal |

Kenapa Prometheus & Loki dikunci di localhost? Karena **keduanya tidak punya login**. Kalau UI mentahnya dibuka ke internet, siapa pun bisa membaca semua log dan metrics server. Pola amannya: data mereka diakses **lewat Grafana yang ber-login** (Grafana jadi "Kibana"-nya di sini).

### Masalah-masalah nyata yang ditemui & solusinya (bagian paling berharga untuk belajar!)

**a) Prometheus restart berulang — `permission denied` membaca config.**
Penyebab: file `prometheus.yml` yang di-upload izinnya `660`, sedangkan user di dalam container Prometheus bukan pemiliknya. Solusi: `chmod 755` folder & `644` file config. Pelajaran: bind-mount config = izin file host ikut berlaku di dalam container.

**b) cAdvisor membuat scrape Prometheus timeout.**
Awalnya target cAdvisor di Prometheus berstatus `down`: endpoint `/metrics` butuh **> 40 detik** (timeout scrape cuma 10 detik) dan memorinya mentok 256 MB. Penyebabnya: cAdvisor sibuk menghitung pemakaian disk (filesystem) satu per satu untuk 28 container — pekerjaan yang sangat lambat.
Solusi: matikan metrics yang berat & tidak dipakai, naikkan batas memori:
```yaml
command:
  - --docker_only=true
  - --disable_metrics=disk,diskIO,percpu,process,sched,tcp,udp,cpu_topology,perf_event,referenced_memory,hugetlb,resctrl,cpuset,memory_numa,advtcp
mem_limit: 384m
```
Hasilnya: scrape turun dari >40 detik → **0,18 detik**, target jadi `up` dan sehat.
(Satu drama kecil: nama metric `accelerator` ternyata tidak didukung build cAdvisor ini — container langsung exit dengan pesan `unsupported metric "accelerator"`. Pelajaran: selalu baca `docker logs` sampai baris error pertamanya, jangan menebak.)

**c) Datasource Grafana kosong.**
Grafana membaca folder *provisioning* hanya saat start. Saat pertama naik, izin file provisioning belum benar, jadi Prometheus & Loki tidak terdaftar. Setelah izin diperbaiki dan Grafana di-restart, dua datasource muncul otomatis dari file:
```yaml
# grafana/provisioning/datasources/datasources.yml
datasources:
  - {name: Prometheus, type: prometheus, url: http://platform-prometheus:9090, isDefault: true}
  - {name: Loki, type: loki, url: http://platform-loki:3100}
```
Teknik provisioning ini penting: konfigurasi Grafana jadi **kode** (bisa diulang, tidak hilang kalau container dibuat ulang), bukan klik-klik manual.

**d) Dashboard di-import lewat API.** Dashboard *Node Exporter Full* (id 1860) dan *cAdvisor exporter* (id 14282) di-download dari grafana.com lalu di-import via `POST /api/dashboards/import` dengan pemetaan datasource otomatis.

**e) Portainer versi baru butuh setup token.** Membuat admin pertama kali tidak cukup dengan memanggil API init — Portainer menjawab `Invalid or missing setup token`. Tokennya tercetak sekali di log container saat startup (`setup_token=...`); token itu dikirim sebagai header `X-Setup-Token`. Setelah admin jadi dan dites login (JWT berhasil), endpoint environment `local` (Docker socket) didaftarkan via API. Pelajaran: fitur keamanan produk berubah antar versi — pesan error API biasanya memberi tahu persis apa yang kurang.

**f) Akun admin dibuatkan dengan password acak yang disimpan di server.** Password Grafana & Portainer digenerate di server dan disimpan di `~/devops-platform/.env` (izin `600`, hanya pemilik yang bisa membaca). Password **tidak pernah ditulis di chat/dokumen**; pemilik server mengambilnya sendiri dengan perintah di Bab 1.

### Cara platform memantau dirinya & aplikasi lain
- Prometheus men-*scrape* (menarik) `/metrics` dari Node Exporter, cAdvisor, dirinya sendiri, dan nanti example app (Bab 8).
- Promtail menemukan container lewat Docker socket, lalu memberi **label** pada setiap log: `container` (nama container), `compose_project`, `compose_service`. Label-label ini yang nanti dipakai untuk menyaring log di Grafana.

---

## 8. Tahap 7 — Example App: 2 Frontend + 3 Backend + PostgreSQL + Redis + RabbitMQ

### Arsitektur
```
Browser
  ├─ FE1 (:3200) ─┐
  └─ FE2 (:3201) ─┴─► API Gateway (:18080) ─┬─► user-service ──► PostgreSQL
                                            │        └──────► Redis (cache)
                                            ├─► order-service ─► PostgreSQL
                                            │        ├─ validasi ─► user-service
                                            │        └─ publish ──► RabbitMQ
                                            └─► notification-service ◄─ konsumsi ─ RabbitMQ
```
Semua backend ditulis dengan **Golang** (satu module, package bersama `internal/httpx` untuk log/trace/metrics, `internal/redisx` client Redis buatan sendiri, `internal/mqx` publisher RabbitMQ). Setiap service adalah container sendiri, dikemas multi-stage Dockerfile (build di image `golang:alpine`, hasil akhir binary kecil di `alpine`).

### Data & pendukung
| Komponen | Dipakai untuk |
|---|---|
| PostgreSQL `exampledb` | tabel `users` (oleh user-service) & `orders` (oleh order-service) |
| Redis | cache daftar/detail user (TTL 30 detik) + demo SET/GET/counter |
| RabbitMQ | queue `order.created`, `order.status.changed`, `manual.events`, `fe.events` |

### Kebiasaan baik yang diterapkan di semua service
1. **Log JSON terstruktur** satu baris per kejadian ke stdout:
   ```json
   {"time":"...","level":"info","service":"order-service","trace_id":"ab12...","method":"POST","endpoint":"POST /orders","status":201,"duration_ms":12.3,"message":"request selesai"}
   ```
2. **trace_id di mana-mana**: request masuk tanpa `X-Trace-Id` akan dibuatkan satu oleh middleware; gateway & pemanggilan antar-service meneruskannya; event RabbitMQ membawanya di body pesan; notification-service mencatatnya saat mengonsumsi. Hasilnya: **satu aksi user = satu trace_id yang bisa dicari di semua log service.**
3. **Endpoint `/health`** untuk cek hidup & **`/metrics`** format Prometheus: `http_requests_total{service,endpoint,method,status}` + ringkasan durasi — bahan grafik "API apa yang di-hit, error berapa, latency berapa".
4. **Batas resource** (`mem_limit`) per container supaya satu service nakal tidak menghabiskan server.
5. Example app bergabung ke **dua network**: network-nya sendiri (`example-net`) untuk bicara internal, dan network platform (`devops-platform_platform-net`) **khusus supaya Prometheus bisa men-scrape** service-service contoh. Di Prometheus ditambah job:
   ```yaml
   - job_name: example
     scrape_interval: 15s
     static_configs:
       - targets: ["example-gateway:8080","example-user-service:8080","example-order-service:8080","example-notification-service:8080"]
   ```

### Hasil verifikasi example app (nyata, 22 Oktober 2026)
Semua pengujian dilakukan dari dalam server ke gateway (`127.0.0.1:18080`):

| # | Case | Hasil |
|---|---|---|
| 1 | Health semua service via gateway | ✅ 200, status `ok` (gateway + 3 backend, satu trace_id) |
| 2 | Buat user | ✅ 201, user tersimpan di PostgreSQL |
| 3 | List user 2x | ✅ klik-1 `MISS` dari postgres, klik-2 `HIT` dari Redis (header `X-Cache: HIT`) |
| 4 | Detail user per ID | ✅ 200, data cocok |
| 5 | Buat order | ✅ 201 dalam ±28 ms, `rabbitmq: published: order.created` |
| 6 | Event order terkonsumsi | ✅ notification-service menyimpan event `order.created` dengan **trace_id yang persis sama** dengan response order |
| 7 | Pesan FE1 → FE2 | ✅ publish 202, pesan muncul di `GET /events?target=fe2` |
| 8 | Broadcast | ✅ event `target=all` muncul untuk target fe1 **dan** fe2 |
| 9 | FE2 Setujui order | ✅ status di `GET /orders` berubah menjadi `APPROVED`, event `order.status.changed` muncul untuk target fe1 dengan trace_id aksi tersebut |
| 10 | Redis SET/GET + counter | ✅ nilai terbaca kembali; counter naik 1 → 2 |
| 11 | RabbitMQ manual | ✅ pesan terkonsumsi notification-service |
| 12 | Simulasi error | ✅ HTTP 500, metrik `http_requests_total{status="500"}` tercatat di Prometheus |
| 13 | Simulasi slow 2000 ms | ✅ durasi terukur 2003 ms |
| 14 | Halaman FE1 & FE2 | ✅ HTTP 200, judul masing-masing benar |
| 15 | RabbitMQ Management | ✅ HTTP 200 di port 15673 |
| 16 | Prometheus job `example` | ✅ 4/4 target `up`; total `http_requests_total{job="example"}` terakumulasi **112** request selama pengujian |
| 17 | **Loki — 1 trace_id, banyak service** | ✅ trace_id order yang sama ditemukan di **4 container**: example-gateway (2 baris log), example-order-service (3), example-user-service (1), example-notification-service (1) — total 7 baris log |

Sesudah example app terpasang: total container di VPS **37** (28 sebelumnya + 9 example app), disk 84% (sisa 9,5 GB), RAM tersedia ±1,4 GB. Sembilan container example app: `example-fe1`, `example-fe2`, `example-gateway`, `example-user-service`, `example-order-service`, `example-notification-service`, `example-postgres`, `example-redis`, `example-rabbitmq`.

---

## 9. Konsep-Konsep Penting

### 9.1 Container vs Pod, dan Portainer vs OpenShift Console
- Di **Docker**, unit terkecil yang jalan adalah **container**: satu proses utama + filesystem-nya sendiri, dibuat dari image.
- Di **Kubernetes/OpenShift**, unitnya adalah **Pod**: satu atau beberapa container yang selalu jalan bersama di satu "mesin virtual mini" (berbagi network & bisa berbagi volume). Yang kamu kelola sehari-hari di OpenShift (Deployment, scale, restart count) sebenarnya adalah Pod-Pod yang diatur controller.
- Di platform ringan ini, yang kamu kelola di **Portainer** adalah container langsung — konsepnya sama-sama "workload yang jalan", hanya tanpa lapisan Pod. Di Portainer kamu bisa: melihat semua container & statusnya, membuka **log per container**, masuk **console/terminal** ke dalam container, **restart**, melihat & mengubah **env**, menghentikan/menyalakan, dan melihat statistik CPU/RAM. Itulah padanan fungsi harian OpenShift Console untuk dunia Docker.

### 9.2 Kenapa log terpusat itu penting
Tanpa log terpusat, mencari "kenapa checkout gagal jam 10 tadi?" berarti membuka log belasan container satu per satu. Dengan Promtail → Loki:
- Semua log masuk ke satu tempat, diberi label `container`, `compose_project`, `compose_service`.
- Di Grafana → **Explore** → datasource **Loki**, kamu menyaring dengan **LogQL**, contoh:
  ```logql
  {container="example-order-service"}                        # semua log satu service
  {container=~"example-.+"} |= "error"                       # semua error di example app
  {container=~"example-.+"} |= "<trace_id>"                  # SATU request di SEMUA service
  {compose_project="example-app"} |= "order"                 # berdasarkan project compose
  ```
- Bedanya dengan Kibana/Elasticsearch: Loki tidak mengindeks isi teks (hanya label), jadi pencarian teks memakai filter `|=` setelah label dipilih. Untuk pola pemakaian kita (per service, per trace_id), ini justru cepat dan sangat hemat.

### 9.3 trace_id & Distributed Tracing
Saat satu klik di FE memicu rantai gateway → order-service → user-service → database → RabbitMQ → notification-service, setiap service menulis log sendiri. **trace_id adalah "nomor resi" yang ditempel di request itu sejak awal dan dibawa ke mana-mana** (header HTTP `X-Trace-Id` untuk panggilan HTTP, field di body pesan untuk RabbitMQ). Dengan nomor resi itu kamu bisa merekonstruksi cerita lengkap satu request dari log yang tersebar — persis alasan kenapa di tempat kerja dulu satu request di 3 service harus punya 1 ID yang sama. Di sistem yang lebih besar, konsep ini berkembang menjadi distributed tracing visual (OpenTelemetry + Jaeger/Tempo) dengan diagram waterfall; trace_id di log adalah fondasinya.

### 9.4 Metrics & Metode RED
Prometheus menyimpan angka time-series. Untuk setiap API, tiga hal terpenting (metode **RED**):
- **Rate** — berapa request per detik: `rate(http_requests_total[1m])`
- **Errors** — berapa yang status 5xx: `rate(http_requests_total{status=~"5.."}[1m])`
- **Duration** — berapa lama: dari `http_request_duration_seconds_sum / _count`.

Di Grafana, dashboard Node Exporter menunjukkan kesehatan server (CPU/RAM/disk), dashboard cAdvisor menunjukkan kesehatan per container, dan Explore → Prometheus dipakai untuk query manual seperti contoh RED di atas.

### 9.5 Event-Driven & Message Broker
RabbitMQ adalah "kantor pos": service pengirim (*publisher*) menitipkan pesan ke **queue**, service penerima (*consumer*) mengambilnya kapan pun siap. Keuntungannya: pengirim tidak perlu menunggu penerima (order tetap tercatat walau notification-service sedang restart — pesan menunggu di queue), dan satu event bisa dibaca untuk banyak keperluan. Queue di example app:
| Queue | Dipublish oleh | Dikonsumsi | Isi |
|---|---|---|---|
| `order.created` | order-service | notification-service | order baru dibuat |
| `order.status.changed` | order-service | notification-service | status order diubah FE2 |
| `fe.events` | notification-service (atas perintah FE) | notification-service | pesan antar-FE & broadcast |
| `manual.events` | notification-service (atas perintah FE) | notification-service | tes RabbitMQ manual |

---

## 10. Panduan Memakai Semua Tombol Case di FE

Buka FE1 (http://43.167.194.197:3200). FE2 (http://43.167.194.197:3201) tampilannya sama dengan tema & panel admin. Setiap klik menampilkan di panel output: **status HTTP, durasi (ms), trace_id, dan JSON response**. Tombol **"Salin Query Loki"** menyalin query untuk trace_id terakhir.

### A. Cek Health Semua Service
Gateway menembak `/health` ke tiga backend dengan satu trace_id. Hasilnya peta status tiap service. **Latihan:** salin trace_id-nya, cari di Loki — kamu akan melihat log dari gateway + ketiga backend untuk satu panggilan itu.

### B. User & Order (PostgreSQL + antar-service + cache)
1. **Buat User** — `POST /api/users`: gateway → user-service → `INSERT` PostgreSQL. Response 201 berisi user baru; ID-nya otomatis terisi ke form detail & order.
2. **Lihat Semua User** — klik pertama: `cache: MISS`, `source: postgres` (data diambil dari DB lalu disimpan ke Redis, TTL 30 detik). Klik lagi dalam 30 detik: `cache: HIT`, `source: redis-cache` (DB tidak disentuh sama sekali). Setelah membuat user baru, cache otomatis dibuang (*invalidasi*), jadi klik berikutnya MISS lagi. Perhatikan juga header `X-Cache`.
3. **Detail User per ID** — pola yang sama per user (`user:<id>`), ID yang tidak ada menghasilkan 404.
4. **Buat Order** — rantai terpanjang: gateway → order-service → **validasi user** ke user-service (kalau user tidak ada: 422 dan order batal) → `INSERT` ke PostgreSQL → **publish `order.created`** ke RabbitMQ → notification-service mengonsumsi & mencatatnya. Response menampilkan `flow` lengkapnya. Order user yang tidak terdaftar (mis. user_id 9999) adalah cara mencoba jalur gagalnya.
5. **Lihat Semua Order** — tabel order; ter-refresh otomatis tiap 5 detik.

### C. Redis
- **SET/GET**: simpan key custom dengan TTL 60 detik, lalu ambil lagi. Tunggu lewat TTL lalu GET lagi → 404 `MISS` (bukti expired bekerja).
- **Counter INCR/GET**: membuktikan operasi atomik Redis; nilainya dipakai bersama oleh semua service yang terhubung ke Redis yang sama.

### D. RabbitMQ manual
- **Kirim Pesan** menerbitkan pesan ke queue `manual.events`; **Lihat Pesan yang Sudah Dikonsumsi** menunjukkan notification-service benar-benar menerimanya (ada jeda konsumsi yang wajar, hitungan milidetik–detik).
- Buka **RabbitMQ Management (15673)** untuk melihat queue, jumlah pesan, dan consumer secara visual.

### E. Simulasi (bahan observability)
- **Error 500**: order-service sengaja error & menulis log level `error`. Cari trace_id-nya di Loki untuk melihat log error-nya, dan lihat metrik `status="500"` bertambah di Prometheus/Grafana.
- **Slow 2 detik**: request sengaja tidur 2000 ms. Lihat `duration_ms` di log (≈2000) dan lonjakan latency.
- **Spam 20x**: menembak `GET /api/users` 20 kali paralel — untuk membuat grafik traffic di Grafana "hidup".

---

## 11. Interaksi FE1 ↔ FE2: Event-Driven Antar Frontend

### Kenapa tidak FE-ke-FE langsung?
Browser FE1 tidak punya cara (dan tidak boleh) memanggil FE2 secara langsung: beda origin, tidak ada alamat "panggil halaman lain", dan kalau satu FE mati, FE lain ikut gagal. Pola yang benar di sistem nyata: **FE hanya bicara ke backend; backend menyebarkan event lewat message broker; FE tujuan membaca event itu.** Itulah yang dicontohkan di sini.

### Alurnya
```
FE1 ──POST /api/events──► Gateway ──► notification-service ──publish──► RabbitMQ (fe.events)
                                                                             │ consume
FE2 ──GET /api/events?target=fe2──► Gateway ──► notification-service ◄── simpan event
(polling tiap 4 detik + tombol Refresh Manual)
```

### Case-case-nya
1. **FE1 → FE2 (pesan)**: Di FE1, panel F, tulis pesan → **Kirim Pesan ke FE2**. Di FE2 pesan muncul di panel **Pesan Masuk untuk FE1... (untuk fe2)** lengkap dengan waktu & trace_id.
2. **FE1 → FE2 (order)**: Order yang dibuat di FE1 muncul di tabel order FE2 (polling) **dan** event `order.created` muncul di feed FE2.
3. **FE2 → FE1 (status order)**: Di tabel order FE2 ada tombol **Setujui** / **Tolak** per order. Klik → order-service mengubah status di PostgreSQL → publish `order.status.changed` (target=fe1) → FE1 melihat badge status ordernya berubah + event-nya muncul di panel masuk FE1.
4. **FE2 → FE1 (pesan balasan)**: Sama seperti arah sebaliknya, dari panel F di FE2.
5. **Broadcast**: Tombol **Broadcast ke Semua FE** mengirim event `target=all` — muncul di kedua FE.

### Polling vs WebSocket
FE membaca event dengan **polling** (bertanya tiap beberapa detik). Ini sederhana dan cukup untuk belajar & banyak kasus nyata. Kekurangannya: ada jeda sampai ± interval polling dan ada request kosong. Pengembangan alaminya adalah **WebSocket / Server-Sent Events** (server mendorong event saat itu juga) — itulah langkah berikutnya bila butuh real-time sejati.

---

## 12. Latihan Belajar (Langkah demi Langkah)

**Latihan 1 — Menelusuri satu request (15 menit).**
1. Buka FE1 & FE2 di dua tab berdampingan.
2. Di FE1: Buat User, lalu Buat Order. Catat trace_id order.
3. Klik **Salin Query Loki**, buka Grafana → Explore → Loki, tempel query, jalankan. Hitung ada log dari service apa saja dengan trace_id itu (target: gateway, order-service, user-service, notification-service).
4. Di FE2: lihat order yang sama muncul; klik **Setujui**. Kembali ke FE1: status berubah. Catat trace_id event status (beda dari trace order — setiap aksi punya resinya sendiri).

**Latihan 2 — Cache.**
Klik Lihat Semua User (MISS) → klik lagi (HIT) → Buat User baru → klik lagi (MISS lagi). Jelaskan dengan kata-katamu kenapa urutannya begitu. Lalu di panel Redis, SET key dengan TTL 60, GET, tunggu 61 detik, GET lagi.

**Latihan 3 — Error & grafik.**
Klik Simulasi Error → cari log error-nya di Loki. Spam 20x → buka Grafana dashboard cAdvisor & Explore Prometheus, jalankan `rate(http_requests_total[1m])`. Bandingkan sebelum/sesudah spam.

**Latihan 4 — Mengelola dari Portainer.**
Buka Portainer → Containers: temukan `example-order-service` → buka **Logs** (bandingkan dengan Loki), buka **Console** (terminal ke dalam container), lihat **Env**-nya, lalu **Restart** container itu. Kembali ke FE: Cek Health (order-service sempat DOWN lalu pulih), buat order lagi — berhasil, dan selama restart tadi, apakah ada pesan RabbitMQ yang hilang? (Seharusnya tidak: queue menahannya.)

**Latihan 5 — Scale ala Docker.**
Di Portainer/terminal, jalankan 2 replika user-service tidak bisa begitu saja karena nama container & port unik di Compose — ini batas Docker Compose dibanding Kubernetes (di K8s tinggal ubah angka replicas). Catat ini sebagai alasan migrasi ke K8s nanti.

---

## 13. Cheatsheet Perintah

```bash
# --- Platform (di ~/devops-platform) ---
cd ~/devops-platform && docker compose ps                 # status semua service platform
docker compose logs -f grafana                             # log satu service
docker compose restart prometheus                          # restart satu service saja
docker compose up -d                                       # terapkan perubahan compose

# --- Example app (di ~/example-app) ---
cd ~/example-app && docker compose ps
docker compose logs -f order-service
docker compose up -d --build user-service                  # rebuild satu service setelah ubah kode
docker compose down                                        # matikan example app (data postgres/rabbitmq tetap di volume)
docker compose down -v                                     # + hapus volume (DATA HILANG, hati-hati)

# --- Umum ---
docker ps                                                  # semua container yang jalan
docker stats                                               # CPU/RAM container real-time
docker exec -it example-redis redis-cli PING               # masuk ke Redis
docker exec -it example-postgres psql -U example -d exampledb   # masuk ke PostgreSQL
curl -s http://127.0.0.1:9091/api/v1/targets | head        # target Prometheus (dari dalam server)
curl -s "http://127.0.0.1:3101/loki/api/v1/labels"         # label Loki (dari dalam server)
```

---

## 14. Troubleshooting Umum

| Gejala | Cek pertama | Penyebab umum |
|---|---|---|
| Dashboard tidak bisa dibuka dari browser | `docker compose ps` di folder platform; coba dari server: `curl -I localhost:3100` | Container mati, atau port belum diizinkan di **Security Group Tencent Cloud** (port baru 9443/3100/3200/3201/18080/15673 harus dibuka bila SG membatasi) |
| FE error "Gagal fetch ke gateway" | Buka langsung `http://<ip>:18080/health` | Gateway mati / port 18080 tertutup SG |
| Buat order balas 422 | Baca body error-nya | user_id tidak terdaftar (memang begitu desainnya) |
| Buat order balas 502 | `docker compose logs order-service` | user-service / RabbitMQ / DB belum siap atau mati |
| List user selalu MISS | `docker exec example-redis redis-cli ping`; logs user-service | Redis mati, atau cache selalu ter-invalidasi karena ada pembuatan user |
| Pesan FE tidak muncul di FE sebelah | logs notification-service; RabbitMQ Management → Queues | Consumer putus (dia auto-reconnect ±3 detik), atau event target-nya salah |
| Target Prometheus `down` | `docker logs platform-prometheus`; buka URL target dari dalam network | Service mati, port salah, atau endpoint `/metrics` lambat (lihat kasus cAdvisor) |
| Log tidak ada di Loki | labels di Explore; `docker logs platform-promtail` | Promtail belum menemukan container / Loki baru mulai (log lama sebelum Promtail jalan tidak ditarik) |
| Disk penuh lagi | `df -h`, `docker system df` | Image/log menumpuk — hapus image tak terpakai secara selektif (`docker image prune` hati-hati: hanya yang dangling), cek container Exited lama |
| Container platform restart loop | `docker logs <container> --tail 50` | Baca **baris error pertama**, bukan terakhir — hampir selalu config/izin file/volume |

Prinsip emas troubleshooting di perjalanan ini terbukti berkali-kali: **baca log sampai error pertamanya, perbaiki satu hal, verifikasi, baru lanjut.**

---

## 15. Batasan & Pengembangan Selanjutnya

Batasan yang disadari (jujur):
- **Satu server = single point of failure.** Server mati, semua mati. Tidak ada HA.
- Example app **tanpa autentikasi** — murni untuk belajar di lingkungan ini; jangan jadikan pola untuk aplikasi publik berisi data asli.
- Password demo (DB example, dsb.) tersimpan di file project — untuk production gunakan secret management & rotasi.
- Grafana/Portainer memakai HTTP/IP & sertifikat self-signed — untuk production pasang domain + sertifikat sah (Caddy yang sudah ada bisa jadi pintunya) dan batasi akses dashboard dengan VPN/allowlist bila bisa.
- Loki tanpa retensi ketat: log akan tumbuh; atur retensi & pantau disk (saat tutorial ini ditulis disk 82%).

Jalur pengembangan alami berikutnya:
1. **Upgrade VPS / tambah node** → migrasi ke **K3s + Rancher** (pengalaman OpenShift penuh) dan **Elasticsearch/Kibana** bila memang butuh pencarian full-text tingkat lanjut.
2. Ganti polling FE menjadi **WebSocket/SSE** untuk event real-time.
3. Tambah **Jaeger/Tempo + OpenTelemetry** untuk visual waterfall tracing (trace_id sudah jadi fondasinya).
4. CI/CD: build image di GitHub Actions → deploy otomatis (GitOps) ke server.
5. Alerting: Alertmanager → Telegram/email untuk error rate & disk.

---

## 16. Perkembangan Lanjutan — Env Bertingkat, Scheduler, HA, CI/CD

Di sore yang sama (22 Oktober 2026), example app naik kelas dari “satu
lingkungan untuk belajar” menjadi miniatur cara kerja tim beneran. Ringkasannya
di sini; detail langkah demi langkahnya ada di lima dokumen pendamping di
folder `docs/` yang sama.

### 16.1 Environment development / staging / production

Example app sekarang hidup dalam **tiga environment** yang terisolasi —
masing-masing dengan PostgreSQL, Redis, RabbitMQ, dan passwordnya sendiri:

| Env | FE1/FE2 | Gateway | Folder | Project Compose |
|---|---|---|---|---|
| development | :3400 / :3401 | :18100 | `~/example-app-dev` | `example-app-dev` |
| staging | :3300 / :3301 | :18090 | `~/example-app-staging` | `example-app-staging` |
| production | :3200 / :3201 | :18080 | `~/example-app` | `example-app` |

Satu file `docker-compose.yml` dipakai semua env; pembedanya file `app.env`
(non-rahasia, masuk Git di `example-app/env/*.env`) + `secrets.env` (password,
hanya di server). Grouping-nya terlihat di mana-mana: nama container &
project di Portainer, label `compose_project` di Loki, dan label `env` di
metrik Prometheus — lengkap dengan dashboard Grafana **“Example App per
Environment”**. Detail: **`tutorial-environment-dev-staging-prod.md`**.

> **Pelajaran mahal dari tahap ini:** nama service polos (`user-service`)
> di jaringan Docker bersama (`platform-net`) bertabrakan antar env — DNS
> bisa menjawab dengan container env sebelah, dan data seed kami sempat
> masuk ke database env yang salah! Solusinya: alamat antar-service memakai
> **nama unik per env** (nama container / alias ber-prefix, mis.
> `example-dev-user-service`). Di Kubernetes, masalah ini tidak ada karena
> ada namespace — di Compose, kedisiplinan penamaan adalah penggantinya.

### 16.2 Scheduler terpusat

Service keempat bergabung: **scheduler-service**. Polanya meniru koreksi dari
pengalaman kantor (scheduler di tiap service = job dobel saat replica
banyak): satu scheduler menembak `POST /jobs/cleanup-demo` ke order-service,
dengan **lock Redis (SET NX EX)** — job yang masih jalan membuat eksekusi
berikutnya tercatat `SKIPPED_LOCKED`, bukan dobel. Ada panel khususnya di FE
(panel G) dan endpoint internal job-nya **tidak** dibuka lewat gateway.
Detail & skenario demonya: **`tutorial-ha-dan-scheduler.md`** (Bagian 2).

### 16.3 HA: replica & blue-green

Order-service prod kini jalan **2 replica** di belakang gateway
(round-robin + resolve DNS per request); panel H di FE (“Hit Order-Service
10x”) membuktikan dua hostname bergantian menjawab. Untuk update tanpa
downtime ada **blue-green** di dev: `scripts/rolling-update.sh v1.1.0`
menyalakan versi baru (green) dulu, health-check, mengalihkan trafik
bertahap lewat daftar upstream gateway, mematikan blue terakhir — terbukti
**0 request gagal dari 34 request** selama proses. Detail:
**`tutorial-ha-dan-scheduler.md`** (Bagian 1). Catatan: Latihan 5 di Bab 12
sekarang sudah bisa dilanjutkan — order-service memang sudah multi-replica.

### 16.4 CI/CD: otomatis di dev, promosi manual ke staging & prod

- **CI** — GitHub Actions (`.github/workflows/ci.yml`): `go vet`, `go build`,
  `docker build` setiap push yang menyentuh `example-app/**`. (Status saat
  dokumen ini diperbarui: file workflow sudah dibuat & tervalidasi; untuk
  mulai berjalan di GitHub ia menunggu satu kali penambahan izin *scope
  workflow* pada token akses — lihat `tutorial-deploy-cicd.md` Bagian B.)
- **CD dev** — pull-based GitOps-lite: cron di VPS tiap 1 menit menjalankan
  `~/deploy/deploy-dev.sh`; commit baru di repo yang menyentuh example-app
  otomatis ter-deploy ke dev (terbukti: bump `APP_VERSION` dev ke v1.0.1
  murni lewat push, dev berubah sendiri tanpa disentuh). Log di
  `~/deploy/deploy.log`.
- **Staging & prod** — *tidak* otomatis, meniru pola kantor: promosi manual
  dengan checklist + perintah + rollback.

Detail semuanya, termasuk cara deploy manual lewat Portainer dan perbandingan
dengan Jenkins: **`tutorial-deploy-cicd.md`**.

### 16.5 Backup, restore & tambah service

- Backup/restore PostgreSQL per env sudah ada script-nya
  (`example-app/scripts/backup-db.sh`, `restore-db.sh`, hasil di
  `~/backups/example/<env>/`) dan **uji restore ke database sementara sudah
  dilakukan**. Cara menyalin data antar env juga didokumentasikan:
  **`tutorial-backup-restore-copy.md`**.
- Menambah service baru (mis. `menu-service` → `/api/menu`,
  `auth-service` → `/api/auth`) ternyata cuma soal: buat service berpola
  sama, daftarkan di Compose, tambah satu baris rute di gateway, tambah
  target scrape Prometheus. Panduan + latihannya:
  **`tutorial-tambah-service-baru.md`**.

### 16.6 Tata folder VPS sekarang

```text
~/example-app/          prod        ~/deploy/            clone repo + CD dev
~/example-app-staging/  staging     ~/backups/example/   hasil backup per env
~/example-app-dev/      dev         ~/tutorial/          semua file tutorial ini
~/devops-platform/      Portainer/Grafana/Prometheus/Loki
```

### 16.7 Penjagaan: rate limit, logging forensik & alert Telegram

Setelah sistem jalan, lapisan penjagaan ditambahkan (dokumen pendamping: **`tutorial-ratelimit-logging-alert.md`** — baca itu untuk versi lengkapnya). Ringkasnya:

- **Rate limit di gateway** per client IP, dihitung di Redis (fixed window 60 detik): semua `/api/*` maks 600/menit/IP, endpoint demo `/api/limited-ping` 10/menit/IP. Kelebihan dibalas **429 + `Retry-After`** dan tercatat sebagai log `warn` ber-field `rate_limited: true` serta metrik `status="429"`. Di FE ada panel **I. Rate Limit & Keamanan** (tombol whoami & spam 15x) untuk merasakannya sendiri.
- **Log dimaksimalkan untuk forensik**: tiap request kini mencatat `client_ip`, `user_id`, `user_agent`, `error_kind`, ukuran request/respons, dan versi service — ditambah *panic recovery* agar crash tercatat ber-`trace_id`. Skenario andalannya: "user bilang error jam segini" → saring Loki per rentang waktu + `status >= 500` → ambil `trace_id` → telusuri semua service (langkah persisnya di tutorial pendamping).
- **Alert Telegram dari Grafana Alerting** (provisioning di `platform/grafana/provisioning/alerting/`): ErrorRateHigh, RateLimitSpike, ServiceDown, HostDiskHigh → contact point `telegram`. Bot token & chat ID hidup di `.env` platform (diisi manual di server, tidak masuk Git).
- **Dashboard baru "Pusat Kendali Example App"** (provisioning, folder Example App di Grafana): filter `$env`/`$service`, stat ringkasan, tabel-tabel terurut terbesar → terkecil (container terboros CPU/RAM, endpoint teramai, service error terbanyak, IP teramai dari Loki) — menjawab pertanyaan operasional tanpa menulis query.

---

*Dokumen ini adalah catatan hidup dari sistem yang benar-benar terpasang. Kalau ada yang berubah di server (tambah service, ganti port), perbarui dokumen ini supaya tetap jadi sumber kebenaran.*
