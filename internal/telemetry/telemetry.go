package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

const testTimeout = 5 * time.Second

type Config struct {
	ServiceName  string
	OTelEndpoint string
	MetricsAddr  string
	Env          string
	SampleRate   float64
}

type Provider struct {
	otel           bool
	tracerProvider *sdktrace.TracerProvider
	meterProvider  *sdkmetric.MeterProvider
	loggerProvider *sdklog.LoggerProvider
	promExporter   *prometheus.Exporter
	shutdownFns    []func(ctx context.Context) error
}

func Init(cfg Config) (*Provider, error) {
	if cfg.OTelEndpoint == "" {
		slog.Info("telemetry disabled — no OTel endpoint configured")
		return &Provider{otel: false}, nil
	}

	opts := []otlptracehttp.Option{otlptracehttp.WithEndpointURL(cfg.OTelEndpoint)}
	traceExporter, err := otlptracehttp.New(context.Background(), opts...)
	if err != nil {
		slog.Warn("telemetry: trace exporter init failed — traces disabled", "err", err)
	}
	metricOpts := []otlpmetrichttp.Option{otlpmetrichttp.WithEndpointURL(cfg.OTelEndpoint)}
	metricExporter, err := otlpmetrichttp.New(context.Background(), metricOpts...)
	if err != nil {
		slog.Warn("telemetry: metric exporter init failed — OTLP metrics disabled", "err", err)
	}
	logOpts := []otlploghttp.Option{otlploghttp.WithEndpointURL(cfg.OTelEndpoint)}
	logExporter, err := otlploghttp.New(context.Background(), logOpts...)
	if err != nil {
		slog.Warn("telemetry: log exporter init failed — OTel logs disabled", "err", err)
	}

	res, err := resource.New(context.Background(),
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion("0.1.0"),
			attribute.String("deployment.environment", cfg.Env),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry resource: %w", err)
	}

	p := &Provider{otel: true}
	var shutdownFns []func(ctx context.Context) error

	if traceExporter != nil {
		sampler := sdktrace.TraceIDRatioBased(cfg.SampleRate)
		tp := sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(traceExporter),
			sdktrace.WithResource(res),
			sdktrace.WithSampler(sdktrace.ParentBased(sampler)),
		)
		otel.SetTracerProvider(tp)
		p.tracerProvider = tp
		shutdownFns = append(shutdownFns, tp.Shutdown)
	}

	var readers []sdkmetric.Reader

	if metricExporter != nil {
		readers = append(readers, sdkmetric.NewPeriodicReader(metricExporter))
	}

	promExp, err := prometheus.New()
	if err != nil {
		slog.Warn("telemetry: prometheus exporter init failed — /metrics disabled", "err", err)
	} else {
		readers = append(readers, promExp)
		p.promExporter = promExp
	}

	if len(readers) > 0 {
		mpOpts := make([]sdkmetric.Option, 0, len(readers)+1)
		for _, r := range readers {
			mpOpts = append(mpOpts, sdkmetric.WithReader(r))
		}
		mpOpts = append(mpOpts, sdkmetric.WithResource(res))
		mp := sdkmetric.NewMeterProvider(mpOpts...)
		otel.SetMeterProvider(mp)
		p.meterProvider = mp
		shutdownFns = append(shutdownFns, mp.Shutdown)
	}

	if logExporter != nil {
		lp := sdklog.NewLoggerProvider(
			sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
			sdklog.WithResource(res),
		)
		global.SetLoggerProvider(lp)
		p.loggerProvider = lp
		shutdownFns = append(shutdownFns, lp.Shutdown)
	}

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	p.shutdownFns = shutdownFns
	slog.Info("telemetry initialized", "service", cfg.ServiceName, "endpoint", cfg.OTelEndpoint, "metricsAddr", cfg.MetricsAddr)
	return p, nil
}

func (p *Provider) otelEnabled() bool {
	return p.otel
}

func (p *Provider) Shutdown(ctx context.Context) {
	for _, fn := range slices.Backward(p.shutdownFns) {
		if err := fn(ctx); err != nil {
			slog.Warn("telemetry shutdown error", "err", err)
		}
	}
	p.shutdownFns = nil
}

func (p *Provider) PrometheusHandler() http.HandlerFunc {
	if p.promExporter == nil {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}
	return promhttp.Handler().ServeHTTP
}

func InstrumentPool(cfg *pgxpool.Config) {
	cfg.ConnConfig.Tracer = otelpgx.NewTracer()
}
