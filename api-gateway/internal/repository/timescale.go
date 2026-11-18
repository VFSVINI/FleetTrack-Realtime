package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"fleettrack/pkg/models"
)

// Timescale executa as consultas históricas e de alertas.
type Timescale struct {
	pool *pgxpool.Pool
}

// NewTimescale cria o repositório.
func NewTimescale(pool *pgxpool.Pool) *Timescale { return &Timescale{pool: pool} }

// Ping verifica a conectividade com o banco.
func (r *Timescale) Ping(ctx context.Context) error { return r.pool.Ping(ctx) }

// History devolve os últimos `limit` pontos do intervalo, em ordem cronológica (para desenhar a rota).
func (r *Timescale) History(ctx context.Context, vehicleID string, from, to time.Time, limit int) ([]models.Telemetry, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT time, vehicle_id, latitude, longitude, speed_kmh, fuel_level, engine_status FROM (
			SELECT time, vehicle_id, latitude, longitude, speed_kmh, fuel_level, engine_status
			FROM telemetry
			WHERE vehicle_id = $1 AND time >= $2 AND time <= $3
			ORDER BY time DESC
			LIMIT $4
		) recent ORDER BY time ASC`, vehicleID, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.Telemetry{}
	for rows.Next() {
		var t models.Telemetry
		var engine string
		if err := rows.Scan(&t.Timestamp, &t.VehicleID, &t.Latitude, &t.Longitude, &t.SpeedKmh, &t.FuelLevel, &engine); err != nil {
			return nil, err
		}
		t.EngineStatus = models.EngineStatus(engine)
		out = append(out, t)
	}
	return out, rows.Err()
}

// SeriesPoint é um ponto da série temporal agregada por minuto.
type SeriesPoint struct {
	Bucket   time.Time `json:"bucket"`
	AvgSpeed float64   `json:"avg_speed_kmh"`
	Messages int64     `json:"messages"`
}

// SpeedSeries agrega velocidade média e volume por minuto usando time_bucket do TimescaleDB.
func (r *Timescale) SpeedSeries(ctx context.Context, minutes int) ([]SeriesPoint, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT time_bucket('1 minute', time) AS bucket, avg(speed_kmh), count(*)
		FROM telemetry
		WHERE time > now() - make_interval(mins => $1::int)
		GROUP BY bucket ORDER BY bucket`, minutes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []SeriesPoint{}
	for rows.Next() {
		var p SeriesPoint
		if err := rows.Scan(&p.Bucket, &p.AvgSpeed, &p.Messages); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// VehicleStats são as métricas das últimas 24h de um veículo (vindas do continuous aggregate).
type VehicleStats struct {
	VehicleID string  `json:"vehicle_id"`
	AvgSpeed  float64 `json:"avg_speed_kmh"`
	MaxSpeed  float64 `json:"max_speed_kmh"`
	MinFuel   float64 `json:"min_fuel_level"`
	Samples   int64   `json:"samples"`
}

// VehicleStats consulta o continuous aggregate telemetry_5m (pré-agregado em buckets de 5 min).
func (r *Timescale) VehicleStats(ctx context.Context) ([]VehicleStats, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT vehicle_id,
		       sum(sum_speed) / NULLIF(sum(samples), 0),
		       max(max_speed), min(min_fuel), sum(samples)::bigint
		FROM telemetry_5m
		WHERE bucket > now() - interval '24 hours'
		GROUP BY vehicle_id ORDER BY vehicle_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []VehicleStats{}
	for rows.Next() {
		var s VehicleStats
		if err := rows.Scan(&s.VehicleID, &s.AvgSpeed, &s.MaxSpeed, &s.MinFuel, &s.Samples); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// MessagesLastMinute conta os pontos de telemetria do último minuto.
func (r *Timescale) MessagesLastMinute(ctx context.Context) (int64, error) {
	var n int64
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM telemetry WHERE time > now() - interval '1 minute'`).Scan(&n)
	return n, err
}

// Alerts lista alertas (apenas ativos, ou todos) do mais recente para o mais antigo.
func (r *Timescale) Alerts(ctx context.Context, all bool, limit int) ([]models.Alert, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, vehicle_id, type, severity, message, latitude, longitude, created_at, acknowledged_at
		FROM alerts
		WHERE ($1 OR acknowledged_at IS NULL)
		ORDER BY created_at DESC
		LIMIT $2`, all, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.Alert{}
	for rows.Next() {
		var a models.Alert
		var typ, sev string
		if err := rows.Scan(&a.ID, &a.VehicleID, &typ, &sev, &a.Message, &a.Latitude, &a.Longitude, &a.CreatedAt, &a.AcknowledgedAt); err != nil {
			return nil, err
		}
		a.Type, a.Severity = models.AlertType(typ), models.Severity(sev)
		out = append(out, a)
	}
	return out, rows.Err()
}

// ActiveAlertCount conta os alertas ainda não reconhecidos.
func (r *Timescale) ActiveAlertCount(ctx context.Context) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE acknowledged_at IS NULL`).Scan(&n)
	return n, err
}

// AckAlert marca o alerta como reconhecido. Retorna false se não existir ou já estiver reconhecido.
func (r *Timescale) AckAlert(ctx context.Context, id int64) (bool, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE alerts SET acknowledged_at = now() WHERE id = $1 AND acknowledged_at IS NULL`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// Geofences lista as cercas ativas.
func (r *Timescale) Geofences(ctx context.Context) ([]models.Geofence, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, name, latitude, longitude, radius_m FROM geofences WHERE active ORDER BY id`)
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
