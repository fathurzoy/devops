// api-gateway: satu-satunya pintu masuk API dari frontend.
// Routing /api/* ke service terkait, CORS terbuka, trace_id diteruskan.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"exampleapp/internal/httpx"
)

var userURL, orderURL, notifURL string

func main() {
	httpx.ServiceName = httpx.Env("SERVICE_NAME", "api-gateway")
	userURL = httpx.Env("USER_SERVICE_URL", "http://example-user-service:8080")
	orderURL = httpx.Env("ORDER_SERVICE_URL", "http://example-order-service:8080")
	notifURL = httpx.Env("NOTIFICATION_SERVICE_URL", "http://example-notification-service:8080")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", httpx.HealthHandler)
	mux.HandleFunc("GET /metrics", httpx.MetricsHandler)
	mux.HandleFunc("/", route)
	port := httpx.Env("PORT", "8080")
	httpx.Log("info", "", "api-gateway listen :"+port)
	if err := http.ListenAndServe(":"+port, cors(httpx.Middleware(mux))); err != nil {
		panic(err)
	}
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PATCH,PUT,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Trace-Id")
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
	case strings.HasPrefix(p, "/api/users"):
		proxy(w, r, userURL, strings.TrimPrefix(p, "/api"))
	case strings.HasPrefix(p, "/api/cache"):
		proxy(w, r, userURL, strings.TrimPrefix(p, "/api"))
	case strings.HasPrefix(p, "/api/orders"):
		proxy(w, r, orderURL, strings.TrimPrefix(p, "/api"))
	case strings.HasPrefix(p, "/api/simulate"):
		proxy(w, r, orderURL, strings.TrimPrefix(p, "/api"))
	case p == "/api/rabbitmq/publish":
		proxy(w, r, notifURL, "/publish")
	case strings.HasPrefix(p, "/api/notifications"):
		proxy(w, r, notifURL, strings.TrimPrefix(p, "/api"))
	case strings.HasPrefix(p, "/api/events"):
		proxy(w, r, notifURL, strings.TrimPrefix(p, "/api"))
	default:
		httpx.WriteJSON(w, 404, map[string]any{"error": "route tidak dikenal: " + p, "trace_id": trace,
			"routes": []string{"/api/health/all", "/api/users", "/api/orders", "/api/cache", "/api/simulate/*", "/api/notifications", "/api/events", "/api/rabbitmq/publish"}})
	}
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
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
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

func healthAll(w http.ResponseWriter, r *http.Request) {
	trace := httpx.TraceID(r.Context())
	services := map[string]string{
		"user-service": userURL, "order-service": orderURL, "notification-service": notifURL,
	}
	result := map[string]any{"api-gateway": "ok"}
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
