// Package repository acessa o Redis (estado em tempo real) e o TimescaleDB (histórico).
package repository

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/redis/go-redis/v9"

	"fleettrack/pkg/models"
)

// ErrNotFound indica que o recurso não existe.
var ErrNotFound = errors.New("não encontrado")

// Fleet lê o estado atual da frota no Redis.
type Fleet struct {
	rdb    *redis.Client
	window time.Duration
}

// NewFleet cria o leitor. window define por quanto tempo sem telemetria o veículo é "offline".
func NewFleet(rdb *redis.Client, window time.Duration) *Fleet {
	return &Fleet{rdb: rdb, window: window}
}

// All devolve a última posição de todos os veículos, ordenada por ID (1 round-trip: HGETALL).
func (f *Fleet) All(ctx context.Context) ([]models.VehicleState, error) {
	raw, err := f.rdb.HGetAll(ctx, models.KeyFleetPositions).Result()
	if err != nil {
		return nil, err
	}
	out := make([]models.VehicleState, 0, len(raw))
	for _, s := range raw {
		if v, ok := f.decode(s); ok {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].VehicleID < out[j].VehicleID })
	return out, nil
}

// Get devolve a posição atual de um veículo.
func (f *Fleet) Get(ctx context.Context, id string) (models.VehicleState, error) {
	s, err := f.rdb.HGet(ctx, models.KeyFleetPositions, id).Result()
	if errors.Is(err, redis.Nil) {
		return models.VehicleState{}, ErrNotFound
	}
	if err != nil {
		return models.VehicleState{}, err
	}
	v, ok := f.decode(s)
	if !ok {
		return models.VehicleState{}, ErrNotFound
	}
	return v, nil
}

func (f *Fleet) decode(s string) (models.VehicleState, bool) {
	var v models.VehicleState
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return v, false
	}
	v.Online = time.Since(v.UpdatedAt) <= f.window
	return v, true
}

// Ping verifica a conectividade com o Redis.
func (f *Fleet) Ping(ctx context.Context) error { return f.rdb.Ping(ctx).Err() }
