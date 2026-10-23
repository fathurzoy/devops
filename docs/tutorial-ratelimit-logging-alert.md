# Tutorial: Rate Limit, Logging Forensik & Alert Telegram

### Menjaga gateway dari spam/brute force, mencatat log selengkap mungkin untuk pelacakan, dan menerima alert otomatis ke Telegram

Ditulis dalam Bahasa Indonesia, berdasarkan implementasi nyata di example app VPS `43.167.194.197` pada 23 Oktober 2026. Semua password/token **tidak ditulis** di dokumen ini — token Telegram disimpan di file `.env` platform di server.

---

## Daftar Isi

1. Gambaran Besar: Tiga Lapisan Penjagaan
2. Rate Limit — Konsep & Cara Kerja di Gateway Kita
3. Mencoba Rate Limit dari FE
4. Logging yang Dimaksimalkan — Daftar Field & Artinya
5. Panduan Forensik: "User Bilang Error Jam Segini"
6. Contoh Query Loki Siap Salin
7. Memblokir IP yang Mengganggu
8. Alert Telegram — Arsitektur & Setup Token
9. Dashboard Grafana "Pusat Kendali Example App"
10. Latihan Belajar

---

## 1. Gambaran Besar: Tiga Lapisan Penjagaan

Sistem penjagaan yang dibangun di bab ini ada tiga, dan saling melengkapi:

| Lapisan | Alat | Gunanya |
|---|---|---|
| **Pencegahan** | Rate limit di gateway (dihitung di Redis) | Menahan spam & brute force sebelum membebani service |
| **Pencatatan** | Log JSON lengkap di Loki (IP, user-agent, trace_id, error) | Bahan membuktikan & melacak apa yang terjadi, kapan, oleh siapa |
| **Peringatan** | Grafana Alerting → Telegram | Kamu diberi tahu saat ada yang aneh, tanpa harus menatap dashboard |

Prinsipnya sama seperti gedung: rate limit adalah satpam di pintu, log adalah CCTV, alert adalah alarmnya.

---

## 2. Rate Limit — Konsep & Cara Kerja di Gateway Kita

### Kenapa perlu?

Tanpa rate limit, siapa pun bisa menembak API kita ribuan kali per detik: menghabiskan CPU/RAM (DoS kecil-kecilan), mencoba-coba kredensial (brute force), atau mengeruk data (scraping). Rate limit = aturan "satu IP hanya boleh sekian request per satuan waktu".

### Kenapa per IP? Kenapa disimpan di Redis?

- **Per client IP**, karena penyerang biasanya datang dari IP tertentu — user lain yang tidak bersalah tidak ikut dihukum.
- **Disimpan di Redis**, bukan di variabel lokal gateway, karena gateway bisa jalan banyak replica: kalau hitungan disimpan di memori masing-masing replica, penyerang cukup menyebar request ke semua replica untuk melipatgandakan jatahnya. Di Redis hitungannya **satu untuk bersama** — persis alasan scheduler kita memakai lock Redis.

### Algoritma: fixed window

Yang kita pakai adalah **fixed window per menit**:

1. Setiap request masuk, gateway mengambil IP klien.
2. Gateway menjalankan `INCR ratelimit:<ip>:<kelompok>` di Redis.
3. Hit pertama pada jendela memasang `EXPIRE 60 detik` — jendela berakhir 60 detik kemudian, hitungan mulai lagi dari nol.
4. Bila hitungan **melebihi limit**, request ditolak: **HTTP 429 Too Many Requests** + header **`Retry-After: <detik>`** (sisa umur jendela — klien yang sopan akan menunggu selama itu) + body JSON:

```json
{"error":"rate_limited","limit":10,"window_sec":60,"retry_after_sec":42,"trace_id":"...","pesan":"Terlalu banyak request dari IP kamu. Coba lagi setelah Retry-After detik."}
```

