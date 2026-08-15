package db

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestInstrumentPool_InstallsTracer(t *testing.T) {
	cfg, err := pgxpool.ParseConfig("postgres://user:pass@localhost:5432/db?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	instrumentPool(cfg)
	if cfg.ConnConfig.Tracer == nil {
		t.Fatal("pgx tracer not installed on pool config")
	}
}
