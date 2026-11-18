// Package worker implementa o pool de workers que consome as entregas do RabbitMQ.
package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"fleettrack/pkg/models"
)

// Processor trata o corpo de uma mensagem.
type Processor interface {
	Process(ctx context.Context, body []byte) error
}

// Run inicia N workers que leem do mesmo canal de entregas e bloqueia até que ele
// seja fechado (Cancel do consumer ou perda de conexão) e todos terminem a mensagem atual.
func Run(deliveries <-chan amqp.Delivery, p Processor, workers int, log *slog.Logger) {
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for d := range deliveries {
				handle(d, p, id, log)
			}
		}(i)
	}
	wg.Wait()
}

func handle(d amqp.Delivery, p Processor, id int, log *slog.Logger) {
	// Contexto independente do shutdown: a mensagem em andamento deve terminar.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := p.Process(ctx, d.Body)
	switch {
	case err == nil:
		_ = d.Ack(false)
	case errors.Is(err, models.ErrInvalid):
		log.Warn("mensagem inválida enviada para a DLQ", "worker", id, "error", err)
		_ = d.Nack(false, false) // sem requeue: vai para fleet.dlx -> telemetry.dead
	default:
		log.Error("falha transitória, mensagem será reenfileirada", "worker", id, "error", err)
		time.Sleep(500 * time.Millisecond)
		_ = d.Nack(false, true)
	}
}
