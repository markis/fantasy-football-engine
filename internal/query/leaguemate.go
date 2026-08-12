package query

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"ff-engine/internal/util"
)

// --- Leaguemate intelligence tools (ported from the Python Sleeper MCP) ---

var (
	errUsernameRequired   = errors.New("username is required")
	errLeaguemateNotFound = errors.New("no leaguemate found")
)

// LeaguemateOverlap shows which leaguemates own a player across all their leagues.
func (s *Service) LeaguemateOverlap(ctx context.Context, playerID string, includeMarkis bool) (map[string]any, error) {
	player := map[string]any{colPlayerID: playerID, colName: nil, colPosition: nil, colTeam: nil}
	var name, pos, team *string
	err := s.pool.QueryRow(ctx,
		"SELECT full_name, position, team FROM player WHERE sleeper_player_id = $1", playerID,
	).Scan(&name, &pos, &team)
	if err == nil {
		player[colName] = util.StrOrEmpty(name)
		player[colPosition] = util.StrOrEmpty(pos)
		player[colTeam] = util.StrOrEmpty(team)
	}

	var markisOwns bool
	if mErr := s.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM leaguemate_roster_player r
		JOIN league_manager m ON m.league_id=r.league_id AND m.roster_id=r.roster_id
		WHERE r.sleeper_player_id=$1 AND m.is_markis)
	`, playerID).Scan(&markisOwns); mErr != nil {
		markisOwns = false // best-effort lookup; miss leaves false
	}

	excludeMarkis := ""
	if !includeMarkis {
		excludeMarkis = "AND NOT m.is_markis "
	}
	rows, err := s.pool.Query(ctx, `
		SELECT m.user_id, su.username, su.display_name,
		  bool_or(l.is_markis_league) AS in_my_leagues,
		  count(*) FILTER (WHERE l.is_markis_league) AS in_my_leagues_count,
		  count(*) FILTER (WHERE NOT l.is_markis_league) AS in_other_leagues_count,
		  count(*) AS total_leagues,
		  string_agg(DISTINCT l.name, ', ' ORDER BY l.name) AS leagues
		FROM leaguemate_roster_player r
		JOIN league_manager m ON m.league_id=r.league_id AND m.roster_id=r.roster_id
		JOIN league l ON l.league_id=r.league_id
		JOIN sleeper_user su ON su.user_id=m.user_id
		WHERE r.sleeper_player_id = $1 `+excludeMarkis+`
		GROUP BY m.user_id, su.username, su.display_name
		ORDER BY total_leagues DESC, in_my_leagues_count DESC
	`, playerID)
	if err != nil {
		return nil, fmt.Errorf("query leaguemate overlap owners: %w", err)
	}
	defer rows.Close()

	var owners []map[string]any
	for rows.Next() {
		var userID string
		var uname, dname *string
		var inMy bool
		var inMyCount, inOtherCount, total int
		var leagues *string
		if scanErr := rows.Scan(&userID, &uname, &dname, &inMy, &inMyCount, &inOtherCount, &total, &leagues); scanErr != nil {
			continue
		}
		owners = append(owners, map[string]any{
			colUserID:                userID,
			"username":               util.StrOrEmpty(uname),
			"display_name":           util.StrOrEmpty(dname),
			"in_my_leagues":          inMy,
			"in_my_leagues_count":    inMyCount,
			"in_other_leagues_count": inOtherCount,
			"total_leagues":          total,
			colLeagues:               util.StrOrEmpty(leagues),
		})
	}
	if owners == nil {
		owners = []map[string]any{}
	}
	return map[string]any{
		colPlayer:     player,
		"markis_owns": markisOwns,
		"owners":      owners,
	}, nil
}

// ManagerProfile builds a cross-league dossier for a leaguemate.
func (s *Service) ManagerProfile(ctx context.Context, username string) (map[string]any, error) {
	handle := strings.TrimSpace(username)
	if handle == "" {
		return nil, errUsernameRequired
	}

	var uid string
	var uname, dname *string
	err := s.pool.QueryRow(ctx,
		"SELECT user_id, username, display_name FROM sleeper_user WHERE username = $1 OR display_name = $1 LIMIT 1", handle,
	).Scan(&uid, &uname, &dname)
	if err != nil || uid == "" {
		if fErr := s.pool.QueryRow(ctx,
			"SELECT user_id, username, display_name FROM sleeper_user WHERE username ILIKE $1 OR display_name ILIKE $1 LIMIT 1", handle,
		).Scan(&uid, &uname, &dname); fErr != nil {
			uid = "" // fallback miss; triggers not-found error below
		}
	}
	if uid == "" {
		return nil, fmt.Errorf("%w: %q", errLeaguemateNotFound, handle)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT l.name, l.league_id, l.is_markis_league, l.status,
		  l.has_superflex, l.is_best_ball, lm.team_name,
		  lm.wins, lm.losses, lm.ties, lm.roster_id
		FROM league_manager lm JOIN league l ON l.league_id=lm.league_id
		WHERE lm.user_id = $1 AND NOT lm.co_owner
		ORDER BY l.is_markis_league DESC, l.name
	`, uid)
	if err != nil {
		return nil, fmt.Errorf("query manager leagues: %w", err)
	}
	defer rows.Close()
	var leagues []map[string]any
	for rows.Next() {
		var lName, status, teamName *string
		var leagueID string
		var isMarkis, hasSF, isBB *bool
		var wins, losses, ties, rosterID *int
		if scanErr := rows.Scan(&lName, &leagueID, &isMarkis, &status, &hasSF, &isBB,
			&teamName, &wins, &losses, &ties, &rosterID); scanErr != nil {
			continue
		}
		leagues = append(leagues, map[string]any{
			colName:            util.StrOrEmpty(lName),
			colLeagueID:        leagueID,
			"is_markis_league": boolPtrVal(isMarkis),
			"status":           util.StrOrEmpty(status),
			"has_superflex":    boolPtrVal(hasSF),
			"is_best_ball":     boolPtrVal(isBB),
			"team_name":        util.StrOrEmpty(teamName),
			"wins":             intPtrVal(wins),
			"losses":           intPtrVal(losses),
			"ties":             intPtrVal(ties),
			"roster_id":        intPtrVal(rosterID),
		})
	}

	var lgCount, totalWins, totalLosses, totalTies, withStandings *int
	var avgWinPct *float64
	if aggErr := s.pool.QueryRow(ctx, `
		SELECT count(*), sum(wins), sum(losses), sum(ties),
		  count(*) FILTER (WHERE wins IS NOT NULL),
		  avg(wins*1.0/nullif(wins+losses+ties,0))
		FROM league_manager WHERE user_id=$1 AND NOT co_owner
	`, uid).Scan(&lgCount, &totalWins, &totalLosses, &totalTies, &withStandings, &avgWinPct); aggErr != nil {
		// best-effort aggregate; nil pointers yield null in response
		lgCount, totalWins, totalLosses, totalTies, withStandings = nil, nil, nil, nil, nil
		avgWinPct = nil
	}
	standings := map[string]any{
		colLeagues:               intPtrVal(lgCount),
		"wins":                   intPtrVal(totalWins),
		"losses":                 intPtrVal(totalLosses),
		"ties":                   intPtrVal(totalTies),
		"leagues_with_standings": intPtrVal(withStandings),
		"avg_win_pct":            floatPtrVal(avgWinPct),
	}

	mRows, err := s.pool.Query(ctx, `
		SELECT r.sleeper_player_id, p.full_name, p.position, p.team, p.injury_status,
		  count(*) AS leagues_owned,
		  count(*) FILTER (WHERE l.is_markis_league) AS in_my_leagues,
		  bool_or(r.slot='starter') AS starting_somewhere
		FROM leaguemate_roster_player r
		JOIN league_manager lm ON lm.league_id=r.league_id AND lm.roster_id=r.roster_id
		JOIN league l ON l.league_id=lm.league_id
		LEFT JOIN player p ON p.sleeper_player_id=r.sleeper_player_id
		WHERE lm.user_id=$1 AND NOT lm.co_owner
		GROUP BY r.sleeper_player_id, p.full_name, p.position, p.team, p.injury_status
		HAVING count(*) >= 2
		ORDER BY leagues_owned DESC, in_my_leagues DESC, p.full_name
		LIMIT 25
	`, uid)
	if err != nil {
		return nil, fmt.Errorf("query manager most-held players: %w", err)
	}
	defer mRows.Close()
	var mostHeld []map[string]any
	for mRows.Next() {
		var pid, fName, pos, team, inj *string
		var leaguesOwned, inMy int
		var starting bool
		if scanErr := mRows.Scan(&pid, &fName, &pos, &team, &inj, &leaguesOwned, &inMy, &starting); scanErr != nil {
			continue
		}
		mostHeld = append(mostHeld, map[string]any{
			colPlayerID:          util.StrOrEmpty(pid),
			colName:              util.StrOrEmpty(fName),
			colPosition:          util.StrOrEmpty(pos),
			colTeam:              util.StrOrEmpty(team),
			"injury_status":      util.StrOrEmpty(inj),
			"leagues_owned":      leaguesOwned,
			"in_my_leagues":      inMy,
			"starting_somewhere": starting,
		})
	}
	if mostHeld == nil {
		mostHeld = []map[string]any{}
	}
	if leagues == nil {
		leagues = []map[string]any{}
	}

	return map[string]any{
		"username":     util.StrOrEmpty(uname),
		"display_name": util.StrOrEmpty(dname),
		colUserID:      uid,
		colLeagues:     leagues,
		"standings":    standings,
		"most_held":    mostHeld,
	}, nil
}

