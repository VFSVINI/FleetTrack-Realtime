// Package service orquestra o processamento de cada mensagem de telemetria:
// estado em tempo real (Redis), regras de negócio, alertas e persistência em lote.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"fleettrack/pkg/models"
	"fleettrack/processor-go/internal/rules"
)

// StateStore guarda o estado em tempo real (implementado com Redis).
type StateStore interface {
	SaveVehicle(ctx context.Context, v models.VehicleState) error
	GeofenceStates(ctx context.Context, vehicleID string, fenceIDs []int) (map[int]bool, error)
	SetGeofenceStates(ctx context.Context, vehicleID string, states map[int]bool) error
	AcquireCooldown(ctx context.Context, key string, ttl time.Duration) (bool, error)
}

// AlertStore persiste alertas (implementado com PostgreSQL).
type AlertStore interface {
	InsertAlert(ctx context.Context, a *models.Alert) error
}

// TelemetrySink recebe a telemetria válida para persistência em lote.
type TelemetrySink interface {
	Add(ctx context.Context, t models.Telemetry) error
}

// Service processa mensagens de telemetria.
type Service struct {
	state    StateStore
	alerts   AlertStore
	sink     TelemetrySink
	fences   *GeofenceCache
	th       rules.Thresholds
	cooldown time.Duration
	log      *slog.Logger
	now      func() time.Time
}

// New cria o serviço. cooldown evita tempestade de alertas repetidos por veículo/regra.
func New(state StateStore, alerts AlertStore, sink TelemetrySink, fences *GeofenceCache,
	th rules.Thresholds, cooldown time.Duration, log *slog.Logger) *Service {
	return &Service{state: state, alerts: alerts, sink: sink, fences: fences,
		th: th, cooldown: cooldown, log: log, now: time.Now}
}

// Process trata uma mensagem. Retorna erro que embrulha models.ErrInvalid para
// mensagens inválidas (descartar/DLQ) e outro erro para falhas transitórias (reenfileirar).
func (s *Service) Process(ctx context.Context, body []byte) error {
	var t models.Telemetry
	if err := json.Unmarshal(body, &t); err != nil {
		return fmt.Errorf("%w: json: %v", models.ErrInvalid, err)
	}
	if err := t.Validate(s.now()); err != nil {
		return err
	}

	if err := s.state.SaveVehicle(ctx, models.VehicleState{Telemetry: t, UpdatedAt: s.now().UTC()}); err != nil {
		s.log.Warn("falha ao atualizar estado no redis", "vehicle_id", t.VehicleID, "error", err)
	}

	for _, a := range s.evaluate(ctx, t) {
		if err := s.alerts.InsertAlert(ctx, &a); err != nil {
			s.log.Error("falha ao persistir alerta", "vehicle_id", t.VehicleID, "type", a.Type, "error", err)
			continue
		}
		s.log.Info("alerta gerado", "alert_id", a.ID, "vehicle_id", a.VehicleID, "type", a.Type, "severity", a.Severity)
	}

	return s.sink.Add(ctx, t)
}

// evaluate executa todas as regras e aplica o cooldown de alertas.
func (s *Service) evaluate(ctx context.Context, t models.Telemetry) []models.Alert {
	var out []models.Alert

	for _, a := range rules.EvaluateThresholds(s.th, t) {
		if s.allow(ctx, fmt.Sprintf("%s:%s", t.VehicleID, a.Type)) {
			out = append(out, a)
		}
	}

	fences := s.fences.List()
	if len(fences) == 0 {
		return out
	}
	ids := make([]int, len(fences))
	for i, f := range fences {
		ids[i] = f.ID
	}
	prev, err := s.state.GeofenceStates(ctx, t.VehicleID, ids)
	if err != nil {
		s.log.Warn("falha ao ler estado das cercas", "vehicle_id", t.VehicleID, "error", err)
		return out
	}

	updates := make(map[int]bool)
	for _, f := range fences {
		inside := rules.Inside(f, t.Latitude, t.Longitude)
		was, known := prev[f.ID]
		if known && was == inside {
			continue
		}
		updates[f.ID] = inside
		typ, ok := rules.GeofenceTransition(known, was, inside)
		if !ok {
			continue
		}
		// O cooldown também protege contra "flapping" causado por ruído de GPS na borda.
		if s.allow(ctx, fmt.Sprintf("%s:%s:%d", t.VehicleID, typ, f.ID)) {
			out = append(out, rules.GeofenceAlert(t, f, typ))
		}
	}
	if len(updates) > 0 {
		if err := s.state.SetGeofenceStates(ctx, t.VehicleID, updates); err != nil {
			s.log.Warn("falha ao gravar estado das cercas", "vehicle_id", t.VehicleID, "error", err)
		}
	}
	return out
}

// allow retorna true se o alerta pode ser emitido. Em caso de erro no Redis,
// opta por emitir (melhor um alerta duplicado do que perdido).
func (s *Service) allow(ctx context.Context, key string) bool {
	ok, err := s.state.AcquireCooldown(ctx, key, s.cooldown)
	if err != nil {
		s.log.Warn("falha no cooldown de alertas", "key", key, "error", err)
		return true
	}
	return ok
}
