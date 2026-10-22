// scheduler-service: SATU penjadwal terpusat untuk seluruh sistem.
//
// Pelajaran dari pengalaman produksi: kalau tiap service memasang scheduler
// sendiri-sendiri, saat service di-scale menjadi banyak replica, job yang
// sama jalan berkali-kali bersamaan (bentrok/dobel). Pola yang benar:
// satu scheduler-service yang MENEMBAK API service lain, dan sebelum jalan
// mengambil KUNCI (lock) di Redis (SET NX EX). Kalau lock tidak didapat,
// eksekusi dilewati (SKIPPED_LOCKED) — jadi job yang masih berjalan
// "menahan" eksekusi berikutnya, dan tidak pernah ada dua eksekusi paralel.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"exampleapp/internal/httpx"
	"exampleapp/internal/redisx"
)

const lockKey = "scheduler:lock:cleanup-demo"
const jobName = "cleanup-demo"

type Run struct {
	ID         int64   `json:"id"`
	StartedAt  string  `json:"started_at"`
	FinishedAt string  `json:"finished_at,omitempty"`
	Source     string  `json:"source"` // SCHEDULED / MANUAL
	Status     string  `json:"status"` // RUNNING / SUCCESS / SKIPPED_LOCKED / FAILED
	DurationMs float64 `json:"duration_ms"`
	TraceID    string  `json:"trace_id"`
	Detail     string  `json:"detail,omitempty"`
	Replica    string  `json:"replica,omitempty"` // replica order-service yang mengerjakan
}

var (
	redisC     *redisx.Client
	orderURL   string
	interval   int // detik
	defDurMs   int
	mu     sync.Mutex
	runs   []Run
	nextID int64 = 1
)

func main() {
	httpx.ServiceName = httpx.Env("SERVICE_NAME", "scheduler-service")
	orderURL = httpx.Env("ORDER_SERVICE_URL", "http://order-service:8080")
	redisC = &redisx.Client{Addr: httpx.Env("REDIS_ADDR", "redis:6379")}
	interval = 60
	if v := httpx.Env("SCHED_INTERVAL_SEC", ""); v != "" {
		fmt.Sscan(v, &interval)
	}
	defDurMs = 3000
	if v := httpx.Env("SCHED_JOB_DURATION_MS", ""); v != "" {
		fmt.Sscan(v, &defDurMs)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", httpx.HealthHandler)
	mux.HandleFunc("GET /metrics", httpx.MetricsHandler)
	mux.HandleFunc("GET /status", statusHandler)
	mux.HandleFunc("POST /run-now", runNowHandler)

	httpx.Log("info", "", fmt.Sprintf("scheduler-service siap: interval=%ds job=%s target=%s/jobs/%s lock=%s",
		interval, jobName, orderURL, jobName, lockKey),
		"interval_sec", interval, "job", jobName)
	go tickerLoop()

	port := httpx.Env("PORT", "8080")
	httpx.Log("info", "", "listen :"+port)
	if err := http.ListenAndServe(":"+port, httpx.Middleware(mux)); err != nil {
		panic(err)
	}
}

func tickerLoop() {
	t := time.NewTicker(time.Duration(interval) * time.Second)
	defer t.Stop()
	for range t.C {
		// Tiap tick diluncurkan sebagai goroutine: bila job sebelumnya masih
		// jalan, tick ini akan kalah lock dan tercatat SKIPPED_LOCKED —
		// persis perilaku "job yang masih aktif menahan tick berikutnya".
		go executeRun("SCHEDULED", defDurMs)
	}
}

func addRun(r Run) int64 {
	mu.Lock()
	defer mu.Unlock()
	r.ID = nextID
	nextID++
	runs = append([]Run{r}, runs...)
	if len(runs) > 50 {
		runs = runs[:50]
	}
	return r.ID
}

func updateRun(id int64, fn func(*Run)) {
	mu.Lock()
	defer mu.Unlock()
	for i := range runs {
		if runs[i].ID == id {
			fn(&runs[i])
			return
		}
	}
}

func lockHeld() (bool, string) {
	v, ok, err := redisC.Get(lockKey)
	if err != nil || !ok {
		return false, ""
	}
	return true, v
}

// executeRun menjalankan satu siklus job dengan mekanisme lock Redis.
func executeRun(source string, durationMs int) Run {
	trace := httpx.NewTraceID()
	start := time.Now()
	run := Run{StartedAt: start.UTC().Format(time.RFC3339), Source: source, Status: "RUNNING", TraceID: trace}
	id := addRun(run)

	// TTL lock HARUS lebih lama dari durasi job, supaya job tidak dianggap
	// selesai oleh penjaga lain selagi ia masih bekerja.
	ttl := durationMs/1000 + 10
	ok, err := redisC.SetNX(lockKey, trace, ttl)
	if err != nil {
		updateRun(id, func(r *Run) {
			r.Status = "FAILED"
			r.Detail = "Redis tidak terjangkau saat ambil lock: " + err.Error()
			r.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		})
		httpx.Log("error", trace, "scheduler: gagal mengambil lock Redis", "job", jobName, "source", source)
		return getRun(id)
	}
	if !ok {
		held, holder := lockHeld()
		_ = held
		updateRun(id, func(r *Run) {
			r.Status = "SKIPPED_LOCKED"
			r.Detail = "Lock sedang dipegang run lain (trace_id pemegang: " + holder + "). Eksekusi dilewati agar tidak dobel."
			r.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		})
		httpx.Log("warn", trace, "scheduler: eksekusi DILEWATI — lock masih terpegang job yang sedang jalan",
			"job", jobName, "source", source, "lock", lockKey, "pemegang_lock", holder)
		return getRun(id)
	}

	httpx.Log("info", trace, "scheduler: lock didapat, menembak API order-service untuk job "+jobName,
		"job", jobName, "source", source, "duration_ms", durationMs, "lock", lockKey)

	// Tembak API order-service dengan trace_id run ini.
	body := fmt.Sprintf(`{"duration_ms":%d,"job":"%s"}`, durationMs, jobName)
	req, err := http.NewRequest(http.MethodPost, orderURL+"/jobs/"+jobName, strings.NewReader(body))
	if err == nil {
		req.Header.Set("X-Trace-Id", trace)
	}
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
		var resp *http.Response
		resp, err = (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == 200 {
				var parsed map[string]any
				_ = json.Unmarshal(b, &parsed)
				dur := float64(time.Since(start).Microseconds()) / 1000.0
				updateRun(id, func(r *Run) {
					r.Status = "SUCCESS"
					r.DurationMs = dur
					r.FinishedAt = time.Now().UTC().Format(time.RFC3339)
					if rep, ok := parsed["replica"].(string); ok {
						r.Replica = rep
					}
					r.Detail = "order-service selesai: " + string(b)
					if len(r.Detail) > 300 {
						r.Detail = r.Detail[:300]
					}
				})
				httpx.Log("info", trace, "scheduler: job selesai SUKSES", "job", jobName, "source", source, "duration_ms", dur)
			} else {
				failRun(id, trace, source, fmt.Sprintf("order-service menjawab %d: %s", resp.StatusCode, string(b)))
			}
		}
	}
	if err != nil {
		failRun(id, trace, source, "gagal memanggil order-service: "+err.Error())
	}

	// Lepas lock hanya bila kuncinya masih milik run ini.
	if v, okGet, _ := redisC.Get(lockKey); okGet && v == trace {
		_ = redisC.Del(lockKey)
	}
	return getRun(id)
}

