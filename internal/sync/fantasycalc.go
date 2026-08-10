package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/markis/fantasy-football-engine/internal/db"
	"github.com/markis/fantasy-football-engine/internal/models"
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
	sid2pid := make(map[string]uuid.UUID)
	rows, err := s.pool.Query(ctx, "SELECT id, sleeper_player_id FROM player")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var sid string
		if err := rows.Scan(&id, &sid); err != nil {
			continue
		}
		if sid != "" {
			sid2pid[sid] = id
		}
	}
	return sid2pid, nil
}

func (s *FantasyCalcSyncer) syncCombo(ctx context.Context, combo models.FormatCombo, sid2pid map[string]uuid.UUID) (*FantasyCalcResult, error) {
	source := "FantasyCalc"
	result := &FantasyCalcResult{Market: combo.Market, Label: combo.Label}

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
	result.Fetched = len(data)

	cols := []string{
		colPosition, colTeam, "overall_rank", "position_rank",
		"trade_value", "last_month_value", "redraft_value", "percent_owned",
	}

	var freshIDs []uuid.UUID
	for _, rec := range data {
		p, ok := rec["player"].(map[string]any)
		sid := ""
		if ok && p != nil {
			sid = fmt.Sprint(p["sleeperId"])
		}
		if sid == "" || sid == nilStr {
			result.Skipped++
			continue
		}
		pid, ok := sid2pid[sid]
		if !ok {
			result.Skipped++
			continue
		}
		result.Matched++
		freshIDs = append(freshIDs, pid)

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

		params := []any{pid, source, combo.Market}
		vals := []any{
			getStrFromMap(p, "position"), getStrFromMap(p, "maybeTeam"),
			toInt(rec["overallRank"]), toInt(rec["positionRank"]),
			value, lastMonth, toInt(rec["redraftValue"]), pct,
		}
		params = append(params, vals...)

		colNames := ""
		placeholders := ""
		updates := ""
		var colNamesSb161 strings.Builder
		var placeholdersSb161 strings.Builder
		var updatesSb161 strings.Builder
		for i, col := range cols {
			if i > 0 {
				colNamesSb161.WriteString(", ")
				placeholdersSb161.WriteString(", ")
				updatesSb161.WriteString(", ")
			}
			colNamesSb161.WriteString(col)
			placeholdersSb161.WriteString("$" + strconv.Itoa(i+4))
			updatesSb161.WriteString(col + " = EXCLUDED." + col)
		}
		colNames += colNamesSb161.String()
		placeholders += placeholdersSb161.String()
		updates += updatesSb161.String()

		sql := fmt.Sprintf(`
			INSERT INTO player_ranking (player_id, source, market, %s, snapshot_date)
			VALUES ($1, $2, $3, %s, CURRENT_DATE)
			ON CONFLICT (player_id, source, market) DO UPDATE SET
				%s, snapshot_date = CURRENT_DATE, updated_at = now()
		`, colNames, placeholders, updates)

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
	}

	// Remove stale
	if len(freshIDs) > 0 {
		ct, err := s.pool.Exec(ctx,
			"DELETE FROM player_ranking WHERE source=$1 AND market=$2 AND NOT (player_id = ANY($3))",
			source, combo.Market, freshIDs)
		if err == nil {
			result.RemovedStale = int(ct.RowsAffected())
		}
	}

	slog.Info("FantasyCalc sync complete", "market", combo.Market, "matched", result.Matched)
	return result, nil
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
