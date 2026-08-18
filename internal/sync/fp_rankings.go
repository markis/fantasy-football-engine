package sync

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"ff-engine/internal/config"
	"ff-engine/internal/db"
	"ff-engine/internal/telemetry"
)

// FPRankingsSyncer syncs FantasyPros ECR dynasty consensus ranks.
type FPRankingsSyncer struct {
	pool   *db.Pool
	cfg    *config.Config
	client *http.Client
}

// NewFPRankingsSyncer creates a new FP rankings syncer.
func NewFPRankingsSyncer(pool *db.Pool, cfg *config.Config) *FPRankingsSyncer {
	return &FPRankingsSyncer{pool: pool, cfg: cfg, client: telemetry.NewHTTPClient(30 * time.Second)}
}

const (
	fpRankingsSource = "FantasyPros ECR"
	fpRankingsMarket = 1
)

// fpRankingsURL is a var (not a const) so tests can point it at a stub server.
var fpRankingsURL = "https://api.fantasypros.com/public/v2/json/nfl/2026/rankings"

// FPRankingsResult is the result of an FP rankings sync.
type FPRankingsResult struct {
	Source         string `json:"source"`
	PlayersWithDyn int    `json:"playersWithDyn"`
	Matched        int    `json:"matched"`
	Unmatched      int    `json:"unmatched"`
	Status         string `json:"status"`
}

// Sync fetches FantasyPros ECR dynasty ranks and upserts into player_ranking.
func (s *FPRankingsSyncer) Sync(ctx context.Context) (*FPRankingsResult, error) {
	result := &FPRankingsResult{Source: fpRankingsSource, Status: "ok"}

	players, err := s.fetchFPRankingsData(ctx)
	if err != nil {
		return nil, err
	}

	byNameTeam, err := s.playerIDsByNameTeam(ctx)
	if err != nil {
		return nil, err
	}

	var matched, skipped int
	var freshIDs []uuid.UUID
	for _, p := range players {
		out := s.processFPRankingRecord(ctx, p, byNameTeam)
		if out.matched {
			matched++
		}
		if out.skipped {
			skipped++
		}
		if out.fresh {
			freshIDs = append(freshIDs, out.pid)
		}
	}

	// Delete stale rows for this source/market, but only once we know we
	// have fresh data to replace them — never wipe on an empty/failed sync.
	if _, err := deleteStaleRankings(ctx, s.pool, fpRankingsSource, fpRankingsMarket, freshIDs); err != nil {
		slog.Warn("failed to delete stale rankings", "err", err)
	}

	result.Matched = matched
	result.Unmatched = skipped
	result.PlayersWithDyn = matched + skipped
	slog.Info("FP rankings sync complete", "matched", matched, "unmatched", skipped)
	return result, nil
}

// fetchFPRankingsData fetches and decodes the raw FantasyPros ECR players
// payload.
func (s *FPRankingsSyncer) fetchFPRankingsData(ctx context.Context) ([]map[string]any, error) {
	var apiResp struct {
		Players []map[string]any `json:"players"`
	}
	if err := fetchFPJSON(ctx, s.client, s.cfg, fpRankingsURL, errFPRankingsHTTP,
		"FP rankings request", "decode FP rankings", &apiResp); err != nil {
		return nil, err
	}
	return apiResp.Players, nil
}

// playerIDsByNameTeam builds a (lower(name)|team) -> player.id lookup used to
// match FantasyPros records against the local player table.
func (s *FPRankingsSyncer) playerIDsByNameTeam(ctx context.Context) (map[string]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx,
		"SELECT id, lower(full_name), team FROM player WHERE active = true")
	if err != nil {
		return nil, fmt.Errorf("query active players: %w", err)
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
	return byNameTeam, nil
}

// fpRankOutcome captures the per-record result of processFPRankingRecord.
// It mirrors the original inline loop's counting semantics exactly: some
// early-exit paths (missing DYN map, missing overall rank) count neither as
// matched nor skipped.
type fpRankOutcome struct {
	pid     uuid.UUID
	matched bool
	skipped bool
	fresh   bool
}

// processFPRankingRecord matches and upserts a single FantasyPros ECR
// record.
func (s *FPRankingsSyncer) processFPRankingRecord(ctx context.Context, p map[string]any, byNameTeam map[string]uuid.UUID) fpRankOutcome {
	rankMap, ok := p["rank"].(map[string]any)
	if !ok {
		return fpRankOutcome{skipped: true}
	}
	ecrMap, ok := getNested(rankMap, "ECR")
	if !ok {
		return fpRankOutcome{skipped: true}
	}
	dynMap, dynOk := ecrMap["DYN"].(map[string]any)
	_ = dynOk
	if dynMap == nil {
		return fpRankOutcome{}
	}
	overall := toInt(dynMap["ALL"])
	if overall == nil {
		return fpRankOutcome{}
	}
	pos := fmt.Sprint(p["position_id"])
	if pos == nilStr {
		pos = ""
	}
	posRank := toInt(dynMap[pos])
	if posRank == nil {
		posRank = toInt(dynMap["ALL"])
	}

	nameKey := strings.ToLower(fmt.Sprint(p["player_name"])) + "|" + fmt.Sprint(p["team_id"])
	pid, ok := byNameTeam[nameKey]
	if !ok {
		return fpRankOutcome{skipped: true}
	}

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
		return fpRankOutcome{pid: pid, matched: true}
	}

	return fpRankOutcome{pid: pid, matched: true, fresh: true}
}

func getNested(m map[string]any, key string) (map[string]any, bool) {
	v, ok := m[key]
	if !ok {
		return nil, false
	}
	result, ok := v.(map[string]any)
	return result, ok
}
