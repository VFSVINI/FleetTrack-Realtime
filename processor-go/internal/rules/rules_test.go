package rules

import (
	"math"
	"testing"

	"fleettrack/pkg/models"
)

var th = Thresholds{SpeedLimitKmh: 100, SpeedCriticalKmh: 120, FuelCriticalPct: 15}

func TestEvaluateThresholds(t *testing.T) {
	tests := []struct {
		name  string
		speed float64
		fuel  float64
		want  []models.AlertType
		sev   models.Severity // severidade do primeiro alerta esperado
	}{
		{"normal", 80, 50, nil, ""},
		{"no limite exato", 100, 50, nil, ""},
		{"acima do limite", 110, 50, []models.AlertType{models.AlertSpeeding}, models.SeverityWarning},
		{"muito acima", 130, 50, []models.AlertType{models.AlertSpeeding}, models.SeverityCritical},
		{"combustível crítico", 50, 10, []models.AlertType{models.AlertLowFuel}, models.SeverityCritical},
		{"ambos", 130, 5, []models.AlertType{models.AlertSpeeding, models.AlertLowFuel}, models.SeverityCritical},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := EvaluateThresholds(th, models.Telemetry{VehicleID: "V1", SpeedKmh: tc.speed, FuelLevel: tc.fuel})
			if len(got) != len(tc.want) {
				t.Fatalf("alertas = %d, esperado %d (%+v)", len(got), len(tc.want), got)
			}
			for i, a := range got {
				if a.Type != tc.want[i] {
					t.Errorf("alerta[%d].Type = %s, esperado %s", i, a.Type, tc.want[i])
				}
			}
			if len(got) > 0 && got[0].Severity != tc.sev {
				t.Errorf("severidade = %s, esperado %s", got[0].Severity, tc.sev)
			}
		})
	}
}

func TestHaversine(t *testing.T) {
	// 1 grau de longitude no equador ≈ 111,19 km.
	d := Haversine(0, 0, 0, 1)
	if math.Abs(d-111195) > 200 {
		t.Fatalf("distância = %.0f m, esperado ≈ 111195 m", d)
	}
	if Haversine(-8.05, -34.9, -8.05, -34.9) != 0 {
		t.Fatal("distância entre pontos iguais deve ser 0")
	}
}

func TestInside(t *testing.T) {
	g := models.Geofence{ID: 1, Name: "Teste", Latitude: -8.05, Longitude: -34.9, RadiusM: 1000}
	if !Inside(g, -8.05, -34.9) {
		t.Error("centro deveria estar dentro")
	}
	if !Inside(g, -8.054, -34.9) { // ≈ 445 m ao sul
		t.Error("ponto a ~445 m deveria estar dentro")
	}
	if Inside(g, -8.07, -34.9) { // ≈ 2,2 km ao sul
		t.Error("ponto a ~2,2 km deveria estar fora")
	}
}

func TestGeofenceTransition(t *testing.T) {
	tests := []struct {
		name               string
		known, was, inside bool
		want               models.AlertType
		wantOK             bool
	}{
		{"primeira observação dentro", false, false, true, "", false},
		{"primeira observação fora", false, false, false, "", false},
		{"entrada", true, false, true, models.AlertGeofenceEnter, true},
		{"saída", true, true, false, models.AlertGeofenceExit, true},
		{"permanece dentro", true, true, true, "", false},
		{"permanece fora", true, false, false, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := GeofenceTransition(tc.known, tc.was, tc.inside)
			if got != tc.want || ok != tc.wantOK {
				t.Fatalf("obteve (%q, %v), esperado (%q, %v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
