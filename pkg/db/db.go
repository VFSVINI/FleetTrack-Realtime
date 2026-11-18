// Package db cria o pool de conexões do PostgreSQL/TimescaleDB.
package db

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Connect abre o pool e aguarda o banco responder (com retentativas).
func Connect(ctx context.Context, dsn string, log *slog.Logger) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("criar pool: %w", err)
	}
	var lastErr error
	for attempt := 1; attempt <= 30; attempt++ {
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		lastErr = pool.Ping(pingCtx)
		cancel()
		if lastErr == nil {
			return pool, nil
		}
		log.Warn("banco indisponível, tentando novamente", "attempt", attempt, "error", lastErr)
		select {
		case <-ctx.Done():
			pool.Close()
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	pool.Close()
	return nil, fmt.Errorf("conectar ao banco: %w", lastErr)
}
