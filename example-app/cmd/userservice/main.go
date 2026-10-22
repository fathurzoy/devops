// user-service: CRUD user di PostgreSQL + cache Redis.
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	_ "github.com/lib/pq"

	"exampleapp/internal/httpx"
	"exampleapp/internal/redisx"
)

var (
	db    *sql.DB
	cache *redisx.Client
)

type User struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

func main() {
	httpx.ServiceName = httpx.Env("SERVICE_NAME", "user-service")
	cache = &redisx.Client{Addr: httpx.Env("REDIS_ADDR", "example-redis:6379")}
	var err error
	db, err = sql.Open("postgres", httpx.Env("DATABASE_URL", ""))
	if err != nil {
		httpx.Log("error", "", "gagal open db: "+err.Error())
		panic(err)
	}
	// tunggu PostgreSQL siap
	for i := 0; i < 30; i++ {
		if err = db.Ping(); err == nil {
			break
		}
		httpx.Log("warn", "", fmt.Sprintf("menunggu postgres... percobaan %d: %v", i+1, err))
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		panic(err)
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS users(
		id SERIAL PRIMARY KEY, name TEXT NOT NULL, email TEXT UNIQUE NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now())`)
	if err != nil {
		panic(err)
	}
	httpx.Log("info", "", "user-service siap, tabel users ok")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", httpx.HealthHandler)
	mux.HandleFunc("GET /metrics", httpx.MetricsHandler)
	mux.HandleFunc("GET /users", listUsers)
	mux.HandleFunc("POST /users", createUser)
	mux.HandleFunc("GET /users/{id}", getUser)
	mux.HandleFunc("GET /cache", cacheGet)
	mux.HandleFunc("POST /cache", cacheSet)
	mux.HandleFunc("POST /cache/counter", counterIncr)
	mux.HandleFunc("GET /cache/counter", counterGet)
	port := httpx.Env("PORT", "8080")
	httpx.Log("info", "", "listen :"+port)
	if err := http.ListenAndServe(":"+port, httpx.Middleware(mux)); err != nil {
		panic(err)
	}
}

func listUsers(w http.ResponseWriter, r *http.Request) {
	trace := httpx.TraceID(r.Context())
	if raw, hit, _ := cache.Get("users:all"); hit {
		w.Header().Set("X-Cache", "HIT")
		httpx.Log("info", trace, "list users dari REDIS CACHE (HIT)", "endpoint", "/users")
		var users []User
		_ = json.Unmarshal([]byte(raw), &users)
		httpx.WriteJSON(w, 200, map[string]any{"source": "redis-cache", "cache": "HIT", "trace_id": trace, "users": users})
		return
	}
	w.Header().Set("X-Cache", "MISS")
	rows, err := db.QueryContext(r.Context(), "SELECT id,name,email,created_at FROM users ORDER BY id")
	if err != nil {
		httpx.WriteJSON(w, 500, map[string]any{"error": err.Error(), "trace_id": trace})
		return
	}
	defer rows.Close()
	users := []User{}
	for rows.Next() {
		var u User
		_ = rows.Scan(&u.ID, &u.Name, &u.Email, &u.CreatedAt)
		users = append(users, u)
	}
	raw, _ := json.Marshal(users)
	_ = cache.SetEX("users:all", 30, string(raw))
	httpx.Log("info", trace, "list users dari POSTGRES (cache MISS), hasil disimpan ke Redis 30 detik", "endpoint", "/users", "jumlah", len(users))
	httpx.WriteJSON(w, 200, map[string]any{"source": "postgres", "cache": "MISS", "trace_id": trace, "users": users})
}

func createUser(w http.ResponseWriter, r *http.Request) {
	trace := httpx.TraceID(r.Context())
	var in struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Name == "" || in.Email == "" {
		httpx.WriteJSON(w, 400, map[string]any{"error": "name dan email wajib diisi", "trace_id": trace})
		return
	}
	var u User
	err := db.QueryRowContext(r.Context(),
		"INSERT INTO users(name,email) VALUES($1,$2) RETURNING id,name,email,created_at", in.Name, in.Email).
		Scan(&u.ID, &u.Name, &u.Email, &u.CreatedAt)
	if err != nil {
		httpx.WriteJSON(w, 409, map[string]any{"error": "gagal simpan user (email duplikat?): " + err.Error(), "trace_id": trace})
		return
	}
	_ = cache.Del("users:all")
	httpx.Log("info", trace, "user baru dibuat di postgres, cache users:all dibuang", "user_id", u.ID)
	httpx.WriteJSON(w, 201, map[string]any{"user": u, "trace_id": trace})
}

func getUser(w http.ResponseWriter, r *http.Request) {
	trace := httpx.TraceID(r.Context())
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	key := fmt.Sprintf("user:%d", id)
	if raw, hit, _ := cache.Get(key); hit {
		w.Header().Set("X-Cache", "HIT")
		var u User
		_ = json.Unmarshal([]byte(raw), &u)
		httpx.WriteJSON(w, 200, map[string]any{"source": "redis-cache", "cache": "HIT", "trace_id": trace, "user": u})
		return
	}
	w.Header().Set("X-Cache", "MISS")
	var u User
	err := db.QueryRowContext(r.Context(), "SELECT id,name,email,created_at FROM users WHERE id=$1", id).
		Scan(&u.ID, &u.Name, &u.Email, &u.CreatedAt)
	if err == sql.ErrNoRows {
		httpx.WriteJSON(w, 404, map[string]any{"error": "user tidak ditemukan", "trace_id": trace})
		return
	}
	if err != nil {
		httpx.WriteJSON(w, 500, map[string]any{"error": err.Error(), "trace_id": trace})
		return
	}
	raw, _ := json.Marshal(u)
	_ = cache.SetEX(key, 30, string(raw))
	httpx.WriteJSON(w, 200, map[string]any{"source": "postgres", "cache": "MISS", "trace_id": trace, "user": u})
}

// --- Endpoint khusus belajar Redis ---
func cacheSet(w http.ResponseWriter, r *http.Request) {
	trace := httpx.TraceID(r.Context())
	var in struct {
		Key   string `json:"key"`
		Value string `json:"value"`
		TTL   int    `json:"ttl"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Key == "" {
		httpx.WriteJSON(w, 400, map[string]any{"error": "key wajib diisi", "trace_id": trace})
		return
	}
	if in.TTL <= 0 {
		in.TTL = 60
	}
	if err := cache.SetEX(in.Key, in.TTL, in.Value); err != nil {
		httpx.WriteJSON(w, 502, map[string]any{"error": "redis: " + err.Error(), "trace_id": trace})
		return
	}
	httpx.Log("info", trace, "SETEX ke Redis", "key", in.Key, "ttl_detik", in.TTL)
	httpx.WriteJSON(w, 200, map[string]any{"ok": true, "key": in.Key, "value": in.Value, "ttl_detik": in.TTL, "trace_id": trace})
}

func cacheGet(w http.ResponseWriter, r *http.Request) {
	trace := httpx.TraceID(r.Context())
	key := r.URL.Query().Get("key")
	val, hit, err := cache.Get(key)
	if err != nil {
		httpx.WriteJSON(w, 502, map[string]any{"error": "redis: " + err.Error(), "trace_id": trace})
		return
	}
	if !hit {
		w.Header().Set("X-Cache", "MISS")
		httpx.WriteJSON(w, 404, map[string]any{"key": key, "cache": "MISS", "error": "key tidak ada / sudah expired", "trace_id": trace})
		return
	}
	w.Header().Set("X-Cache", "HIT")
	httpx.WriteJSON(w, 200, map[string]any{"key": key, "value": val, "cache": "HIT", "trace_id": trace})
}

func counterIncr(w http.ResponseWriter, r *http.Request) {
	trace := httpx.TraceID(r.Context())
	n, err := cache.Incr("counter:demo")
	if err != nil {
		httpx.WriteJSON(w, 502, map[string]any{"error": "redis: " + err.Error(), "trace_id": trace})
		return
	}
	httpx.Log("info", trace, "INCR counter:demo di Redis", "nilai", n)
	httpx.WriteJSON(w, 200, map[string]any{"key": "counter:demo", "nilai": n, "trace_id": trace})
}

func counterGet(w http.ResponseWriter, r *http.Request) {
	trace := httpx.TraceID(r.Context())
	val, hit, _ := cache.Get("counter:demo")
	n := int64(0)
	if hit {
		n, _ = strconv.ParseInt(val, 10, 64)
	}
	httpx.WriteJSON(w, 200, map[string]any{"key": "counter:demo", "nilai": n, "trace_id": trace})
}
