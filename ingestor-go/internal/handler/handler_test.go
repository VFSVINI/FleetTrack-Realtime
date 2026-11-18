package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"fleettrack/ingestor-go/internal/publisher"
)

type fakeQueue struct {
	err error
	got [][]byte
}

func (f *fakeQueue) Enqueue(b []byte) error {
	if f.err != nil {
		return f.err
	}
	f.got = append(f.got, b)
	return nil
}

func newApp(q Enqueuer) *fiber.App {
	app := fiber.New()
	New(q, func() (uint64, uint64) { return 0, 0 }, slog.Default(), 3).Register(app)
	return app
}

func post(t *testing.T, app *fiber.App, path, body string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func payload(lat float64) string {
	return fmt.Sprintf(`{"vehicle_id":"VEH-001","latitude":%v,"longitude":-34.9,"speed_kmh":50,"fuel_level":70,"engine_status":"on","timestamp":%q}`,
		lat, time.Now().UTC().Format(time.RFC3339))
}

func TestIngestOne(t *testing.T) {
	q := &fakeQueue{}
	app := newApp(q)

	if code, _ := post(t, app, "/v1/telemetry", payload(-8.05)); code != http.StatusAccepted {
		t.Fatalf("status = %d, esperado 202", code)
	}
	if len(q.got) != 1 {
		t.Fatalf("esperava 1 mensagem enfileirada, obteve %d", len(q.got))
	}
	if code, _ := post(t, app, "/v1/telemetry", payload(200)); code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, esperado 422", code)
	}
	if code, _ := post(t, app, "/v1/telemetry", `{invalid`); code != http.StatusBadRequest {
		t.Fatalf("status = %d, esperado 400", code)
	}
}

func TestIngestOneBackpressure(t *testing.T) {
	app := newApp(&fakeQueue{err: publisher.ErrBackpressure})
	if code, _ := post(t, app, "/v1/telemetry", payload(-8.05)); code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, esperado 503", code)
	}
}

func TestIngestOneInternalError(t *testing.T) {
	app := newApp(&fakeQueue{err: errors.New("boom")})
	if code, _ := post(t, app, "/v1/telemetry", payload(-8.05)); code != http.StatusInternalServerError {
		t.Fatalf("status = %d, esperado 500", code)
	}
}

func TestIngestBatchPartial(t *testing.T) {
	q := &fakeQueue{}
	app := newApp(q)

	code, body := post(t, app, "/v1/telemetry/batch", "["+payload(-8.05)+","+payload(200)+"]")
	if code != http.StatusAccepted {
		t.Fatalf("status = %d, esperado 202", code)
	}
	var res batchResult
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatal(err)
	}
	if res.Accepted != 1 || res.Rejected != 1 || len(res.Errors) != 1 {
		t.Fatalf("resultado inesperado: %+v", res)
	}

	big := "[" + strings.Repeat(payload(-8.05)+",", 3) + payload(-8.05) + "]"
	if code, _ := post(t, app, "/v1/telemetry/batch", big); code != http.StatusBadRequest {
		t.Fatalf("lote acima do limite: status = %d, esperado 400", code)
	}
}
