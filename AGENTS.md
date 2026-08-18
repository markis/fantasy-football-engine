# AGENTS.md

Go 1.26 daemon (module `ff-engine`). Single binary: internal cron scheduler + pipeline workers + MCP server over Streamable HTTP. Postgres (pgvector) is the only datastore.

## Commands

```bash
go build ./cmd/fantasy-football-engine   # build
go test ./...                            # all tests (pure unit tests; no DB/services needed)
go test ./internal/pipeline -run TestX   # single test
golangci-lint run                        # lint — CI pins v2.12.2; config is very strict
```

No Makefile/Taskfile. CI (`.github/workflows/lint.yml`) only runs golangci-lint; `docker.yml` builds/pushes to ghcr.io on main and `v*` tags. Run lint before pushing — with 80+ linters enabled, `go vet` alone is not enough.

## Lint conventions that will bite you

- Errors from outside `ff-engine/internal/*` must be wrapped (`wrapcheck`); internal errors may pass through.
- JSON/YAML struct tags must be camelCase (`tagliatelle`), **except** `internal/sleeper/` (mirrors Sleeper's snake_case API) and `internal/corpus/documents.go` (mirrors the corpus JSON schemas in `schemas/`).
- `//nolint` directives require a specific linter name and an explanation.
- Formatter is gofumpt + goimports with local prefix `ff-engine` (own imports grouped last).
- Line limit 140; cyclomatic complexity max 15.

## Migrations — location gotcha

Authoritative migrations are embedded in **`internal/db/migrations/`** (via `go:embed`, run by `db.Pool.RunMigrations`). The root `migrations/` directory is a stale copy — do not add new migrations there. New migration = new `NNN_name.sql` in `internal/db/migrations/`; the runner detects pre-existing databases and marks migrations applied without rerunning (in-place upgrade path).

## Architecture

- `cmd/fantasy-football-engine` — entrypoint; also has a `health` subcommand (Docker HEALTHCHECK on distroless; probes `/readyz`).
- `internal/scheduler` — robfig/cron; schedules use **6-field (seconds) cron** in the configured timezone (see `scheduler.jobs` in `config.example.yaml`).
- `internal/pipeline` — RSS fetch → enrich → embed → dedup (simhash) → cluster → facts → stories.
- `internal/sync` — players/rankings/leaguemates/FantasyPros syncs.
- `internal/corpus` — publishes documents to a separate git repo (go-git push); `schemas/*.schema.json` are the contract for corpus output.
- `internal/mcp` — MCP tool server on `:3100` (compose maps host `3102`).
- `internal/llm`, `internal/embed` — Ollama Cloud + llama-server clients.
- `internal/db` — pgx pool (max 20 conns, pgvector types registered, otel tracing).

## Config & secrets

`config.yaml` is gitignored (contains secrets); copy from `config.example.yaml`. Secrets resolve in order: env var → docker secret file via `FF_SECRETS_DIR` (see `compose.yml`). Required env for local compose: `FF_PG_PASSWORD`, plus `LLM_API_KEY`/`FF_GITHUB_PAT` or files under `./secrets/`.

## Conventions

Conventional Commits (`feat:`, `fix:`, `refactor:`, `chore:` — often scoped, e.g. `feat(telemetry):`). README.md documents the MCP tools and deployment flow; keep it updated when adding tools or jobs.
