// Package rules implementa as regras de negócio puras (sem I/O) do processador:
// excesso de velocidade, combustível crítico e geofencing.
package rules

import (
	"fmt"
	"math"
	"time"

	"fleettrack/pkg/models"
)

// Thresholds agrupa os limites configuráveis das regras.
type Thresholds struct {
	SpeedLimitKmh    float64 // acima disso: alerta de severidade "warning"
	SpeedCriticalKmh float64 // acima disso: severidade "critical"
	FuelCriticalPct  float64 // igual ou abaixo disso: combustível crítico
}

func newAlert(t models.Telemetry, typ models.AlertType, sev models.Severity, msg string) models.Alert {
	return models.Alert{
		VehicleID: t.VehicleID, Type: typ, Severity: sev, Message: msg,
		Latitude: t.Latitude, Longitude: t.Longitude, CreatedAt: time.Now().UTC(),
	}
}

// EvaluateThresholds aplica as regras de velocidade e combustível.
func EvaluateThresholds(th Thresholds, t models.Telemetry) []models.Alert {
	var out []models.Alert
	if t.SpeedKmh > th.SpeedLimitKmh {
		sev := models.SeverityWarning
		if t.SpeedKmh > th.SpeedCriticalKmh {
			sev = models.SeverityCritical
		}
		out = append(out, newAlert(t, models.AlertSpeeding, sev,
			fmt.Sprintf("Velocidade de %.0f km/h acima do limite de %.0f km/h", t.SpeedKmh, th.SpeedLimitKmh)))
	}
	if t.FuelLevel <= th.FuelCriticalPct {
		out = append(out, newAlert(t, models.AlertLowFuel, models.SeverityCritical,
			fmt.Sprintf("Combustível crítico: %.0f%% restante", t.FuelLevel)))
	}
	return out
}

const earthRadiusM = 6371000.0

// Haversine calcula a distância em metros entre dois pontos geográficos.
func Haversine(lat1, lon1, lat2, lon2 float64) float64 {
	rad := math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLon := (lon2 - lon1) * rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadiusM * math.Asin(math.Sqrt(a))
}

// Inside informa se o ponto está dentro da cerca circular.
func Inside(g models.Geofence, lat, lon float64) bool {
	return Haversine(g.Latitude, g.Longitude, lat, lon) <= g.RadiusM
}

// GeofenceTransition decide se houve entrada ou saída da cerca.
// Na primeira observação (known=false) não há evento: apenas se registra o estado.
func GeofenceTransition(known, wasInside, inside bool) (models.AlertType, bool) {
	if !known || wasInside == inside {
		return "", false
	}
	if inside {
		return models.AlertGeofenceEnter, true
	}
	return models.AlertGeofenceExit, true
}

// GeofenceAlert monta o alerta de entrada/saída de uma cerca.
func GeofenceAlert(t models.Telemetry, g models.Geofence, typ models.AlertType) models.Alert {
	if typ == models.AlertGeofenceEnter {
		return newAlert(t, typ, models.SeverityInfo, fmt.Sprintf("Entrou na cerca \"%s\"", g.Name))
	}
	return newAlert(t, typ, models.SeverityWarning, fmt.Sprintf("Saiu da cerca \"%s\"", g.Name))
}
