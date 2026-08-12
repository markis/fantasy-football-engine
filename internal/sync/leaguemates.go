package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"ff-engine/internal/db"
	"ff-engine/internal/models"
	"ff-engine/internal/sleeper"
	"ff-engine/internal/util"
)

// LeaguemateSyncer builds the 1-hop manager graph from Sleeper.
type LeaguemateSyncer struct {
	pool    *db.Pool
	sleeper *sleeper.Client
}

// NewLeaguemateSyncer creates a new leaguemate syncer.
func NewLeaguemateSyncer(pool *db.Pool, sleeperClient *sleeper.Client) *LeaguemateSyncer {
	return &LeaguemateSyncer{pool: pool, sleeper: sleeperClient}
}

// LeaguemateSyncResult is the result of a leaguemate sync.
type LeaguemateSyncResult struct {
	ManagersSeen int    `json:"managersSeen"`
	LeaguesSeen  int    `json:"leaguesSeen"`
	RosterRows   int    `json:"rosterRows"`
	Status       string `json:"status"`
}

// Sync builds the 1-hop manager graph.
func (s *LeaguemateSyncer) Sync(ctx context.Context, maxLeagues int, season string) (*LeaguemateSyncResult, error) {
	if season == "" {
		season = s.sleeper.CurrentSeason(ctx)
	}
	if maxLeagues <= 0 {
		maxLeagues = 15
	}
	result := &LeaguemateSyncResult{Status: "ok"}

	// Get Markis's leagues
	markisLeagues, err := s.sleeper.GetUserLeagues(ctx, models.MarkisUserID, season)
	if err != nil {
		return nil, fmt.Errorf("get Markis leagues: %w", err)
	}
	parsedLeagues := sleeper.ParseLeagues(markisLeagues)

	// Track all leagues and managers
	seenLeagues := make(map[string]bool)
	seenManagers := make(map[string]bool)
	var rosterRows int

	// Process each of Markis's leagues
	for i := range parsedLeagues {
		lg := &parsedLeagues[i]
		if result.LeaguesSeen >= maxLeagues {
			break
		}
		leagueID := lg.LeagueID
		if seenLeagues[leagueID] {
			continue
		}
		seenLeagues[leagueID] = true
		result.LeaguesSeen++

		// Upsert league
		s.upsertLeague(ctx, lg, true, "")

		rows, discoveredLeagues := s.processMarkisLeagueRosters(ctx, leagueID, season, seenLeagues, seenManagers)
		rosterRows += rows
		result.LeaguesSeen += discoveredLeagues
	}

	result.ManagersSeen = len(seenManagers)
	result.RosterRows = rosterRows
	slog.Info("leaguemate sync complete", "managers", result.ManagersSeen, "leagues", result.LeaguesSeen, "rosters", rosterRows)
	return result, nil
}

// processMarkisLeagueRosters fetches rosters/users for one of Markis's leagues,
// upserts managers and roster players, and discovers other leagues 1-hop away.
// It returns the number of roster player rows written and the discovered-league
// count to add to the sync result (mirrors the original inline accumulation).
func (s *LeaguemateSyncer) processMarkisLeagueRosters(
	ctx context.Context,
	leagueID, season string,
	seenLeagues, seenManagers map[string]bool,
) (int, int) {
	var rosterRows, discoveredLeagues int
	rawRosters, err := s.sleeper.GetLeagueRosters(ctx, leagueID)
	if err != nil {
		slog.Warn("get rosters", "league", leagueID, "err", err)
		return 0, 0
	}
	rosters := sleeper.ParseRosters(rawRosters)
	users, err := s.sleeper.GetLeagueUsers(ctx, leagueID)
	if err != nil {
		slog.Warn("get users", "league", leagueID, "err", err)
		return 0, 0
	}

	userMap := s.buildUserMap(ctx, users)

	for i := range rosters {
		roster := &rosters[i]
		ownerID := util.StrOrEmpty(roster.OwnerID.Ptr())
		if ownerID == "" {
			continue
		}
		seenManagers[ownerID] = true

		rid := roster.RosterID

		// Upsert league_manager
		user := userMap[ownerID]
		s.upsertLeagueManager(ctx, leagueID, ownerID, rid, user, false, ownerID == models.MarkisUserID)

		rosterRows += s.storeRosterPlayers(ctx, leagueID, rid, roster, "failed to insert roster player")

		// Discover manager's other leagues (1-hop)
		if ownerID == models.MarkisUserID {
			continue
		}
		rows := s.syncOtherManagerLeagues(ctx, ownerID, season, seenLeagues, seenManagers)
		rosterRows += rows
		discoveredLeagues += len(seenLeagues)
	}
	return rosterRows, discoveredLeagues
}

