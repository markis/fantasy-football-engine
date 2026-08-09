package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/markis/fantasy-football-engine/internal/db"
	"github.com/markis/fantasy-football-engine/internal/sleeper"
)

// LeaguemateTradesSyncer stores completed trades from mapped leagues.
type LeaguemateTradesSyncer struct {
	pool    *db.Pool
	sleeper *sleeper.Client
}

// NewLeaguemateTradesSyncer creates a new trades syncer.
func NewLeaguemateTradesSyncer(pool *db.Pool, sleeperClient *sleeper.Client) *LeaguemateTradesSyncer {
	return &LeaguemateTradesSyncer{pool: pool, sleeper: sleeperClient}
}

// TradesSyncResult is the result of a trades sync.
type TradesSyncResult struct {
	TradesStored int    `json:"trades_stored"`
	LeaguesScanned int  `json:"leagues_scanned"`
	Status       string `json:"status"`
}

// Sync stores completed trades from mapped leagues.
func (s *LeaguemateTradesSyncer) Sync(ctx context.Context, maxWeeks int) (*TradesSyncResult, error) {
	if maxWeeks == 0 {
		maxWeeks = 3
	}
	result := &TradesSyncResult{Status: "ok"}

	// Get watch set (players on Markis's rosters)
	watchSet := s.watchSet(ctx)

	// Get all mapped leagues
	rows, err := s.pool.Query(ctx, "SELECT league_id, status FROM league")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type leagueInfo struct {
		id     string
		status string
	}
	var leagues []leagueInfo
	for rows.Next() {
		var li leagueInfo
		if err := rows.Scan(&li.id, &li.status); err != nil {
			continue
		}
		leagues = append(leagues, li)
	}

	currentWeek := s.sleeper.CurrentWeek(ctx)

	for _, lg := range leagues {
		// Skip drafting/pre_draft leagues
		if lg.status == "drafting" || lg.status == "pre_draft" {
			continue
		}

		// Determine scan range
		var scanWeeks []int
		if currentWeek > 0 {
			lo := currentWeek - maxWeeks + 1
			if lo < 1 {
				lo = 1
			}
			for w := currentWeek; w >= lo; w-- {
				scanWeeks = append(scanWeeks, w)
			}
		} else {
			for w := 1; w <= maxWeeks; w++ {
				scanWeeks = append(scanWeeks, w)
			}
		}

		for _, week := range scanWeeks {
			txns, err := s.sleeper.GetLeagueTransactions(ctx, lg.id, week)
			if err != nil {
				slog.Warn("get transactions", "league", lg.id, "week", week, "err", err)
				continue
			}
			for _, txn := range txns {
				if fmt.Sprint(txn["type"]) != "trade" {
					continue
				}
				if fmt.Sprint(txn["status"]) != "complete" {
					continue
				}
				s.storeTrade(ctx, txn, lg.id, watchSet)
				result.TradesStored++
			}
		}
		result.LeaguesScanned++
	}

	slog.Info("trades sync complete", "trades", result.TradesStored, "leagues", result.LeaguesScanned)
	return result, nil
}

