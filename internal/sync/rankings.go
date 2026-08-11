package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"ff-engine/internal/db"
)

// RankingsSyncer syncs dynasty rankings from Dynasty Daddy.
type RankingsSyncer struct {
	pool   *db.Pool
	client *http.Client
}

// NewRankingsSyncer creates a new rankings syncer.
func NewRankingsSyncer(pool *db.Pool) *RankingsSyncer {
	return &RankingsSyncer{pool: pool, client: &http.Client{Timeout: 120 * time.Second}}
}

const (
	ddURL = "https://dynasty-daddy.com/api/v1/player/all/today"
)

// RankingsSyncResult is the result of a rankings sync.
type RankingsSyncResult struct {
	Fetched        int    `json:"fetched"`
	Matched        int    `json:"matched"`
	SkippedPicks   int    `json:"skippedPicks"`
	SkippedNoMatch int    `json:"skippedNoMatch"`
	RemovedStale   int    `json:"removedStale"`
	Market         int    `json:"market"`
	Source         string `json:"source"`
	Status         string `json:"status"`
}

// Sync fetches Dynasty Daddy rankings and upserts into player_ranking.
func (s *RankingsSyncer) Sync(ctx context.Context, market int, source string) (*RankingsSyncResult, error) {
	if source == "" {
		source = "Dynasty Daddy"
	}
	result := &RankingsSyncResult{Market: market, Source: source, Status: "ok"}

	data, err := s.fetchRankingsData(ctx, market)
	if err != nil {
		return nil, err
	}
	result.Fetched = len(data)
	slog.Info("rankings fetched", "count", len(data), "source", source, "market", market)

	sid2pid, err := sid2pidMap(ctx, s.pool)
	if err != nil {
		return nil, err
	}

	// Data columns to sync
	dataCols := []string{
		"name_id", colPosition, colTeam, "overall_rank", "position_rank",
		"sf_overall_rank", "sf_position_rank", "trade_value", "sf_trade_value",
		"all_time_high", "all_time_low", "all_time_high_sf", "all_time_low_sf",
		"all_time_best_rank", "all_time_worst_rank", "all_time_best_rank_sf", "all_time_worst_rank_sf",
		"three_month_high", "three_month_low", "three_month_high_sf", "three_month_low_sf",
		"three_month_best_rank", "three_month_worst_rank", "three_month_best_rank_sf", "three_month_worst_rank_sf",
		"last_month_value", "last_month_value_sf", "last_month_rank", "last_month_rank_sf",
		"avg_adp", "fantasypro_adp", "bb10_adp", "rtsports_adp", "underdog_adp", "drafters_adp", "dynasty_daddy_adp",
		"avg_ros", "espn_ros", "fantasyguys_ros", "fantasypros_ros", "fanduel_ros",
		"percent_owned", "percent_started",
	}

	var freshIDs []uuid.UUID
	for _, p := range data {
		pid, matched := s.processRankingRecord(ctx, p, market, source, dataCols, sid2pid, result)
		if matched {
			freshIDs = append(freshIDs, pid)
		}
	}

	if removed, err := deleteStaleRankings(ctx, s.pool, source, market, freshIDs); err == nil {
		result.RemovedStale = removed
	}

	slog.Info("rankings sync complete", "matched", result.Matched, "removed", result.RemovedStale)
	return result, nil
}

// fetchRankingsData fetches and decodes the raw Dynasty Daddy rankings
// payload for the given market.
func (s *RankingsSyncer) fetchRankingsData(ctx context.Context, market int) ([]map[string]any, error) {
	url := fmt.Sprintf("%s?market=%d", ddURL, market)
	slog.Info("fetching rankings", "url", url)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build rankings request: %w", err)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch rankings: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w (%d)", errRankingsHTTP, resp.StatusCode)
	}

	var data []map[string]any
	if decodeErr := json.NewDecoder(resp.Body).Decode(&data); decodeErr != nil {
		return nil, fmt.Errorf("decode rankings: %w", decodeErr)
	}
	return data, nil
}

// processRankingRecord matches and upserts a single Dynasty Daddy record,
// returning the matched player id and whether it should count as fresh.
func (s *RankingsSyncer) processRankingRecord(
	ctx context.Context, p map[string]any, market int, source string, dataCols []string,
	sid2pid map[string]uuid.UUID, result *RankingsSyncResult,
) (uuid.UUID, bool) {
	sid := getRankingStr(p, "sleeper_id")
	nid := getRankingStr(p, "name_id")
	if sid == "" || strings.HasSuffix(nid, "pi") {
		result.SkippedPicks++
		return uuid.UUID{}, false
	}
	pid, ok := sid2pid[sid]
	if !ok {
		result.SkippedNoMatch++
		return uuid.UUID{}, false
	}
	result.Matched++

	// Build params
	params := make([]any, 0, len(dataCols)+4)
	params = append(params, pid, source, market)
	for _, col := range dataCols {
		params = append(params, p[col])
	}
	// data_date
	params = append(params, p["date"])

	up := buildUpsertParts(dataCols, 4)

	sql := fmt.Sprintf(`
		INSERT INTO player_ranking (player_id, source, market, %s, data_date, snapshot_date)
		VALUES ($1, $2, $3, %s, $%d, CURRENT_DATE)
		ON CONFLICT (player_id, source, market) DO UPDATE SET
			%s, data_date = EXCLUDED.data_date, snapshot_date = CURRENT_DATE, updated_at = now()
	`, up.colNames, up.placeholders, len(dataCols)+4, up.updates)

	if _, err := s.pool.Exec(ctx, sql, params...); err != nil {
		slog.Warn("ranking upsert", "err", err)
	}

	return pid, true
}

func getRankingStr(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
