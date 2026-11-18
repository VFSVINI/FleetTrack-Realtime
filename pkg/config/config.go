// Package config lê configuração a partir de variáveis de ambiente (12-factor).
package config

import (
	"os"
	"strconv"
	"time"
)

// String retorna a variável de ambiente ou o valor padrão.
func String(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// Int retorna a variável como inteiro ou o valor padrão se ausente/inválida.
func Int(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

// Float retorna a variável como float64 ou o valor padrão se ausente/inválida.
func Float(key string, def float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(key), 64); err == nil {
		return v
	}
	return def
}

// Duration retorna a variável como time.Duration (ex.: "2s") ou o padrão.
func Duration(key string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(key)); err == nil {
		return v
	}
	return def
}
