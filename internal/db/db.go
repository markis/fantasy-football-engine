package db

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
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
	cfg.AfterConnect = pgxvec.RegisterTypes
	cfg.MaxConns = 20
	instrumentPool(cfg)
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

func instrumentPool(cfg *pgxpool.Config) {
	cfg.ConnConfig.Tracer = otelpgx.NewTracer()
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

	// If the DB already has tables but no migration tracking history yet,
	// this is the first run after tracking was introduced on a pre-existing
	// database: backfill all currently-shipped migrations as already applied
	// (they predate tracking) without running them, so we don't re-run
	// CREATE TABLE IF NOT EXISTS / ALTER TABLE ADD COLUMN IF NOT EXISTS
	// statements that are idempotent but noisy. Any migration added after
	// this backfill will have no row in schema_migrations and will be
	// applied normally by the loop below on a later run.
	if existingTable {
		var trackedCount int
		if err := p.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&trackedCount); err != nil {
			return fmt.Errorf("count tracked migrations: %w", err)
		}
		if trackedCount == 0 {
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
			slog.Info("existing database detected — backfilling pre-tracking migrations as applied")
			return nil
		}
	}

	// Fresh DB, or existing DB with tracking already bootstrapped: apply any
	// pending migrations in order.
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

// dsnKeyValueRedactor matches libpq key=value style credentials, e.g.
// "password=secret".
var dsnKeyValueRedactor = regexp.MustCompile(`(password|passwd|pwd)=([^ ]+)`)

// dsnURLRedactor matches the password in a postgres:// URL DSN, e.g.
// "postgres://user:secret@host/db".
var dsnURLRedactor = regexp.MustCompile(`(://[^:/?#@]+):([^@/?#]+)@`)

func redactDSN(dsn string) string {
	dsn = dsnURLRedactor.ReplaceAllString(dsn, "${1}:***@")
	return dsnKeyValueRedactor.ReplaceAllString(dsn, "${1}=***")
}
