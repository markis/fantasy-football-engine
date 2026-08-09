package telemetry

import (
	"context"
	"log/slog"
	"os"
)

// Init sets up structured logging (slog) and optional OTel.
// OTel export is deferred to when the daemon is running with a collector.
func Init(serviceName, otelEndpoint string) {
	// Set up slog with JSON output for production, text for dev
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	slog.SetDefault(slog.New(handler))

	if otelEndpoint != "" {
		slog.Info("telemetry configured", "service", serviceName, "otel_endpoint", otelEndpoint)
		// OTel SDK init would go here — deferred until the daemon is deployed
		// with a running collector. The daemon logs are structured and
		// can be collected by any log shipper.
	}
}

// Ensure context import is used
var _ context.Context