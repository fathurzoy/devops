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
9. Menguji Alert Sampai Masuk Telegram (Chaos Test — Khusus DEV)
10. Dashboard Grafana "Pusat Kendali Example App"
11. Latihan Belajar

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
| `user_id` | Identitas user dari header `X-User-Id` bila ada (fondasi untuk auth kelak; sekarang biasanya kosong) | `42` |
| `method` | Method HTTP | `POST` |
| `endpoint` | Pola rute logis (label metrik), mis. `/api/orders` | `/api/orders` |
| `path` | Path URL persis yang diminta | `/orders/17/status` |
| `query_params` | **Nama** parameter query saja (nilainya tidak dicatat, demi privasi) | `ms` |
| `status` | Kode status HTTP respons | `500` |
| `duration_ms` | Lama pemrosesan request ini | `12.4` |
| `user_agent` | Identitas browser/aplikasi pemanggil | `Mozilla/5.0 ...` |
| `request_bytes` | Ukuran body request yang diterima | `128` |
| `response_bytes` | Ukuran respons yang dikirim | `342` |
| `error` | Teks status untuk `status >= 400` | `Internal Server Error` |
| `error_kind` | Jenis error: `http_5xx`, `downstream_error`, `downstream_timeout`, `rate_limited`, `panic_recovered`, dll | `downstream_timeout` |
| `rate_limited` | `true` bila request ini ditolak rate limiter | `true` |
| `test` | `true` bila request bagian dari uji/chaos test (header `X-Test`) | `true` |
| `message` | Ringkasan kejadian | `request selesai` |

Hal lain yang sekarang dijamin middleware bersama (`internal/httpx`):

- **Panic recovery**: bila handler panik, proses **tidak mati**. Panik tercatat sebagai log `error` dengan `error_kind: "panic_recovered"` dan field `panic` berisi nilainya, klien menerima 500 yang rapi, dan `trace_id` tetap utuh.
- **Identitas menular antar-service**: gateway meneruskan `X-Client-Ip` & `X-User-Id` ke service downstream, jadi log order-service/notification-service mencatat IP & user asal yang sama — bukan IP sesama container.
- **Promtail tidak perlu diubah**: Loki mengekstrak semua field ini **saat query** memakai parser `| json`, jadi query lama `{container="..."}` tetap bekerja persis seperti sebelumnya; field baru tinggal dipakai sebagai filter.

---

## 5. Panduan Forensik: "User Bilang Error Jam Segini"

Ini skenario paling nyata di pekerjaan support/DevOps: user tidak tahu apa-apa selain *"tadi jam 2-an error"*. Dengan log lama (minim field), kamu buta. Dengan log sekarang, langkahnya:

1. **Perjelas jamnya** sebisanya: "jam 2" = rentang 14:00–15:00. Catat juga user-nya mengaku sedang apa (buat order? buka FE2?).
2. Buka **Grafana → Explore**, pilih datasource **Loki**, atur **rentang waktu** persis di pojok kanan atas (mis. Custom range 14:00–15:00, zona waktu Asia/Tokyo).
3. **Saring error-nya dulu** (query lengkap ada di Bab 6):

   ```logql
   {container=~"example-.+"} | json | status >= 500
   ```

   Hasilnya semua baris error pada jam itu dari semua service, terbaru di atas.
4. **Persempit bila tahu ciri-cirinya**: dari IP user (`client_ip`), endpoint (`path`/`endpoint` field log), atau env (`env="production"`).
5. **Ambil `trace_id`** dari baris yang paling cocok dengan cerita user (waktunya, endpoint-nya).
6. **Telusuri `trace_id` itu** — query `|= "<trace_id>"` memperlihatkan perjalanan lengkap request tersebut: masuk gateway jam berapa, diteruskan ke service apa, service mana yang membalas error, `error_kind`-nya apa (downstream timeout? DB? panik?), dan berapa lama tiap langkah. Inilah jawaban "error-nya karena apa" yang bisa kamu sampaikan balik ke user.
7. Bila ternyata bukan error aplikasi melainkan **serangan/uji**: cek apakah `client_ip`-nya asing & mendominasi (dashboard Pusat Kendali, panel IP Paling Sering Hit), dan apakah barisnya bertanda `test=true` (berarti itu uji chaos kita sendiri, bukan kejadian asli).

Dengan cara yang sama kamu bisa menjawab pertanyaan lain: "siapa yang menghapus/mengubah ini?" (saring per `client_ip`/`path` pada rentang waktu), "kenapa lambat?" (`duration_ms` besar + endpoint yang sama), dan "request saya yang barusan mana?" (minta user sebutkan jam + buka dari IP mana).

