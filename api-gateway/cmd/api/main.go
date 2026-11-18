// Comando api-gateway: API REST (Gin) para consulta da frota, histórico, métricas e alertas.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"fleettrack/api-gateway/internal/handler"
	"fleettrack/api-gateway/internal/repository"
	"fleettrack/pkg/config"
	"fleettrack/pkg/db"
	"fleettrack/pkg/logger"
)

func main() {
	if err := run(); err != nil {
		slog.Error("falha fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	log := logger.New("api-gateway")
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, config.String("DATABASE_URL", "postgres://fleet:fleet@localhost:5432/fleet?sslmode=disable"), log)
	if err != nil {
		return err
	}
	defer pool.Close()

	rdb := redis.NewClient(&redis.Options{Addr: config.String("REDIS_ADDR", "localhost:6379")})
	defer rdb.Close()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("conectar ao redis: %w", err)
	}

	api := handler.New(
		repository.NewTimescale(pool),
		repository.NewFleet(rdb, config.Duration("ONLINE_WINDOW", 30*time.Second)),
		log)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), handler.AccessLog(log), handler.CORS())
	api.Routes(r)

	srv := &http.Server{
		Addr:              ":" + config.String("API_PORT", "8081"),
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("api-gateway ouvindo", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	var runErr error
	select {
	case <-ctx.Done():
		log.Info("sinal de encerramento recebido")
	case runErr = <-errCh:
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("erro ao encerrar servidor http", "error", err)
	}
	log.Info("api-gateway encerrado")
	return runErr
}
