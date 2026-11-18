// Package cache implementa o estado em tempo real do processador sobre o Redis.
package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"fleettrack/pkg/models"
)

// Redis guarda posições, estado das cercas e cooldown de alertas.
type Redis struct {
	rdb *redis.Client
}

// NewRedis cria o adaptador.
func NewRedis(rdb *redis.Client) *Redis { return &Redis{rdb: rdb} }

// SaveVehicle grava a última posição do veículo (um HSET por mensagem).
func (r *Redis) SaveVehicle(ctx context.Context, v models.VehicleState) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return r.rdb.HSet(ctx, models.KeyFleetPositions, v.VehicleID, b).Err()
}

func fenceField(vehicleID string, fenceID int) string {
	return fmt.Sprintf("%s:%d", vehicleID, fenceID)
}

// GeofenceStates devolve, em um único round-trip (HMGET), o estado conhecido de cada cerca.
func (r *Redis) GeofenceStates(ctx context.Context, vehicleID string, fenceIDs []int) (map[int]bool, error) {
	fields := make([]string, len(fenceIDs))
	for i, id := range fenceIDs {
		fields[i] = fenceField(vehicleID, id)
	}
	vals, err := r.rdb.HMGet(ctx, models.KeyGeofenceState, fields...).Result()
	if err != nil {
		return nil, err
	}
	out := make(map[int]bool, len(vals))
	for i, v := range vals {
		if s, ok := v.(string); ok {
			out[fenceIDs[i]] = s == "1"
		}
	}
	return out, nil
}

// SetGeofenceStates atualiza o estado das cercas informadas.
func (r *Redis) SetGeofenceStates(ctx context.Context, vehicleID string, states map[int]bool) error {
	values := make([]any, 0, len(states)*2)
	for id, inside := range states {
		v := "0"
		if inside {
			v = "1"
		}
		values = append(values, fenceField(vehicleID, id), v)
	}
	return r.rdb.HSet(ctx, models.KeyGeofenceState, values...).Err()
}

// AcquireCooldown retorna true se a chave não existia (alerta liberado) e inicia o TTL.
func (r *Redis) AcquireCooldown(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	return r.rdb.SetNX(ctx, "cooldown:"+key, 1, ttl).Result()
}