// buildUserMap indexes league users by user_id and upserts each as a sleeper_user.
func (s *LeaguemateSyncer) buildUserMap(ctx context.Context, users []map[string]any) map[string]*sleeper.User {
	parsed := sleeper.ParseUsers(users)
	userMap := make(map[string]*sleeper.User, len(parsed))
	for i := range parsed {
		u := &parsed[i]
		uid := util.StrOrEmpty(u.UserID.Ptr())
		userMap[uid] = u
		s.upsertSleeperUser(ctx, uid, u, uid == models.MarkisUserID)
	}
	return userMap
}

// rosterSlot classifies a player id into starter/taxi/reserve/bench.
func rosterSlot(pid string, starters, taxi, reserve []string) string {
	switch {
	case contains(starters, pid):
		return "starter"
	case contains(taxi, pid):
		return "taxi"
	case contains(reserve, pid):
		return "reserve"
	default:
		return "bench"
	}
}

// storeRosterPlayers writes one roster's players to leaguemate_roster_player
// and returns the number of rows successfully written.
func (s *LeaguemateSyncer) storeRosterPlayers(
	ctx context.Context,
	leagueID string,
	rosterID int,
	roster *sleeper.Roster,
	warnMsg string,
) int {
	players := roster.Players
	starters := roster.Starters
	taxi := roster.Taxi
	reserve := roster.Reserve

	rows := 0
	for _, pid := range players {
		slot := rosterSlot(pid, starters, taxi, reserve)
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO leaguemate_roster_player (league_id, roster_id, sleeper_player_id, slot, snapshot_at)
			VALUES ($1, $2, $3, $4, now())
			ON CONFLICT (league_id, roster_id, sleeper_player_id) DO UPDATE SET slot = EXCLUDED.slot, snapshot_at = now()
		`, leagueID, rosterID, pid, slot); err != nil {
			slog.Warn(warnMsg, "league_id", leagueID, "roster_id", rosterID, "err", err)
		} else {
			rows++
		}
	}
	return rows
}

func (s *LeaguemateSyncer) upsertLeague(ctx context.Context, lg *sleeper.League, isMarkis bool, discoveredVia string) {
	leagueID := lg.LeagueID
	rosterPositions, err := json.Marshal(lg.RosterPositions)
	if err != nil {
		slog.Warn("failed to marshal roster positions", "league_id", leagueID, "err", err)
		rosterPositions = []byte("null")
	}
	settings, err2 := json.Marshal(lg.Settings)
	if err2 != nil {
		slog.Warn("failed to marshal league settings", "league_id", leagueID, "err", err2)
		settings = []byte("null")
	}
	var leagueType *int
	if lg.Settings != nil {
		leagueType = toInt(lg.Settings["type"])
	}

	if _, err := s.pool.Exec(ctx, `
		INSERT INTO league (league_id, name, season, sport, status, num_teams,
			has_superflex, is_best_ball, league_type, roster_positions, settings,
			previous_league_id, is_markis_league, discovered_via_user_id, last_synced_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, now())
		ON CONFLICT (league_id) DO UPDATE SET
			name = COALESCE(EXCLUDED.name, league.name),
			season = EXCLUDED.season, status = EXCLUDED.status,
			num_teams = EXCLUDED.num_teams, has_superflex = EXCLUDED.has_superflex,
			is_best_ball = EXCLUDED.is_best_ball, league_type = EXCLUDED.league_type,
			roster_positions = EXCLUDED.roster_positions, settings = EXCLUDED.settings,
			is_markis_league = league.is_markis_league OR EXCLUDED.is_markis_league,
			last_synced_at = now()
	`, leagueID, lg.Name, lg.Season, "nfl", lg.Status, lg.TotalRosters,
		hasSuperflex(lg), isBestBall(lg), leagueType,
		rosterPositions, settings, lg.PreviousLeagueID,
		isMarkis, nilIfEmpty(discoveredVia)); err != nil {
		slog.Warn("upsert league", "id", leagueID, "err", err)
	}
}

func (s *LeaguemateSyncer) upsertSleeperUser(ctx context.Context, userID string, user *sleeper.User, isMarkis bool) {
	var username, displayName, avatar string
	if user != nil {
		username = util.StrOrEmpty(user.Username.Ptr())
		displayName = util.StrOrEmpty(user.DisplayName.Ptr())
		avatar = util.StrOrEmpty(user.Avatar.Ptr())
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO sleeper_user (user_id, username, display_name, avatar, is_markis, last_synced_at)
		VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), NULLIF($4, ''), $5, now())
		ON CONFLICT (user_id) DO UPDATE SET
			display_name = COALESCE(NULLIF(EXCLUDED.display_name, ''), sleeper_user.display_name),
			avatar = COALESCE(NULLIF(EXCLUDED.avatar, ''), sleeper_user.avatar),
			is_markis = sleeper_user.is_markis OR EXCLUDED.is_markis,
			last_synced_at = now()
	`, userID, username, displayName, avatar, isMarkis)
	if err != nil {
		slog.Warn("upsert sleeper_user", "id", userID, "err", err)
	}
}

