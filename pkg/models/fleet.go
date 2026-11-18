package models

import "time"

// Chaves do Redis compartilhadas entre processor (escrita) e api-gateway (leitura).
const (
	KeyFleetPositions = "fleet:positions"      // hash: vehicle_id -> VehicleState (JSON)
	KeyGeofenceState  = "fleet:geofence_state" // hash: "<vehicle_id>:<fence_id>" -> "1"|"0"
)

// VehicleState é a última posição conhecida de um veículo (estado em tempo real).
type VehicleState struct {
	Telemetry
	UpdatedAt time.Time `json:"updated_at"`
	Online    bool      `json:"online"` // calculado pela API a partir de UpdatedAt
}

// AlertType identifica a regra de negócio que gerou o alerta.
type AlertType string

const (
	AlertSpeeding      AlertType = "SPEEDING"
	AlertLowFuel       AlertType = "LOW_FUEL"
	AlertGeofenceEnter AlertType = "GEOFENCE_ENTER"
	AlertGeofenceExit  AlertType = "GEOFENCE_EXIT"
)

// Severity classifica a gravidade do alerta.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// Alert é um evento gerado pelo motor de regras.
type Alert struct {
	ID             int64      `json:"id"`
	VehicleID      string     `json:"vehicle_id"`
	Type           AlertType  `json:"type"`
	Severity       Severity   `json:"severity"`
	Message        string     `json:"message"`
	Latitude       float64    `json:"latitude"`
	Longitude      float64    `json:"longitude"`
	CreatedAt      time.Time  `json:"created_at"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
}

// Geofence é uma cerca virtual circular.
type Geofence struct {
	ID        int     `json:"id"`
	Name      string  `json:"name"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	RadiusM   float64 `json:"radius_m"`
}
