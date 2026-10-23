// api-gateway: satu-satunya pintu masuk API dari frontend.
// Routing /api/* ke service terkait, CORS terbuka, trace_id diteruskan.
//
// Order-service diakses lewat DAFTAR UPSTREAM (ORDER_UPSTREAMS) yang dipilih
// bergantian (round-robin) — inilah yang memungkinkan replica banyak dan
// blue-green: daftar upstream aktif bisa diganti saat runtime lewat
// /admin/upstreams (dilindungi X-Admin-Token) tanpa restart gateway.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"exampleapp/internal/httpx"
	"exampleapp/internal/redisx"
)

var (
	userURL, notifURL, schedURL string
	adminToken                  string
	orderUpstreams              atomic.Value // []string
	rrCounter                   atomic.Uint64
	rateRedis                   *redisx.Client
	testEnabled                 bool // ENABLE_TEST_ENDPOINTS — chaos test, HANYA true di env dev
	proxyClient                 = &http.Client{
		Timeout: 20 * time.Second,
		Transport: &http.Transport{
			// Koneksi baru tiap request => DNS di-resolve ulang =>
			// replica di balik satu nama service terpilih bergantian.
			DisableKeepAlives: true,
		},
	}
)

func getUpstreams() []string {
	if v := orderUpstreams.Load(); v != nil {
		return v.([]string)
	}
	return []string{"http://order-service:8080"}
}

func setUpstreams(list []string) { orderUpstreams.Store(list) }

func pickOrderUpstream() string {
	list := getUpstreams()
	if len(list) == 0 {
		return "http://order-service:8080"
	}
	n := rrCounter.Add(1)
	return list[(n-1)%uint64(len(list))]
}

func parseUpstreams(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, strings.TrimSuffix(p, "/"))
		}
	}
	return out
}

