package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/markis/fantasy-football-engine/internal/db"
	"github.com/markis/fantasy-football-engine/internal/models"
	"github.com/markis/fantasy-football-engine/internal/sleeper"
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
	ManagersSeen int    `json:"managers_seen"`
	LeaguesSeen  int    `json:"leagues_seen"`
	RosterRows   int    `json:"roster_rows"`
	Status       string `json:"status"`
}

// Sync builds the 1-hop manager graph.
func (s *LeaguemateSyncer) Sync(ctx context.Context, maxLeagues int, season string) (*LeaguemateSyncResult, error) {
	if season == "" {
		season = s.sleeper.CurrentSeason(ctx)
	}
	if maxLeagues == 0 {
		maxLeagues = 15
	}
	result := &LeaguemateSyncResult{Status: "ok"}

	// Get Markis's leagues
	markisLeagues, err := s.sleeper.GetUserLeagues(ctx, models.MarkisUserID, season)
	if err != nil {
		return nil, fmt.Errorf("get Markis leagues: %w", err)
	}

	// Track all leagues and managers
	seenLeagues := make(map[string]bool)
	seenManagers := make(map[string]bool)
	var rosterRows int

	// Process each of Markis's leagues
	for _, lg := range markisLeagues {
		leagueID := fmt.Sprint(lg["league_id"])
		if seenLeagues[leagueID] {
			continue
		}
		seenLeagues[leagueID] = true
		result.LeaguesSeen++

		// Upsert league
		s.upsertLeague(ctx, lg, true, "")

		// Get rosters + users
		rosters, err := s.sleeper.GetLeagueRosters(ctx, leagueID)
		if err != nil {
			slog.Warn("get rosters", "league", leagueID, "err", err)
			continue
		}
		users, err := s.sleeper.GetLeagueUsers(ctx, leagueID)
		if err != nil {
			slog.Warn("get users", "league", leagueID, "err", err)
			continue
		}

		// Build user map
		userMap := make(map[string]map[string]any)
		for _, u := range users {
			uid := fmt.Sprint(u["user_id"])
			userMap[uid] = u
			// Upsert sleeper_user
			s.upsertSleeperUser(ctx, uid, u, uid == models.MarkisUserID)
		}

		// Process rosters
		for _, roster := range rosters {
			ownerID := fmt.Sprint(roster["owner_id"])
			if ownerID == "" || ownerID == "<nil>" {
				continue
			}
			seenManagers[ownerID] = true

			rosterID := toInt(roster["roster_id"])
			var rid int
			if rosterID != nil {
				rid = *rosterID
			}

			// Upsert league_manager
			user := userMap[ownerID]
			s.upsertLeagueManager(ctx, leagueID, ownerID, rid, user, false, ownerID == models.MarkisUserID)

			// Store roster players
			players := toStringSlice(roster["players"])
			starters := toStringSlice(roster["starters"])
			taxi := toStringSlice(roster["taxi"])
			reserve := toStringSlice(roster["reserve"])

			for _, pid := range players {
				slot := "bench"
				if contains(starters, pid) {
					slot = "starter"
				} else if contains(taxi, pid) {
					slot = "taxi"
				} else if contains(reserve, pid) {
					slot = "reserve"
				}
				if _, err := s.pool.Exec(ctx, `
					INSERT INTO leaguemate_roster_player (league_id, roster_id, sleeper_player_id, slot, snapshot_at)
					VALUES ($1, $2, $3, $4, now())
					ON CONFLICT (league_id, roster_id, sleeper_player_id) DO UPDATE SET slot = EXCLUDED.slot, snapshot_at = now()
				`, leagueID, rid, pid, slot); err != nil {
					slog.Warn("failed to insert roster player", "league_id", leagueID, "roster_id", rid, "err", err)
				} else {
					rosterRows++
				}
			}

			// Discover manager's other leagues (1-hop)
			if ownerID != models.MarkisUserID {
				otherLeagues, err := s.sleeper.GetUserLeagues(ctx, ownerID, season)
				if err != nil {
					slog.Warn("get other leagues", "user", ownerID, "err", err)
					continue
				}
				count := 0
				for _, ol := range otherLeagues {
					olID := fmt.Sprint(ol["league_id"])
					if seenLeagues[olID] || count >= maxLeagues {
						continue
					}
					seenLeagues[olID] = true
					count++
					result.LeaguesSeen++

					s.upsertLeague(ctx, ol, false, ownerID)

					// Get rosters for discovered league
					olRosters, err := s.sleeper.GetLeagueRosters(ctx, olID)
					if err != nil {
						slog.Warn("get rosters (discovered league)", "league", olID, "err", err)
						continue
					}
					olUsers, err := s.sleeper.GetLeagueUsers(ctx, olID)
					if err != nil {
						slog.Warn("get users (discovered league)", "league", olID, "err", err)
						continue
					}
					olUserMap := make(map[string]map[string]any)
					for _, u := range olUsers {
						uid := fmt.Sprint(u["user_id"])
						olUserMap[uid] = u
						s.upsertSleeperUser(ctx, uid, u, uid == models.MarkisUserID)
					}

					for _, olRoster := range olRosters {
						olOwnerID := fmt.Sprint(olRoster["owner_id"])
						if olOwnerID == "" || olOwnerID == "<nil>" {
							continue
						}
						seenManagers[olOwnerID] = true
						olRid := toInt(olRoster["roster_id"])
						var rrid int
						if olRid != nil {
							rrid = *olRid
						}
						s.upsertLeagueManager(ctx, olID, olOwnerID, rrid, olUserMap[olOwnerID], false, olOwnerID == models.MarkisUserID)

						olPlayers := toStringSlice(olRoster["players"])
						olStarters := toStringSlice(olRoster["starters"])
						olTaxi := toStringSlice(olRoster["taxi"])
						olReserve := toStringSlice(olRoster["reserve"])
						for _, pid := range olPlayers {
							slot := "bench"
							if contains(olStarters, pid) {
								slot = "starter"
							} else if contains(olTaxi, pid) {
								slot = "taxi"
							} else if contains(olReserve, pid) {
								slot = "reserve"
							}
							if _, err := s.pool.Exec(ctx, `
								INSERT INTO leaguemate_roster_player (league_id, roster_id, sleeper_player_id, slot, snapshot_at)
								VALUES ($1, $2, $3, $4, now())
								ON CONFLICT (league_id, roster_id, sleeper_player_id) DO UPDATE SET slot = EXCLUDED.slot, snapshot_at = now()
							`, olID, rrid, pid, slot); err != nil {
								slog.Warn("failed to insert other league roster player", "league_id", olID, "roster_id", rrid, "err", err)
							} else {
								rosterRows++
							}
						}
					}
				}
			}
		}
	}

	result.ManagersSeen = len(seenManagers)
	result.RosterRows = rosterRows
	slog.Info("leaguemate sync complete", "managers", result.ManagersSeen, "leagues", result.LeaguesSeen, "rosters", rosterRows)
	return result, nil
}

