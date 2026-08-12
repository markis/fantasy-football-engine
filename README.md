# fantasy-football-engine

A standalone Go daemon that runs the fantasy football intelligence pipeline
and exposes it as an MCP server.

## Overview

- **Standalone daemon** — no dependency on zeroclaw's scheduler or skill system
- **Internal cron scheduler** — runs 20 pipeline jobs autonomously
- **MCP server** — exposes ~15 query/interaction tools over Streamable HTTP
- **Single binary** — no venv, no Python, no self-heal scripts

## Architecture

```
fantasy-football-engine (Go daemon)
 ├── internal cron scheduler (20 jobs)
 ├── pipeline workers (RSS, enrich, embed, dedup, cluster, facts, stories)
 ├── sync workers (players, rankings, fantasycalc, leaguemates, FP)
 ├── corpus publisher (render → validate → go-git push)
 ├── MCP server (Streamable HTTP, :3100)
 ├── Postgres (ff-pg, pgvector)
 └── HTTP clients: RSS, Sleeper API, Ollama Cloud, llama-server
```

## Quick start

1. Copy `config.example.yaml` to `config.yaml` and adjust
2. Set environment variables: `FF_PG_PASSWORD`, `LLM_API_KEY`, `FF_GITHUB_PAT`
3. `docker compose up -d`
4. Connect an MCP client to `http://localhost:3102/mcp`

## MCP tools

### News & stories
- `search_news` — semantic search over news items
- `get_stories` — top story clusters by time window
- `search_facts` — semantic search over extracted facts
- `get_recent_news` — latest N news items

### Players & valuations
- `search_players` — by name, position, team
- `get_player` — full player profile
- `get_rankings` — dynasty trade values (1QB / SF)
- `get_trending_players` — Sleeper trending (add/drop)
- `get_free_agents` — best ranked available players in a league

### Leagues
- `get_nfl_state` — current week, season

### Team & assessment
- `evaluate_roster` — structured roster data with trade values
- `evaluate_trade` — trade proposal data (both sides' values, delta)
- `get_study_material` — digest for agent self-study

### Pipeline control
- `trigger_pipeline` — manually trigger a named step

## Configuration

See `config.example.yaml` for all options. Secrets can be provided via
environment variables or `pass` (the password store).

## Development

```bash
go build ./cmd/fantasy-football-engine
./fantasy-football-engine -config config.yaml
```

## Migration from the Python pipeline

The daemon supports an **in-place upgrade**: point it at the existing `ff-pg`
data directory and it detects the existing schema, marks all migrations as
applied, and continues from where the Python pipeline left off. No data loss.