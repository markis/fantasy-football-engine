package corpus

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/markis/fantasy-football-engine/internal/models"
)

// renderTeam renders team/ (team-state, roster, picks, settings, transactions).
func (p *Publisher) renderTeam(ctx context.Context, targetDir string) (map[string]interface{}, error) {
	teamDir := filepath.Join(targetDir, "team")
	if err := os.MkdirAll(teamDir, 0755); err != nil {
		return nil, err
	}
	leaguesDir := filepath.Join(teamDir, "leagues")
	os.MkdirAll(leaguesDir, 0755)

	now := p.common.NowISO()
	var leagues []map[string]interface{}

	for leagueID, lf := range models.LeagueFormats {
		rosters, err := p.common.LeagueRosters(ctx, leagueID)
		if err != nil {
			slog.Warn("get rosters for team", "league", leagueID, "err", err)
			continue
		}
		myRoster := p.common.MyRoster(rosters)
		if myRoster == nil {
			continue
		}

		players := toStringSlice(myRoster["players"])
		playerRows := p.common.PlayerRows(ctx, players)
		rkRows := p.common.RankingRows(ctx, players, "Dynasty Daddy", 14)

		var roster []map[string]interface{}
		for _, sid := range players {
			pr := playerRows[sid]
			rk := rkRows[sid]
			if pr == nil {
				continue
			}
			fullName := getStr(pr, "full_name")
			pos := getStr(pr, "position")
			teamAbbr := getStr(pr, "team_abbr")
			ageStr := ""
			if v, ok := pr["age"].(*int); ok && v != nil {
				ageStr = fmt.Sprint(*v)
			}
			injuryStatus := getStr(pr, "injury_status")

			var tradeValue *int
			if rk != nil {
				if v, ok := rk["trade_value"].(*int); ok {
					tradeValue = v
				}
			}

			roster = append(roster, map[string]interface{}{
				"sleeper_player_id": sid,
				"full_name":         fullName,
				"position":          pos,
				"nfl_team":          teamAbbr,
				"age":               ageStr,
				"injury_status":     injuryStatus,
				"trade_value":       tradeValue,
			})
		}

		// Get future picks
		tradedPicks, _ := p.common.LeagueTradedPicks(ctx, leagueID)
		var futurePicks []map[string]interface{}
		for _, pick := range tradedPicks {
			if fmt.Sprint(pick["owner_id"]) == models.MarkisUserID {
				futurePicks = append(futurePicks, pick)
			}
		}

		// FAAB
		settings, _ := myRoster["settings"].(map[string]interface{})
		faab := 0
		if v, ok := settings["waiver_budget_used"]; ok {
			if n, ok := v.(float64); ok {
				faab = int(n)
			}
		}

		leagueState := map[string]interface{}{
			"league": map[string]interface{}{
				"league_id": leagueID,
				"name":      lf.Name,
				"format":    lf.Type,
				"teams":     lf.Teams,
				"num_qbs":   lf.NumQbs,
			},
			"team": map[string]interface{}{
				"roster":            roster,
				"future_picks":      futurePicks,
				"faab_remaining":    100 - faab,
				"competitive_mode":  "unknown", // would be read from TEAM_STATE.md
				"taxi_squad":        []interface{}{},
				"injured_reserve":   []interface{}{},
			},
			"as_of": now,
			"data_freshness": map[string]interface{}{
				"roster_updated_at":       now,
				"league_updated_at":       now,
				"transactions_updated_at": now,
			},
		}

		// Write per-league
		slug := slugify(lf.Name)
		WriteJSON(filepath.Join(leaguesDir, slug, "team-state.json"), leagueState)

		leagues = append(leagues, leagueState)
	}

	// Write multi-league team-state
	multi := map[string]interface{}{
		"leagues":    leagues,
		"as_of":      now,
		"generated_at": now,
	}
	WriteJSON(filepath.Join(teamDir, "team-state.json"), multi)

	// Roster markdown
	var mdLines []string
	mdLines = append(mdLines, "# Roster", "", fmt.Sprintf("_Generated %s._", now), "")
	for _, lg := range leagues {
		l := lg["league"].(map[string]interface{})
		t := lg["team"].(map[string]interface{})
		mdLines = append(mdLines, fmt.Sprintf("## %s", l["name"]), "")
		for _, r := range t["roster"].([]map[string]interface{}) {
			mdLines = append(mdLines, fmt.Sprintf("- %s (%s, %s) — value: %v",
				r["full_name"], r["position"], r["nfl_team"], r["trade_value"]))
		}
		mdLines = append(mdLines, "")
	}
	WriteText(filepath.Join(teamDir, "roster.md"), strings.Join(mdLines, "\n"))

	// Future picks
	WriteJSON(filepath.Join(teamDir, "future-picks.json"), map[string]interface{}{
		"picks":       []interface{}{},
		"generated_at": now,
	})

	// League settings
	WriteJSON(filepath.Join(teamDir, "league-settings.json"), map[string]interface{}{
		"leagues": models.LeagueFormats,
		"generated_at": now,
	})

	// Transaction history (empty for now)
	os.WriteFile(filepath.Join(teamDir, "transaction-history.jsonl"), []byte(""), 0644)

	return map[string]interface{}{"leagues": len(leagues)}, nil
}

var slugNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(name string) string {
	s := slugNonAlnum.ReplaceAllString(strings.ToLower(name), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "league"
	}
	return s
}

// renderDatasets renders datasets/ JSONL/JSON feeds.
func (p *Publisher) renderDatasets(ctx context.Context, targetDir string) (map[string]interface{}, error) {
	dsDir := filepath.Join(targetDir, "datasets")
	if err := os.MkdirAll(dsDir, 0755); err != nil {
		return nil, err
	}

	watchIDs, _, ownership := p.buildWatchSet(ctx)
	pr := p.common.PlayerRows(ctx, watchIDs)
	now := p.common.NowISO()

	// players.jsonl
	playersPath := filepath.Join(dsDir, "players.jsonl")
	os.WriteFile(playersPath, []byte(""), 0644)
	for _, sid := range sortedStringSlice(watchIDs) {
		p := pr[sid]
		if p == nil {
			p = map[string]interface{}{}
		}
		own := ownership[sid]
		var leagues []map[string]string
		for _, pair := range own {
			leagues = append(leagues, map[string]string{"league": pair[0], "role": pair[1]})
		}
		rec := map[string]interface{}{
			"sleeper_player_id": sid,
			"nfl_id":            "nfl:" + sid,
			"full_name":         getStr(p, "full_name"),
			"position":          getStr(p, "position"),
			"nfl_team":          getStr(p, "team_abbr"),
			"age":               getStr(p, "age"),
			"status":            getStr(p, "status"),
			"active":            p["active"],
			"injury_status":     getStr(p, "injury_status"),
			"injury_body_part":  getStr(p, "injury_body_part"),
			"injury_notes":      getStr(p, "injury_notes"),
			"leagues":           leagues,
		}
		appendJSONLFile(playersPath, rec)
	}

	// player-signals.jsonl
	sigPath := filepath.Join(dsDir, "player-signals.jsonl")
	os.WriteFile(sigPath, []byte(""), 0644)
	sigCount := 0
	for _, sid := range sortedStringSlice(watchIDs) {
		p := pr[sid]
		if p == nil {
			continue
		}
		injStatus := getStr(p, "injury_status")
		if injStatus != "" {
			obs := getStr(p, "last_synced_at")
			if obs == "" {
				obs = now
			}
			rec := map[string]interface{}{
				"signal_id":            SignalID("nfl:"+sid, "injury", injStatus, obs),
				"player_id":            "nfl:" + sid,
				"signal_type":          "injury",
				"value":                map[string]interface{}{"status": injStatus, "body_part": getStr(p, "injury_body_part"), "notes": getStr(p, "injury_notes")},
				"source":               "Sleeper",
				"observed_at":          obs,
				"published_at":         nil,
				"confidence":           "high",
				"status":               "current",
				"evidence_record_id":   nil,
			}
			appendJSONLFile(sigPath, rec)
			sigCount++
		}
	}

	// valuations.jsonl
	valPath := filepath.Join(dsDir, "valuations.jsonl")
	os.WriteFile(valPath, []byte(""), 0645)
	valCount := 0
	sources := []struct{ source string; market int }{
		{"Dynasty Daddy", 14}, {"FantasyCalc", 1}, {"FantasyCalc", 2}, {"FantasyCalc", 3}, {"KeepTradeCut", 0},
	}
	for _, src := range sources {
		rk := p.common.RankingRows(ctx, watchIDs, src.source, src.market)
		fmtCtx := map[int]string{1: "12t-1QB-PPR", 2: "12t-SF-PPR-TEP1.0", 3: "10t-SF-HalfPPR", 14: "Dynasty Daddy composite", 0: "KeepTradeCut composite"}[src.market]
		for sid, r := range rk {
			obs := now
			if v, ok := r["data_date"].(*time.Time); ok && v != nil {
				obs = v.UTC().Format("2006-01-02T15:04:05Z")
			} else if v, ok := r["snapshot_date"].(time.Time); ok {
				obs = v.UTC().Format("2006-01-02T15:04:05Z")
			}
			if v, ok := r["trade_value"].(*int); ok && v != nil {
				rec := map[string]interface{}{
					"valuation_id":         ValuationID("nfl:"+sid, src.source, "trade-value", fmtCtx, obs+"1qb"),
					"player_id":            "nfl:" + sid,
					"source":               src.source,
					"valuation_type":       "trade-value",
					"format_context":       fmtCtx,
					"value":                *v,
					"observed_at":          obs,
					"source_url":           nil,
					"confidence":           "medium",
					"evidence_record_id":   nil,
				}
				appendJSONLFile(valPath, rec)
				valCount++
			}
		}
	}

	// news-events.jsonl (from evidence records)
	newsPath := filepath.Join(dsDir, "news-events.jsonl")
	os.WriteFile(newsPath, []byte(""), 0644)
	recDir := filepath.Join(targetDir, "evidence", "records")
	entries, _ := os.ReadDir(recDir)
	newsCount := 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(recDir, entry.Name()))
		if err != nil {
			continue
		}
		var rec map[string]interface{}
		if json.Unmarshal(data, &rec) != nil {
			continue
		}
		if rec["status"] != "current" {
			continue
		}
		appendJSONLFile(newsPath, map[string]interface{}{
			"evidence_record_id": rec["id"],
			"canonical_url":      rec["canonical_url"],
			"title":              rec["title"],
			"published_at":       rec["published_at"],
			"topic":              rec["topic"],
			"publisher":          rec["publisher"],
			"player_ids":         rec["player_ids"],
			"team_ids":           rec["team_ids"],
		})
		newsCount++
	}

	// league-transactions.jsonl (empty for now)
	os.WriteFile(filepath.Join(dsDir, "league-transactions.jsonl"), []byte(""), 0644)

	// entities.json
	teams := make(map[string]bool)
	for _, p := range pr {
		t := getStr(p, "team_abbr")
		if t != "" {
			teams[t] = true
		}
	}
	var teamList []string
	for t := range teams {
		teamList = append(teamList, t)
	}
	var leagueList []map[string]interface{}
	for lid, lf := range models.LeagueFormats {
		leagueList = append(leagueList, map[string]interface{}{
			"league_id": lid, "name": lf.Name, "format": lf.Type, "teams": lf.Teams,
		})
	}
	entities := map[string]interface{}{
		"as_of":         now,
		"leagues":       leagueList,
		"teams":         teamList,
		"players_count": len(watchIDs),
		"players":       []interface{}{},
	}
	WriteJSON(filepath.Join(dsDir, "entities.json"), entities)

	return map[string]interface{}{
		"players":  len(watchIDs),
		"signals":  sigCount,
		"valuations": valCount,
		"news_events": newsCount,
		"transactions": 0,
	}, nil
}

func appendJSONLFile(path string, obj interface{}) {
	data, _ := json.Marshal(obj)
	data = append(data, '\n')
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(data)
}

func sortedStringSlice(ids []string) []string {
	result := make([]string, len(ids))
	copy(result, ids)
	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			if result[i] > result[j] {
				result[i], result[j] = result[j], result[i]
			}
		}
	}
	return result
}