func (s *LeaguemateSyncer) upsertLeague(ctx context.Context, lg map[string]any, isMarkis bool, discoveredVia string) {
	leagueID := fmt.Sprint(lg["league_id"])
	rosterPositions, err := json.Marshal(lg["roster_positions"])
	if err != nil {
		slog.Warn("failed to marshal roster positions", "league_id", leagueID, "err", err)
		rosterPositions = []byte("null")
	}
	settings, err2 := json.Marshal(lg["settings"])
	if err2 != nil {
		slog.Warn("failed to marshal league settings", "league_id", leagueID, "err", err2)
		settings = []byte("null")
	}
	var leagueType *int
	if settingsMap, ok := lg["settings"].(map[string]any); ok {
		leagueType = toInt(settingsMap["type"])
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
	`, leagueID, lg["name"], lg["season"], "nfl", lg["status"], toInt(lg["total_rosters"]),
		hasSuperflex(lg), isBestBall(lg), leagueType,
		rosterPositions, settings, lg["previous_league_id"],
		isMarkis, nilIfEmpty(discoveredVia)); err != nil {
		slog.Warn("upsert league", "id", leagueID, "err", err)
	}
}

func (s *LeaguemateSyncer) upsertSleeperUser(ctx context.Context, userID string, user map[string]any, isMarkis bool) {
	username := ""
	if user != nil {
		username = fmt.Sprint(user["username"])
		if username == "<nil>" {
			username = ""
		}
	}
	displayName := ""
	if user != nil {
		displayName = fmt.Sprint(user["display_name"])
		if displayName == "<nil>" {
			displayName = ""
		}
	}
	avatar := ""
	if user != nil {
		avatar = fmt.Sprint(user["avatar"])
		if avatar == "<nil>" {
			avatar = ""
		}
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

func (s *LeaguemateSyncer) upsertLeagueManager(ctx context.Context, leagueID, userID string, rosterID int, user map[string]any, coOwner bool, isMarkis bool) {
	teamName := ""
	if user != nil {
		if meta, ok := user["metadata"].(map[string]any); ok {
			teamName = fmt.Sprint(meta["team_name"])
			if teamName == "<nil>" {
				teamName = ""
			}
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

func hasSuperflex(lg map[string]any) bool {
	rp, ok := lg["roster_positions"]
	if !ok {
		return false
	}
	arr, ok := rp.([]any)
	if !ok {
		return false
	}
	for _, v := range arr {
		if strings.ToUpper(fmt.Sprint(v)) == "SUPER_FLEX" {
			return true
		}
	}
	return false
}

func isBestBall(lg map[string]any) bool {
	settings, ok := lg["settings"].(map[string]any)
	if !ok {
		return false
	}
	v := toInt(settings["best_ball"])
	return v != nil && *v == 1
}

func toStringSlice(v any) []string {
	if v == nil {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(arr))
	for _, item := range arr {
		s := fmt.Sprint(item)
		if s != "" && s != "<nil>" {
			result = append(result, s)
		}
	}
	return result
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
