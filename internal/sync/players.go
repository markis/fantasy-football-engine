package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"ff-engine/internal/db"
	"ff-engine/internal/sleeper"
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
	Synced     int            `json:"synced"`
	ByPosition map[string]int `json:"byPosition"`
	Status     string         `json:"status"`
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

	batch.flush(ctx)

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

func projectPlayer(pid string, p *sleeper.Player) []any {
	// active is bool/number/string/null in the dump; coerce to bool.
	var active bool
	switch v := p.Active.(type) {
	case bool:
		active = v
	case float64:
		active = v != 0
	case string:
		active = v == "true" || v == "True"
	default:
		active = false
	}

	// news_updated is an epoch-ms number or null; convert to a timestamp.
	newsUpdated := p.NewsUpdated
	switch x := newsUpdated.(type) {
	case float64:
		newsUpdated = time.UnixMilli(int64(x)).UTC()
	case json.Number:
		if n, err := x.Int64(); err == nil {
			newsUpdated = time.UnixMilli(n).UTC()
		}
	}

	return []any{
		pid,
		p.FirstName,
		p.LastName,
		p.FullName,
		p.SearchFullName,
		p.Position,
		p.FantasyPositions,
		p.Team,
		p.TeamAbbr,
		p.Status,
		active,
		p.InjuryStatus,
		p.InjuryBodyPart,
		p.InjuryNotes,
		p.InjuryStartDate,
		p.Age,
		p.YearsExp,
		p.BirthDate,
		p.Height,
		p.Weight,
		p.College,
		p.Number,
		p.DepthChartPosition, // int or string; pgx coerces to TEXT
		p.DepthChartOrder,
		p.PracticeParticipation,
		p.PracticeDescription,
		p.GsisID,
		p.EspnID.Ptr(),
		p.RotowireID.Ptr(),
		p.RotoworldID.Ptr(),
		p.YahooID.Ptr(),
		p.SportradarID.Ptr(),
		p.StatsID.Ptr(),
		newsUpdated,
	}
}

// pgxBatch accumulates player rows and executes them in batches.
type pgxBatch struct {
	pool *db.Pool
	rows [][]any
}

func (b *pgxBatch) add(row []any) {
	b.rows = append(b.rows, row)
}

func (b *pgxBatch) flush(ctx context.Context) {
	if len(b.rows) == 0 {
		return
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
	placeholders := make([]string, len(cols))
	for i := range cols {
		placeholders[i] = "$" + strconv.Itoa(i+2)
	}
	colNames := make([]string, len(cols))
	updates := make([]string, len(cols))
	for i, c := range cols {
		colNames[i] = c
		updates[i] = c + " = EXCLUDED." + c
	}

	sql := fmt.Sprintf(`
		INSERT INTO player (sleeper_player_id, %s, last_synced_at)
		VALUES ($1, %s, now())
		ON CONFLICT (sleeper_player_id) DO UPDATE SET %s, last_synced_at = now(), updated_at = now()
	`, strings.Join(colNames, ", "), strings.Join(placeholders, ", "), strings.Join(updates, ", "))

	// Execute batch
	batchSize := 500
	for i := 0; i < len(b.rows); i += batchSize {
		end := min(i+batchSize, len(b.rows))
		for _, row := range b.rows[i:end] {
			if _, err := b.pool.Exec(ctx, sql, row...); err != nil {
				slog.Warn("player upsert error", "err", err)
			}
		}
	}
}
