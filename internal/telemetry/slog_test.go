package telemetry

import (
	"context"
	"log/slog"
	"testing"
)

func TestSlogHandler_NoOpMode(t *testing.T) {
	p, err := Init(&Config{ServiceName: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Shutdown(context.Background())

	slog.Info("test message", "key", "val")
}

func TestSlogHandler_StdoutJSON(t *testing.T) {
	p, err := Init(&Config{ServiceName: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Shutdown(context.Background())

	handler := slog.Default().Handler()
	if handler == nil {
		t.Fatal("default slog handler is nil")
	}
	slog.Info("stdout test message")
}
