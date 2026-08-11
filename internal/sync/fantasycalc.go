package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"ff-engine/internal/db"
	"ff-engine/internal/models"
)

var (
	errFantasyCalcHTTP = errors.New("FantasyCalc HTTP error")
	errFPInjuriesHTTP  = errors.New("FP injuries HTTP error")
	errFPRankingsHTTP  = errors.New("FP rankings HTTP error")
	errRankingsHTTP    = errors.New("rankings HTTP error")
)

// FantasyCalcSyncer syncs FantasyCalc dynasty values per league format.
type FantasyCalcSyncer struct {
	pool   *db.Pool
	client *http.Client
}

// NewFantasyCalcSyncer creates a new FantasyCalc syncer.
func NewFantasyCalcSyncer(pool *db.Pool) *FantasyCalcSyncer {
	return &FantasyCalcSyncer{pool: pool, client: &http.Client{Timeout: 120 * time.Second}}
}

const (
	fcBase = "https://api.fantasycalc.com/values/current"
)

// FantasyCalcResult is the result of syncing one format combo.
type FantasyCalcResult struct {
	Market       int    `json:"market"`
	Label        string `json:"label"`
	Fetched      int    `json:"fetched"`
	Matched      int    `json:"matched"`
	Skipped      int    `json:"skipped"`
	RemovedStale int    `json:"removedStale"`
}

// SyncAll syncs all format combos.
func (s *FantasyCalcSyncer) SyncAll(ctx context.Context) ([]FantasyCalcResult, error) {
	// Build sleeper_id -> player.id once — every combo below matches against
	// the same player table, so there's no need to re-scan it per combo.
	sid2pid, err := s.playerIDsBySleeperID(ctx)
	if err != nil {
		return nil, err
	}

	var results []FantasyCalcResult
	for _, combo := range models.FormatCombos {
		r, err := s.syncCombo(ctx, combo, sid2pid)
		if err != nil {
			slog.Warn("FantasyCalc sync combo", "market", combo.Market, "err", err)
			continue
		}
		results = append(results, *r)
	}
	return results, nil
}

func (s *FantasyCalcSyncer) playerIDsBySleeperID(ctx context.Context) (map[string]uuid.UUID, error) {
	return sid2pidMap(ctx, s.pool)
}

func (s *FantasyCalcSyncer) syncCombo(
	ctx context.Context, combo models.FormatCombo, sid2pid map[string]uuid.UUID,
) (*FantasyCalcResult, error) {
	source := "FantasyCalc"
	result := &FantasyCalcResult{Market: combo.Market, Label: combo.Label}

	data, err := s.fetchFantasyCalcData(ctx, combo)
	if err != nil {
		return nil, err
	}
	result.Fetched = len(data)

	cols := []string{
		colPosition, colTeam, "overall_rank", "position_rank",
		"trade_value", "last_month_value", "redraft_value", "percent_owned",
	}

	var freshIDs []uuid.UUID
	for _, rec := range data {
		pid, matched := s.processFantasyCalcRecord(ctx, rec, combo, sid2pid, source, cols, result)
		if matched {
			freshIDs = append(freshIDs, pid)
		}
	}

	if removed, err := deleteStaleRankings(ctx, s.pool, source, combo.Market, freshIDs); err == nil {
		result.RemovedStale = removed
	}

	slog.Info("FantasyCalc sync complete", "market", combo.Market, "matched", result.Matched)
	return result, nil
}

// fetchFantasyCalcData fetches and decodes the raw FantasyCalc values payload
// for a single format combo.
func (s *FantasyCalcSyncer) fetchFantasyCalcData(ctx context.Context, combo models.FormatCombo) ([]map[string]any, error) {
	params := fmt.Sprintf("?isDynasty=true&numQbs=%d&numTeams=%d&ppr=%d&tep=%s&includeAdp=false&includeRosterPercent=false",
		combo.NumQbs, combo.Teams, combo.PPR, combo.TEP)
	url := fcBase + params
	slog.Info("fetching FantasyCalc", "market", combo.Market, "label", combo.Label)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch FantasyCalc: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w (%d)", errFantasyCalcHTTP, resp.StatusCode)
	}

	var data []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode FantasyCalc: %w", err)
	}
	return data, nil
}

