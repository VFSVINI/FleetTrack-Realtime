package service

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"fleettrack/pkg/models"
)

// GeofenceCache mantém um snapshot imutável das cercas, lido sem locks no hot path.
type GeofenceCache struct {
	v atomic.Pointer[[]models.Geofence]
}

// List devolve o snapshot atual (nunca modificar o slice retornado).
func (g *GeofenceCache) List() []models.Geofence {
	if p := g.v.Load(); p != nil {
		return *p
	}
	return nil
}

// Set substitui o snapshot.
func (g *GeofenceCache) Set(list []models.Geofence) { g.v.Store(&list) }

// Refresh recarrega as cercas periodicamente até o contexto ser cancelado.
func (g *GeofenceCache) Refresh(ctx context.Context, load func(context.Context) ([]models.Geofence, error), every time.Duration, log *slog.Logger) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			list, err := load(ctx)
			if err != nil {
				log.Warn("falha ao recarregar cercas virtuais", "error", err)
				continue
			}
			g.Set(list)
			log.Debug("cercas virtuais atualizadas", "count", len(list))
		}
	}
}
