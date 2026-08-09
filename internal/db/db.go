package db

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
	pgxvec "github.com/pgvector/pgvector-go/pgx"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Pool is the global pgx connection pool.
type Pool struct {
	*pgxpool.Pool
}

// New creates a new connection pool with pgvector types registered.
func New(ctx context.Context, dsn string) (*Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		return pgxvec.RegisterTypes(ctx, conn)
	}
	cfg.MaxConns = 20
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}
	slog.Info("database connected", "dsn", redactDSN(dsn))
	return &Pool{pool}, nil
}

// RunMigrations applies any pending SQL migrations. It uses a simple
// schema_migrations table to track applied migrations. For an existing DB
// (in-place upgrade), it detects existing tables and marks all current
// migrations as already applied without re-running them.
func (p *Pool) RunMigrations(ctx context.Context) error {
	// Create the migrations tracking table.
	_, err := p.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version  TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	// Check if this is an existing DB (has the `source` table from 001_schema).
	var existingTable bool
	err = p.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = 'source'
		)
	`).Scan(&existingTable)
	if err != nil {
		return fmt.Errorf("check existing schema: %w", err)
	}

	// List embedded migration files.
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	// If the DB already has tables (in-place upgrade), mark all existing
	// migrations as applied without running them. This prevents re-running
	// CREATE TABLE IF NOT EXISTS / ALTER TABLE ADD COLUMN IF NOT EXISTS
	// statements that are idempotent but noisy.
	if existingTable {
		for _, e := range entries {
			version := strings.TrimSuffix(e.Name(), ".sql")
			_, err := p.Exec(ctx, `
				INSERT INTO schema_migrations (version) VALUES ($1)
				ON CONFLICT (version) DO NOTHING
			`, version)
			if err != nil {
				return fmt.Errorf("mark migration %s: %w", version, err)
			}
		}
		slog.Info("existing database detected — all migrations marked as applied (in-place upgrade)")
		return nil
	}

	// Fresh DB: apply migrations in order.
	for _, e := range entries {
		version := strings.TrimSuffix(e.Name(), ".sql")
		var already bool
		err := p.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, version).Scan(&already)
		if err != nil {
			return fmt.Errorf("check migration %s: %w", version, err)
		}
		if already {
			continue
		}

		data, err := migrationsFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", version, err)
		}

		slog.Info("applying migration", "version", version)
		_, err = p.Exec(ctx, string(data))
		if err != nil {
			return fmt.Errorf("apply migration %s: %w", version, err)
		}

		_, err = p.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1) ON CONFLICT DO NOTHING`, version)
		if err != nil {
			return fmt.Errorf("record migration %s: %w", version, err)
		}
	}
	slog.Info("migrations complete")
	return nil
}

// Vector is an alias for pgvector.Vector for convenience.
type Vector = pgvector.Vector

// NewVector creates a new pgvector.Vector from a float32 slice.
func NewVector(vals []float32) Vector {
	return pgvector.NewVector(vals)
}

var dsnRedactor = regexp.MustCompile(`(password|passwd|pwd)=([^ ]+)`)

func redactDSN(dsn string) string {
	return dsnRedactor.ReplaceAllString(dsn, "${1}=***")
}