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
)

var (
	userURL, notifURL, schedURL string
	adminToken                  string
	orderUpstreams              atomic.Value // []string
	rrCounter                   atomic.Uint64
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
	if ups := parseUpstreams(httpx.Env("ORDER_UPSTREAMS", "http://order-service:8080")); len(ups) > 0 {
		setUpstreams(ups)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", httpx.HealthHandler)
	mux.HandleFunc("GET /metrics", httpx.MetricsHandler)
	mux.HandleFunc("/admin/", adminRoute)
	mux.HandleFunc("/", route)
	port := httpx.Env("PORT", "8080")
	httpx.Log("info", "", "api-gateway listen :"+port, "order_upstreams", strings.Join(getUpstreams(), ","))
	if err := http.ListenAndServe(":"+port, cors(httpx.Middleware(mux))); err != nil {
		panic(err)
	}
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PATCH,PUT,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Trace-Id, X-Admin-Token")
		w.Header().Set("Access-Control-Expose-Headers", "X-Trace-Id, X-Cache")
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func route(w http.ResponseWriter, r *http.Request) {
	trace := httpx.TraceID(r.Context())
	p := r.URL.Path
	switch {
	case p == "/api/health/all":
		healthAll(w, r)
	case p == "/api/orders/whoami":
		proxyOrder(w, r, "/whoami")
	case p == "/api/ha/check":
		haCheck(w, r)
	case p == "/api/scheduler/status":
		proxy(w, r, schedURL, "/status")
	case p == "/api/scheduler/run-now":
		proxy(w, r, schedURL, "/run-now")
	case strings.HasPrefix(p, "/api/users"):
		proxy(w, r, userURL, strings.TrimPrefix(p, "/api"))
	case strings.HasPrefix(p, "/api/cache"):
		proxy(w, r, userURL, strings.TrimPrefix(p, "/api"))
	case strings.HasPrefix(p, "/api/orders"):
		proxyOrder(w, r, strings.TrimPrefix(p, "/api"))
	case strings.HasPrefix(p, "/api/simulate"):
		proxyOrder(w, r, strings.TrimPrefix(p, "/api"))
	case p == "/api/rabbitmq/publish":
		proxy(w, r, notifURL, "/publish")
	case strings.HasPrefix(p, "/api/notifications"):
		proxy(w, r, notifURL, strings.TrimPrefix(p, "/api"))
	case strings.HasPrefix(p, "/api/events"):
		proxy(w, r, notifURL, strings.TrimPrefix(p, "/api"))
	default:
		httpx.WriteJSON(w, 404, map[string]any{"error": "route tidak dikenal: " + p, "trace_id": trace,
			"routes": []string{"/api/health/all", "/api/users", "/api/orders", "/api/cache", "/api/simulate/*", "/api/notifications", "/api/events", "/api/rabbitmq/publish", "/api/scheduler/status", "/api/scheduler/run-now", "/api/orders/whoami", "/api/ha/check"}})
	}
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
		httpx.Log("error", trace, "downstream tidak terjangkau: "+err.Error(), "target", url)
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
