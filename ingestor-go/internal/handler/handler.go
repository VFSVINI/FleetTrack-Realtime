// Package handler contém os endpoints HTTP (Fiber) de ingestão de telemetria.
package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v2"

	"fleettrack/ingestor-go/internal/publisher"
	"fleettrack/pkg/models"
)

// Enqueuer é implementado por *publisher.Publisher.
type Enqueuer interface {
	Enqueue(body []byte) error
}

// Handler agrupa as dependências dos endpoints.
type Handler struct {
	q        Enqueuer
	stats    func() (published, failed uint64)
	log      *slog.Logger
	maxBatch int
	accepted counter
	rejected counter
}

// New cria o handler. stats pode devolver os contadores do publisher.
func New(q Enqueuer, stats func() (uint64, uint64), log *slog.Logger, maxBatch int) *Handler {
	return &Handler{q: q, stats: stats, log: log, maxBatch: maxBatch}
}

// Register registra as rotas no app Fiber.
func (h *Handler) Register(app *fiber.App) {
	app.Get("/health", h.health)
	v1 := app.Group("/v1")
	v1.Post("/telemetry", h.ingestOne)
	v1.Post("/telemetry/batch", h.ingestBatch)
}

// AccessLog registra apenas respostas com erro (evita custo de log por requisição em alta carga).
func AccessLog(log *slog.Logger) fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()
		err := c.Next()
		if status := c.Response().StatusCode(); status >= 400 {
			log.Warn("requisição com erro", "method", c.Method(), "path", c.Path(),
				"status", status, "duration_ms", time.Since(start).Milliseconds())
		}
		return err
	}
}

func (h *Handler) health(c *fiber.Ctx) error {
	published, failed := h.stats()
	return c.JSON(fiber.Map{
		"status":         "ok",
		"accepted":       h.accepted.Load(),
		"rejected":       h.rejected.Load(),
		"published":      published,
		"publish_failed": failed,
	})
}

func (h *Handler) ingestOne(c *fiber.Ctx) error {
	var t models.Telemetry
	if err := json.Unmarshal(c.Body(), &t); err != nil {
		h.rejected.Add(1)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido: " + err.Error()})
	}
	if err := t.Validate(time.Now()); err != nil {
		h.rejected.Add(1)
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"error": err.Error()})
	}
	if err := h.enqueue(t); err != nil {
		return h.enqueueFailed(c, err)
	}
	h.accepted.Add(1)
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"status": "accepted"})
}

type batchError struct {
	Index int    `json:"index"`
	Error string `json:"error"`
}

type batchResult struct {
	Accepted int          `json:"accepted"`
	Rejected int          `json:"rejected"`
	Errors   []batchError `json:"errors,omitempty"`
}

func (h *Handler) ingestBatch(c *fiber.Ctx) error {
	var batch []models.Telemetry
	if err := json.Unmarshal(c.Body(), &batch); err != nil {
		h.rejected.Add(1)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido: " + err.Error()})
	}
	if len(batch) == 0 || len(batch) > h.maxBatch {
		h.rejected.Add(1)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "o lote deve ter entre 1 e " + itoa(h.maxBatch) + " itens"})
	}

	now := time.Now()
	var res batchResult
	for i, t := range batch {
		if err := t.Validate(now); err != nil {
			res.Rejected++
			if len(res.Errors) < 10 {
				res.Errors = append(res.Errors, batchError{Index: i, Error: err.Error()})
			}
			continue
		}
		if err := h.enqueue(t); err != nil {
			// Itens restantes não foram processados: o cliente deve reenviá-los.
			res.Rejected += len(batch) - i
			h.accepted.Add(uint64(res.Accepted))
			h.rejected.Add(uint64(res.Rejected))
			status := fiber.StatusInternalServerError
			if errors.Is(err, publisher.ErrBackpressure) {
				status = fiber.StatusServiceUnavailable
				c.Set(fiber.HeaderRetryAfter, "1")
			}
			return c.Status(status).JSON(res)
		}
		res.Accepted++
	}
	h.accepted.Add(uint64(res.Accepted))
	h.rejected.Add(uint64(res.Rejected))
	return c.Status(fiber.StatusAccepted).JSON(res)
}

func (h *Handler) enqueue(t models.Telemetry) error {
	body, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return h.q.Enqueue(body)
}

func (h *Handler) enqueueFailed(c *fiber.Ctx, err error) error {
	h.rejected.Add(1)
	if errors.Is(err, publisher.ErrBackpressure) {
		c.Set(fiber.HeaderRetryAfter, "1")
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "sistema sobrecarregado, tente novamente"})
	}
	h.log.Error("falha ao enfileirar telemetria", "error", err)
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "erro interno"})
}
