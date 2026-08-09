package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/markis/fantasy-football-engine/internal/db"
	"github.com/markis/fantasy-football-engine/internal/sleeper"
)

// PlayerSyncer syncs the full Sleeper NFL player database.
type PlayerSyncer struct {
	pool    *db.Pool
	sleeper *sleeper.Client
}

// NewPlayerSyncer creates a new player syncer.
func NewPlayerSyncer(pool *db.Pool, sleeperClient *sleeper.Client) *PlayerSyncer {
	return &PlayerSyncer{pool: pool, sleeper: sleeperClient}
}

// PlayerSyncResult is the result of a player sync.
type PlayerSyncResult struct {
	Synced     int               `json:"synced"`
	ByPosition map[string]int    `json:"by_position"`
	Status     string            `json:"status"`
}

// Sync fetches the full player dump and upserts into the player table.
func (s *PlayerSyncer) Sync(ctx context.Context) (*PlayerSyncResult, error) {
	slog.Info("fetching player dump from Sleeper...")
	players, err := s.sleeper.FetchPlayerDump(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch player dump: %w", err)
	}
	slog.Info("player dump received", "players", len(players))

	// Build batch
	batch := &pgxBatch{pool: s.pool}

	count := 0
	for pid, p := range players {
		row := projectPlayer(pid, p)
		batch.add(row)
		count++
	}

	if err := batch.flush(ctx); err != nil {
		return nil, fmt.Errorf("flush player batch: %w", err)
	}

	// Count by position
	result := &PlayerSyncResult{
		Synced:     count,
		Status:     "ok",
		ByPosition: make(map[string]int),
	}

	rows, err := s.pool.Query(ctx, `
		SELECT position, count(*), count(*) FILTER (WHERE active)
		FROM player GROUP BY position ORDER BY position
	`)
	if err != nil {
		return result, nil
	}
	defer rows.Close()
	for rows.Next() {
		var pos *string
		var total, active int
		if err := rows.Scan(&pos, &total, &active); err != nil {
			continue
		}
		key := "?"
		if pos != nil {
			key = *pos
		}
		result.ByPosition[key] = total
	}

	slog.Info("player sync complete", "synced", count)
	return result, nil
}

func projectPlayer(pid string, p map[string]interface{}) []interface{} {
	cols := []string{
		"first_name", "last_name", "full_name", "search_full_name",
		"position", "fantasy_positions", "team", "team_abbr", "status", "active",
		"injury_status", "injury_body_part", "injury_notes", "injury_start_date",
		"age", "years_exp", "birth_date", "height", "weight", "college", "number",
		"depth_chart_position", "depth_chart_order",
		"practice_participation", "practice_description",
		"gsis_id", "espn_id", "rotowire_id", "rotoworld_id", "yahoo_id",
		"sportradar_id", "stats_id", "news_updated",
	}

	vals := make([]interface{}, len(cols))
	for i, col := range cols {
		vals[i] = p[col]
	}

	// Convert numeric IDs to strings
	for _, idx := range []int{27, 28, 29, 30, 32} { // espn_id, rotowire_id, rotoworld_id, yahoo_id, stats_id
		if vals[idx] != nil {
			vals[idx] = fmt.Sprint(vals[idx])
		}
	}

	// Convert news_updated epoch ms to timestamp
	if vals[33] != nil {
		switch v := vals[33].(type) {
		case float64:
			ts := time.UnixMilli(int64(v)).UTC()
			vals[33] = ts
		case json.Number:
			if n, err := v.Int64(); err == nil {
				vals[33] = time.UnixMilli(n).UTC()
			}
		}
	}

	// fantasy_positions as string array
	if vals[5] != nil {
		switch v := vals[5].(type) {
		case []interface{}:
			arr := make([]string, 0, len(v))
			for _, item := range v {
				arr = append(arr, fmt.Sprint(item))
			}
			vals[5] = arr
		}
	}

	// active as bool
	if vals[9] != nil {
		switch v := vals[9].(type) {
		case bool:
			vals[9] = v
		case float64:
			vals[9] = v != 0
		case string:
			vals[9] = v == "true" || v == "True"
		default:
			vals[9] = false
		}
	} else {
		vals[9] = false
	}

	// Prepend sleeper_player_id
	all := make([]interface{}, 0, len(vals)+1)
	all = append(all, pid)
	all = append(all, vals...)

	_ = cols
	return all
}

// pgxBatch accumulates player rows and executes them in batches.
type pgxBatch struct {
	pool   *db.Pool
	rows   [][]interface{}
}

func (b *pgxBatch) add(row []interface{}) {
	b.rows = append(b.rows, row)
}

func (b *pgxBatch) flush(ctx context.Context) error {
	if len(b.rows) == 0 {
		return nil
	}

	cols := []string{
		"first_name", "last_name", "full_name", "search_full_name",
		"position", "fantasy_positions", "team", "team_abbr", "status", "active",
		"injury_status", "injury_body_part", "injury_notes", "injury_start_date",
		"age", "years_exp", "birth_date", "height", "weight", "college", "number",
		"depth_chart_position", "depth_chart_order",
		"practice_participation", "practice_description",
		"gsis_id", "espn_id", "rotowire_id", "rotoworld_id", "yahoo_id",
		"sportradar_id", "stats_id", "news_updated",
	}

	// Build the SQL
	placeholders := ""
	for i := range cols {
		if i > 0 {
			placeholders += ", "
		}
		placeholders += "$" + strconv.Itoa(i+2)
	}
	colNames := ""
	for i, c := range cols {
		if i > 0 {
			colNames += ", "
		}
		colNames += c
	}
	updates := ""
	for i, c := range cols {
		if i > 0 {
			updates += ", "
		}
		updates += c + " = EXCLUDED." + c
	}

	sql := fmt.Sprintf(`
		INSERT INTO player (sleeper_player_id, %s, last_synced_at)
		VALUES ($1, %s, now())
		ON CONFLICT (sleeper_player_id) DO UPDATE SET %s, last_synced_at = now(), updated_at = now()
	`, colNames, placeholders, updates)

	// Execute batch
	batchSize := 500
	for i := 0; i < len(b.rows); i += batchSize {
		end := i + batchSize
		if end > len(b.rows) {
			end = len(b.rows)
		}
		for _, row := range b.rows[i:end] {
			if _, err := b.pool.Exec(ctx, sql, row...); err != nil {
				slog.Warn("player upsert error", "err", err)
			}
		}
	}
	return nil
}