func (s *LeaguemateSyncer) upsertLeagueManager(
	ctx context.Context,
	leagueID, userID string,
	rosterID int,
	user *sleeper.User,
	coOwner, isMarkis bool,
) {
	teamName := ""
	if user != nil {
		teamName = fmt.Sprint(user.Metadata["team_name"])
		if teamName == nilStr {
			teamName = ""
		}
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO league_manager (league_id, user_id, roster_id, team_name, co_owner, is_markis, last_synced_at)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, now())
		ON CONFLICT (league_id, roster_id, user_id) DO UPDATE SET
			team_name = EXCLUDED.team_name, last_synced_at = now()
	`, leagueID, userID, rosterID, teamName, coOwner, isMarkis)
	if err != nil {
		slog.Warn("upsert league_manager", "err", err)
	}
}

func hasSuperflex(lg *sleeper.League) bool {
	for _, v := range lg.RosterPositions {
		if strings.EqualFold(v, "SUPER_FLEX") {
			return true
		}
	}
	return false
}

func isBestBall(lg *sleeper.League) bool {
	if lg.Settings == nil {
		return false
	}
	return util.ToInt(lg.Settings["best_ball"]) == 1
}

func contains(arr []string, s string) bool {
	return slices.Contains(arr, s)
}

func nilIfEmpty(s string) any {
	if s == "" || s == "<nil>" {
		return nil
	}
	return s
}

// syncOtherManagerLeagues syncs other leagues for a given manager.
func (s *LeaguemateSyncer) syncOtherManagerLeagues(
	ctx context.Context,
	ownerID, season string,
	seenLeagues, seenManagers map[string]bool,
) int {
	otherLeagues, err := s.sleeper.GetUserLeagues(ctx, ownerID, season)
	if err != nil {
		slog.Warn("get other leagues", "user", ownerID, "err", err)
		return 0
	}
	parsedOthers := sleeper.ParseLeagues(otherLeagues)
	count := 0
	maxLeagues := 3
	rosterRows := 0
	for i := range parsedOthers {
		ol := &parsedOthers[i]
		olID := ol.LeagueID
		if seenLeagues[olID] || count >= maxLeagues {
			continue
		}
		seenLeagues[olID] = true
		count++

		rosterRows += s.syncDiscoveredLeague(ctx, ol, olID, ownerID, seenManagers)
	}
	return rosterRows
}

// syncDiscoveredLeague upserts a league discovered via another manager's
// leagues, along with its managers and roster players.
func (s *LeaguemateSyncer) syncDiscoveredLeague(
	ctx context.Context,
	ol *sleeper.League,
	olID, discoveredVia string,
	seenManagers map[string]bool,
) int {
	s.upsertLeague(ctx, ol, false, discoveredVia)

	rawRosters, err := s.sleeper.GetLeagueRosters(ctx, olID)
	if err != nil {
		slog.Warn("get rosters (discovered league)", "league", olID, "err", err)
		return 0
	}
	olRosters := sleeper.ParseRosters(rawRosters)
	olUsers, err := s.sleeper.GetLeagueUsers(ctx, olID)
	if err != nil {
		slog.Warn("get users (discovered league)", "league", olID, "err", err)
		return 0
	}
	olUserMap := s.buildUserMap(ctx, olUsers)

	rosterRows := 0
	for i := range olRosters {
		olRoster := &olRosters[i]
		olOwnerID := util.StrOrEmpty(olRoster.OwnerID.Ptr())
		if olOwnerID == "" {
			continue
		}
		seenManagers[olOwnerID] = true
		rrid := olRoster.RosterID
		s.upsertLeagueManager(ctx, olID, olOwnerID, rrid, olUserMap[olOwnerID], false, olOwnerID == models.MarkisUserID)

		rosterRows += s.storeRosterPlayers(ctx, olID, rrid, olRoster, "failed to insert other league roster player")
	}
	return rosterRows
}