---

## 6. Contoh Query Loki Siap Salin

Semua query di bawah dijalankan di Grafana → Explore → datasource Loki, rentang waktu disesuaikan kebutuhan.

**Semua log env production saja:**

```logql
{container=~"example-.+"} | json | env = "production"
```

**Per IP tertentu (melacak satu pemanggil):**

```logql
{container=~"example-.+"} | json | client_ip = "198.51.100.77"
```

**Semua error 5xx (bahan forensik):**

```logql
{container=~"example-.+"} | json | status >= 500
```

**Error pada satu endpoint:**

```logql
{container=~"example-.+"} | json | status >= 500 | endpoint = "/api/orders"
```

**Mengikuti satu trace_id lintas service:**

```logql
{container=~"example-.+"} |= "GANTI_DENGAN_TRACE_ID"
```

**Request yang ditolak rate limiter:**

```logql
{container=~"example-.+"} | json | status = 429
```

**IP paling ramai dalam rentang waktu (statistik):**

```logql
topk(10, sum by (client_ip) (count_over_time({container=~"example-.+"} | json | client_ip != "" [1h])))
```

**Request lambat (di atas 1 detik):**

```logql
{container=~"example-.+"} | json | duration_ms > 1000
```

**Hasil run scheduler (SUKSES / dilewati / gagal):**

```logql
{container=~"example-.+"} | json | service = "scheduler-service" | message =~ "scheduler: (job selesai|eksekusi DILEWATI|job GAGAL).*"
```

---

## 7. Memblokir IP yang Mengganggu

Log memberi tahu *siapa*; memblokir adalah tindakan terpisah. Urutan dari yang paling ringan (dan cara kita di setup ini):

1. **Andalkan rate limit dulu.** Untuk spam biasa, 429 sudah cukup menahan beban — penyerang menerima penolakan murah, service tetap sehat. Banyak kasus selesai di sini.
2. **Blokir di gateway (daftar blokir aplikasi).** Cara paling terarah untuk example app: tambahkan daftar IP terlarang yang diperiksa middleware gateway sebelum rate limit (IP terdaftar → langsung 403). Saat tulisan ini dibuat daftar blokir permanen **belum** diaktifkan — mekanismenya disiapkan dari sisi logging (IP terbukti dari log) dan penambahannya adalah perubahan kode kecil di `cmd/gateway` (PR seperti biasa: dev dulu, uji, promosi). Dokumentasikan IP & alasannya di commit.
3. **Blokir di Caddy (reverse proxy)** untuk service yang berada di belakang domain: tambahkan matcher `remote_ip` pada blok situs terkait yang membalas 403/abort. Cocok untuk budtech/gakupay.
4. **Blokir di Tencent Cloud Security Group** (paling kuat): tolak IP pada aturan inbound untuk port terkait, atau semua port. Ini memutus penyerang sebelum mencapai server sama sekali — gunakan untuk IP yang menyerang banyak port sekaligus.
5. **Fail2ban** (otomatis, untuk SSH): membaca log auth dan memblokir IP yang gagal login berulang ke firewall host. Belum terpasang di setup ini; jalur manual di atas cukup untuk example app.

Prinsipnya: blokir **sedekat mungkin ke sumber masalah, sesempit mungkin cakupannya** (satu IP, port yang relevan), dan selalu bisa dicabut. Catat setiap blokir: IP, tanggal, alasan, bukti log-nya.

---

## 8. Alert Telegram — Arsitektur & Setup Token

### Arsitekturnya

```text
Prometheus (metrik) ──► Grafana Alerting (rules dievaluasi tiap 1 menit)
                             │ kondisi terpenuhi selama "for"
                             ▼
                        status FIRING ──► Notification Policy ──► Contact Point "telegram"
                             │                                            │
                             │ kondisi pulih                              ▼
                             ▼                                     Bot Telegram ──► chat kamu
                        status RESOLVED ──► notifikasi "sudah pulih" terkirim juga
```

Komponennya dibagi dua jalur (alasannya teknis dan jujur, lihat catatan di bawah):

- **Alert rules** di-provision sebagai file: `platform/grafana/provisioning/alerting/rules.yaml` — grup **"Example App"** berisi rule-rule di bawah ini. Tidak ada secret di sana, jadi file provisioning aman & masuk Git.
- **Contact point `telegram` + notification policy** (kebijakan default: semua alert → `telegram`, dikelompokkan per folder/alertname/env) dibuat lewat **API Grafana** oleh script idempoten `platform/scripts/setup-telegram-contact-point.sh`, yang membaca token dari `.env` server.