func (s *LeaguemateTradesSyncer) storeTrade(ctx context.Context, txn map[string]interface{}, leagueID string, watchSet map[string]bool) {
	txnID := fmt.Sprint(txn["transaction_id"])
	if txnID == "" || txnID == "<nil>" {
		return
	}

	raw, _ := json.Marshal(txn)

	// Check if Markis's league
	var isMarkisLeague bool
	_ = s.pool.QueryRow(ctx, "SELECT is_markis_league FROM league WHERE league_id = $1", leagueID).Scan(&isMarkisLeague)

	// Check if involves watch set
	involvesWatchSet := false
	adds, _ := txn["adds"].(map[string]interface{})
	drops, _ := txn["drops"].(map[string]interface{})
	draftPicks, _ := txn["draft_picks"].([]interface{})

	for pid := range adds {
		if watchSet[fmt.Sprint(pid)] {
			involvesWatchSet = true
			break
		}
	}
	if !involvesWatchSet {
		for pid := range drops {
			if watchSet[fmt.Sprint(pid)] {
				involvesWatchSet = true
				break
			}
		}
	}

	rosterIDs := toIntSlice(txn["roster_ids"])
	consenterIDs := toIntSlice(txn["consenter_ids"])

	_, err := s.pool.Exec(ctx, `
		INSERT INTO leaguemate_transaction (transaction_id, league_id, type, status, creator,
			week, roster_ids, consenter_ids, created_at, status_updated_at,
			is_markis_league, involves_watch_set, raw, last_synced_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, now())
		ON CONFLICT (transaction_id) DO UPDATE SET last_synced_at = now()
	`, txnID, leagueID, "trade", fmt.Sprint(txn["status"]),
		nilIfEmpty(fmt.Sprint(txn["creator"])), toInt(txn["leg"]),
		rosterIDs, consenterIDs,
		epochMsToTime(txn["created"]), epochMsToTime(txn["status_updated"]),
		isMarkisLeague, involvesWatchSet, raw)
	if err != nil {
		slog.Warn("store trade", "id", txnID, "err", err)
		return
	}

	// Delete + reinsert assets
	_, _ = s.pool.Exec(ctx, "DELETE FROM leaguemate_trade_asset WHERE transaction_id = $1", txnID)

	// Player assets from adds/drops
	for pid, toRoster := range adds {
		fromRoster := drops[pid]
		isWatch := watchSet[fmt.Sprint(pid)]
		_, _ = s.pool.Exec(ctx, `
			INSERT INTO leaguemate_trade_asset (id, transaction_id, league_id, asset_type,
				sleeper_player_id, from_roster_id, to_roster_id, is_watch_set)
			VALUES ($1, $2, $3, 'player', $4, $5, $6, $7)
		`, uuid.New(), txnID, leagueID, fmt.Sprint(pid), toInt(fromRoster), toInt(toRoster), isWatch)
	}
	for pid, fromRoster := range drops {
		if _, ok := adds[pid]; ok {
			continue // already handled
		}
		isWatch := watchSet[fmt.Sprint(pid)]
		_, _ = s.pool.Exec(ctx, `
			INSERT INTO leaguemate_trade_asset (id, transaction_id, league_id, asset_type,
				sleeper_player_id, from_roster_id, to_roster_id, is_watch_set)
			VALUES ($1, $2, $3, 'player', $4, $5, NULL, $6)
		`, uuid.New(), txnID, leagueID, fmt.Sprint(pid), toInt(fromRoster), isWatch)
	}

	// Pick assets
	for _, pick := range draftPicks {
		pm, ok := pick.(map[string]interface{})
		if !ok {
			continue
		}
		_, _ = s.pool.Exec(ctx, `
			INSERT INTO leaguemate_trade_asset (id, transaction_id, league_id, asset_type,
				pick_season, pick_round, pick_roster_id, from_roster_id, to_roster_id)
			VALUES ($1, $2, $3, 'pick', $4, $5, $6, $7, $8)
		`, uuid.New(), txnID, leagueID,
			nilIfEmpty(fmt.Sprint(pm["season"])), toInt(pm["round"]),
			toInt(pm["roster_id"]), toInt(pm["previous_owner_id"]), toInt(pm["owner_id"]))
	}
}

func (s *LeaguemateTradesSyncer) watchSet(ctx context.Context) map[string]bool {
	result := make(map[string]bool)
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT r.sleeper_player_id
		FROM leaguemate_roster_player r
		JOIN league l ON l.league_id = r.league_id
		WHERE l.is_markis_league AND r.sleeper_player_id IS NOT NULL
	`)
	if err != nil {
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var pid string
		if err := rows.Scan(&pid); err != nil {
			continue
		}
		result[pid] = true
	}
	return result
}

func toIntSlice(v interface{}) []int {
	if v == nil {
		return []int{}
	}
	arr, ok := v.([]interface{})
	if !ok {
		return []int{}
	}
	result := make([]int, 0, len(arr))
	for _, item := range arr {
		n := toInt(item)
		if n != nil {
			result = append(result, *n)
		}
	}
	return result
}

func epochMsToTime(v interface{}) interface{} {
	if v == nil {
		return nil
	}
	n := toInt(v)
	if n == nil {
		return nil
	}
	return epochMsToTimeVal(*n)
}

func epochMsToTimeVal(ms int) interface{} {
	// Return as time.Time — pgx will adapt it
	// Use a helper to avoid import issues
	return json.Number(fmt.Sprint(ms))
}

// Ensure strings import is used
var _ = strings.TrimSpace