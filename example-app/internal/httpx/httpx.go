// Package httpx berisi utilitas bersama semua service contoh:
// log JSON terstruktur, middleware trace_id, dan metrics Prometheus manual.
package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

var ServiceName = "unknown"

// EnvName & AppVersion dibaca sekali dari environment container.
// ENV_NAME: development / staging / production. APP_VERSION: versi rilis (mis. v1.0.0).
var EnvName = func() string {
	if v := os.Getenv("ENV_NAME"); v != "" {
		return v
	}
	return "production"
}()

var AppVersion = func() string {
	if v := os.Getenv("APP_VERSION"); v != "" {
		return v
	}
	return "v1.0.0"
}()

type ctxKey struct{}

var logMu sync.Mutex

// Log menulis satu baris log JSON ke stdout.
// Field wajib: time, level, service, trace_id, message (+ field tambahan dari kv berpasangan).
func Log(level, traceID, msg string, kv ...any) {
	m := map[string]any{
		"time":     time.Now().UTC().Format(time.RFC3339Nano),
		"level":    level,
		"service":  ServiceName,
		"env":      EnvName,
		"version":  AppVersion,
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

// --- Info per-request (diisi Middleware, bisa dilengkapi handler) ---

type reqInfoKey struct{}

// ReqInfo adalah catatan per-request yang dibagikan Middleware & handler:
// identitas klien, label endpoint untuk metrik, jenis error, status rate limit.
// Dipakai agar log request akhir memuat field selengkap mungkin untuk forensik.
type ReqInfo struct {
	ClientIP    string
	UserID      string
	Endpoint    string // override label endpoint metrik (mis. "/api/users")
	ErrorKind   string // mis. downstream_timeout, db_error, panic_recovered
	RateLimited bool
	Test        bool // request bagian dari uji/chaos test (header X-Test: 1) — terlihat di log untuk audit
}

// ReqInfoFrom mengambil ReqInfo dari context (nil bila bukan dari Middleware).
func ReqInfoFrom(ctx context.Context) *ReqInfo {
	if v, ok := ctx.Value(reqInfoKey{}).(*ReqInfo); ok {
		return v
	}
	return nil
}

// SetEndpoint menandai label endpoint logis untuk request ini (dipakai gateway
// yang me-routing banyak pola URL lewat satu handler).
func SetEndpoint(ctx context.Context, endpoint string) {
	if info := ReqInfoFrom(ctx); info != nil {
		info.Endpoint = endpoint
	}
}

// SetErrorKind menandai jenis error request ini (muncul sebagai field error_kind di log).
func SetErrorKind(ctx context.Context, kind string) {
	if info := ReqInfoFrom(ctx); info != nil {
		info.ErrorKind = kind
	}
}

// SetRateLimited menandai request ini ditolak rate limiter.
func SetRateLimited(ctx context.Context) {
	if info := ReqInfoFrom(ctx); info != nil {
		info.RateLimited = true
	}
}

// ClientIP menentukan IP klien: entri pertama X-Forwarded-For bila ada,
// selain itu header X-Client-Ip (diisi gateway), selain itu RemoteAddr.
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first := strings.TrimSpace(strings.Split(xff, ",")[0])
		if first != "" {
			return first
		}
	}
	if ip := strings.TrimSpace(r.Header.Get("X-Client-Ip")); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

// queryParamNames mengembalikan nama-nama parameter query (tanpa nilainya,
// agar tidak ada data sensitif yang ikut tercatat di log), terurut, dipisah koma.
func queryParamNames(raw string) string {
	if raw == "" {
		return ""
	}
	vals, err := url.ParseQuery(raw)
	if err != nil {
		return ""
	}
	names := make([]string, 0, len(vals))
	for k := range vals {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, ",")
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
		fmt.Fprintf(w, "http_requests_total{service=\"%s\",env=\"%s\",endpoint=\"%s\",method=\"%s\",status=\"%s\"} %d\n",
			escLabel(ServiceName), escLabel(EnvName), escLabel(p[0]), escLabel(p[1]), escLabel(p[2]), reqCounts[k])
	}
	fmt.Fprintln(w, "# HELP http_request_duration_seconds Durasi request HTTP (sum & count)")
	fmt.Fprintln(w, "# TYPE http_request_duration_seconds summary")
	var eps []string
	for e := range reqDurCount {
		eps = append(eps, e)
	}
	sort.Strings(eps)
	for _, e := range eps {
		fmt.Fprintf(w, "http_request_duration_seconds_sum{service=\"%s\",env=\"%s\",endpoint=\"%s\"} %f\n", escLabel(ServiceName), escLabel(EnvName), escLabel(e), reqDurSum[e])
		fmt.Fprintf(w, "http_request_duration_seconds_count{service=\"%s\",env=\"%s\",endpoint=\"%s\"} %d\n", escLabel(ServiceName), escLabel(EnvName), escLabel(e), reqDurCount[e])
	}
}