func failRun(id int64, trace, source, detail string) {
	updateRun(id, func(r *Run) {
		r.Status = "FAILED"
		r.Detail = detail
		r.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	})
	httpx.Log("error", trace, "scheduler: job GAGAL — "+detail, "job", jobName, "source", source)
}

func getRun(id int64) Run {
	mu.Lock()
	defer mu.Unlock()
	for _, r := range runs {
		if r.ID == id {
			return r
		}
	}
	return Run{ID: id}
}

func statusHandler(w http.ResponseWriter, r *http.Request) {
	held, holder := lockHeld()
	mu.Lock()
	list := make([]Run, len(runs))
	copy(list, runs)
	mu.Unlock()
	httpx.WriteJSON(w, 200, map[string]any{
		"service":       httpx.ServiceName,
		"env":           httpx.EnvName,
		"version":       httpx.AppVersion,
		"job":           jobName,
		"target":        orderURL + "/jobs/" + jobName,
		"interval_sec":  interval,
		"lock_key":      lockKey,
		"lock_held":     held,
		"lock_holder":   holder,
		"penjelasan":    "Satu scheduler terpusat menembak API order-service. Lock Redis memastikan tidak ada eksekusi dobel; job yang masih jalan membuat eksekusi berikutnya SKIPPED_LOCKED.",
		"runs":          list,
		"trace_id":      httpx.TraceID(r.Context()),
	})
}

func runNowHandler(w http.ResponseWriter, r *http.Request) {
	var in struct {
		DurationMs int `json:"duration_ms"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	if in.DurationMs <= 0 {
		in.DurationMs = defDurMs
	}
	if in.DurationMs > 20000 {
		in.DurationMs = 20000
	}
	run := executeRun("MANUAL", in.DurationMs)
	httpx.WriteJSON(w, 200, map[string]any{"run": run, "trace_id": run.TraceID})
}
