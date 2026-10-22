# Tutorial: High Availability (Replica & Blue-Green) dan Scheduler Terpusat

Dua topik yang saling melengkapi: bagaimana layanan tetap tersedia saat
di-update/di-scale (HA), dan bagaimana pekerjaan terjadwal dijalankan dengan
aman saat layanan punya banyak replica (scheduler terpusat).

---

## Bagian 1 — HA: Replica dan Blue-Green

### Replica ≠ sekadar “banyak container”

Menjalankan order-service sebanyak 2 replica berarti ada 2 container identik
yang bisa menjawab request yang sama. Manfaatnya:

- Satu replica mati → request tetap dilayani replica lain (ketersediaan).
- Beban terbagi → tidak ada satu container yang bekerja sendirian.

Syaratnya: service harus **stateless** (tidak menyimpan data penting di
memori lokal). Order-service kita memenuhi syarat itu — data ada di
PostgreSQL, cache di Redis, antrean di RabbitMQ.

Di setup kita, prod menjalankan order-service 2 replica:

```ini
# env/prod.env
ORDER_REPLICAS=2
```

yang diterjemahkan Compose menjadi `deploy.replicas: 2`. Itu sebabnya
order-service satu-satunya service **tanpa** `container_name` — nama tetap
tidak bisa dipakai container yang jumlahnya dinamis.

### Bagaimana gateway membagi beban?

Di `cmd/gateway/main.go`:

1. Untuk order, gateway tidak memanggil satu alamat tetap, melainkan memilih
   dari **daftar upstream** secara bergantian (*round-robin*).
2. Client HTTP gateway memakai `DisableKeepAlives` — koneksi baru tiap
   request — supaya DNS service di-resolve ulang dan replica yang menjawab
   berganti-ganti (Docker menyeimbangkan IP di balik nama service).

Buktinya ada di FE: panel **“H. HA — Replica & Load Balancing”**, tombol
**Hit Order-Service 10x**. Gateway memanggil `/whoami` (endpoint yang
menjawab identitas: hostname container + versi) 10 kali dan merangkum siapa
saja yang menjawab. Di prod kamu akan melihat **2 hostname berbeda**;
di dev/staging (1 replica) hanya 1.

### Blue-green: menaikkan versi tanpa downtime

Replica saja tidak menyelesaikan soal *update*: mengganti image berarti
container lama mati dulu. **Blue-green** menyiasatinya dengan dua kelompok:

- **Blue** = versi yang sedang melayani.
- **Green** = versi baru, dinyalakan di samping blue.
- Trafik dipindah **setelah** green terbukti sehat; blue dimatikan terakhir.
- Rollback = pindahkan trafik kembali ke blue. Selesai dalam hitungan detik.

Di dev, mekanismenya memakai daftar upstream gateway yang bisa diubah saat
runtime lewat endpoint admin (dilindungi `X-Admin-Token`):

```bash
GET  /admin/upstreams                     # lihat daftar aktif
POST /admin/upstreams {"upstreams":["http://order-green:8080"]}
GET  /admin/probe?url=http://order-green:8080   # cek /whoami satu upstream
```

Scriptnya sudah jadi: `example-app/scripts/rolling-update.sh <versi-baru>`

```bash
bash ~/example-app/scripts/rolling-update.sh v1.1.0
```

Fase-fasenya (semua tercetak ber-waktu di layar):

1. Nyalakan green (service `order-green`, profil Compose `bluegreen`,
   `APP_VERSION` = versi baru).
2. Health-check green lewat `/admin/probe` sampai versinya benar.
3. Upstream → `[blue, green]` (trafik terbagi; dua versi menjawab bergantian).
4. Upstream → `[green]` (100% versi baru).
5. Matikan blue.
6. **Promosi**: `APP_VERSION` di `app.env` dev diperbarui, blue dinyalakan
   ulang sebagai versi baru, upstream dikembalikan ke `[blue]`, green
   dibersihkan. Hasil akhir konsisten — “blue” selalu berarti versi yang
   sedang live.

Selama proses, script menjalankan **checker** yang memukul
`/api/orders/whoami` lewat gateway setiap 0,5 detik dan menghitung kegagalan.
Kriteria sukses kita ketat: **0 request gagal**. Kalau green ternyata tidak
sehat di fase 2, script berhenti dan blue tidak pernah disentuh — itulah
gunanya urutan “yang baru aktif dulu, yang lama mati belakangan”.

