// Comando processor: consome telemetria do RabbitMQ com um worker pool, aplica as
// regras de negócio, atualiza o Redis e persiste em lote no TimescaleDB.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"

	"fleettrack/pkg/config"
	"fleettrack/pkg/db"
	"fleettrack/pkg/logger"
	"fleettrack/pkg/mq"
	"fleettrack/processor-go/internal/cache"
	"fleettrack/processor-go/internal/rules"
	"fleettrack/processor-go/internal/service"
	"fleettrack/processor-go/internal/storage"
	"fleettrack/processor-go/internal/worker"
)

func main() {
	if err := run(); err != nil {
		slog.Error("falha fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	log := logger.New("processor")
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	workers := config.Int("PROCESSOR_WORKERS", 8)

	// --- Dependências -----------------------------------------------------
	pool, err := db.Connect(ctx, config.String("DATABASE_URL", "postgres://fleet:fleet@localhost:5432/fleet?sslmode=disable"), log)
	if err != nil {
		return err
	}
	defer pool.Close()
	pg := storage.NewPostgres(pool)

	rdb := redis.NewClient(&redis.Options{
		Addr:     config.String("REDIS_ADDR", "localhost:6379"),
		PoolSize: workers * 2,
	})
	defer rdb.Close()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("conectar ao redis: %w", err)
	}

	conn, err := mq.Dial(ctx, config.String("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"), log)
	if err != nil {
		return err
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("abrir canal: %w", err)
	}
	if err := mq.DeclareTopology(ch); err != nil {
		return err
	}
	// Prefetch limita as mensagens em voo por consumer (controle de backpressure).
	if err := ch.Qos(config.Int("PREFETCH", workers*8), 0, false); err != nil {
		return fmt.Errorf("configurar qos: %w", err)
	}

	// --- Cercas virtuais (snapshot atualizado periodicamente) --------------
	fences := &service.GeofenceCache{}
	list, err := pg.LoadGeofences(ctx)
	if err != nil {
		return fmt.Errorf("carregar cercas: %w", err)
	}
	fences.Set(list)
	log.Info("cercas virtuais carregadas", "count", len(list))
	refreshCtx, stopRefresh := context.WithCancel(ctx)
	defer stopRefresh()
	go fences.Refresh(refreshCtx, pg.LoadGeofences, config.Duration("GEOFENCE_REFRESH", 30*time.Second), log)

	// --- Pipeline -----------------------------------------------------------
	writer := storage.NewBatchWriter(pg,
		config.Int("BATCH_SIZE", 500),
		config.Duration("BATCH_INTERVAL", time.Second),
		config.Int("BATCH_BUFFER", 10000), log)

	svc := service.New(cache.NewRedis(rdb), pg, writer, fences,
		rules.Thresholds{
			SpeedLimitKmh:    config.Float("SPEED_LIMIT_KMH", 100),
			SpeedCriticalKmh: config.Float("SPEED_CRITICAL_KMH", 120),
			FuelCriticalPct:  config.Float("FUEL_CRITICAL_PCT", 15),
		},
		config.Duration("ALERT_COOLDOWN", time.Minute), log)

	host, _ := os.Hostname()
	tag := "processor-" + host
	deliveries, err := ch.Consume(mq.QueueRaw, tag, false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("consumir fila: %w", err)
	}

	done := make(chan struct{})
	go func() {
		worker.Run(deliveries, svc, workers, log)
		close(done)
	}()
	log.Info("processor consumindo", "queue", mq.QueueRaw, "workers", workers)

	connClosed := conn.NotifyClose(make(chan *amqp.Error, 1))

	var runErr error
	select {
	case <-ctx.Done():
		log.Info("sinal de encerramento recebido")
		_ = ch.Cancel(tag, false) // fecha o canal de entregas; workers terminam as mensagens em voo
	case amqpErr := <-connClosed:
		runErr = fmt.Errorf("conexão com rabbitmq perdida: %v", amqpErr)
	case <-done:
		runErr = fmt.Errorf("canal de entregas fechado inesperadamente")
	}

	select {
	case <-done:
	case <-time.After(20 * time.Second):
		// Workers ainda ativos: fechar o writer agora causaria envio em canal fechado.
		log.Warn("timeout aguardando workers; encerrando sem flush final")
		return runErr
	}
	stopRefresh()
	writer.Close() // flush final do lote pendente
	log.Info("processor encerrado")
	return runErr
}
