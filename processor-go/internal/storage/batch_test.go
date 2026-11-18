package storage

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"fleettrack/pkg/models"
)

type recordingSink struct{ batches []int }

func (r *recordingSink) CopyTelemetry(_ context.Context, b []models.Telemetry) error {
	r.batches = append(r.batches, len(b))
	return nil
}

func TestBatchWriterFlushesBySizeAndOnClose(t *testing.T) {
	sink := &recordingSink{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := NewBatchWriter(sink, 3, time.Hour, 16, log)

	for i := 0; i < 7; i++ {
		if err := w.Add(context.Background(), models.Telemetry{VehicleID: "V"}); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()

	want := []int{3, 3, 1}
	if len(sink.batches) != len(want) {
		t.Fatalf("lotes = %v, esperado %v", sink.batches, want)
	}
	for i := range want {
		if sink.batches[i] != want[i] {
			t.Fatalf("lotes = %v, esperado %v", sink.batches, want)
		}
	}
}

func TestBatchWriterFlushesByInterval(t *testing.T) {
	sink := &recordingSink{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := NewBatchWriter(sink, 100, 20*time.Millisecond, 16, log)

	if err := w.Add(context.Background(), models.Telemetry{VehicleID: "V"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	w.Close()

	if len(sink.batches) != 1 || sink.batches[0] != 1 {
		t.Fatalf("lotes = %v, esperado [1]", sink.batches)
	}
}