5. Penolakan tetap **dicatat di log** (level `warn`, `status: 429`, `rate_limited: true`) dan di metrik (`http_requests_total{status="429"}`) — inilah bahan alert RateLimitSpike di Bab 8.

Limit yang berlaku sekarang:

| Kelompok | Limit | Untuk apa |
|---|---|---|
| Semua `/api/*` | 600 request/menit/IP | Pelindung umum; pemakaian normal jauh di bawah ini |
| `GET /api/limited-ping` | 10 request/menit/IP | Endpoint demo khusus agar kamu bisa *merasakan* 429 dengan cepat |

Catatan desain penting:
- **Fail-open**: bila Redis sedang tidak terjangkau, gateway tetap melayani request (dan mencatat warn di log). Rate limiter tidak boleh ikut menjatuhkan layanan — penjaga yang mengamuk dan mengunci semua orang di luar sama buruknya dengan tidak ada penjaga.
- **IP klien ditentukan dari `X-Forwarded-For`** (entri pertama) bila ada — karena di balik reverse proxy/load balancer, alamat TCP langsung hanyalah alamat proxy-nya. Bila tidak ada, dipakai alamat koneksi langsung. IP ini kemudian diteruskan ke service downstream lewat header `X-Client-Ip`, jadi semua service mencatat IP yang sama.
- Rangkaian perintah Redis kita (`INCR` → `EXPIRE` → `TTL`) tidak atomik 100%; untuk skala belajar & proteksi umum ini cukup. Sistem yang sangat ketat biasanya memakai skrip Lua atau algoritma token bucket/sliding window — konsepnya sama, presisinya beda.

### Di mana kodenya?

- `cmd/gateway/main.go` — fungsi `rateLimit(...)` (middleware) & endpoint `/api/limited-ping` dan `/api/whoami`.
- `internal/redisx/redisx.go` — fungsi `IncrWindow(key, windowSec)`.
- Gateway membaca alamat Redis dari env `REDIS_ADDR` (ditambahkan ke `docker-compose.yml`).

---

## 3. Mencoba Rate Limit dari FE

Buka FE1 environment mana saja (prod `:3200`, staging `:3300`, dev `:3400`), gulir ke panel **I. Rate Limit & Keamanan**:

1. **"Cek IP Saya (GET /api/whoami)"** — menampilkan IP kamu sebagaimana dilihat & dicatat server, plus user-agent browser kamu. Endpoint ini menjawab pertanyaan "di log, saya tercatat sebagai siapa?"
2. **"Spam Endpoint Terbatas 15x"** — menembak `/api/limited-ping` 15 kali berturut-turut. Hasil yang sehat:
   - Request #1–#10 → **200**
   - Request #11–#15 → **429** dengan `Retry-After` berisi sisa detik jendela
   - Tunggu 60 detik, coba lagi → kembali 200.
3. Request biasa (mis. Lihat Semua User) tetap 200 — limit umumnya 600/menit, tidak mengganggu pemakaian normal.

---

## 4. Logging yang Dimaksimalkan — Daftar Field & Artinya

Setiap request yang masuk ke service mana pun kini menghasilkan **satu baris log JSON** yang memuat field lengkap. Ini disengaja dibuat "berisik tapi terstruktur": saat ada masalah, kamu tidak kekurangan bukti.

| Field | Arti | Contoh |
|---|---|---|
| `time` | Waktu request selesai (UTC, presisi nano) | `2026-10-23T05:47:11.123Z` |
| `level` | `info` (2xx/3xx), `warn` (4xx), `error` (5xx/panik) | `error` |
| `service` | Service yang menulis log ini | `order-service` |
| `env` | `production` / `staging` / `development` | `production` |
| `version` | Versi rilis service (APP_VERSION) | `v1.1.0` |
| `trace_id` | ID unik per request, **sama di semua service** yang dilewati | `9f2c...` |
| `client_ip` | IP klien asal (dari X-Forwarded-For / diteruskan gateway) | `203.0.113.7` |
| `user_id` | Identitas user dari header `X-
...[truncated 12169 chars]