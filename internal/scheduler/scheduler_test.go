package scheduler

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"ff-engine/internal/config"
)

func TestRunJob_EmitsSpan(t *testing.T) {
	spanExporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(spanExporter),
	)
	otel.SetTracerProvider(tp)
	defer tp.Shutdown(context.Background())

	s, err := New("UTC")
	if err != nil {
		t.Fatal(err)
	}
	s.RegisterStep("test.step", func(_ context.Context, _ config.JobConfig) error {
		return nil
	})

	job := &config.JobConfig{Name: "test-job", Step: "test.step"}
	s.runJob(context.Background(), job, s.steps["test.step"])

	spans := spanExporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if spans[0].Name != "cron.test-job" {
		t.Errorf("span name = %q, want %q", spans[0].Name, "cron.test-job")
	}
}

func TestRunJob_EmitsMetrics(t *testing.T) {
	reader := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(reader))
	otel.SetMeterProvider(mp)
	defer mp.Shutdown(context.Background())

	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(tracetest.NewInMemoryExporter()))
	otel.SetTracerProvider(tp)
	defer tp.Shutdown(context.Background())

	s, err := New("UTC")
	if err != nil {
		t.Fatal(err)
	}
	s.RegisterStep("test.step", func(_ context.Context, _ config.JobConfig) error {
		return nil
	})

	job := &config.JobConfig{Name: "metric-job", Step: "test.step"}
	s.runJob(context.Background(), job, s.steps["test.step"])

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	foundDuration := false
	foundTotal := false
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			switch m.Name {
			case "ff.jobs.duration":
				foundDuration = true
			case "ff.jobs.total":
				foundTotal = true
			}
		}
	}
	if !foundDuration {
		t.Error("ff.jobs.duration histogram not emitted")
	}
	if !foundTotal {
		t.Error("ff.jobs.total counter not emitted")
	}
}

func TestRunJob_SkipsDuplicateRun(t *testing.T) {
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(tracetest.NewInMemoryExporter()))
	otel.SetTracerProvider(tp)
	defer tp.Shutdown(context.Background())

	s, err := New("UTC")
	if err != nil {
		t.Fatal(err)
	}

	block := make(chan struct{})
	s.RegisterStep("slow.step", func(_ context.Context, _ config.JobConfig) error {
		<-block
		return nil
	})

	job := &config.JobConfig{Name: "dupe-job", Step: "slow.step"}
	go s.runJob(context.Background(), job, s.steps["slow.step"])
	time.Sleep(50 * time.Millisecond)
	s.runJob(context.Background(), job, s.steps["slow.step"])
	close(block)
}