> Bedanya rolling update ala Kubernetes: di sana orchestrator yang mengatur
> pod baru/lama secara deklaratif (`maxSurge`/`maxUnavailable`). Di sini kita
> melakukan hal yang sama secara manual dengan upstream gateway — konsepnya
> identik, alatnya lebih sederhana.

---

## Bagian 2 — Scheduler Terpusat

### Masalah lama (yang pernah terjadi di kantor)

Tiap service memasang scheduler di dalam kodenya masing-masing. Selama
replica-nya satu, semua baik-baik saja. Begitu di-scale menjadi 3 replica,
**satu job jalan 3 kali bersamaan** — data dibersihkan dobel, email terkirim
tiga kali, API pihak ketiga dipukul bertiga. Scheduler di dalam service tidak
tahu ada “dirinya yang lain”.

### Pola yang benar

```text
                 ┌────────────────────┐   POST /jobs/cleanup-demo   ┌─────────────────┐
                 │ scheduler-service  │ ──────────────────────────▶ │  order-service  │
   ticker 20-60s │ (SATU instance)    │    header X-Trace-Id        │  (boleh banyak  │
       ──────────│ ambil lock dulu!   │                             │   replica)      │
                 └─────────┬──────────┘                             └─────────────────┘
                           │ SET NX EX  scheduler:lock:cleanup-demo
                           ▼
                        Redis
```

- **Satu** scheduler-service untuk seluruh sistem. Ia tidak mengerjakan job
  apa pun sendiri — tugasnya hanya *menembak API* service pemilik pekerjaan.
- Endpoint job (`POST /jobs/cleanup-demo` di order-service) bersifat
  **internal**: hanya bisa dipanggil antar-service, **tidak** diekspos lewat
  gateway. Yang diekspos gateway hanyalah status & pemicu manual scheduler:
  `/api/scheduler/status` dan `/api/scheduler/run-now`.
- Sebelum menembak, scheduler mengambil **lock Redis**
  (`SET scheduler:lock:cleanup-demo <trace_id> NX EX <ttl>`), dengan TTL
  **lebih lama dari durasi job**. Tiga kemungkinan hasil tiap eksekusi:
  - `SUCCESS` — lock didapat, API dipanggil, job selesai, lock dilepas.
  - `SKIPPED_LOCKED` — lock sedang dipegang eksekusi lain → eksekusi ini
    **dilewati**. Inilah “job yang masih jalan menahan tick berikutnya”.
  - `FAILED` — API target error/tidak terjangkau (tercatat jelas detailnya).
- Setiap run memakai **trace_id baru**, yang sama-sama muncul di log
  scheduler-service dan order-service → telusuri di Loki untuk melihat
  sepasang log “menembak” dan “mengerjakan”.

### Membaca panel scheduler di FE

Panel **“G. Scheduler Terpusat”** di FE1 (semua env) menampilkan: env,
interval, status lock saat ini, dan tabel riwayat run (waktu, sumber
SCHEDULED/MANUAL, status, durasi, replica order-service yang mengerjakan,
trace_id + tombol salin query Loki).

Interval per env: dev 20 detik, staging 30 detik, prod 60 detik
(`SCHED_INTERVAL_SEC`) — jadi di FE dev, run `SCHEDULED` baru muncul sendiri
tiap ±20 detik tanpa kamu klik apa pun.

### Skenario demo yang wajib dicoba

1. Klik **Jalankan Job Lambat (15 detik), lalu Coba Lagi**.
2. Run pertama berjalan 15 detik memegang lock; run kedua yang ditembak
   ±1 detik kemudian akan berstatus **`SKIPPED_LOCKED`**.
3. Tunggu sampai run pertama `SUCCESS`, lalu klik **Jalankan Sekarang
   (3 detik)** → `SUCCESS`.
4. Salin query Loki dari salah satu run dan lihat log gandanya di Grafana.

### Latihan

- Ubah `SCHED_INTERVAL_SEC` dev menjadi 10, recreate scheduler-service, dan
  amati ritme run SCHEDULED berubah.
- Naikkan durasi job terjadwal (`SCHED_JOB_DURATION_MS`) melewati interval
  (mis. durasi 25 detik, interval 20 detik): sebagian tick akan tercatat
  SKIPPED_LOCKED — buktikan lock, bukan kebetulan, yang mencegah dobel.
- Diskusikan: apa yang terjadi bila scheduler-service sendiri di-scale 2?
  (Jawaban: tetap aman — mereka berebut lock yang sama; yang kalah mencatat
  SKIPPED_LOCKED. Itulah gunanya lock terdistribusi, bukan lock di memori.)
