// notification-service: konsumsi event RabbitMQ (order.created,
// order.status.changed, manual.events, fe.events), simpan di memori,
// dan sediakan endpoint daftar pesan/event + publish untuk FE.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"exampleapp/internal/httpx"
	"exampleapp/internal/mqx"
)

type Event struct {
	ID         int64          `json:"id"`
	Queue      string         `json:"queue"`
	Kind       string         `json:"kind"`
	From       string         `json:"from"`
	Target     string         `json:"target"`
	Text       string         `json:"text"`
	OrderID    any            `json:"order_id,omitempty"`
	Status     string         `json:"status,omitempty"`
	TraceID    string         `json:"trace_id"`
	Body       map[string]any `json:"body"`
	ReceivedAt time.Time      `json:"received_at"`
}

var (
	mu     sync.Mutex
	events []Event
	nextID int64 = 1
	pub    *mqx.Publisher
	mqURL  string
)

var queues = []string{"order.created", "order.status.changed", "manual.events", "fe.events"}

func store(queue string, body map[string]any) Event {
	mu.Lock()
	defer mu.Unlock()
	str := func(k string) string { s, _ := body[k].(string); return s }
	ev := Event{
		ID: nextID, Queue: queue, Kind: str("kind"), From: str("from"), Target: str("target"),
		Text: str("text"), OrderID: body["order_id"], Status: str("status"),
		TraceID: str("trace_id"), Body: body, ReceivedAt: time.Now().UTC(),
	}
	if ev.Kind == "" {
		ev.Kind = str("type")
	}
	if ev.Target == "" {
		ev.Target = "all"
	}
	nextID++
	events = append(events, ev)
	if len(events) > 300 {
		events = events[len(events)-300:]
	}
	return ev
}

func consumeLoop() {
	for {
		if err := consumeOnce(); err != nil {
			httpx.Log("warn", "", "koneksi RabbitMQ putus/gagal, coba lagi 3 detik: "+err.Error())
			time.Sleep(3 * time.Second)
		}
	}
}

func consumeOnce() error {
	conn, err := amqp.Dial(mqURL)
	if err != nil {
		return err
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()
	httpx.Log("info", "", "terhubung ke RabbitMQ, mulai konsumsi queue: order.created, order.status.changed, manual.events, fe.events")
	for _, q := range queues {
		if _, err := ch.QueueDeclare(q, false, false, false, false, nil); err != nil {
			return err
		}
		msgs, err := ch.Consume(q, "", true, false, false, false, nil)
		if err != nil {
			return err
		}
		go func(queue string, msgs <-chan amqp.Delivery) {
			for d := range msgs {
				var body map[string]any
				if err := json.Unmarshal(d.Body, &body); err != nil {
					body = map[string]any{"text": string(d.Body)}
				}
				ev := store(queue, body)
				httpx.Log("info", ev.TraceID,
					fmt.Sprintf("pesan terkonsumsi dari queue %s (kind=%s from=%s target=%s)", queue, ev.Kind, ev.From, ev.Target),
					"queue", queue, "kind", ev.Kind, "from", ev.From, "target", ev.Target, "text", ev.Text)
			}
		}(q, msgs)
	}
	// blokir sampai koneksi mati
	closed := make(chan *amqp.Error, 1)
	conn.NotifyClose(closed)
	return <-closed
}

func filterEvents(target string) []Event {
	mu.Lock()
	defer mu.Unlock()
	out := []Event{}
	for _, e := range events {
		if target == "" || e.Target == target || e.Target == "all" {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

func main() {
	httpx.ServiceName = httpx.Env("SERVICE_NAME", "notification-service")
	mqURL = httpx.Env("RABBITMQ_URL", "")
	pub = &mqx.Publisher{URL: mqURL}
	go consumeLoop()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", httpx.HealthHandler)
	mux.HandleFunc("GET /metrics", httpx.MetricsHandler)
	mux.HandleFunc("GET /notifications", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, 200, map[string]any{"events": filterEvents(""), "trace_id": httpx.TraceID(r.Context())})
	})
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
		target := r.URL.Query().Get("target")
		httpx.WriteJSON(w, 200, map[string]any{"target_filter": target, "events": filterEvents(target), "trace_id": httpx.TraceID(r.Context())})
	})
	publish := func(queue, defKind string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			trace := httpx.TraceID(r.Context())
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in == nil {
				in = map[string]any{}
			}
			if _, ok := in["kind"]; !ok {
				in["kind"] = defKind
			}
			if _, ok := in["type"]; !ok {
				in["type"] = defKind
			}
			in["trace_id"] = trace
			if err := pub.PublishJSON(queue, in); err != nil {
				httpx.WriteJSON(w, 502, map[string]any{"error": "gagal publish ke RabbitMQ: " + err.Error(), "trace_id": trace})
				return
			}
			httpx.Log("info", trace, "event terpublish ke queue "+queue, "queue", queue, "kind", in["kind"], "from", in["from"], "target", in["target"])
			httpx.WriteJSON(w, 202, map[string]any{"published_to": queue, "event": in, "trace_id": trace})
		}
	}
	// POST /publish = test RabbitMQ manual (queue manual.events)
	mux.HandleFunc("POST /publish", publish("manual.events", "manual"))
	// POST /events = event antar-FE (queue fe.events)
	mux.HandleFunc("POST /events", publish("fe.events", "message"))

	port := httpx.Env("PORT", "8080")
	httpx.Log("info", "", "notification-service listen :"+port)
	if err := http.ListenAndServe(":"+port, httpx.Middleware(mux)); err != nil {
		panic(err)
	}
}
