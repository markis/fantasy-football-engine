package telemetry

import (
	"context"
	"testing"
)

func TestInit_NoOpWhenEndpointEmpty(t *testing.T) {
	p, err := Init(Config{ServiceName: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if p == nil {
		t.Fatal("Init returned nil provider")
	}
	if p.otelEnabled() {
		t.Fatal("provider should be in no-op mode when OTelEndpoint is empty")
	}
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	p.Shutdown(ctx)
}

func TestInit_OTelEnabledWhenEndpointSet(t *testing.T) {
	p, err := Init(Config{
		ServiceName:  "test",
		OTelEndpoint: "http://localhost:4318",
		MetricsAddr:  "",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !p.otelEnabled() {
		t.Fatal("provider should be in OTel mode when OTelEndpoint is set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	p.Shutdown(ctx)
}

func TestShutdown_Idempotent(t *testing.T) {
	p, err := Init(Config{ServiceName: "test"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	p.Shutdown(ctx)
	p.Shutdown(ctx)
}