> **Catatan Grafana 13**: provisioning *file* untuk contact point Telegram ternyata tidak bisa diandalkan di versi ini — validasinya menolak secret-nya (`could not find Bot Token in settings`) baik token dibaca dari `$__env{}` maupun `$__file{}`, dan pada beberapa boot Grafana masuk restart-loop karenanya. Jalur API resmi adalah cara yang terbukti bekerja; script-nya ada di repo dan bisa dijalankan ulang kapan pun (aman, tidak menduplikasi).

### Rule yang aktif

| Rule | Kondisi | Arti & tindak lanjut |
|---|---|---|
| ErrorRateHigh | Error 5xx **> 10 dalam 5 menit**, bertahan (`for`) 1 menit | Ada yang rusak beneran — cek service error terbanyak & forensik Bab 5 |
| RateLimitSpike | 429 **> 20 dalam 5 menit**, bertahan 1 menit | Ada yang spam/brute force — cek IP teramai (Bab 6/7) |
| ServiceDown | Target scrape example `up == 0` selama 2 menit | Container mati/tidak terjangkau — cek Portainer |
| HostDiskHigh | Disk `/` > 85% selama 5 menit | Bersihkan image/log lama sebelum penuh |
| ContainerCpuHigh | CPU container order-service > 0,15 core (jendela 1 menit), bertahan 1 menit | Beban CPU tak wajar di order-service |

Arti `for`: kondisi harus **bertahan** selama itu dulu sebelum alert dinyatakan firing — penahan agar lonjakan sesaat (yang memang normal sesekali) tidak jadi spam notifikasi.

### Menyiapkan token Telegram (dilakukan user, sekali saja)

1. Di Telegram, chat ke **@BotFather** → `/newbot` → ikuti langkahnya → kamu menerima **bot token** (bentuknya `123456:ABC-...`). Simpan seperti password.
2. Chat apa saja ke bot barumu (mis. "halo") — ini wajib agar bot boleh mengirim ke chat kamu.
3. Ambil **chat ID**: buka `https://api.telegram.org/bot<TOKEN_KAMU>/getUpdates` di browser; cari `"chat":{"id":123456789` — angka itulah chat ID kamu.
4. Di server, edit `~/devops-platform/.env` dan isi dua baris (jangan kirim token ini ke siapa pun/ke chat mana pun selain tempat aman):

   ```ini
   TELEGRAM_BOT_TOKEN=<token dari BotFather>
   TELEGRAM_CHAT_ID=<chat id kamu>
   ```

5. Terapkan dari folder platform (membuat contact point + policy lewat API; tidak perlu restart Grafana):

   ```bash
   cd ~/devops-platform && bash scripts/setup-telegram-contact-point.sh
   ```

6. Uji: cara paling meyakinkan adalah **chaos test Bab 9** — picu spike error di FE dev dan saksikan alert firing mengirim pesan ke Telegram, lalu resolved mengirim pesan pulih. (Tombol Test di UI Grafana → Alerting → Contact points juga bisa dipakai.)

Catatan: file `.env` tidak pernah masuk Git; di repo hanya ada `.env.example` berisi nama variabelnya. Bila bot token bocor, revoke dari BotFather (`/revoke`) dan buat baru — bot tidak punya akses apa pun selain mengirim pesan ke chat yang memulai percakapan dengannya.

---

## 9. Menguji Alert Sampai Masuk Telegram (Chaos Test — Khusus DEV)

Alert yang tidak pernah diuji adalah alert yang belum tentu bekerja. Karena itu gateway punya **fasilitas uji (chaos test)**: endpoint yang sengaja membuat lonjakan agar kamu bisa menyaksikan seluruh rantainya bekerja — seperti fire drill untuk alarm kebakaran.

### Di mana & bagaimana mengaksesnya

Di FE1 (semua env) ada panel **J. Uji Alert & Beban**. Statusnya diperiksa otomatis ke `GET /api/test/status`:

- **Env development** (`:3400`): panel **AKTIF**, tombol bisa dipakai.
- **Env staging & production**: panel tampil **NONAKTIF** dengan penjelasan — endpoint-nya membalas **404**. Ini disengaja dan tidak bisa ditawar dari FE.

### Tombol-tombolnya

