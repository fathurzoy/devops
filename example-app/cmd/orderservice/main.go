// order-service: buat order (validasi user ke user-service, simpan PostgreSQL,
// publish event ke RabbitMQ), ubah status order, dan endpoint simulasi.
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	_ "github.com/lib/pq"

	"exampleapp/internal/httpx"
	"exampleapp/internal/mqx"
)

var (
	db     *sql.DB
	pub    *mqx.Publisher
	userURL string
)

type Order struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	Item      string    `json:"item"`
	Qty       int       `json:"qty"`
	Status    string    `json:"status"`
	TraceID   string    `json:"trace_id"`
	CreatedAt time.Time `json:"created_at"`
}

func main() {
	httpx.ServiceName = httpx.Env("SERVICE_NAME", "order-service")
	userURL = httpx.Env("USER_SERVICE_URL", "http://example-user-service:8080")
	pub = &mqx.Publisher{URL: httpx.Env("RABBITMQ_URL", "")}
	var err error
	db, err = sql.Open("postgres", httpx.Env("DATABASE_URL", ""))
	if err != nil {
		panic(err)
	}
	for i := 0; i < 30; i++ {
		if err = db.Ping(); err == nil {
			break
		}
		httpx.Log("warn", "", fmt.Sprintf("menunggu postgres... percobaan %d", i+1))
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		panic(err)
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS orders(
		id SERIAL PRIMARY KEY, user_id BIGINT NOT NULL, item TEXT NOT NULL,
		qty INT NOT NULL DEFAULT 1, status TEXT NOT NULL DEFAULT 'CREATED',
		trace_id TEXT, created_at TIMESTAMPTZ NOT NULL DEFAULT now())`)
	if err != nil {
		panic(err)
	}
	httpx.Log("info", "", "order-service siap, tabel orders ok")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", httpx.HealthHandler)
	mux.HandleFunc("GET /metrics", httpx.MetricsHandler)
	mux.HandleFunc("GET /orders", listOrders)
	mux.HandleFunc("POST /orders", createOrder)
	mux.HandleFunc("POST /orders/{id}/status", updateStatus)
	mux.HandleFunc("GET /simulate/error", simError)
	mux.HandleFunc("GET /simulate/slow", simSlow)
	port := httpx.Env("PORT", "8080")
	httpx.Log("info", "", "listen :"+port)
	if err := http.ListenAndServe(":"+port, httpx.Middleware(mux)); err != nil {
		panic(err)
	}
}

func createOrder(w http.ResponseWriter, r *http.Request) {
	trace := httpx.TraceID(r.Context())
	var in struct {
		UserID int64  `json:"user_id"`
		Item   string `json:"item"`
		Qty    int    `json:"qty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.UserID == 0 || in.Item == "" {
		httpx.WriteJSON(w, 400, map[string]any{"error": "user_id dan item wajib diisi", "trace_id": trace})
		return
	}
	if in.Qty <= 0 {
		in.Qty = 1
	}
	// 1) Validasi user ke user-service (trace_id diteruskan otomatis)
	req, _ := httpx.NewRequest(r.Context(), "GET", fmt.Sprintf("%s/users/%d", userURL, in.UserID), nil)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		httpx.WriteJSON(w, 502, map[string]any{"error": "user-service tidak terjangkau: " + err.Error(), "trace_id": trace})
		return
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode == 404 {
		httpx.Log("warn", trace, "order ditolak: user tidak ditemukan", "user_id", in.UserID)
		httpx.WriteJSON(w, 422, map[string]any{"error": "user tidak ditemukan, order dibatalkan", "trace_id": trace})
		return
	}
	if resp.StatusCode != 200 {
		httpx.WriteJSON(w, 502, map[string]any{"error": "user-service error: " + string(body), "trace_id": trace})
		return
	}
	httpx.Log("info", trace, "validasi user OK dari user-service", "user_id", in.UserID)
	// 2) Simpan ke PostgreSQL
	var o Order
	err = db.QueryRowContext(r.Context(),
		"INSERT INTO orders(user_id,item,qty,status,trace_id) VALUES($1,$2,$3,'CREATED',$4) RETURNING id,user_id,item,qty,status,trace_id,created_at",
		in.UserID, in.Item, in.Qty, trace).
		Scan(&o.ID, &o.UserID, &o.Item, &o.Qty, &o.Status, &o.TraceID, &o.CreatedAt)
	if err != nil {
		httpx.WriteJSON(w, 500, map[string]any{"error": err.Error(), "trace_id": trace})
		return
	}
	// 3) Publish event ke RabbitMQ (untuk notification-service & feed FE2)
	ev := map[string]any{
		"type": "order.created", "kind": "order.created",
		"order_id": o.ID, "user_id": o.UserID, "item": o.Item, "qty": o.Qty, "status": o.Status,
		"from": "fe1", "target": "all", "trace_id": trace,
		"text": fmt.Sprintf("Order baru #%d: %s x%d (status %s)", o.ID, o.Item, o.Qty, o.Status),
	}
	mqStatus := "published: order.created"
	if err := pub.PublishJSON("order.created", ev); err != nil {
		mqStatus = "GAGAL publish: " + err.Error()
		httpx.Log("error", trace, "gagal publish order.created ke RabbitMQ: "+err.Error(), "order_id", o.ID)
	} else {
		httpx.Log("info", trace, "event order.created terpublish ke RabbitMQ", "order_id", o.ID)
	}
	httpx.WriteJSON(w, 201, map[string]any{
		"order": o, "trace_id": trace, "rabbitmq": mqStatus,
		"flow": []string{"api-gateway", "order-service", "user-service (validasi)", "postgres (simpan)", "rabbitmq (order.created)", "notification-service (konsumsi)"},
	})
}