// tradeRow holds one completed trade involving the target player.
type tradeRow struct {
	txnID, lName, leagueID string
	isMarkis               bool
	createdAt              *time.Time
	fromR, toR             *int
}

// PlayerTradeValue shows what a player has actually been traded for across tracked leagues.
func (s *Service) PlayerTradeValue(ctx context.Context, playerID string, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 12
	}
	trades, err := s.fetchPlayerTrades(ctx, playerID, limit)
	if err != nil {
		return nil, err
	}
	if len(trades) == 0 {
		return []map[string]any{}, nil
	}

	txnIDs := make([]string, len(trades))
	leagueSet := make(map[string]bool, len(trades))
	for i, t := range trades {
		txnIDs[i] = t.txnID
		leagueSet[t.leagueID] = true
	}
	byTxn, err := s.fetchTradeAssets(ctx, txnIDs)
	if err != nil {
		return nil, err
	}

	leagueIDs := make([]string, 0, len(leagueSet))
	for l := range leagueSet {
		leagueIDs = append(leagueIDs, l)
	}
	mgr, err := s.fetchLeagueManagers(ctx, leagueIDs)
	if err != nil {
		return nil, err
	}
	return buildTradeValueOut(trades, byTxn, mgr, playerID), nil
}

func (s *Service) fetchPlayerTrades(ctx context.Context, playerID string, limit int) ([]tradeRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT tx.transaction_id, l.name, l.league_id, l.is_markis_league,
		  tx.created_at, a.from_roster_id, a.to_roster_id
		FROM leaguemate_trade_asset a
		JOIN leaguemate_transaction tx ON tx.transaction_id=a.transaction_id
		JOIN league l ON l.league_id=tx.league_id
		WHERE a.sleeper_player_id=$1 AND a.asset_type='player' AND tx.status='complete'
		ORDER BY tx.created_at DESC NULLS LAST LIMIT $2
	`, playerID, limit)
	if err != nil {
		return nil, fmt.Errorf("query player trades: %w", err)
	}
	defer rows.Close()
	var trades []tradeRow
	for rows.Next() {
		var t tradeRow
		if scanErr := rows.Scan(&t.txnID, &t.lName, &t.leagueID, &t.isMarkis, &t.createdAt, &t.fromR, &t.toR); scanErr != nil {
			continue
		}
		trades = append(trades, t)
	}
	return trades, nil
}

func (s *Service) fetchTradeAssets(ctx context.Context, txnIDs []string) (map[string][]map[string]any, error) {
	aRows, err := s.pool.Query(ctx, `
		SELECT a.transaction_id, a.asset_type, a.sleeper_player_id,
		  p.full_name, p.position, p.team, a.pick_season, a.pick_round,
		  a.from_roster_id, a.to_roster_id
		FROM leaguemate_trade_asset a
		LEFT JOIN player p ON p.sleeper_player_id=a.sleeper_player_id
		WHERE a.transaction_id = ANY($1)
	`, txnIDs)
	if err != nil {
		return nil, fmt.Errorf("query trade assets: %w", err)
	}
	defer aRows.Close()
	byTxn := make(map[string][]map[string]any)
	for aRows.Next() {
		var txnID, assetType string
		var pid, fName, pos, team, pickSeason *string
		var pickRound, fromR, toR *int
		if scanErr := aRows.Scan(&txnID, &assetType, &pid, &fName, &pos, &team, &pickSeason, &pickRound, &fromR, &toR); scanErr != nil {
			continue
		}
		byTxn[txnID] = append(byTxn[txnID], buildTradeAsset(assetType, pid, fName, pos, team, pickSeason, pickRound))
	}
	return byTxn, nil
}

func buildTradeAsset(assetType string, pid, fName, pos, team, pickSeason *string, pickRound *int) map[string]any {
	var asset map[string]any
	if assetType == colPlayer {
		asset = map[string]any{
			"type":      colPlayer,
			colPlayerID: util.StrOrEmpty(pid),
			colName:     util.StrOrEmpty(fName),
			colPosition: util.StrOrEmpty(pos),
			colTeam:     util.StrOrEmpty(team),
		}
	} else {
		asset = map[string]any{
			"type":   "pick",
			"desc":   fmt.Sprintf("%s R%d", util.StrOrEmpty(pickSeason), intPtrOr(pickRound, 0)),
			"season": util.StrOrEmpty(pickSeason),
			"round":  intPtrOr(pickRound, 0),
		}
	}
	asset["raw_pid"] = util.StrOrEmpty(pid)
	return asset
}

func (s *Service) fetchLeagueManagers(ctx context.Context, leagueIDs []string) (map[string]string, error) {
	mRows, err := s.pool.Query(ctx, `
		SELECT lm.league_id, lm.roster_id, su.username, su.display_name
		FROM league_manager lm JOIN sleeper_user su ON su.user_id=lm.user_id
		WHERE NOT lm.co_owner AND lm.league_id = ANY($1)
	`, leagueIDs)
	if err != nil {
		return nil, fmt.Errorf("query league managers: %w", err)
	}
	defer mRows.Close()
	mgr := make(map[string]string)
	for mRows.Next() {
		var lgID string
		var rosterID int
		var uname, dname *string
		if scanErr := mRows.Scan(&lgID, &rosterID, &uname, &dname); scanErr != nil {
			continue
		}
		name := util.StrOrEmpty(dname)
		if name == "" {
			name = util.StrOrEmpty(uname)
		}
		mgr[lgID+"|"+strconv.Itoa(rosterID)] = name
	}
	return mgr, nil
}

func buildTradeValueOut(trades []tradeRow, byTxn map[string][]map[string]any, mgr map[string]string, playerID string) []map[string]any {
	out := make([]map[string]any, 0, len(trades))
	for _, t := range trades {
		var pkg []map[string]any
		for _, a := range byTxn[t.txnID] {
			if a["type"] == colPlayer && a["raw_pid"] == playerID {
				continue
			}
			clean := map[string]any{}
			for k, v := range a {
				if k == "raw_pid" {
					continue
				}
				clean[k] = v
			}
			pkg = append(pkg, clean)
		}
		if pkg == nil {
			pkg = []map[string]any{}
		}
		var fromMgr, toMgr string
		if t.fromR != nil {
			fromMgr = mgr[t.leagueID+"|"+strconv.Itoa(*t.fromR)]
		}
		if t.toR != nil {
			toMgr = mgr[t.leagueID+"|"+strconv.Itoa(*t.toR)]
		}
		tradedAt := ""
		if t.createdAt != nil {
			tradedAt = t.createdAt.UTC().Format(time.RFC3339)
		}
		out = append(out, map[string]any{
			"league":           t.lName,
			"is_markis_league": t.isMarkis,
			"traded_at":        tradedAt,
			"from_manager":     fromMgr,
			"to_manager":       toMgr,
			"traded_for":       pkg,
		})
	}
	return out
}

// --- Sleeper API passthrough tools (league/user/draft endpoints) ---

func (s *Service) GetUserInfo(ctx context.Context, usernameOrID string) (map[string]any, error) {
	return s.sleeper.GetUserInfo(ctx, usernameOrID)
}

func (s *Service) GetUserLeagues(ctx context.Context, userID, season string) ([]map[string]any, error) {
	return s.sleeper.GetUserLeagues(ctx, userID, season)
}

func (s *Service) GetLeagueInfo(ctx context.Context, leagueID string) (map[string]any, error) {
	return s.sleeper.GetLeagueInfo(ctx, leagueID)
}

func (s *Service) GetLeagueRosters(ctx context.Context, leagueID string) ([]map[string]any, error) {
	return s.sleeper.GetLeagueRosters(ctx, leagueID)
}

func (s *Service) GetLeagueUsers(ctx context.Context, leagueID string) ([]map[string]any, error) {
	return s.sleeper.GetLeagueUsers(ctx, leagueID)
}

func (s *Service) GetLeagueMatchups(ctx context.Context, leagueID string, week int) ([]map[string]any, error) {
	return s.sleeper.GetLeagueMatchups(ctx, leagueID, week)
}

func (s *Service) GetLeagueTransactions(ctx context.Context, leagueID string, week int) ([]map[string]any, error) {
	return s.sleeper.GetLeagueTransactions(ctx, leagueID, week)
}

func (s *Service) GetLeagueDrafts(ctx context.Context, leagueID string) ([]map[string]any, error) {
	return s.sleeper.GetLeagueDrafts(ctx, leagueID)
}

func (s *Service) GetLeagueTradedPicks(ctx context.Context, leagueID string) ([]map[string]any, error) {
	return s.sleeper.GetLeagueTradedPicks(ctx, leagueID)
}

// --- helpers ---

func boolPtrVal(b *bool) any {
	if b == nil {
		return nil
	}
	return *b
}

func intPtrVal(i *int) any {
	if i == nil {
		return nil
	}
	return *i
}

func intPtrOr(i *int, def int) int {
	if i == nil {
		return def
	}
	return *i
}

func floatPtrVal(f *float64) any {
	if f == nil {
		return nil
	}
	return *f
}
