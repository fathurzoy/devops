// Package mqx membantu publish ke RabbitMQ dengan koneksi yang dipakai ulang
// dan reconnect otomatis sederhana.
package mqx

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

type Publisher struct {
	URL string
	mu  sync.Mutex
	ch  *amqp.Channel
}

func (p *Publisher) channel() (*amqp.Channel, error) {
	if p.ch != nil && !p.ch.IsClosed() {
		return p.ch, nil
	}
	conn, err := amqp.Dial(p.URL)
	if err != nil {
		return nil, err
	}
	ch, err := conn.Channel()
	if err != nil {
		return nil, err
	}
	p.ch = ch
	return ch, nil
}

// PublishJSON mengirim map sebagai JSON ke queue (queue dibuat bila belum ada).
// trace_id ikut di body (dari pemanggil) dan juga di header AMQP.
func (p *Publisher) PublishJSON(queue string, body map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for attempt := 0; attempt < 3; attempt++ {
		ch, err := p.channel()
		if err != nil {
			time.Sleep(time.Second)
			continue
		}
		if _, err := ch.QueueDeclare(queue, false, false, false, false, nil); err != nil {
			p.ch = nil
			continue
		}
		b, _ := json.Marshal(body)
		trace, _ := body["trace_id"].(string)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = ch.PublishWithContext(ctx, "", queue, false, false, amqp.Publishing{
			ContentType: "application/json",
			Body:        b,
			Headers:     amqp.Table{"x-trace-id": trace},
		})
		cancel()
		if err == nil {
			return nil
		}
		p.ch = nil
	}
	return amqp.ErrClosed
}
