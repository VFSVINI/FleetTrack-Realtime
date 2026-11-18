package storage

import (
	"context"
	"log/slog"
	"time"

	"fleettrack/pkg/models"
)

// Sink persiste um lote de telemetria.
type Sink interface {
	CopyTelemetry(ctx context.Context, batch []models.Telemetry) error
}

// BatchWriter acumula telemetria e grava em lote quando atinge o tamanho máximo
// ou quando o intervalo expira, o que ocorrer primeiro.
type BatchWriter struct {
	sink     Sink
	in       chan models.Telemetry
	size     int
	interval time.Duration
	log      *slog.Logger
	done     chan struct{}
}

// NewBatchWriter inicia o writer. buffer controla o backpressure sobre os workers.
func NewBatchWriter(sink Sink, size int, interval time.Duration, buffer int, log *slog.Logger) *BatchWriter {
	b := &BatchWriter{
		sink: sink, in: make(chan models.Telemetry, buffer),
		size: size, interval: interval, log: log, done: make(chan struct{}),
	}
	go b.run()
	return b
}

// Add enfileira um ponto para o próximo lote (bloqueia se o buffer estiver cheio).
func (b *BatchWriter) Add(ctx context.Context, t models.Telemetry) error {
	select {
	case b.in <- t:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close grava o que restou e encerra. Só pode ser chamado depois que ninguém mais chama Add.
func (b *BatchWriter) Close() {
	close(b.in)
	<-b.done
}

func (b *BatchWriter) run() {
	defer close(b.done)
	buf := make([]models.Telemetry, 0, b.size)
	ticker := time.NewTicker(b.interval)
	defer ticker.Stop()

	flush := func() {
		if len(buf) == 0 {
			return
		}
		b.flush(buf)
		buf = buf[:0]
	}

	for {
		select {
		case t, ok := <-b.in:
			if !ok {
				flush()
				return
			}
			buf = append(buf, t)
			if len(buf) >= b.size {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// flush tenta gravar o lote com backoff exponencial antes de descartá-lo.
func (b *BatchWriter) flush(batch []models.Telemetry) {
	backoff := 200 * time.Millisecond
	for attempt := 1; attempt <= 4; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := b.sink.CopyTelemetry(ctx, batch)
		cancel()
		if err == nil {
			b.log.Debug("lote persistido", "rows", len(batch))
			return
		}
		b.log.Warn("falha ao persistir lote", "attempt", attempt, "rows", len(batch), "error", err)
		time.Sleep(backoff)
		backoff *= 2
	}
	b.log.Error("lote descartado após retentativas", "rows", len(batch))
}
