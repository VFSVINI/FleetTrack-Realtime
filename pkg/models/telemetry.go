// Package models contém os tipos de domínio compartilhados entre os serviços.
package models

import (
	"errors"
	"fmt"
	"time"
)

// EngineStatus representa o estado do motor reportado pelo dispositivo.
type EngineStatus string

const (
	EngineOn   EngineStatus = "on"
	EngineOff  EngineStatus = "off"
	EngineIdle EngineStatus = "idle"
)

// ErrInvalid sinaliza um pacote de telemetria que nunca será válido (poison message).
var ErrInvalid = errors.New("telemetria inválida")

// Telemetry é o pacote enviado pelos dispositivos IoT.
type Telemetry struct {
	VehicleID    string       `json:"vehicle_id"`
	Latitude     float64      `json:"latitude"`
	Longitude    float64      `json:"longitude"`
	SpeedKmh     float64      `json:"speed_kmh"`
	FuelLevel    float64      `json:"fuel_level"` // percentual 0-100
	EngineStatus EngineStatus `json:"engine_status"`
	Timestamp    time.Time    `json:"timestamp"`
}

// Validate verifica os limites físicos e de formato do pacote.
func (t Telemetry) Validate(now time.Time) error {
	switch {
	case t.VehicleID == "" || len(t.VehicleID) > 64:
		return fmt.Errorf("%w: vehicle_id é obrigatório (máx. 64 caracteres)", ErrInvalid)
	case t.Latitude < -90 || t.Latitude > 90:
		return fmt.Errorf("%w: latitude fora do intervalo [-90, 90]", ErrInvalid)
	case t.Longitude < -180 || t.Longitude > 180:
		return fmt.Errorf("%w: longitude fora do intervalo [-180, 180]", ErrInvalid)
	case t.SpeedKmh < 0 || t.SpeedKmh > 300:
		return fmt.Errorf("%w: speed_kmh fora do intervalo [0, 300]", ErrInvalid)
	case t.FuelLevel < 0 || t.FuelLevel > 100:
		return fmt.Errorf("%w: fuel_level fora do intervalo [0, 100]", ErrInvalid)
	case t.Timestamp.IsZero():
		return fmt.Errorf("%w: timestamp é obrigatório (RFC 3339)", ErrInvalid)
	case t.Timestamp.After(now.Add(5 * time.Minute)):
		return fmt.Errorf("%w: timestamp no futuro", ErrInvalid)
	case t.Timestamp.Before(now.Add(-24 * time.Hour)):
		return fmt.Errorf("%w: timestamp com mais de 24h", ErrInvalid)
	}
	switch t.EngineStatus {
	case EngineOn, EngineOff, EngineIdle:
	default:
		return fmt.Errorf("%w: engine_status deve ser on, off ou idle", ErrInvalid)
	}
	return nil
}
