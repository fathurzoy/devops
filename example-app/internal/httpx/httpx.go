// Package httpx berisi utilitas bersama semua service contoh:
// log JSON terstruktur, middleware trace_id, dan metrics Prometheus manual.
package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

var ServiceName = "unknown"

type ctxKey struct{}

var logMu sync.Mutex

// Log menulis satu baris log JSON ke stdout.
// Field wajib: time, level, service, trace_id, message (+ field tambahan dari kv berpasangan).
func Log(level, traceID, msg string, kv ...any) {
	m := map[string]any{
		"time":     time.Now().UTC().Format(time.RFC3339Nano),
		"level":    level,
		"service":  ServiceName,
		"trace_id": traceID,
		"message":  msg,
	}
	for i := 0; i+1 < len(kv); i += 2 {
		if k, ok := kv[i].(string); ok {
			m[k] = kv[i+1]
		}
	}
	b, _ := json.Marshal(m)
	logMu.Lock()
	fmt.Fprintln(os.Stdout, string(b))
	logMu.Unlock()
}

// NewTraceID membuat trace_id acak 32 hex char.
func NewTraceID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// TraceID mengambil trace_id dari context (diisi Middleware).
func TraceID(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKey{}).(string); ok {
		return v
	}
	return ""
}

// --- Metrics Prometheus manual ---
var (
	metricMu     sync.Mutex
	reqCounts    = map[string]int64{}
	reqDurSum    = map[string]float64{}
	reqDurCount  = map[string]int64{}
)

func record(endpoint, method string, status int, dur time.Duration) {
	metricMu.Lock()
	defer metricMu.Unlock()
	k := endpoint + "|" + method + "|" + fmt.Sprint(status)
	reqCounts[k]++
	reqDurSum[endpoint] += dur.Seconds()
	reqDurCount[endpoint]++
}

func escLabel(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s)
}

// MetricsHandler melayani GET /metrics format Prometheus.
func MetricsHandler(w http.ResponseWriter, r *http.Request) {
	metricMu.Lock()
	defer metricMu.Unlock()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	var keys []string
	for k := range reqCounts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Fprintln(w, "# HELP http_requests_total Total request HTTP per endpoint & status")
	fmt.Fprintln(w, "# TYPE http_requests_total counter")
	for _, k := range keys {
		p := strings.SplitN(k, "|", 3)
		fmt.Fprintf(w, "http_requests_total{service=\"%s\",endpoint=\"%s\",method=\"%s\",status=\"%s\"} %d\n",
			escLabel(ServiceName), escLabel(p[0]), escLabel(p[1]), escLabel(p[2]), reqCounts[k])
	}
	fmt.Fprintln(w, "# HELP http_request_duration_seconds Durasi request HTTP (sum & count)")
	fmt.Fprintln(w, "# TYPE http_request_duration_seconds summary")
	var eps []string
	for e := range reqDurCount {
		eps = append(eps, e)
	}
	sort.Strings(eps)
	for _, e := range eps {
		fmt.Fprintf(w, "http_request_duration_seconds_sum{service=\"%s\",endpoint=\"%s\"} %f\n", escLabel(ServiceName), escLabel(e), reqDurSum[e])
		fmt.Fprintf(w, "http_request_duration_seconds_count{service=\"%s\",endpoint=\"%s\"} %d\n", escLabel(ServiceName), escLabel(e), reqDurCount[e])
	}
}

// statusWriter menangkap status code untuk log & metrics.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}
func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = 200
	}
	return s.ResponseWriter.Write(b)
}

// Middleware: trace_id (ambil dari header X-Trace-Id atau buat baru),
// log JSON per request, dan catat metrics.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trace := r.Header.Get("X-Trace-Id")
		if trace == "" {
			trace = NewTraceID()
		}
		w.Header().Set("X-Trace-Id", trace)
		ctx := context.WithValue(r.Context(), ctxKey{}, trace)
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r.WithContext(ctx))
		status := sw.status
		if status == 0 {
			status = 200
		}
		dur := time.Since(start)
		endpoint := r.Pattern
		if endpoint == "" {
			endpoint = r.URL.Path
		}
		record(endpoint, r.Method, status, dur)
		level := "info"
		if status >= 500 {
			level = "error"
		} else if status >= 400 {
			level = "warn"
		}
		Log(level, trace, "request selesai",
			"method", r.Method, "endpoint", endpoint, "status", status,
			"duration_ms", float64(dur.Microseconds())/1000.0, "path", r.URL.Path)
	})
}

// NewRequest membuat request keluar yang otomatis membawa header X-Trace-Id dari context.
func NewRequest(ctx context.Context, method, url string, body *strings.Reader) (*http.Request, error) {
	var req *http.Request
	var err error
	if body == nil {
		req, err = http.NewRequestWithContext(ctx, method, url, nil)
	} else {
		req, err = http.NewRequestWithContext(ctx, method, url, body)
	}
	if err != nil {
		return nil, err
	}
	if t := TraceID(ctx); t != "" {
		req.Header.Set("X-Trace-Id", t)
	}
	return req, nil
}

// WriteJSON menulis response JSON.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// HealthHandler handler GET /health standar.
func HealthHandler(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, 200, map[string]any{"service": ServiceName, "status": "ok", "trace_id": TraceID(r.Context())})
}

// Env membaca env dengan default.
func Env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
