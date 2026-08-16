package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"slices"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
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

const protocolGRPC = "grpc"

type Config struct {
	ServiceName  string
	OTelEndpoint string
	Protocol     string // "http" (default) or "grpc"
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

func Init(cfg *Config) (*Provider, error) {
	if cfg.OTelEndpoint == "" {
		slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		})))
		slog.Info("telemetry disabled — no OTel endpoint configured")
		return &Provider{otel: false}, nil
	}

	traceExporter, err := newTraceExporter(cfg.Protocol, cfg.OTelEndpoint)
	if err != nil {
		slog.Warn("telemetry: trace exporter init failed — traces disabled", "err", err)
	}
	metricExporter, err := newMetricExporter(cfg.Protocol, cfg.OTelEndpoint)
	if err != nil {
		slog.Warn("telemetry: metric exporter init failed — OTLP metrics disabled", "err", err)
	}
	logExporter, err := newLogExporter(cfg.Protocol, cfg.OTelEndpoint)
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
	installSlogHandler(cfg.ServiceName)
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

func newTraceExporter(protocol, endpoint string) (sdktrace.SpanExporter, error) {
	if protocol == protocolGRPC {
		exp, err := otlptracegrpc.New(context.Background(), otlptracegrpc.WithEndpointURL(endpoint))
		if err != nil {
			return nil, fmt.Errorf("trace grpc exporter: %w", err)
		}
		return exp, nil
	}
	exp, err := otlptracehttp.New(context.Background(), otlptracehttp.WithEndpointURL(endpoint))
	if err != nil {
		return nil, fmt.Errorf("trace http exporter: %w", err)
	}
	return exp, nil
}

func newMetricExporter(protocol, endpoint string) (sdkmetric.Exporter, error) {
	if protocol == protocolGRPC {
		exp, err := otlpmetricgrpc.New(context.Background(), otlpmetricgrpc.WithEndpointURL(endpoint))
		if err != nil {
			return nil, fmt.Errorf("metric grpc exporter: %w", err)
		}
		return exp, nil
	}
	exp, err := otlpmetrichttp.New(context.Background(), otlpmetrichttp.WithEndpointURL(endpoint))
	if err != nil {
		return nil, fmt.Errorf("metric http exporter: %w", err)
	}
	return exp, nil
}

func newLogExporter(protocol, endpoint string) (sdklog.Exporter, error) {
	if protocol == protocolGRPC {
		exp, err := otlploggrpc.New(context.Background(), otlploggrpc.WithEndpointURL(endpoint))
		if err != nil {
			return nil, fmt.Errorf("log grpc exporter: %w", err)
		}
		return exp, nil
	}
	exp, err := otlploghttp.New(context.Background(), otlploghttp.WithEndpointURL(endpoint))
	if err != nil {
		return nil, fmt.Errorf("log http exporter: %w", err)
	}
	return exp, nil
}
