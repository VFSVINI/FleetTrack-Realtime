-- FleetTrack-Realtime: esquema do TimescaleDB
-- Executado automaticamente pelo entrypoint do container (somente na primeira subida do volume).

CREATE EXTENSION IF NOT EXISTS timescaledb;

-- ---------------------------------------------------------------------------
-- Telemetria (série temporal) -> hypertable particionada por tempo
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS telemetry (
    time          TIMESTAMPTZ      NOT NULL,
    vehicle_id    TEXT             NOT NULL,
    latitude      DOUBLE PRECISION NOT NULL,
    longitude     DOUBLE PRECISION NOT NULL,
    speed_kmh     DOUBLE PRECISION NOT NULL,
    fuel_level    DOUBLE PRECISION NOT NULL,
    engine_status TEXT             NOT NULL
);

SELECT create_hypertable('telemetry', 'time',
                         chunk_time_interval => INTERVAL '1 day',
                         if_not_exists => TRUE);

-- Consulta típica: "últimos N pontos do veículo X"
CREATE INDEX IF NOT EXISTS idx_telemetry_vehicle_time ON telemetry (vehicle_id, time DESC);

-- Compressão nativa (chunks com mais de 7 dias) e retenção de 90 dias
ALTER TABLE telemetry SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'vehicle_id',
    timescaledb.compress_orderby   = 'time DESC'
);
SELECT add_compression_policy('telemetry', INTERVAL '7 days', if_not_exists => TRUE);
SELECT add_retention_policy('telemetry', INTERVAL '90 days', if_not_exists => TRUE);

-- ---------------------------------------------------------------------------
-- Continuous aggregate: estatísticas por veículo em buckets de 5 minutos.
-- materialized_only = false => combina dados materializados com os mais recentes (tempo real).
-- ---------------------------------------------------------------------------
CREATE MATERIALIZED VIEW IF NOT EXISTS telemetry_5m
WITH (timescaledb.continuous, timescaledb.materialized_only = false) AS
SELECT time_bucket('5 minutes', time) AS bucket,
       vehicle_id,
       sum(speed_kmh) AS sum_speed,
       max(speed_kmh) AS max_speed,
       min(fuel_level) AS min_fuel,
       count(*)        AS samples
FROM telemetry
GROUP BY bucket, vehicle_id
WITH NO DATA;

SELECT add_continuous_aggregate_policy('telemetry_5m',
    start_offset      => INTERVAL '2 days',
    end_offset        => INTERVAL '1 minute',
    schedule_interval => INTERVAL '1 minute',
    if_not_exists     => TRUE);

-- ---------------------------------------------------------------------------
-- Alertas (baixo volume, tabela relacional comum)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS alerts (
    id              BIGSERIAL        PRIMARY KEY,
    vehicle_id      TEXT             NOT NULL,
    type            TEXT             NOT NULL,
    severity        TEXT             NOT NULL CHECK (severity IN ('info', 'warning', 'critical')),
    message         TEXT             NOT NULL,
    latitude        DOUBLE PRECISION NOT NULL,
    longitude       DOUBLE PRECISION NOT NULL,
    created_at      TIMESTAMPTZ      NOT NULL DEFAULT now(),
    acknowledged_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_alerts_active  ON alerts (created_at DESC) WHERE acknowledged_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_alerts_vehicle ON alerts (vehicle_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- Cercas virtuais circulares
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS geofences (
    id        SERIAL           PRIMARY KEY,
    name      TEXT             NOT NULL,
    latitude  DOUBLE PRECISION NOT NULL,
    longitude DOUBLE PRECISION NOT NULL,
    radius_m  DOUBLE PRECISION NOT NULL CHECK (radius_m > 0),
    active    BOOLEAN          NOT NULL DEFAULT TRUE
);