| Tombol (endpoint) | Yang terjadi | Alert yang ditargetkan |
|---|---|---|
| Spike Error 500 — `POST /api/test/spike-errors {count}` (default 30, **maks 100**) | Gateway menembak `/simulate/error` order-service `count` kali → metrik 5xx menumpuk | **ErrorRateHigh** |
| Spike 429 — `POST /api/test/spike-429 {count}` (default 40, **maks 60**) | Gateway menembak `/api/limited-ping` dari **IP uji khusus** `198.51.100.77` (TEST-NET) melewati limit 10/menit → tumpukan 429. IP kamu sendiri tidak ikut terbakar kuotanya | **RateLimitSpike** |
| Beban CPU — `POST /api/test/load-cpu {seconds}` (default 15, **maks 30 detik**) | Order-service membakar CPU terbatas (lihat batas aman di bawah) | **ContainerCpuHigh** + panel CPU dashboard |
| Spam Sukses — `POST /api/test/spam-ok {count}` (default 100, **maks 200**) | Request 200 cepat beruntun ke endpoint ringan | *Tidak ada* — pembanding visual: trafik naik, error tetap hijau |

Semua endpoint penguji membalas ringkasan JSON: berapa yang dibuat, alert apa yang ditargetkan, dan perkiraan menit sampai alert menyala.

### Alur waktu yang diharapkan (spike error / spike 429)

```text
T+0:00  tombol ditekan; respons ringkasan langsung kembali
T+0:30  panel error/429 di dashboard Pusat Kendali mulai memerah
T+1:00  evaluasi rule berikutnya menangkap kondisi (jendela 5 menit sudah di atas ambang)
T+2:00  "for" 1 menit terpenuhi → alert FIRING
T+2:30  notifikasi Telegram masuk (group_wait ±30 detik)
...     lonjakan berhenti → hitungan meluruh keluar dari jendela 5 menit
T+±7:00 kondisi tak lagi terpenuhi → alert RESOLVED → Telegram menerima pesan pulih
```

Angka persisnya bisa bergeser 1–2 menit (interval evaluasi 1 menit), itu normal.

**Firing vs resolved**: *firing* = masalah sedang terjadi dan kamu sedang diberi tahu. *Resolved* = kondisi sudah kembali di bawah ambang; Grafana mengirim notifikasi penutupnya juga. Alert yang firing lalu resolved sendiri setelah uji adalah bukti rantai sehat — alert yang **tidak pernah resolved** berarti masalahnya masih berlangsung.

### Batas aman (HARD LIMITS — sengaja dikunci di kode & konfigurasi)

User menekankan: *"jangan sampai benar-benar mati server yang bikin ribet"*. Karena itu:

1. **Hanya dev yang bisa.** Saklarnya env `ENABLE_TEST_ENDPOINTS`: default `false`; hanya `env/dev.env` yang `true`. Prod & staging selalu `false` → 404. Mematikannya total = ubah env itu, deploy ulang env terkait.
2. **Endpoint penguji dikecualikan dari rate limit** (kalau tidak, pengujinya sendiri yang kena tahan) — tetapi **targetnya tetap kena limit** (itu intinya spike-429), dan semua aktivitas penguji tercatat di log dengan `test=true` serta di metrik. Tidak ada yang anonim.
3. **Spike error/429/spam murni request HTTP ringan** dengan cap keras di kode (100 / 60 / 200). Tidak ada penulisan data, tidak ada penghapusan apa pun.
4. **Beban CPU dibuat sangat terbatas**:
   - durasi **maks 30 detik**, berhenti sendiri (auto-stop);
   - pola **duty-cycle**: sibuk 50 ms lalu tidur 50 ms — tidak memonopoli core;
   - **kunci Redis satu-satuan**: burn kedua ditolak (409) selama yang pertama jalan; tanpa Redis, endpoint menolak bekerja;
   - container **order-service dev dibatasi `cpus: "0.5"`** di Compose (maksimal setengah dari 2 vCPU host) — aplikasi asli (budtech, gaku-pay, tomodachi, dll) tidak akan kehabisan napas.
5. Selama pengujian resmi, load average & RAM host dipantau; ambang berhenti: load 1-menit > ±3 atau RAM available < 300 MB → pengujian dihentikan.

> ⚠️ **Jangan pernah membuka endpoint seperti ini di produksi sungguhan** tanpa perlindungan (auth/token + allowlist IP). Di sini ia aman karena tiga hal: hanya dev, cap keras, dan server ini milikmu untuk belajar. Di kantor, fasilitas semacam ini biasanya hidup di tool terpisah (chaos engineering platform) dengan approval — bukan menempel di aplikasi.

