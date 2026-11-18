// Package mq encapsula a conexão e a topologia do RabbitMQ usadas pelos serviços.
package mq

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Topologia (espelhada em infra/rabbitmq/definitions.json).
const (
	ExchangeTelemetry = "fleet.telemetry"
	ExchangeDLX       = "fleet.dlx"
	QueueRaw          = "telemetry.raw"
	QueueDead         = "telemetry.dead"
	RoutingKey        = "telemetry.raw"
)

// Dial conecta ao broker com retentativas (útil na subida do docker compose).
func Dial(ctx context.Context, url string, log *slog.Logger) (*amqp.Connection, error) {
	var lastErr error
	for attempt := 1; attempt <= 30; attempt++ {
		conn, err := amqp.Dial(url)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		log.Warn("rabbitmq indisponível, tentando novamente", "attempt", attempt, "error", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return nil, fmt.Errorf("conectar ao rabbitmq: %w", lastErr)
}

// DeclareTopology cria (de forma idempotente) exchanges, filas e bindings.
// Mensagens rejeitadas (nack sem requeue) seguem para a fila telemetry.dead.
func DeclareTopology(ch *amqp.Channel) error {
	if err := ch.ExchangeDeclare(ExchangeTelemetry, "direct", true, false, false, false, nil); err != nil {
		return fmt.Errorf("declarar exchange %s: %w", ExchangeTelemetry, err)
	}
	if err := ch.ExchangeDeclare(ExchangeDLX, "fanout", true, false, false, false, nil); err != nil {
		return fmt.Errorf("declarar exchange %s: %w", ExchangeDLX, err)
	}
	args := amqp.Table{"x-dead-letter-exchange": ExchangeDLX}
	if _, err := ch.QueueDeclare(QueueRaw, true, false, false, false, args); err != nil {
		return fmt.Errorf("declarar fila %s: %w", QueueRaw, err)
	}
	if _, err := ch.QueueDeclare(QueueDead, true, false, false, false, nil); err != nil {
		return fmt.Errorf("declarar fila %s: %w", QueueDead, err)
	}
	if err := ch.QueueBind(QueueRaw, RoutingKey, ExchangeTelemetry, false, nil); err != nil {
		return fmt.Errorf("bind %s: %w", QueueRaw, err)
	}
	if err := ch.QueueBind(QueueDead, "", ExchangeDLX, false, nil); err != nil {
		return fmt.Errorf("bind %s: %w", QueueDead, err)
	}
	return nil
}
