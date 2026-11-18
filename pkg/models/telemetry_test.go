package models

import (
	"errors"
	"testing"
	"time"
)

func validTelemetry(now time.Time) Telemetry {
	return Telemetry{
		VehicleID: "VEH-001", Latitude: -8.05, Longitude: -34.9,
		SpeedKmh: 60, FuelLevel: 50, EngineStatus: EngineOn, Timestamp: now,
	}
}

func TestTelemetryValidate(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		mutate  func(*Telemetry)
		wantErr bool
	}{
		{"válido", func(*Telemetry) {}, false},
		{"sem vehicle_id", func(x *Telemetry) { x.VehicleID = "" }, true},
		{"latitude inválida", func(x *Telemetry) { x.Latitude = 91 }, true},
		{"longitude inválida", func(x *Telemetry) { x.Longitude = -181 }, true},
		{"velocidade negativa", func(x *Telemetry) { x.SpeedKmh = -1 }, true},
		{"combustível acima de 100", func(x *Telemetry) { x.FuelLevel = 101 }, true},
		{"motor desconhecido", func(x *Telemetry) { x.EngineStatus = "turbo" }, true},
		{"sem timestamp", func(x *Telemetry) { x.Timestamp = time.Time{} }, true},
		{"timestamp no futuro", func(x *Telemetry) { x.Timestamp = now.Add(time.Hour) }, true},
		{"timestamp antigo", func(x *Telemetry) { x.Timestamp = now.Add(-48 * time.Hour) }, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := validTelemetry(now)
			tc.mutate(&v)
			err := v.Validate(now)
			if (err != nil) != tc.wantErr {
				t.Fatalf("erro = %v, esperado erro = %v", err, tc.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalid) {
				t.Fatalf("erro deve embrulhar ErrInvalid, obtido %v", err)
			}
		})
	}
}