func listOrders(w http.ResponseWriter, r *http.Request) {
	trace := httpx.TraceID(r.Context())
	rows, err := db.QueryContext(r.Context(), "SELECT id,user_id,item,qty,status,trace_id,created_at FROM orders ORDER BY id DESC LIMIT 100")
	if err != nil {
		httpx.WriteJSON(w, 500, map[string]any{"error": err.Error(), "trace_id": trace})
		return
	}
	defer rows.Close()
	orders := []Order{}
	for rows.Next() {
		var o Order
		_ = rows.Scan(&o.ID, &o.UserID, &o.Item, &o.Qty, &o.Status, &o.TraceID, &o.CreatedAt)
		orders = append(orders, o)
	}
	httpx.WriteJSON(w, 200, map[string]any{"orders": orders, "trace_id": trace})
}

func updateStatus(w http.ResponseWriter, r *http.Request) {
	trace := httpx.TraceID(r.Context())
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var in struct {
		Status string `json:"status"`
		Actor  string `json:"actor"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	if in.Status != "APPROVED" && in.Status != "REJECTED" && in.Status != "DONE" && in.Status != "CREATED" {
		httpx.WriteJSON(w, 400, map[string]any{"error": "status harus APPROVED / REJECTED / DONE / CREATED", "trace_id": trace})
		return
	}
	if in.Actor == "" {
		in.Actor = "fe2"
	}
	var o Order
	err := db.QueryRowContext(r.Context(),
		"UPDATE orders SET status=$1 WHERE id=$2 RETURNING id,user_id,item,qty,status,trace_id,created_at", in.Status, id).
		Scan(&o.ID, &o.UserID, &o.Item, &o.Qty, &o.Status, &o.TraceID, &o.CreatedAt)
	if err == sql.ErrNoRows {
		httpx.WriteJSON(w, 404, map[string]any{"error": "order tidak ditemukan", "trace_id": trace})
		return
	}
	if err != nil {
		httpx.WriteJSON(w, 500, map[string]any{"error": err.Error(), "trace_id": trace})
		return
	}
	ev := map[string]any{
		"type": "order.status.changed", "kind": "order.status.changed",
		"order_id": o.ID, "status": o.Status, "from": in.Actor, "target": "fe1", "trace_id": trace,
		"text": fmt.Sprintf("Order #%d status berubah menjadi %s (oleh %s)", o.ID, o.Status, in.Actor),
	}
	mqStatus := "published: order.status.changed"
	if err := pub.PublishJSON("order.status.changed", ev); err != nil {
		mqStatus = "GAGAL publish: " + err.Error()
		httpx.Log("error", trace, "gagal publish order.status.changed: "+err.Error(), "order_id", o.ID)
	} else {
		httpx.Log("info", trace, "status order diubah & event terpublish", "order_id", o.ID, "status_baru", o.Status)
	}
	httpx.WriteJSON(w, 200, map[string]any{"order": o, "trace_id": trace, "rabbitmq": mqStatus})
}

func simError(w http.ResponseWriter, r *http.Request) {
	trace := httpx.TraceID(r.Context())
	httpx.Log("error", trace, "SIMULASI ERROR 500: ini contoh log level error untuk belajar di Loki/Grafana", "endpoint", "/simulate/error")
	httpx.WriteJSON(w, 500, map[string]any{"error": "Simulasi error 500 dari order-service. Cari trace_id ini di Loki untuk melihat log error-nya.", "trace_id": trace})
}

func simSlow(w http.ResponseWriter, r *http.Request) {
	trace := httpx.TraceID(r.Context())
	ms, _ := strconv.Atoi(r.URL.Query().Get("ms"))
	if ms <= 0 {
		ms = 2000
	}
	if ms > 10000 {
		ms = 10000
	}
	httpx.Log("warn", trace, "SIMULASI SLOW: mulai tidur", "ms", ms)
	time.Sleep(time.Duration(ms) * time.Millisecond)
	httpx.WriteJSON(w, 200, map[string]any{"slept_ms": ms, "trace_id": trace, "pesan": "Request ini sengaja lambat. Lihat duration_ms di log & grafik latency di Grafana."})
}