// processFantasyCalcRecord matches and upserts a single FantasyCalc record,
// returning the matched player id and whether it should count as fresh.
func (s *FantasyCalcSyncer) processFantasyCalcRecord(
	ctx context.Context, rec map[string]any, combo models.FormatCombo,
	sid2pid map[string]uuid.UUID, source string, cols []string, result *FantasyCalcResult,
) (uuid.UUID, bool) {
	p, ok := rec["player"].(map[string]any)
	sid := ""
	if ok && p != nil {
		sid = fmt.Sprint(p["sleeperId"])
	}
	if sid == "" || sid == nilStr {
		result.Skipped++
		return uuid.UUID{}, false
	}
	pid, ok := sid2pid[sid]
	if !ok {
		result.Skipped++
		return uuid.UUID{}, false
	}
	result.Matched++

	value := toInt(rec["value"])
	trend30 := toInt(rec["trend30Day"])
	var lastMonth *int
	if value != nil {
		lm := *value
		if trend30 != nil {
			lm -= *trend30
		}
		lastMonth = &lm
	}

	var pct *string
	if p != nil {
		if rp, ok := p["maybeRosterPercent"].(float64); ok {
			pctStr := fmt.Sprintf("%.2f", rp*100)
			pct = &pctStr
		}
	}

	params := []any{
		pid, source, combo.Market,
		getStrFromMap(p, "position"), getStrFromMap(p, "maybeTeam"),
		toInt(rec["overallRank"]), toInt(rec["positionRank"]),
		value, lastMonth, toInt(rec["redraftValue"]), pct,
	}

	up := buildUpsertParts(cols, 4)

	sql := fmt.Sprintf(`
		INSERT INTO player_ranking (player_id, source, market, %s, snapshot_date)
		VALUES ($1, $2, $3, %s, CURRENT_DATE)
		ON CONFLICT (player_id, source, market) DO UPDATE SET
			%s, snapshot_date = CURRENT_DATE, updated_at = now()
	`, up.colNames, up.placeholders, up.updates)

	if _, err := s.pool.Exec(ctx, sql, params...); err != nil {
		slog.Warn("FC ranking upsert", "err", err)
	}

	// History snapshot
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO player_ranking_history (player_id, source, market, snapshot_date, trade_value, overall_rank, position_rank, redraft_value)
		VALUES ($1, $2, $3, CURRENT_DATE, $4, $5, $6, $7)
		ON CONFLICT (player_id, source, market, snapshot_date) DO UPDATE SET
			trade_value = EXCLUDED.trade_value, overall_rank = EXCLUDED.overall_rank,
			position_rank = EXCLUDED.position_rank, redraft_value = EXCLUDED.redraft_value, created_at = now()
	`, pid, source, combo.Market, value, toInt(rec["overallRank"]), toInt(rec["positionRank"]), toInt(rec["redraftValue"])); err != nil {
		slog.Warn("insert ranking history", "err", err)
	}

	return pid, true
}

func toInt(v any) *int {
	if v == nil {
		return nil
	}
	switch val := v.(type) {
	case float64:
		n := int(val)
		return &n
	case int:
		n := val
		return &n
	case string:
		if n, err := strconv.Atoi(val); err == nil {
			return &n
		}
		return nil
	case json.Number:
		if n, err := val.Int64(); err == nil {
			i := int(n)
			return &i
		}
		return nil
	default:
		return nil
	}
}

func getStrFromMap(m map[string]any, key string) *string {
	if m == nil {
		return nil
	}
	v, ok := m[key]
	if !ok || v == nil {
		return nil
	}
	s := fmt.Sprint(v)
	return &s
}
