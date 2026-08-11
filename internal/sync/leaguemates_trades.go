package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"ff-engine/internal/db"
	"ff-engine/internal/sleeper"
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
	TradesStored   int    `json:"tradesStored"`
	LeaguesScanned int    `json:"leaguesScanned"`
	Status         string `json:"status"`
}

// leagueInfo is a mapped league fetched for the trades sync.
type leagueInfo struct {
	id             string
	status         string
	isMarkisLeague bool
}

// Sync stores completed trades from mapped leagues.
func (s *LeaguemateTradesSyncer) Sync(ctx context.Context, maxWeeks int) (*TradesSyncResult, error) {
	if maxWeeks == 0 {
		maxWeeks = 3
	}
	result := &TradesSyncResult{Status: "ok"}

	// Get watch set (players on Markis's rosters)
	watchSet := s.watchSet(ctx)

	leagues, err := s.fetchLeagues(ctx)
	if err != nil {
		return nil, err
	}

	currentWeek := s.sleeper.CurrentWeek(ctx)

	for _, lg := range leagues {
		// Skip drafting/pre_draft leagues
		if lg.status == "drafting" || lg.status == "pre_draft" {
			continue
		}

		scanWeeks := scanWeeksFor(currentWeek, maxWeeks)
		result.TradesStored += s.processLeagueTrades(ctx, lg, scanWeeks, watchSet)
		result.LeaguesScanned++
	}

	slog.Info("trades sync complete", "trades", result.TradesStored, "leagues", result.LeaguesScanned)
	return result, nil
}

// fetchLeagues loads all mapped leagues used as candidates for trade scanning.
func (s *LeaguemateTradesSyncer) fetchLeagues(ctx context.Context) ([]leagueInfo, error) {
	rows, err := s.pool.Query(ctx, "SELECT league_id, status, is_markis_league FROM league")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var leagues []leagueInfo
	for rows.Next() {
		var li leagueInfo
		if err := rows.Scan(&li.id, &li.status, &li.isMarkisLeague); err != nil {
			continue
		}
		leagues = append(leagues, li)
	}
	return leagues, nil
}

// scanWeeksFor computes which weeks to scan for transactions, preferring the
// most recent maxWeeks weeks up to the current week, or weeks 1..maxWeeks
// when the current week is unknown.
func scanWeeksFor(currentWeek, maxWeeks int) []int {
	var weeks []int
	if currentWeek > 0 {
		lo := max(currentWeek-maxWeeks+1, 1)
		for w := currentWeek; w >= lo; w-- {
			weeks = append(weeks, w)
		}
	} else {
		for w := 1; w <= maxWeeks; w++ {
			weeks = append(weeks, w)
		}
	}
	return weeks
}

// processLeagueTrades scans the given weeks for one league's completed trades
// and stores them, returning the number of trades stored.
func (s *LeaguemateTradesSyncer) processLeagueTrades(ctx context.Context, lg leagueInfo, weeks []int, watchSet map[string]bool) int {
	stored := 0
	for _, week := range weeks {
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
			if s.storeTrade(ctx, txn, lg.id, lg.isMarkisLeague, watchSet) {
				stored++
			}
		}
	}
	return stored
}

func (s *LeaguemateTradesSyncer) storeTrade(
	ctx context.Context,
	txn map[string]any,
	leagueID string,
	isMarkisLeague bool,
	watchSet map[string]bool,
) bool {
	txnID := fmt.Sprint(txn["transaction_id"])
	if txnID == "" || txnID == nilStr {
		return false
	}

	raw, err := json.Marshal(txn)
	if err != nil {
		slog.Warn("failed to marshal transaction", "txn_id", txnID, "err", err)
		raw = []byte("{}")
	}

	adds, _ := txn["adds"].(map[string]any)     //nolint:errcheck // Type assertion returns empty map if fails
	drops, _ := txn["drops"].(map[string]any)   //nolint:errcheck // Type assertion returns empty map if fails
	draftPicks, _ := txn["draft_picks"].([]any) //nolint:errcheck // Type assertion returns empty slice if fails

	involvesWatchSet := tradeInvolvesWatchSet(adds, drops, watchSet)

	if err := s.insertTradeTransaction(ctx, txnID, leagueID, txn, isMarkisLeague, involvesWatchSet, raw); err != nil {
		slog.Warn("store trade", "id", txnID, "err", err)
		return false
	}

	// Delete + reinsert assets. There's no unique constraint on
	// leaguemate_trade_asset (dedup relies on this delete always running
	// before the inserts below), so a failed delete must abort rather than
	// fall through into inserting a second, duplicate set of rows.
	if err := s.deleteTradeAssets(ctx, txnID); err != nil {
		slog.Warn("delete trade assets", "id", txnID, "err", err)
		return false
	}

	s.insertPlayerTradeAssets(ctx, txnID, leagueID, adds, drops, watchSet)
	s.insertPickTradeAssets(ctx, txnID, leagueID, draftPicks)

	return true
}

