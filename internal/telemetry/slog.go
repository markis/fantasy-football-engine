package telemetry

import (
	"context"
	"log/slog"
	"os"

	otelslog "go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/log/global"
)

func installSlogHandler(serviceName string) {
	jsonHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})

	otelHandler := otelslog.NewHandler(
		serviceName,
		otelslog.WithLoggerProvider(global.GetLoggerProvider()),
		otelslog.WithSource(true),
	)

	handler := newFanoutHandler(jsonHandler, otelHandler)
	slog.SetDefault(slog.New(handler))
}

type fanoutHandler struct {
	handlers []slog.Handler
}

func newFanoutHandler(handlers ...slog.Handler) *fanoutHandler {
	return &fanoutHandler{handlers: handlers}
}

func (h *fanoutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

// Handle implements slog.Handler. The record param is passed by value per the
// interface contract; gocritic's hugeParam is unavoidable here.
//
//nolint:gocritic // record by value is dictated by slog.Handler
func (h *fanoutHandler) Handle(ctx context.Context, record slog.Record) error {
	for _, handler := range h.handlers {
		if !handler.Enabled(ctx, record.Level) {
			continue
		}
		if err := handler.Handle(ctx, record.Clone()); err != nil {
			return err //nolint:wrapcheck // forwarding interface errors
		}
	}
	return nil
}

func (h *fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		handlers[i] = handler.WithAttrs(attrs)
	}
	return newFanoutHandler(handlers...)
}

func (h *fanoutHandler) WithGroup(name string) slog.Handler {
	handlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		handlers[i] = handler.WithGroup(name)
	}
	return newFanoutHandler(handlers...)
}
