// Package storage persiste telemetria (COPY em lote) e alertas no TimescaleDB.
package storage

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"fleettrack/pkg/models"
)

// Postgres é o adaptador de persistência.
type Postgres struct {
	pool *pgxpool.Pool
}

// NewPostgres cria o adaptador a partir de um pool existente.
func NewPostgres(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

var telemetryColumns = []string{"time", "vehicle_id", "latitude", "longitude", "speed_kmh", "fuel_level", "engine_status"}

// CopyTelemetry insere o lote com o protocolo COPY (a forma mais rápida de ingestão no Postgres).
func (p *Postgres) CopyTelemetry(ctx context.Context, batch []models.Telemetry) error {
	_, err := p.pool.CopyFrom(ctx, pgx.Identifier{"telemetry"}, telemetryColumns,
		pgx.CopyFromSlice(len(batch), func(i int) ([]any, error) {
			t := batch[i]
			return []any{t.Timestamp, t.VehicleID, t.Latitude, t.Longitude, t.SpeedKmh, t.FuelLevel, string(t.EngineStatus)}, nil
		}))
	if err != nil {
		return fmt.Errorf("copy telemetry: %w", err)
	}
	return nil
}

// InsertAlert grava o alerta e preenche a.ID.
func (p *Postgres) InsertAlert(ctx context.Context, a *models.Alert) error {
	return p.pool.QueryRow(ctx, `
		INSERT INTO alerts (vehicle_id, type, severity, message, latitude, longitude, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id`,
		a.VehicleID, string(a.Type), string(a.Severity), a.Message, a.Latitude, a.Longitude, a.CreatedAt,
	).Scan(&a.ID)
}

// LoadGeofences carrega as cercas ativas.
func (p *Postgres) LoadGeofences(ctx context.Context) ([]models.Geofence, error) {
	rows, err := p.pool.Query(ctx, `SELECT id, name, latitude, longitude, radius_m FROM geofences WHERE active ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Geofence{}
	for rows.Next() {
		var g models.Geofence
		if err := rows.Scan(&g.ID, &g.Name, &g.Latitude, &g.Longitude, &g.RadiusM); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
