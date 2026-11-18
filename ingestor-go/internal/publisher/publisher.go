// Package publisher publica telemetria no RabbitMQ através de um pool de workers.
//
// Cada worker possui o seu próprio canal AMQP (canais não são thread-safe) e
// consome de um channel Go com buffer. Quando o buffer enche, Enqueue falha
// imediatamente com ErrBackpressure, permitindo que a API responda 503.
package publisher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"fleettrack/pkg/mq"
)

var (
	// ErrBackpressure indica que o buffer interno está cheio.
	ErrBackpressure = errors.New("fila interna cheia")
	// ErrClosed indica que o publisher já foi encerrado.
	ErrClosed = errors.New("publisher encerrado")
)

// Publisher é um pool de publicadores AMQP.
type Publisher struct {
	jobs      chan []byte
	wg        sync.WaitGroup
	log       *slog.Logger
	mu        sync.RWMutex
	closed    bool
	published atomic.Uint64
	failed    atomic.Uint64
}

// New abre um canal por worker e inicia o pool.
func New(conn *amqp.Connection, workers, queueSize int, log *slog.Logger) (*Publisher, error) {
	p := &Publisher{jobs: make(chan []byte, queueSize), log: log}
	for i := 0; i < workers; i++ {
		ch, err := conn.Channel()
		if err != nil {
			return nil, fmt.Errorf("abrir canal do worker %d: %w", i, err)
		}
		p.wg.Add(1)
		go p.worker(i, ch)
	}
	return p, nil
}

// Enqueue entrega o corpo ao pool sem bloquear.
func (p *Publisher) Enqueue(body []byte) error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed {
		return ErrClosed
	}
	select {
	case p.jobs <- body:
		return nil
	default:
		return ErrBackpressure
	}
}

// Stats devolve contadores de mensagens publicadas e com falha.
func (p *Publisher) Stats() (published, failed uint64) {
	return p.published.Load(), p.failed.Load()
}

// Close para de aceitar mensagens, drena o buffer e aguarda os workers.
func (p *Publisher) Close() {
	p.mu.Lock()
	if !p.closed {
		p.closed = true
		close(p.jobs)
	}
	p.mu.Unlock()
	p.wg.Wait()
}

func (p *Publisher) worker(id int, ch *amqp.Channel) {
	defer p.wg.Done()
	defer ch.Close()

	for body := range p.jobs {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := ch.PublishWithContext(ctx, mq.ExchangeTelemetry, mq.RoutingKey, false, false, amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Timestamp:    time.Now(),
			Body:         body,
		})
		cancel()
		if err != nil {
			p.failed.Add(1)
			p.log.Error("falha ao publicar no rabbitmq", "worker", id, "error", err)
			continue
		}
		p.published.Add(1)
	}
}