func main() {
	httpx.ServiceName = httpx.Env("SERVICE_NAME", "api-gateway")
	userURL = httpx.Env("USER_SERVICE_URL", "http://user-service:8080")
	notifURL = httpx.Env("NOTIFICATION_SERVICE_URL", "http://notification-service:8080")
	schedURL = httpx.Env("SCHEDULER_SERVICE_URL", "http://scheduler-service:8080")
	adminToken = httpx.Env("GATEWAY_ADMIN_TOKEN", "")
	testEnabled = httpx.Env("ENABLE_TEST_ENDPOINTS", "false") == "true"
	rateRedis = &redisx.Client{Addr: httpx.Env("REDIS_ADDR", "redis:6379")}
	if ups := parseUpstreams(httpx.Env("ORDER_UPSTREAMS", "http://order-service:8080")); len(ups) > 0 {
		setUpstreams(ups)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", httpx.HealthHandler)
	mux.HandleFunc("GET /metrics", httpx.MetricsHandler)
	mux.HandleFunc("/admin/", adminRoute)
	mux.HandleFunc("/", route)
	port := httpx.Env("PORT", "8080")
	httpx.Log("info", "", "api-gateway listen :"+port, "order_upstreams", strings.Join(getUpstreams(), ","),
		"rate_limit", "umum 600/menit/IP, /api/limited-ping 10/menit/IP (fixed window di Redis)")
	if err := http.ListenAndServe(":"+port, cors(httpx.Middleware(rateLimit(mux)))); err != nil {
		panic(err)
	}
}

// --- Rate limiting (fixed window per menit, per client IP, disimpan di Redis) ---
//
// Kenapa di Redis (bukan variabel lokal)? Karena gateway bisa banyak replica:
// hitungan harus bersama agar limit tetap akurat. Fixed window: jendela
// 60 detik yang tetap; hitungan di-INCR, hit pertama memasang EXPIRE 60 detik.
// Bila Redis tak terjangkau, gateway FAIL-OPEN (request tetap dilayani) —
// rate limiter tidak boleh ikut menjatuhkan layanan; kegagalannya tercatat di log.
const (
	rateLimitGeneralPerMin = 600
	rateLimitPingPerMin    = 10
	rateWindowSec          = 60
)

func rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		// Endpoint penguji/chaos (/api/test/*) DIKECUALIKAN dari rate limit:
		// tugasnya memang menguji penjagaan & alert. Ia tetap tercatat di log
		// (field test=true) dan metrik, jadi pemakaiannya bisa diaudit.
		if strings.HasPrefix(r.URL.Path, "/api/test/") {
			next.ServeHTTP(w, r)
			return
		}
		ip := httpx.ClientIP(r)
		limit, suffix := rateLimitGeneralPerMin, "all"
		if r.URL.Path == "/api/limited-ping" {
			limit, suffix = rateLimitPingPerMin, "ping"
		}
		count, ttl, err := rateRedis.IncrWindow("ratelimit:"+ip+":"+suffix, rateWindowSec)
		if err != nil {
			httpx.Log("warn", httpx.TraceID(r.Context()), "rate limiter: Redis tidak terjangkau, fail-open", "client_ip", ip, "error", err.Error())
			next.ServeHTTP(w, r)
			return
		}
		if count > int64(limit) {
			if ttl <= 0 {
				ttl = rateWindowSec
			}
			httpx.SetRateLimited(r.Context())
			httpx.SetErrorKind(r.Context(), "rate_limited")
			w.Header().Set("Retry-After", strconv.FormatInt(ttl, 10))
			httpx.WriteJSON(w, http.StatusTooManyRequests, map[string]any{
				"error": "rate_limited", "limit": limit, "window_sec": rateWindowSec,
				"retry_after_sec": ttl, "trace_id": httpx.TraceID(r.Context()),
				"pesan": "Terlalu banyak request dari IP kamu. Coba lagi setelah Retry-After detik.",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PATCH,PUT,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Trace-Id, X-Admin-Token, X-User-Id")
		w.Header().Set("Access-Control-Expose-Headers", "X-Trace-Id, X-Cache, Retry-After")
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func route(w http.ResponseWriter, r *http.Request) {
	trace := httpx.TraceID(r.Context())
	ctx := r.Context()
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/api/test/"):
		testRoute(w, r)
	case p == "/api/health/all":
		httpx.SetEndpoint(ctx, "/api/health/all")
		healthAll(w, r)
	case p == "/api/whoami":
		httpx.SetEndpoint(ctx, "/api/whoami")
		whoami(w, r)
	case p == "/api/limited-ping":
		httpx.SetEndpoint(ctx, "/api/limited-ping")
		httpx.WriteJSON(w, 200, map[string]any{"pong": true, "env": httpx.EnvName, "version": httpx.AppVersion,
			"client_ip": httpx.ClientIP(r), "trace_id": trace,
			"catatan": "Endpoint demo rate limit ketat: maks 10 request/menit/IP. Kelebihan akan dibalas 429 + Retry-After."})
	case p == "/api/orders/whoami":
		httpx.SetEndpoint(ctx, "/api/orders/whoami")
		proxyOrder(w, r, "/whoami")
	case p == "/api/ha/check":
		httpx.SetEndpoint(ctx, "/api/ha/check")
		haCheck(w, r)
	case p == "/api/scheduler/status":
		httpx.SetEndpoint(ctx, "/api/scheduler/status")
		proxy(w, r, schedURL, "/status")
	case p == "/api/scheduler/run-now":
		httpx.SetEndpoint(ctx, "/api/scheduler/run-now")
		proxy(w, r, schedURL, "/run-now")
	case strings.HasPrefix(p, "/api/users"):
		httpx.SetEndpoint(ctx, "/api/users")
		proxy(w, r, userURL, strings.TrimPrefix(p, "/api"))
	case strings.HasPrefix(p, "/api/cache"):
		httpx.SetEndpoint(ctx, "/api/cache")
		proxy(w, r, userURL, strings.TrimPrefix(p, "/api"))
	case strings.HasPrefix(p, "/api/orders"):
		httpx.SetEndpoint(ctx, "/api/orders")
		proxyOrder(w, r, strings.TrimPrefix(p, "/api"))
	case strings.HasPrefix(p, "/api/simulate"):
		httpx.SetEndpoint(ctx, "/api/simulate")
		proxyOrder(w, r, strings.TrimPrefix(p, "/api"))
	case p == "/api/rabbitmq/publish":
		httpx.SetEndpoint(ctx, "/api/rabbitmq/publish")
		proxy(w, r, notifURL, "/publish")
	case strings.HasPrefix(p, "/api/notifications"):
		httpx.SetEndpoint(ctx, "/api/notifications")
		proxy(w, r, notifURL, strings.TrimPrefix(p, "/api"))
	case strings.HasPrefix(p, "/api/events"):
		httpx.SetEndpoint(ctx, "/api/events")
		proxy(w, r, notifURL, strings.TrimPrefix(p, "/api"))
	default:
		httpx.WriteJSON(w, 404, map[string]any{"error": "route tidak dikenal: " + p, "trace_id": trace,
			"routes": []string{"/api/health/all", "/api/users", "/api/orders", "/api/cache", "/api/simulate/*", "/api/notifications", "/api/events", "/api/rabbitmq/publish", "/api/scheduler/status", "/api/scheduler/run-now", "/api/orders/whoami", "/api/ha/check", "/api/whoami", "/api/limited-ping"}})
	}
}

// --- Endpoint penguji / chaos test (/api/test/*) ---
//
// Fasilitas UJI yang disengaja: membuat lonjakan error/429/trafik/CPU agar
// alert Grafana & Telegram terbukti bekerja. BATAS AMAN yang mengikat:
//   - Hanya aktif bila ENABLE_TEST_ENDPOINTS=true (di setup ini: HANYA dev).
//     Di env lain semua endpoint ini membalas 404.
//   - Dikecualikan dari rate limit (allowlist di middleware rateLimit),
//     tetapi tetap tercatat di log dengan field test=true + di metrik.
//   - Semua count/durasi di-CAP keras di kode (lihat tiap handler).
//   - Beban CPU sangat terbatas: maks 30 detik, duty-cycle, kunci Redis,
//     dan container order-service dev dibatasi cpus 0.5 di Compose.
// PERINGATAN: endpoint seperti ini TIDAK BOLEH aktif di produksi beneran
// tanpa perlindungan (token/auth) — matikan lewat ENABLE_TEST_ENDPOINTS=false.

var testClient = &http.Client{Timeout: 10 * time.Second}

// testIP adalah IP dokumentasi TEST-NET-2 (RFC 5737) yang dipakai sebagai
// "IP penyerang" pada spike-429, agar kuota rate limit IP user asli tidak
// ikut terbakar dan log mudah dikenali sebagai uji.
const testIP = "198.51.100.77"

func testRoute(w http.ResponseWriter, r *http.Request) {
	trace := httpx.TraceID(r.Context())
	p := r.URL.Path
	httpx.SetEndpoint(r.Context(), p)
	if p == "/api/test/status" {
		httpx.WriteJSON(w, 200, map[string]any{"enabled": testEnabled, "env": httpx.EnvName, "version": httpx.AppVersion,
			"trace_id": trace, "catatan": "Endpoint penguji hanya aktif di env development (ENABLE_TEST_ENDPOINTS=true)."})
		return
	}
	if !testEnabled {
		httpx.WriteJSON(w, 404, map[string]any{"error": "endpoint penguji nonaktif di env ini — chaos test hanya boleh di development", "trace_id": trace})
		return
	}
	switch p {
	case "/api/test/spike-errors":
		testSpikeErrors(w, r)
	case "/api/test/spike-429":
		testSpike429(w, r)
	case "/api/test/load-cpu":
		testLoadCPU(w, r)
	case "/api/test/spam-ok":
		testSpamOK(w, r)
	default:
		httpx.WriteJSON(w, 404, map[string]any{"error": "endpoint penguji tidak dikenal: " + p, "trace_id": trace,
			"tersedia": []string{"/api/test/status", "/api/test/spike-errors", "/api/test/spike-429", "/api/test/load-cpu", "/api/test/spam-ok"}})
	}
}

// callInternal menembak satu request internal bertanda X-Test (dan trace_id
// yang sama dengan request penguji) lalu mengembalikan status HTTP-nya.
func callInternal(r *http.Request, method, url, xff string, body []byte) (int, []byte) {
	var rd io.Reader
	if body != nil {
		rd = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(r.Context(), method, url, rd)
	if err != nil {
		return 0, nil
	}
	req.Header.Set("X-Test", "1")
	if t := httpx.TraceID(r.Context()); t != "" {
		req.Header.Set("X-Trace-Id", t)
	}
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := testClient.Do(req)
	if err != nil {
		return 0, nil
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	return resp.StatusCode, out
}

func testCount(r *http.Request, def, capMax int) int {
	var in struct {
		Count int `json:"count"`
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
	if len(body) > 0 {
		_ = json.Unmarshal(body, &in)
	}
	if in.Count <= 0 {
		return def
	}
	if in.Count > capMax {
		return capMax
	}
	return in.Count
}

func testSpikeErrors(w http.ResponseWriter, r *http.Request) {
	trace := httpx.TraceID(r.Context())
	n := testCount(r, 30, 100) // CAP KERAS: maks 100
	base := pickOrderUpstream()
	jadi := 0
	for i := 0; i < n; i++ {
		if code, _ := callInternal(r, "GET", base+"/simulate/error", "", nil); code == 500 {
			jadi++
		}
	}
	httpx.WriteJSON(w, 200, map[string]any{
		"dibuat": jadi, "diminta": n, "jenis": "error 500 dari order-service (/simulate/error)",
		"target_alert":        "ErrorRateHigh — error 5xx > 10 dalam 5 menit",
		"perkiraan_menit_sampai_alert": "±1-3 menit (evaluasi rule tiap 1 menit + for 1 menit + jeda notifikasi)",
		"setelah_berhenti":             "lonjakan keluar dari jendela 5 menit → alert resolved sendiri & Telegram menerima pesan pulih",
		"trace_id":                     trace,
	})
}

func testSpike429(w http.ResponseWriter, r *http.Request) {
	trace := httpx.TraceID(r.Context())
	n := testCount(r, 40, 60) // CAP KERAS: maks 60
	port := httpx.Env("PORT", "8080")
	lolos, ditolak := 0, 0
	for i := 0; i < n; i++ {
		code, _ := callInternal(r, "GET", "http://127.0.0.1:"+port+"/api/limited-ping", testIP, nil)
		if code == 200 {
			lolos++
		} else if code == 429 {
			ditolak++
		}
	}
	httpx.WriteJSON(w, 200, map[string]any{
		"dibuat": n, "lolos_200": lolos, "ditolak_429": ditolak, "ip_uji": testIP,
		"jenis":                "menembak /api/limited-ping dari IP uji sampai melewati limit 10/menit",
		"target_alert":         "RateLimitSpike — 429 > 20 dalam 5 menit",
		"perkiraan_menit_sampai_alert": "±1-3 menit",
		"setelah_berhenti":             "jendela 60 detik berganti & hitungan 5 menit meluruh → alert resolved sendiri",
		"trace_id":                     trace,
	})
}

func testLoadCPU(w http.ResponseWriter, r *http.Request) {
	trace := httpx.TraceID(r.Context())
	var in struct {
		Seconds int `json:"seconds"`
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
	if len(body) > 0 {
		_ = json.Unmarshal(body, &in)
	}
	if in.Seconds <= 0 {
		in.Seconds = 15
	}
	if in.Seconds > 30 {
		in.Seconds = 30 // CAP KERAS: maks 30 detik
	}
	payload, _ := json.Marshal(map[string]any{"seconds": in.Seconds})
	code, out := callInternal(r, "POST", pickOrderUpstream()+"/jobs/burn-cpu", "", payload)
	var detail map[string]any
	_ = json.Unmarshal(out, &detail)
	httpx.WriteJSON(w, 200, map[string]any{
		"dibuat": "burn CPU " + strconv.Itoa(in.Seconds) + " detik di order-service (duty-cycle, terkunci Redis, auto-stop)",
		"status_order_service": code, "detail": detail,
		"target_alert":                 "ContainerCpuHigh (CPU container order-service) — dan panel CPU dashboard naik; host dibatasi cpus 0.5 khusus dev",
		"perkiraan_menit_sampai_alert": "±2-4 menit bila ambang terlampaui selama durasi burn",
		"trace_id":                     trace,
	})
}

func testSpamOK(w http.ResponseWriter, r *http.Request) {
	trace := httpx.TraceID(r.Context())
	n := testCount(r, 100, 200) // CAP KERAS: maks 200
	port := httpx.Env("PORT", "8080")
	ok := 0
	for i := 0; i < n; i++ {
		if code, _ := callInternal(r, "GET", "http://127.0.0.1:"+port+"/api/whoami", "", nil); code == 200 {
			ok++
		}
	}
	httpx.WriteJSON(w, 200, map[string]any{
		"dibuat": ok, "diminta": n, "jenis": "request SUKSES cepat ke /api/whoami (tanpa error)",
		"target_alert":                 "tidak ada — ini pembanding visual: grafik trafik naik, panel error tetap hijau",
		"perkiraan_menit_sampai_alert": "-",
		"trace_id":                     trace,
	})
}

// whoami: memperlihatkan identitas yang dilihat & dicatat server tentang
// request ini — bahan belajar logging & forensik ("IP saya tercatat apa?").
func whoami(w http.ResponseWriter, r *http.Request) {
	info := httpx.ReqInfoFrom(r.Context())
	uid := ""
	if info != nil {
		uid = info.UserID
	}
	httpx.WriteJSON(w, 200, map[string]any{
		"service": "api-gateway", "env": httpx.EnvName, "version": httpx.AppVersion,
		"client_ip": httpx.ClientIP(r), "user_agent": r.UserAgent(), "user_id": uid,
		"trace_id": httpx.TraceID(r.Context()),
		"catatan": "client_ip & user_agent persis seperti inilah yang tercatat di log (Loki) untuk setiap request kamu.",
	})
}

// proxyOrder meneruskan ke salah satu upstream order-service (round-robin).
func proxyOrder(w http.ResponseWriter, r *http.Request, restPath string) {
	proxy(w, r, pickOrderUpstream(), restPath)
}

func proxy(w http.ResponseWriter, r *http.Request, target, restPath string) {
	trace := httpx.TraceID(r.Context())
	body, _ := io.ReadAll(r.Body)
	url := target + restPath
	if r.URL.RawQuery != "" {
		url += "?" + r.URL.RawQuery
	}
	req, err := httpx.NewRequest(r.Context(), r.Method, url, strings.NewReader(string(body)))
	if err != nil {
		httpx.WriteJSON(w, 500, map[string]any{"error": err.Error(), "trace_id": trace})
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	start := time.Now()
	resp, err := proxyClient.Do(req)
	if err != nil {
		kind := "downstream_error"
		if strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "deadline") {
			kind = "downstream_timeout"
		}
		httpx.SetErrorKind(r.Context(), kind)
		httpx.Log("error", trace, "downstream tidak terjangkau: "+err.Error(), "target", url, "error_kind", kind)
		httpx.WriteJSON(w, 502, map[string]any{"error": "downstream error: " + err.Error(), "trace_id": trace})
		return
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(out)
	httpx.Log("info", trace, fmt.Sprintf("proxy %s %s -> %s (%d)", r.Method, r.URL.Path, target, resp.StatusCode),
		"target", target, "downstream_status", resp.StatusCode, "downstream_ms", float64(time.Since(start).Microseconds())/1000.0)
}

// fetchWhoami memanggil /whoami ke satu base URL (dipakai haCheck & admin probe).
func fetchWhoami(r *http.Request, base string) (map[string]any, int, error) {
	req, err := httpx.NewRequest(r.Context(), "GET", strings.TrimSuffix(base, "/")+"/whoami", nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := proxyClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var parsed map[string]any
	if err := json.Unmarshal(b, &parsed); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("respons bukan JSON: %s", string(b))
	}
	return parsed, resp.StatusCode, nil
}

// haCheck: memukul /whoami n kali lewat mekanisme upstream order dan
// merangkum replica+version yang menjawab (bukti load balancing / HA).
func haCheck(w http.ResponseWriter, r *http.Request) {
	trace := httpx.TraceID(r.Context())
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	if n <= 0 {
		n = 10
	}
	if n > 50 {
		n = 50
	}
	answers := []any{}
	counts := map[string]int{}
	fail := 0
	for i := 0; i < n; i++ {
		base := pickOrderUpstream()
		info, code, err := fetchWhoami(r, base)
		if err != nil || code != 200 {
			fail++
			answers = append(answers, map[string]any{"upstream": base, "error": fmt.Sprint(err), "status": code})
			continue
		}
		key := fmt.Sprintf("%v|%v", info["replica"], info["version"])
		counts[key]++
		answers = append(answers, map[string]any{"upstream": base, "replica": info["replica"], "version": info["version"], "env": info["env"]})
	}
	httpx.WriteJSON(w, 200, map[string]any{
		"trace_id": trace, "total": n, "gagal": fail,
		"upstreams_aktif": getUpstreams(),
		"ringkasan":       counts, "jawaban": answers,
		"catatan": "ringkasan memperlihatkan replica (hostname) & version yang menjawab. Lebih dari 1 replica = beban terbagi (HA).",
	})
}

// --- Admin (untuk script rolling-update; dilindungi X-Admin-Token) ---

func adminRoute(w http.ResponseWriter, r *http.Request) {
	trace := httpx.TraceID(r.Context())
	if adminToken == "" || r.Header.Get("X-Admin-Token") != adminToken {
		httpx.WriteJSON(w, 403, map[string]any{"error": "admin token salah/absen", "trace_id": trace})
		return
	}
	switch {
	case r.URL.Path == "/admin/upstreams" && r.Method == http.MethodGet:
		httpx.WriteJSON(w, 200, map[string]any{"upstreams": getUpstreams(), "trace_id": trace})
	case r.URL.Path == "/admin/upstreams" && r.Method == http.MethodPost:
		var in struct {
			Upstreams []string `json:"upstreams"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || len(in.Upstreams) == 0 {
			httpx.WriteJSON(w, 400, map[string]any{"error": "body {upstreams:[...]} wajib", "trace_id": trace})
			return
		}
		setUpstreams(in.Upstreams)
		httpx.Log("warn", trace, "ADMIN: upstream order diganti -> "+strings.Join(in.Upstreams, ","), "upstreams", strings.Join(in.Upstreams, ","))
		httpx.WriteJSON(w, 200, map[string]any{"upstreams": getUpstreams(), "trace_id": trace})
	case r.URL.Path == "/admin/probe" && r.Method == http.MethodGet:
		url := r.URL.Query().Get("url")
		if url == "" {
			httpx.WriteJSON(w, 400, map[string]any{"error": "query url wajib", "trace_id": trace})
			return
		}
		info, code, err := fetchWhoami(r, url)
		if err != nil {
			httpx.WriteJSON(w, 200, map[string]any{"url": url, "ok": false, "error": err.Error(), "trace_id": trace})
			return
		}
		httpx.WriteJSON(w, 200, map[string]any{"url": url, "ok": code == 200, "status": code, "whoami": info, "trace_id": trace})
	default:
		httpx.WriteJSON(w, 404, map[string]any{"error": "admin route tidak dikenal", "trace_id": trace})
	}
}

func healthAll(w http.ResponseWriter, r *http.Request) {
	trace := httpx.TraceID(r.Context())
	services := map[string]string{
		"user-service": userURL, "order-service": getUpstreams()[0], "notification-service": notifURL, "scheduler-service": schedURL,
	}
	result := map[string]any{"api-gateway": map[string]any{"status": "ok", "env": httpx.EnvName, "version": httpx.AppVersion}}
	allOK := true
	for name, base := range services {
		req, _ := httpx.NewRequest(r.Context(), "GET", base+"/health", nil)
		resp, err := (&http.Client{Timeout: 4 * time.Second}).Do(req)
		if err != nil {
			result[name] = "DOWN: " + err.Error()
			allOK = false
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var parsed map[string]any
		_ = json.Unmarshal(b, &parsed)
		result[name] = parsed
		if resp.StatusCode != 200 {
			allOK = false
		}
	}
	status := "ok"
	if !allOK {
		status = "degraded"
	}
	httpx.WriteJSON(w, 200, map[string]any{"status": status, "trace_id": trace, "services": result,
		"catatan": "Gateway memanggil /health ke semua backend dengan trace_id yang sama. Cari trace_id ini di Loki!"})
}