// statusWriter menangkap status code & jumlah byte respons untuk log & metrics.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status != 0 {
		return // header sudah terkirim; panggilan berikutnya diabaikan net/http juga
	}
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}
func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = 200
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}
func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		if s.status == 0 {
			s.status = 200
		}
		f.Flush()
	}
}

// Middleware: trace_id (ambil dari header X-Trace-Id atau buat baru),
// identitas klien (client_ip, user_id), panic recovery, log JSON lengkap
// per request, dan pencatatan metrics.
//
// Log request memuat field forensik lengkap: siapa (client_ip, user_id,
// user_agent), apa (method, endpoint, path, nama parameter query), kapan
// (time), hasil (status, durasi, ukuran request/respons), dan untuk
// status>=400 juga error + error_kind — modal utama menelusuri keluhan
// "error jam segini" di Loki.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trace := r.Header.Get("X-Trace-Id")
		if trace == "" {
			trace = NewTraceID()
		}
		w.Header().Set("X-Trace-Id", trace)
		info := &ReqInfo{ClientIP: ClientIP(r), UserID: r.Header.Get("X-User-Id"), Test: r.Header.Get("X-Test") == "1"}
		ctx := context.WithValue(r.Context(), ctxKey{}, trace)
		ctx = context.WithValue(ctx, reqInfoKey{}, info)
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}

		// Panic recovery: panic di handler TIDAK menjatuhkan proses; ia
		// tercatat sebagai log error ber-trace_id + respons 500.
		var panicVal any
		func() {
			defer func() { panicVal = recover() }()
			next.ServeHTTP(sw, r.WithContext(ctx))
		}()

		status := sw.status
		if panicVal != nil {
			if status == 0 {
				sw.Header().Set("Content-Type", "application/json")
				sw.WriteHeader(500)
				_, _ = sw.Write([]byte(`{"error":"internal server error","trace_id":"` + trace + `"}` + "\n"))
			}
			status = 500
			info.ErrorKind = "panic_recovered"
		}
		if status == 0 {
			status = 200
		}
		dur := time.Since(start)
		endpoint := info.Endpoint
		if endpoint == "" {
			endpoint = r.Pattern
		}
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
		kv := []any{
			"method", r.Method, "endpoint", endpoint, "status", status,
			"duration_ms", float64(dur.Microseconds()) / 1000.0,
			"path", r.URL.Path, "query_params", queryParamNames(r.URL.RawQuery),
			"client_ip", info.ClientIP, "user_id", info.UserID,
			"user_agent", r.UserAgent(),
			"request_bytes", r.ContentLength, "response_bytes", sw.bytes,
		}
		if info.RateLimited {
			kv = append(kv, "rate_limited", true)
		}
		if info.Test {
			kv = append(kv, "test", true)
		}
		msg := "request selesai"
		if status >= 400 {
			kv = append(kv, "error", http.StatusText(status))
			kind := info.ErrorKind
			if kind == "" {
				if status >= 500 {
					kind = "http_5xx"
				} else if status == 429 {
					kind = "rate_limited"
				} else {
					kind = "http_4xx"
				}
			}
			kv = append(kv, "error_kind", kind)
		}
		if panicVal != nil {
			msg = "request panik (ter-recover)"
			kv = append(kv, "panic", fmt.Sprint(panicVal))
		}
		Log(level, trace, msg, kv...)
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
	// Teruskan identitas klien agar service downstream mencatat client_ip &
	// user_id yang sama di log mereka (satu jejak forensik yang utuh).
	if info := ReqInfoFrom(ctx); info != nil {
		if info.ClientIP != "" {
			req.Header.Set("X-Client-Ip", info.ClientIP)
		}
		if info.UserID != "" {
			req.Header.Set("X-User-Id", info.UserID)
		}
		if info.Test {
			req.Header.Set("X-Test", "1")
		}
	}
	return req, nil
}

// WriteJSON menulis response JSON.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// HealthHandler handler GET /health standar (memuat env & versi aplikasi).
func HealthHandler(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, 200, map[string]any{"service": ServiceName, "status": "ok", "env": EnvName, "version": AppVersion, "trace_id": TraceID(r.Context())})
}

// Hostname mengembalikan nama container/host (dipakai sebagai identitas replica).
func Hostname() string {
	h, _ := os.Hostname()
	return h
}

// Env membaca env dengan default.
func Env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