---

## 10. Dashboard Grafana "Pusat Kendali Example App"

Dashboard lama (Node Exporter Full, cAdvisor exporter, Example App per Environment) tetap ada. Yang baru ini dibuat khusus agar **ramah dibaca tanpa hafal PromQL**:

- **URL**: `http://43.167.194.197:3100/d/pusat-kendali-example/pusat-kendali-example-app` (folder **Example App** di Grafana; ter-provision dari file `platform/grafana/provisioning/dashboards/`).
- **Setiap panel punya deskripsi** (arahkan kursor ke ikon ⓘ di judul panel) yang menjelaskan cara membacanya dan apa tindakan lanjutannya.
- **Filter di atas**: `Environment` dan `Service` (multi-pilih, default All). Semua panel mengikuti filter ini — pilih `production` + `order-service` untuk fokus ke satu service di prod.
- **Semua tabel terurut dari terbesar ke terkecil** — baris teratas selalu tersangka pertama.

Peta pertanyaan → panel:

| Pertanyaanmu | Panel yang menjawab |
|---|---|
| "Sistem lagi ramai tidak?" | Stat **Total Request per Menit** + grafik **Trafik Request per Environment** |
| "Ada error tidak? Di service apa?" | Stat **Error 5xx** → tabel **Service dengan Error Terbanyak** → panel **Log Error Terbaru** |
| "Ada yang spam/brute force?" | Stat **Kena Rate Limit 429** → panel **IP Paling Sering Hit** → forensik per-IP di Bab 6 |
| "VPS berat karena apa?" | Stat **CPU/RAM Host %** → tabel **Container Paling Boros CPU/RAM** |
| "Endpoint apa yang paling laku/diincar?" | Tabel **Endpoint Paling Sering Di-hit** |
| "Penjadwalan otomatis sehat?" | Panel **Status Scheduler Terakhir** (SUKSES / SKIPPED_LOCKED / GAGAL dari log) |
| "Service ada yang mati?" | Stat **Service Example yang Up** (15 target = 5 service × 3 env) |

Panel error & 429 diberi **ambang warna** (hijau → kuning → merah): saat chaos test berjalan, kamu akan melihatnya memerah — tanda visual bahwa yang kamu uji benar-benar terbaca sistem.

---

## 11. Latihan Belajar

1. **Rasakan 429**: Di FE dev, klik "Spam Endpoint Terbatas 15x" (panel I). Catat request ke berapa 429 mulai, dan berapa `Retry-After`-nya. Tunggu semenit, ulangi — harus 200 lagi.
2. **Lacak dirimu sendiri**: Klik "Cek IP Saya", salin IP-mu, lalu cari di Loki dengan query per `client_ip`. Temukan semua request yang barusan kamu buat dari berbagai panel.
3. **Forensik pura-pura**: Minta teman (atau jadwalkan) menekan "Simulasi Error 500" di FE prod tanpa memberitahu jam pastinya. Dengan hanya petunjuk "sekitar jam X", temukan baris errornya, sebutkan `trace_id`, endpoint, dan `error_kind`-nya.
4. **Fire drill penuh**: Di FE dev jalankan **Spike Error 500 (30x)**. Ukur dengan stopwatch: berapa lama sampai panel memerah, sampai alert firing di Grafana (Alerting → Alert rules), sampai Telegram berbunyi. Lalu tunggu resolved-nya dan catat total siklusnya. Ulangi dengan **Spike 429**.
5. **Baca beban dengan aman**: Jalankan **Beban CPU 20 detik** sambil membuka dashboard (rentang 15 menit). Seberapa tinggi garis CPU container order-service dev naik? Kenapa tidak sampai 100% host? (Petunjuk: duty-cycle + batas `cpus: 0.5`.)
6. **Audit chaos**: Tulis query Loki yang menampilkan **hanya** aktivitas chaos test (`test = true`) dalam 1 jam terakhir, kelompokkan per endpoint. Inilah jejak audit yang akan ditanyakan bila fasilitas uji dipakai di tempat kerja.

---

*Dokumen pendamping: `tutorial-devops-vps-lengkap.md` (Bab 16.7), `tutorial-deploy-cicd.md` (promosi & operasional), `tutorial-ha-dan-scheduler.md`. Bila mengubah ambang alert/limit di sini, perbarui juga file provisioning & tutorial ini agar tetap sinkron.*
