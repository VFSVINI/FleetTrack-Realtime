package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"fleettrack/pkg/models"
	"fleettrack/processor-go/internal/rules"
)

type fakeState struct {
	vehicles  map[string]models.VehicleState
	geo       map[string]bool
	cooldowns map[string]bool
}

func newFakeState() *fakeState {
	return &fakeState{vehicles: map[string]models.VehicleState{}, geo: map[string]bool{}, cooldowns: map[string]bool{}}
}

func geoKey(v string, id int) string { return v + ":" + string(rune('0'+id)) }

func (f *fakeState) SaveVehicle(_ context.Context, v models.VehicleState) error {
	f.vehicles[v.VehicleID] = v
	return nil
}

func (f *fakeState) GeofenceStates(_ context.Context, v string, ids []int) (map[int]bool, error) {
	out := map[int]bool{}
	for _, id := range ids {
		if in, ok := f.geo[geoKey(v, id)]; ok {
			out[id] = in
		}
	}
	return out, nil
}

func (f *fakeState) SetGeofenceStates(_ context.Context, v string, states map[int]bool) error {
	for id, in := range states {
		f.geo[geoKey(v, id)] = in
	}
	return nil
}

func (f *fakeState) AcquireCooldown(_ context.Context, key string, _ time.Duration) (bool, error) {
	if f.cooldowns[key] {
		return false, nil
	}
	f.cooldowns[key] = true
	return true, nil
}

type fakeAlerts struct{ got []models.Alert }

func (f *fakeAlerts) InsertAlert(_ context.Context, a *models.Alert) error {
	f.got = append(f.got, *a)
	return nil
}

type fakeSink struct{ got []models.Telemetry }

func (f *fakeSink) Add(_ context.Context, t models.Telemetry) error {
	f.got = append(f.got, t)
	return nil
}

func setup() (*Service, *fakeState, *fakeAlerts, *fakeSink) {
	st, al, sk := newFakeState(), &fakeAlerts{}, &fakeSink{}
	fences := &GeofenceCache{}
	fences.Set([]models.Geofence{{ID: 1, Name: "Depósito", Latitude: -8.05, Longitude: -34.9, RadiusM: 1000}})
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := New(st, al, sk, fences, rules.Thresholds{SpeedLimitKmh: 100, SpeedCriticalKmh: 120, FuelCriticalPct: 15}, time.Minute, log)
	return svc, st, al, sk
}

func msg(t *testing.T, lat, speed, fuel float64) []byte {
	t.Helper()
	b, err := json.Marshal(models.Telemetry{
		VehicleID: "V", Latitude: lat, Longitude: -34.9, SpeedKmh: speed, FuelLevel: fuel,
		EngineStatus: models.EngineOn, Timestamp: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func types(as []models.Alert) []models.AlertType {
	out := make([]models.AlertType, len(as))
	for i, a := range as {
		out[i] = a.Type
	}
	return out
}

func TestProcessInvalid(t *testing.T) {
	svc, _, _, sink := setup()
	for _, body := range [][]byte{[]byte("{nope"), []byte(`{"vehicle_id":""}`)} {
		if err := svc.Process(context.Background(), body); !errors.Is(err, models.ErrInvalid) {
			t.Fatalf("esperava ErrInvalid, obteve %v", err)
		}
	}
	if len(sink.got) != 0 {
		t.Fatal("mensagens inválidas não devem ser persistidas")
	}
}

func TestProcessGeofenceFlow(t *testing.T) {
	svc, state, alerts, sink := setup()
	ctx := context.Background()

	// 1ª observação fora da cerca: só registra estado, sem alerta.
	if err := svc.Process(ctx, msg(t, -8.10, 50, 80)); err != nil {
		t.Fatal(err)
	}
	if len(alerts.got) != 0 {
		t.Fatalf("não deveria gerar alerta na primeira observação: %v", types(alerts.got))
	}

	// Entrada na cerca.
	if err := svc.Process(ctx, msg(t, -8.05, 50, 80)); err != nil {
		t.Fatal(err)
	}
	if got := types(alerts.got); len(got) != 1 || got[0] != models.AlertGeofenceEnter {
		t.Fatalf("esperava GEOFENCE_ENTER, obteve %v", got)
	}

	// Saída da cerca.
	if err := svc.Process(ctx, msg(t, -8.10, 50, 80)); err != nil {
		t.Fatal(err)
	}
	if got := types(alerts.got); len(got) != 2 || got[1] != models.AlertGeofenceExit {
		t.Fatalf("esperava GEOFENCE_EXIT, obteve %v", got)
	}

	if len(sink.got) != 3 {
		t.Fatalf("esperava 3 pontos no lote, obteve %d", len(sink.got))
	}
	if _, ok := state.vehicles["V"]; !ok {
		t.Fatal("estado do veículo deveria estar salvo")
	}
}

func TestProcessAlertCooldown(t *testing.T) {
	svc, _, alerts, _ := setup()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := svc.Process(ctx, msg(t, -8.10, 130, 5)); err != nil {
			t.Fatal(err)
		}
	}
	// Apenas 1 SPEEDING e 1 LOW_FUEL apesar de 3 mensagens: o cooldown suprime repetições.
	if len(alerts.got) != 2 {
		t.Fatalf("esperava 2 alertas, obteve %d: %v", len(alerts.got), types(alerts.got))
	}
}