// tradeInvolvesWatchSet reports whether any added or dropped player is in
// the watch set (players on Markis's rosters).
func tradeInvolvesWatchSet(adds, drops map[string]any, watchSet map[string]bool) bool {
	for pid := range adds {
		if watchSet[pid] {
			return true
		}
	}
	for pid := range drops {
		if watchSet[pid] {
			return true
		}
	}
	return false
}

// insertTradeTransaction upserts the leaguemate_transaction row for a trade.
func (s *LeaguemateTradesSyncer) insertTradeTransaction(
	ctx context.Context,
	txnID, leagueID string,
	txn map[string]any,
	isMarkisLeague, involvesWatchSet bool,
	raw []byte,
) error {
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
	return err
}

// deleteTradeAssets removes any existing trade assets for a transaction so
// they can be reinserted fresh.
func (s *LeaguemateTradesSyncer) deleteTradeAssets(ctx context.Context, txnID string) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM leaguemate_trade_asset WHERE transaction_id = $1", txnID)
	return err
}

// insertPlayerTradeAssets stores the player assets moved in a trade, derived
// from the transaction's adds/drops maps.
func (s *LeaguemateTradesSyncer) insertPlayerTradeAssets(
	ctx context.Context,
	txnID, leagueID string,
	adds, drops map[string]any,
	watchSet map[string]bool,
) {
	for pid, toRoster := range adds {
		fromRoster := drops[pid]
		isWatch := watchSet[pid]
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO leaguemate_trade_asset (id, transaction_id, league_id, asset_type,
				sleeper_player_id, from_roster_id, to_roster_id, is_watch_set)
			VALUES ($1, $2, $3, 'player', $4, $5, $6, $7)
		`, uuid.New(), txnID, leagueID, pid, toInt(fromRoster), toInt(toRoster), isWatch); err != nil {
			slog.Warn("failed to insert trade asset (add)", "pid", pid, "err", err)
		}
	}
	for pid, fromRoster := range drops {
		if _, ok := adds[pid]; ok {
			continue // already handled
		}
		isWatch := watchSet[pid]
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO leaguemate_trade_asset (id, transaction_id, league_id, asset_type,
				sleeper_player_id, from_roster_id, to_roster_id, is_watch_set)
			VALUES ($1, $2, $3, 'player', $4, $5, NULL, $6)
		`, uuid.New(), txnID, leagueID, pid, toInt(fromRoster), isWatch); err != nil {
			slog.Warn("failed to insert trade asset (drop)", "pid", pid, "err", err)
		}
	}
}

// insertPickTradeAssets stores the draft pick assets moved in a trade.
func (s *LeaguemateTradesSyncer) insertPickTradeAssets(ctx context.Context, txnID, leagueID string, draftPicks []any) {
	for _, pick := range draftPicks {
		pm, ok := pick.(map[string]any)
		if !ok {
			continue
		}
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO leaguemate_trade_asset (id, transaction_id, league_id, asset_type,
				pick_season, pick_round, pick_roster_id, from_roster_id, to_roster_id)
			VALUES ($1, $2, $3, 'pick', $4, $5, $6, $7, $8)
		`, uuid.New(), txnID, leagueID,
			nilIfEmpty(fmt.Sprint(pm["season"])), toInt(pm["round"]),
			toInt(pm["roster_id"]), toInt(pm["previous_owner_id"]), toInt(pm["owner_id"])); err != nil {
			slog.Warn("failed to insert trade asset (pick)", "err", err)
		}
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

func toIntSlice(v any) []int {
	if v == nil {
		return []int{}
	}
	arr, ok := v.([]any)
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

func epochMsToTime(v any) any {
	if v == nil {
		return nil
	}
	n := toInt(v)
	if n == nil {
		return nil
	}
	return epochMsToTimeVal(*n)
}

func epochMsToTimeVal(ms int) any {
	return time.UnixMilli(int64(ms)).UTC()
}
