# Tutorial: Menambah Service Baru & Mendaftarkannya ke Gateway

Pertanyaan awalnya sederhana: “kalau ada `menu-service` yang melayani
`/api/menu/...` dan `auth-service` yang melayani `/api/auth/...`, cara
menambahkannya bagaimana?” Jawabannya: buat service-nya (pola yang sama
dengan service yang sudah ada), daftarkan ke Compose, lalu **tambahkan satu
baris rute di gateway**. Tutorial ini memandu langkahnya secara umum, memakai
`menu-service` sebagai contoh yang bisa kamu praktikkan.

## Peta Rute Gateway Saat Ini

Gateway (`example-app/cmd/gateway/main.go`, fungsi `route`) adalah satu-satunya
pintu masuk. Tabel rutenya sekarang:

| Pola URL | Diteruskan ke | Keterangan |
|---|---|---|
| `/api/health/all` | semua backend | fan-out health check |
| `/api/users`, `/api/cache` | user-service | |
| `/api/orders`, `/api/simulate`, `/api/orders/whoami`, `/api/ha/check` | order-service (daftar upstream, round-robin) | lihat tutorial HA |
| `/api/notifications`, `/api/events`, `/api/rabbitmq/publish` | notification-service | |
| `/api/scheduler/status`, `/api/scheduler/run-now` | scheduler-service | |
| `/api/menu` → **menu-service (baru)** | | akan kamu tambahkan di latihan |
| `/api/auth` → **auth-service (baru)** | | pola yang sama dengan menu |

FE tidak pernah berubah alamat tujuannya — FE selalu memanggil gateway.
Menambah service di belakang gateway **tidak mengubah FE sama sekali**,
kecuali kamu mau menambah tombol untuk fitur barunya.

## Langkah 1 — Buat Kode Service

Salin pola service paling sederhana (mis. `cmd/notification` atau
`cmd/scheduler`) ke folder baru `cmd/menuservice/main.go`. Kerangka minimal
yang wajib ada di setiap service kita:

```go
package main

import (
    "net/http"
    "exampleapp/internal/httpx"
)

func main() {
    httpx.ServiceName = httpx.Env("SERVICE_NAME", "menu-service")
    mux := http.NewServeMux()
    mux.HandleFunc("GET /health", httpx.HealthHandler)     // wajib: health
    mux.HandleFunc("GET /metrics", httpx.MetricsHandler)   // wajib: metrik Prometheus
    mux.HandleFunc("GET /menus", listMenus)                // endpoint milikmu
    port := httpx.Env("PORT", "8080")
    httpx.Log("info", "", "menu-service listen :"+port)
    http.ListenAndServe(":"+port, httpx.Middleware(mux))   // wajib: middleware trace_id + log JSON + env
}
```

Dengan memakai `httpx.Middleware`, service barumu **otomatis** dapat: log JSON
ber-field `env` & `trace_id`, header `X-Trace-Id` diteruskan, dan metrik
`http_requests_total{service,env,...}`. Tidak ada konfigurasi tambahan.

## Langkah 2 — Daftarkan ke Dockerfile & Compose

Dockerfile kita generik (satu file membangun semua service lewat argumen
`CMD`), jadi cukup tambahkan blok service di `docker-compose.yml`:

```yaml
  menu-service:
    build:
      context: .
      dockerfile: Dockerfile
      args:
        CMD: menuservice
    image: example-menu-service:${IMG_TAG:-prod}
    container_name: ${NAME_PREFIX:-example-}menu-service
    restart: unless-stopped
    environment:
      SERVICE_NAME: menu-service
      ENV_NAME: ${ENV_NAME:-production}
      APP_VERSION: ${APP_VERSION:-v1.0.0}
      PORT: "8080"
    mem_limit: 96m
    networks:
      - example-net
      - platform-net     # <- penting agar bisa di-scrape Prometheus & dikenali dari jaringan platform
```

## Langkah 3 — Tambahkan Rute di Gateway

Di `cmd/gateway/main.go`:

1. Baca alamat service baru dari env: `menuURL = httpx.Env("MENU_SERVICE_URL", "http://menu-service:8080")`
2. Tambahkan satu `case` di fungsi `route` (letakkan **sebelum** rute yang
   lebih umum bila prefix-nya bertabrakan):

   ```go
   case strings.HasPrefix(p, "/api/menu"):
       proxy(w, r, menuURL, strings.TrimPrefix(p, "/api"))
   ```

3. Tambahkan `MENU_SERVICE_URL: http://menu-service:8080` di blok
   `environment` service `gateway` pada Compose.
4. (Opsional tapi bagus) masukkan `menu-service` ke peta `services` di fungsi
   `healthAll`, supaya ikut muncul di tombol “Cek Health Semua Service”.

## Langkah 4 — Tambahkan Target Scrape Prometheus

Edit `~/devops-platform/prometheus/prometheus.yml`: pada job env yang sesuai,
tambahkan `"<prefix>menu-service:8080"` ke daftar targets (prefix per env:
`example-`, `example-staging-`, `example-dev-`). Restart **hanya** container
Prometheus:

```bash
cd ~/devops-platform && docker compose restart prometheus
curl -s http://127.0.0.1:9091/api/v1/targets | grep -o 'menu-service[^"]*'
```

## Langkah 5 — Checklist Verifikasi

- [ ] `docker compose up -d --build menu-service gateway` di env dev dulu.
- [ ] `curl http://127.0.0.1:18100/api/health/all` → menu-service `ok`.
- [ ] `curl http://127.0.0.1:18100/api/menu/menus` → data keluar lewat gateway.
- [ ] Loki: `{container=~"example-dev.+"} |= "<trace_id>"` memperlihatkan log
      gateway **dan** menu-service dengan trace_id yang sama.
- [ ] Prometheus targets: menu-service `up`; query
      `http_requests_total{service="menu-service"}` menghasilkan data.
- [ ] Panel/tombol FE ditambahkan bila memang ada fitur untuk pengguna.

## Catatan untuk auth-service

Sama persis polanya, dengan dua tambahan konseptual: (1) auth-service biasanya
yang menerbitkan token (mis. JWT) — gateway-lah yang nanti memverifikasi token
itu di setiap request sebelum meneruskan, jadi logika ceknya ditambah di
middleware gateway, bukan di tiap service; (2) endpoint login harus diberi
rate-limit & log audit yang jelas. Keduanya bahan lanjutan yang cocok
dibangun setelah latihan menu-service ini beres.

## Latihan

Buatlah `menu-service` betulan di env **dev** dengan data menu statis (JSON di
memori: nama + harga), rute `/api/menu`, dan satu tombol “Lihat Menu” di FE1
dev. Selesaikan checklist Langkah 5 satu per satu — setelah ini, kamu sudah
menguasai siklus hidup lengkap sebuah microservice di platform ini.
