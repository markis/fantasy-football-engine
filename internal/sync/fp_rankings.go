package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/markis/fantasy-football-engine/internal/config"
	"github.com/markis/fantasy-football-engine/internal/db"
)

// FPRankingsSyncer syncs FantasyPros ECR dynasty consensus ranks.
type FPRankingsSyncer struct {
	pool   *db.Pool
	cfg    *config.Config
	client *http.Client
}

// NewFPRankingsSyncer creates a new FP rankings syncer.
func NewFPRankingsSyncer(pool *db.Pool, cfg *config.Config) *FPRankingsSyncer {
	return &FPRankingsSyncer{pool: pool, cfg: cfg, client: &http.Client{Timeout: 30 * time.Second}}
}

const (
	fpRankingsURL    = "https://api.fantasypros.com/public/v2/json/nfl/2026/rankings"
	fpRankingsSource = "FantasyPros ECR"
	fpRankingsMarket = 1
)

// FPRankingsResult is the result of an FP rankings sync.
type FPRankingsResult struct {
	Source         string `json:"source"`
	PlayersWithDyn int    `json:"players_with_dyn_rank"`
	Matched        int    `json:"matched"`
	Unmatched      int    `json:"unmatched"`
	Status         string `json:"status"`
}

// Sync fetches FantasyPros ECR dynasty ranks and upserts into player_ranking.
func (s *FPRankingsSyncer) Sync(ctx context.Context) (*FPRankingsResult, error) {
	result := &FPRankingsResult{Source: fpRankingsSource, Status: "ok"}

	apiKey, err := config.PassShow(s.cfg.FantasyPros.APIKeyPass)
	if err != nil {
		return nil, fmt.Errorf("get FP API key: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fpRankingsURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", apiKey)
	req.Header.Set("Accept", "application/json")
	q := req.URL.Query()
	q.Add("limit", "500")
	req.URL.RawQuery = q.Encode()

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("FP rankings request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			body = []byte("(unable to read error response body)")
		}
		return nil, fmt.Errorf("%w (%d): %s", errFPRankingsHTTP, resp.StatusCode, string(body))
	}

	var apiResp struct {
		Players []map[string]any `json:"players"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("decode FP rankings: %w", err)
	}
	players := apiResp.Players

	// Match by (lower(name), team)
	rows, err := s.pool.Query(ctx,
		"SELECT id, lower(full_name), team FROM player WHERE active = true")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byNameTeam := make(map[string]uuid.UUID)
	for rows.Next() {
		var id uuid.UUID
		var fullName, team *string
		if err := rows.Scan(&id, &fullName, &team); err != nil {
			continue
		}
		if fullName != nil && team != nil {
			byNameTeam[*fullName+"|"+*team] = id
		}
	}

	var matched, skipped int
	var freshIDs []uuid.UUID
	for _, p := range players {
		rankMap, ok := p["rank"].(map[string]any)
		if !ok {
			skipped++
			continue
		}
		ecrMap, ok := getNested(rankMap, "ECR")
		if !ok {
			skipped++
			continue
		}
		dynMap, ok := ecrMap["DYN"].(map[string]any)
		_ = ok
		if dynMap == nil {
			continue
		}
		overall := toInt(dynMap["ALL"])
		if overall == nil {
			continue
		}
		pos := fmt.Sprint(p["position_id"])
		if pos == "<nil>" {
			pos = ""
		}
		posRank := toInt(dynMap[pos])
		if posRank == nil {
			posRank = toInt(dynMap["ALL"])
		}

		nameKey := strings.ToLower(fmt.Sprint(p["player_name"])) + "|" + fmt.Sprint(p["team_id"])
		pid, ok := byNameTeam[nameKey]
		if !ok {
			skipped++
			continue
		}
		matched++

		_, err := s.pool.Exec(ctx, `
			INSERT INTO player_ranking (player_id, source, market, position, team,
			                            overall_rank, position_rank, snapshot_date)
			VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, '<nil>'), $6, $7, CURRENT_DATE)
			ON CONFLICT (player_id, source, market) DO UPDATE SET
				position = EXCLUDED.position, team = EXCLUDED.team,
				overall_rank = EXCLUDED.overall_rank, position_rank = EXCLUDED.position_rank,
				snapshot_date = CURRENT_DATE, updated_at = now()
		`, pid, fpRankingsSource, fpRankingsMarket, pos, fmt.Sprint(p["team_id"]), *overall, posRank)
		if err != nil {
			slog.Warn("FP ranking upsert", "err", err)
			continue
		}
		freshIDs = append(freshIDs, pid)
	}

	// Delete stale rows for this source/market, but only once we know we
	// have fresh data to replace them — never wipe on an empty/failed sync.
	if len(freshIDs) > 0 {
		if _, err := s.pool.Exec(ctx,
			"DELETE FROM player_ranking WHERE source = $1 AND market = $2 AND NOT (player_id = ANY($3))",
			fpRankingsSource, fpRankingsMarket, freshIDs); err != nil {
			slog.Warn("failed to delete stale rankings", "err", err)
		}
	}

	result.Matched = matched
	result.Unmatched = skipped
	result.PlayersWithDyn = matched + skipped
	slog.Info("FP rankings sync complete", "matched", matched, "unmatched", skipped)
	return result, nil
}

func getNested(m map[string]any, key string) (map[string]any, bool) {
	v, ok := m[key]
	if !ok {
		return nil, false
	}
	result, ok := v.(map[string]any)
	return result, ok
}
