// Package handler expõe a API REST (Gin) consumida pelo dashboard.
package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"fleettrack/api-gateway/internal/docs"
	"fleettrack/api-gateway/internal/repository"
)

// API agrupa as dependências dos endpoints.
type API struct {
	ts    *repository.Timescale
	fleet *repository.Fleet
	log   *slog.Logger
}

// New cria a API.
func New(ts *repository.Timescale, fleet *repository.Fleet, log *slog.Logger) *API {
	return &API{ts: ts, fleet: fleet, log: log}
}

// Routes registra todas as rotas.
func (a *API) Routes(r *gin.Engine) {
	r.GET("/health", a.health)
	r.GET("/docs", docs.UI)
	r.GET("/openapi.yaml", docs.Spec)

	v1 := r.Group("/api/v1")
	v1.GET("/fleet", a.listFleet)
	v1.GET("/vehicles/:id", a.getVehicle)
	v1.GET("/vehicles/:id/history", a.history)
	v1.GET("/metrics/summary", a.summary)
	v1.GET("/metrics/speed-series", a.speedSeries)
	v1.GET("/metrics/vehicles", a.vehicleStats)
	v1.GET("/alerts", a.alerts)
	v1.POST("/alerts/:id/ack", a.ackAlert)
	v1.GET("/geofences", a.geofences)
}

// CORS permite acesso do dashboard em desenvolvimento (em produção use o proxy do nginx).
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// AccessLog registra cada requisição em JSON (slog).
func AccessLog(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		log.Info("requisição", "method", c.Request.Method, "path", c.Request.URL.Path,
			"status", c.Writer.Status(), "duration_ms", time.Since(start).Milliseconds())
	}
}

func (a *API) ctx(c *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), 5*time.Second)
}

func (a *API) fail(c *gin.Context, err error) {
	if errors.Is(err, repository.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "recurso não encontrado"})
		return
	}
	a.log.Error("erro na requisição", "path", c.Request.URL.Path, "error", err)
	c.JSON(http.StatusInternalServerError, gin.H{"error": "erro interno"})
}

func badRequest(c *gin.Context, msg string) {
	c.JSON(http.StatusBadRequest, gin.H{"error": msg})
}

func queryInt(c *gin.Context, key string, def, min, max int) int {
	v, err := strconv.Atoi(c.Query(key))
	if err != nil {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func list[T any](c *gin.Context, items []T) {
	c.JSON(http.StatusOK, gin.H{"data": items, "count": len(items)})
}

func (a *API) health(c *gin.Context) {
	ctx, cancel := a.ctx(c)
	defer cancel()
	status, code := "ok", http.StatusOK
	checks := gin.H{"timescaledb": "ok", "redis": "ok"}
	if err := a.ts.Ping(ctx); err != nil {
		checks["timescaledb"], status, code = err.Error(), "degraded", http.StatusServiceUnavailable
	}
	if err := a.fleet.Ping(ctx); err != nil {
		checks["redis"], status, code = err.Error(), "degraded", http.StatusServiceUnavailable
	}
	c.JSON(code, gin.H{"status": status, "checks": checks})
}

func (a *API) listFleet(c *gin.Context) {
	ctx, cancel := a.ctx(c)
	defer cancel()
	vehicles, err := a.fleet.All(ctx)
	if err != nil {
		a.fail(c, err)
		return
	}
	list(c, vehicles)
}

func (a *API) getVehicle(c *gin.Context) {
	ctx, cancel := a.ctx(c)
	defer cancel()
	v, err := a.fleet.Get(ctx, c.Param("id"))
	if err != nil {
		a.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

func (a *API) history(c *gin.Context) {
	to := time.Now().UTC()
	from := to.Add(-time.Hour)
	var err error
	if s := c.Query("from"); s != "" {
		if from, err = time.Parse(time.RFC3339, s); err != nil {
			badRequest(c, "parâmetro 'from' inválido (use RFC 3339)")
			return
		}
	}
	if s := c.Query("to"); s != "" {
		if to, err = time.Parse(time.RFC3339, s); err != nil {
			badRequest(c, "parâmetro 'to' inválido (use RFC 3339)")
			return
		}
	}
	if !from.Before(to) {
		badRequest(c, "'from' deve ser anterior a 'to'")
		return
	}
	ctx, cancel := a.ctx(c)
	defer cancel()
	points, err := a.ts.History(ctx, c.Param("id"), from, to, queryInt(c, "limit", 1000, 1, 10000))
	if err != nil {
		a.fail(c, err)
		return
	}
	list(c, points)
}

func (a *API) summary(c *gin.Context) {
	ctx, cancel := a.ctx(c)
	defer cancel()

	vehicles, err := a.fleet.All(ctx)
	if err != nil {
		a.fail(c, err)
		return
	}
	var online, moving, lowFuel int
	var speedSum float64
	for _, v := range vehicles {
		if !v.Online {
			continue
		}
		online++
		speedSum += v.SpeedKmh
		if v.SpeedKmh >= 2 {
			moving++
		}
		if v.FuelLevel <= 15 {
			lowFuel++
		}
	}
	avg := 0.0
	if online > 0 {
		avg = speedSum / float64(online)
	}

	activeAlerts, err := a.ts.ActiveAlertCount(ctx)
	if err != nil {
		a.fail(c, err)
		return
	}
	msgs, err := a.ts.MessagesLastMinute(ctx)
	if err != nil {
		a.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"vehicles_total":       len(vehicles),
		"vehicles_online":      online,
		"vehicles_moving":      moving,
		"avg_speed_kmh":        avg,
		"low_fuel_count":       lowFuel,
		"active_alerts":        activeAlerts,
		"messages_last_minute": msgs,
	})
}

func (a *API) speedSeries(c *gin.Context) {
	ctx, cancel := a.ctx(c)
	defer cancel()
	points, err := a.ts.SpeedSeries(ctx, queryInt(c, "minutes", 30, 1, 1440))
	if err != nil {
		a.fail(c, err)
		return
	}
	list(c, points)
}

func (a *API) vehicleStats(c *gin.Context) {
	ctx, cancel := a.ctx(c)
	defer cancel()
	stats, err := a.ts.VehicleStats(ctx)
	if err != nil {
		a.fail(c, err)
		return
	}
	list(c, stats)
}

func (a *API) alerts(c *gin.Context) {
	status := c.DefaultQuery("status", "active")
	if status != "active" && status != "all" {
		badRequest(c, "status deve ser 'active' ou 'all'")
		return
	}
	ctx, cancel := a.ctx(c)
	defer cancel()
	items, err := a.ts.Alerts(ctx, status == "all", queryInt(c, "limit", 50, 1, 500))
	if err != nil {
		a.fail(c, err)
		return
	}
	list(c, items)
}

func (a *API) ackAlert(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		badRequest(c, "id inválido")
		return
	}
	ctx, cancel := a.ctx(c)
	defer cancel()
	ok, err := a.ts.AckAlert(ctx, id)
	if err != nil {
		a.fail(c, err)
		return
	}
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "alerta não encontrado ou já reconhecido"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "acknowledged"})
}

func (a *API) geofences(c *gin.Context) {
	ctx, cancel := a.ctx(c)
	defer cancel()
	items, err := a.ts.Geofences(ctx)
	if err != nil {
		a.fail(c, err)
		return
	}
	list(c, items)
}
