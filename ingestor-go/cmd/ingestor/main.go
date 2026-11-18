// Comando ingestor: recebe telemetria via HTTP (Fiber), valida e publica no RabbitMQ.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	amqp "github.com/rabbitmq/amqp091-go"

	"fleettrack/ingestor-go/internal/handler"
	"fleettrack/ingestor-go/internal/publisher"
	"fleettrack/pkg/config"
	"fleettrack/pkg/logger"
	"fleettrack/pkg/mq"
)

func main() {
	if err := run(); err != nil {
		slog.Error("falha fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	log := logger.New("ingestor")
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	conn, err := mq.Dial(ctx, config.String("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"), log)
	if err != nil {
		return err
	}
	defer conn.Close()

	setup, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("abrir canal de setup: %w", err)
	}
	if err := mq.DeclareTopology(setup); err != nil {
		return err
	}
	_ = setup.Close()

	pub, err := publisher.New(conn,
		config.Int("PUBLISHER_WORKERS", 8),
		config.Int("PUBLISHER_QUEUE_SIZE", 10000), log)
	if err != nil {
		return err
	}

	h := handler.New(pub, pub.Stats, log, config.Int("MAX_BATCH_SIZE", 500))
	app := fiber.New(fiber.Config{
		AppName:               "fleettrack-ingestor",
		DisableStartupMessage: true,
		ReadTimeout:           5 * time.Second,
		WriteTimeout:          5 * time.Second,
		BodyLimit:             2 * 1024 * 1024,
	})
	app.Use(recover.New())
	app.Use(handler.AccessLog(log))
	h.Register(app)

	addr := ":" + config.String("INGESTOR_PORT", "8080")
	errCh := make(chan error, 1)
	go func() {
		log.Info("ingestor ouvindo", "addr", addr)
		errCh <- app.Listen(addr)
	}()

	connClosed := conn.NotifyClose(make(chan *amqp.Error, 1))

	var runErr error
	select {
	case <-ctx.Done():
		log.Info("sinal de encerramento recebido")
	case err := <-errCh:
		runErr = err
	case amqpErr := <-connClosed:
		runErr = fmt.Errorf("conexão com rabbitmq perdida: %v", amqpErr)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := app.ShutdownWithContext(shutdownCtx); err != nil {
		log.Error("erro ao encerrar servidor http", "error", err)
	}
	pub.Close() // drena o buffer antes de fechar a conexão
	log.Info("ingestor encerrado")
	return runErr
}
