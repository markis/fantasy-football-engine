package query

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// --- Leaguemate intelligence tools (ported from the Python Sleeper MCP) ---

// LeaguemateOverlap shows which leaguemates own a player across all their leagues.
func (s *Service) LeaguemateOverlap(ctx context.Context, playerID string, includeMarkis bool) (map[string]interface{}, error) {
	player := map[string]interface{}{"player_id": playerID, "name": nil, "position": nil, "team": nil}
	var name, pos, team *string
	err := s.pool.QueryRow(ctx,
		"SELECT full_name, position, team FROM player WHERE sleeper_player_id = $1", playerID,
	).Scan(&name, &pos, &team)
	if err == nil {
		player["name"] = ptrStr(name)
		player["position"] = ptrStr(pos)
		player["team"] = ptrStr(team)
	}

	var markisOwns bool
	_ = s.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM leaguemate_roster_player r
		JOIN league_manager m ON m.league_id=r.league_id AND m.roster_id=r.roster_id
		WHERE r.sleeper_player_id=$1 AND m.is_markis)
	`, playerID).Scan(&markisOwns)

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
		return nil, err
	}
	defer rows.Close()

	var owners []map[string]interface{}
	for rows.Next() {
		var userID string
		var uname, dname *string
		var inMy bool
		var inMyCount, inOtherCount, total int
		var leagues *string
		if err := rows.Scan(&userID, &uname, &dname, &inMy, &inMyCount, &inOtherCount, &total, &leagues); err != nil {
			continue
		}
		owners = append(owners, map[string]interface{}{
			"user_id":                userID,
			"username":               ptrStr(uname),
			"display_name":           ptrStr(dname),
			"in_my_leagues":          inMy,
			"in_my_leagues_count":    inMyCount,
			"in_other_leagues_count": inOtherCount,
			"total_leagues":          total,
			"leagues":                ptrStr(leagues),
		})
	}
	if owners == nil {
		owners = []map[string]interface{}{}
	}
	return map[string]interface{}{
		"player":      player,
		"markis_owns": markisOwns,
		"owners":      owners,
	}, nil
}

// ManagerProfile builds a cross-league dossier for a leaguemate.
func (s *Service) ManagerProfile(ctx context.Context, username string) (map[string]interface{}, error) {
	handle := strings.TrimSpace(username)
	if handle == "" {
		return nil, fmt.Errorf("username is required")
	}

	var uid string
	var uname, dname *string
	err := s.pool.QueryRow(ctx,
		"SELECT user_id, username, display_name FROM sleeper_user WHERE username = $1 OR display_name = $1 LIMIT 1", handle,
	).Scan(&uid, &uname, &dname)
	if err != nil || uid == "" {
		_ = s.pool.QueryRow(ctx,
			"SELECT user_id, username, display_name FROM sleeper_user WHERE username ILIKE $1 OR display_name ILIKE $1 LIMIT 1", handle,
		).Scan(&uid, &uname, &dname)
	}
	if uid == "" {
		return nil, fmt.Errorf("no leaguemate found matching '%s'", handle)
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
		return nil, err
	}
	defer rows.Close()
	var leagues []map[string]interface{}
	for rows.Next() {
		var lName, status, teamName *string
		var leagueID string
		var isMarkis, hasSF, isBB *bool
		var wins, losses, ties, rosterID *int
		if err := rows.Scan(&lName, &leagueID, &isMarkis, &status, &hasSF, &isBB, &teamName, &wins, &losses, &ties, &rosterID); err != nil {
			continue
		}
		leagues = append(leagues, map[string]interface{}{
			"name":             ptrStr(lName),
			"league_id":        leagueID,
			"is_markis_league": boolPtrVal(isMarkis),
			"status":           ptrStr(status),
			"has_superflex":    boolPtrVal(hasSF),
			"is_best_ball":     boolPtrVal(isBB),
			"team_name":        ptrStr(teamName),
			"wins":             intPtrVal(wins),
			"losses":           intPtrVal(losses),
			"ties":             intPtrVal(ties),
			"roster_id":        intPtrVal(rosterID),
		})
	}

	var lgCount, totalWins, totalLosses, totalTies, withStandings *int
	var avgWinPct *float64
	_ = s.pool.QueryRow(ctx, `
		SELECT count(*), sum(wins), sum(losses), sum(ties),
		  count(*) FILTER (WHERE wins IS NOT NULL),
		  avg(wins*1.0/nullif(wins+losses+ties,0))
		FROM league_manager WHERE user_id=$1 AND NOT co_owner
	`, uid).Scan(&lgCount, &totalWins, &totalLosses, &totalTies, &withStandings, &avgWinPct)
	standings := map[string]interface{}{
		"leagues":                intPtrVal(lgCount),
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
		return nil, err
	}
	defer mRows.Close()
	var mostHeld []map[string]interface{}
	for mRows.Next() {
		var pid, fName, pos, team, inj *string
		var leaguesOwned, inMy int
		var starting bool
		if err := mRows.Scan(&pid, &fName, &pos, &team, &inj, &leaguesOwned, &inMy, &starting); err != nil {
			continue
		}
		mostHeld = append(mostHeld, map[string]interface{}{
			"player_id":          ptrStr(pid),
			"name":               ptrStr(fName),
			"position":           ptrStr(pos),
			"team":               ptrStr(team),
			"injury_status":      ptrStr(inj),
			"leagues_owned":      leaguesOwned,
			"in_my_leagues":      inMy,
			"starting_somewhere": starting,
		})
	}
	if mostHeld == nil {
		mostHeld = []map[string]interface{}{}
	}
	if leagues == nil {
		leagues = []map[string]interface{}{}
	}

	return map[string]interface{}{
		"username":     ptrStr(uname),
		"display_name": ptrStr(dname),
		"user_id":      uid,
		"leagues":      leagues,
		"standings":    standings,
		"most_held":    mostHeld,
	}, nil
}

// PlayerTradeValue shows what a player has actually been traded for across tracked leagues.
func (s *Service) PlayerTradeValue(ctx context.Context, playerID string, limit int) ([]map[string]interface{}, error) {
	if limit <= 0 {
		limit = 12
	}
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
		return nil, err
	}
	defer rows.Close()

	type tradeRow struct {
		txnID, lName, leagueID string
		isMarkis               bool
		createdAt              *time.Time
		fromR, toR             *int
	}
	var trades []tradeRow
	var txnIDs []string
	leagueIDs := make(map[string]bool)
	for rows.Next() {
		var t tradeRow
		if err := rows.Scan(&t.txnID, &t.lName, &t.leagueID, &t.isMarkis, &t.createdAt, &t.fromR, &t.toR); err != nil {
			continue
		}
		trades = append(trades, t)
		txnIDs = append(txnIDs, t.txnID)
		leagueIDs[t.leagueID] = true
	}
	if len(trades) == 0 {
		return []map[string]interface{}{}, nil
	}

	aRows, err := s.pool.Query(ctx, `
		SELECT a.transaction_id, a.asset_type, a.sleeper_player_id,
		  p.full_name, p.position, p.team, a.pick_season, a.pick_round,
		  a.from_roster_id, a.to_roster_id
		FROM leaguemate_trade_asset a
		LEFT JOIN player p ON p.sleeper_player_id=a.sleeper_player_id
		WHERE a.transaction_id = ANY($1)
	`, txnIDs)
	if err != nil {
		return nil, err
	}
	defer aRows.Close()
	byTxn := make(map[string][]map[string]interface{})
	for aRows.Next() {
		var txnID, assetType string
		var pid, fName, pos, team, pickSeason *string
		var pickRound, fromR, toR *int
		if err := aRows.Scan(&txnID, &assetType, &pid, &fName, &pos, &team, &pickSeason, &pickRound, &fromR, &toR); err != nil {
			continue
		}
		var asset map[string]interface{}
		if assetType == "player" {
			asset = map[string]interface{}{
				"type": "player", "player_id": ptrStr(pid), "name": ptrStr(fName),
				"position": ptrStr(pos), "team": ptrStr(team),
			}
		} else {
			asset = map[string]interface{}{
				"type": "pick", "desc": fmt.Sprintf("%s R%d", ptrStr(pickSeason), intPtrOr(pickRound, 0)),
				"season": ptrStr(pickSeason), "round": intPtrOr(pickRound, 0),
			}
		}
		asset["raw_pid"] = ptrStr(pid)
		byTxn[txnID] = append(byTxn[txnID], asset)
	}

	var lgList []string
	for l := range leagueIDs {
		lgList = append(lgList, l)
	}
	mRows, err := s.pool.Query(ctx, `
		SELECT lm.league_id, lm.roster_id, su.username, su.display_name
		FROM league_manager lm JOIN sleeper_user su ON su.user_id=lm.user_id
		WHERE NOT lm.co_owner AND lm.league_id = ANY($1)
	`, lgList)
	if err != nil {
		return nil, err
	}
	defer mRows.Close()
	mgr := make(map[string]string)
	for mRows.Next() {
		var lgID string
		var rosterID int
		var uname, dname *string
		if err := mRows.Scan(&lgID, &rosterID, &uname, &dname); err != nil {
			continue
		}
		name := ptrStr(dname)
		if name == "" {
			name = ptrStr(uname)
		}
		mgr[lgID+"|"+fmt.Sprint(rosterID)] = name
	}

	out := make([]map[string]interface{}, 0, len(trades))
	for _, t := range trades {
		var pkg []map[string]interface{}
		for _, a := range byTxn[t.txnID] {
			if a["type"] == "player" && a["raw_pid"] == playerID {
				continue
			}
			clean := map[string]interface{}{}
			for k, v := range a {
				if k == "raw_pid" {
					continue
				}
				clean[k] = v
			}
			pkg = append(pkg, clean)
		}
		if pkg == nil {
			pkg = []map[string]interface{}{}
		}
		var fromMgr, toMgr string
		if t.fromR != nil {
			fromMgr = mgr[t.leagueID+"|"+fmt.Sprint(*t.fromR)]
		}
		if t.toR != nil {
			toMgr = mgr[t.leagueID+"|"+fmt.Sprint(*t.toR)]
		}
		tradedAt := ""
		if t.createdAt != nil {
			tradedAt = t.createdAt.UTC().Format(time.RFC3339)
		}
		out = append(out, map[string]interface{}{
			"league":           t.lName,
			"is_markis_league": t.isMarkis,
			"traded_at":        tradedAt,
			"from_manager":     fromMgr,
			"to_manager":      toMgr,
			"traded_for":       pkg,
		})
	}
	return out, nil
}

// --- Sleeper API passthrough tools (league/user/draft endpoints) ---

func (s *Service) GetUserInfo(ctx context.Context, usernameOrID string) (map[string]interface{}, error) {
	return s.sleeper.GetUserInfo(ctx, usernameOrID)
}
func (s *Service) GetUserLeagues(ctx context.Context, userID, season string) ([]map[string]interface{}, error) {
	return s.sleeper.GetUserLeagues(ctx, userID, season)
}
func (s *Service) GetLeagueInfo(ctx context.Context, leagueID string) (map[string]interface{}, error) {
	return s.sleeper.GetLeagueInfo(ctx, leagueID)
}
func (s *Service) GetLeagueRosters(ctx context.Context, leagueID string) ([]map[string]interface{}, error) {
	return s.sleeper.GetLeagueRosters(ctx, leagueID)
}
func (s *Service) GetLeagueUsers(ctx context.Context, leagueID string) ([]map[string]interface{}, error) {
	return s.sleeper.GetLeagueUsers(ctx, leagueID)
}
func (s *Service) GetLeagueMatchups(ctx context.Context, leagueID string, week int) ([]map[string]interface{}, error) {
	return s.sleeper.GetLeagueMatchups(ctx, leagueID, week)
}
func (s *Service) GetLeagueTransactions(ctx context.Context, leagueID string, week int) ([]map[string]interface{}, error) {
	return s.sleeper.GetLeagueTransactions(ctx, leagueID, week)
}
func (s *Service) GetLeagueDrafts(ctx context.Context, leagueID string) ([]map[string]interface{}, error) {
	return s.sleeper.GetLeagueDrafts(ctx, leagueID)
}
func (s *Service) GetLeagueTradedPicks(ctx context.Context, leagueID string) ([]map[string]interface{}, error) {
	return s.sleeper.GetLeagueTradedPicks(ctx, leagueID)
}

// --- helpers ---

func boolPtrVal(b *bool) interface{} {
	if b == nil {
		return nil
	}
	return *b
}
func intPtrVal(i *int) interface{} {
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
func floatPtrVal(f *float64) interface{} {
	if f == nil {
		return nil
	}
	return *f
}