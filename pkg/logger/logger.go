// Package logger cria loggers estruturados em JSON baseados em log/slog.
package logger

import (
	"log/slog"
	"os"
	"strings"
)

// New devolve um *slog.Logger JSON que identifica o serviço em todos os logs.
// O nível é controlado por LOG_LEVEL (debug, info, warn, error).
func New(service string) *slog.Logger {
	var level slog.Level
	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	return slog.New(h).With("service", service)
}
