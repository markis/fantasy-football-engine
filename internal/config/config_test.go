package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTelemetryConfig_Defaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("telemetry:\n  oTelEndpoint: \"http://localhost:4318\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Telemetry.OTelEndpoint != "http://localhost:4318" {
		t.Errorf("OTelEndpoint = %q", cfg.Telemetry.OTelEndpoint)
	}
	if cfg.Telemetry.ServiceName != "fantasy-football-engine" {
		t.Errorf("ServiceName = %q, want %q", cfg.Telemetry.ServiceName, "fantasy-football-engine")
	}
	if cfg.Telemetry.Env != "production" {
		t.Errorf("Env = %q, want %q", cfg.Telemetry.Env, "production")
	}
	if cfg.Telemetry.SampleRate != 1.0 {
		t.Errorf("SampleRate = %v, want 1.0", cfg.Telemetry.SampleRate)
	}
}

func TestTelemetryConfig_MetricsAddr(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("telemetry:\n  oTelEndpoint: \"\"\n  metricsAddr: \":3101\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Telemetry.MetricsAddr != ":3101" {
		t.Errorf("MetricsAddr = %q, want %q", cfg.Telemetry.MetricsAddr, ":3101")
	}
	if cfg.Server.MCPAddr != ":3100" {
		t.Errorf("MCPAddr default = %q, want %q", cfg.Server.MCPAddr, ":3100")
	}
}

func TestServerConfig_NoMetricsAddr(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("server:\n  mcpAddr: \":3200\"\n  metricsAddr: \":9999\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Telemetry.MetricsAddr != "" {
		t.Errorf("Telemetry.MetricsAddr should be empty when server.metricsAddr is set, got %q", cfg.Telemetry.MetricsAddr)
	}
}
