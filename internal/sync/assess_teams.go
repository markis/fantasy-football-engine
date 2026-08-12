package sync

import (
	"context"
	"fmt"
	"log/slog"

	"ff-engine/internal/db"
	"ff-engine/internal/models"
	"ff-engine/internal/sleeper"
	"ff-engine/internal/util"
)

// TeamAssessor runs the weekly dynasty team-state assessment.
type TeamAssessor struct {
	pool    *db.Pool
	sleeper *sleeper.Client
}

// NewTeamAssessor creates a new team assessor.
func NewTeamAssessor(pool *db.Pool, sleeperClient *sleeper.Client) *TeamAssessor {
	return &TeamAssessor{pool: pool, sleeper: sleeperClient}
}

// TeamAssessResult is the result of a team assessment.
type TeamAssessResult struct {
	LeaguesAssessed int    `json:"leaguesAssessed"`
	Status          string `json:"status"`
}

// Assess runs the team assessment for all of Markis's leagues.
func (a *TeamAssessor) Assess(ctx context.Context) (*TeamAssessResult, error) {
	result := &TeamAssessResult{Status: "ok"}

	for leagueID, lf := range models.LeagueFormats {
		if lf.Type == "redraft" {
			continue // draft-prep mode
		}

		rosters, err := a.sleeper.GetLeagueRosters(ctx, leagueID)
		if err != nil {
			slog.Warn("get rosters for assessment", "league", leagueID, "err", err)
			continue
		}

		// Find Markis's roster
		var myRoster map[string]any
		for _, r := range rosters {
			if fmt.Sprint(r["owner_id"]) == models.MarkisUserID {
				myRoster = r
				break
			}
		}
		if myRoster == nil {
			continue
		}

		players := util.ToStringSlice(myRoster["players"])
		valuation := a.valuePlayers(ctx, players, "Dynasty Daddy", 14)

		// Compute win-now and future values
		var winNow, future float64
		for _, sid := range players {
			pv := valuation[sid]
			if pv == nil {
				continue
			}
			winNow += pv.tradeValue
			future += pv.tradeValue * models.AgeFactor(pv.position, pv.age)
		}

		// Classify
		zone := classifyZone(winNow, future)
		slog.Info("team assessment", "league", lf.Name, "win_now", winNow, "future", future, "zone", zone)
		result.LeaguesAssessed++
	}

	slog.Info("team assessment complete", "leagues", result.LeaguesAssessed)
	return result, nil
}

type playerValuation struct {
	sleeperID  string
	fullName   string
	position   string
	age        *int
	tradeValue float64
}

func (a *TeamAssessor) valuePlayers(ctx context.Context, playerIDs []string, source string, market int) map[string]*playerValuation {
	result := make(map[string]*playerValuation)
	if len(playerIDs) == 0 {
		return result
	}

	rows, err := a.pool.Query(ctx, `
		SELECT p.sleeper_player_id, p.full_name, p.position, p.age,
		       r.trade_value, r.redraft_value
		FROM player p
		LEFT JOIN player_ranking r ON r.player_id = p.id AND r.source = $1 AND r.market = $2
		WHERE p.sleeper_player_id = ANY($3)
	`, source, market, playerIDs)
	if err != nil {
		slog.Warn("value players query", "err", err)
		return result
	}
	defer rows.Close()

	for rows.Next() {
		var pv playerValuation
		var fullName, position *string
		var age *int
		var tradeValue, redraftValue *int
		if err := rows.Scan(&pv.sleeperID, &fullName, &position, &age, &tradeValue, &redraftValue); err != nil {
			continue
		}
		if fullName != nil {
			pv.fullName = *fullName
		}
		if position != nil {
			pv.position = *position
		}
		pv.age = age
		if tradeValue != nil {
			pv.tradeValue = float64(*tradeValue)
		}
		result[pv.sleeperID] = &pv
	}
	return result
}

func classifyZone(winNow, future float64) string {
	// Simple classification: relative to median
	// In the full Python version, this is ranked within the league.
	// Here we use absolute thresholds as a simplified version.
	if winNow > 50000 && future > 40000 {
		return "CONTENDER"
	}
	if winNow > 40000 && future < 30000 {
		return "WIN-NOW-AGING"
	}
	if winNow < 30000 && future > 40000 {
		return "REBUILDING"
	}
	if winNow < 20000 && future < 20000 {
		return "TANKING"
	}
	return "FRINGE/RETOOL"
}
