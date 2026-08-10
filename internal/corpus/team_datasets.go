package corpus

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/markis/fantasy-football-engine/internal/models"
)

// anyToInt coerces a decoded-JSON value (float64, string, or int) to an int,
// returning 0 if it can't be interpreted as a number.
func anyToInt(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case json.Number:
		n, err := t.Int64()
		if err != nil {
			return 0
		}
		return int(n)
	case string:
		n, err := strconv.Atoi(t)
		if err != nil {
			return 0
		}
		return n
	default:
		return 0
	}
}

// nilIfEmpty returns nil for an empty string, otherwise a pointer to it —
// so it marshals to JSON null instead of "" for nullable schema fields.
func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// dfltSlots ensures a nil slice marshals as [] rather than null, since the
// corpus JSON schemas require these fields to be arrays, never null.
func dfltSlots(s []map[string]any) []map[string]any {
	if s == nil {
		return []map[string]any{}
	}
	return s
}

// renderTeam renders team/ (team-state, roster, picks, settings, transactions).
func (p *Publisher) renderTeam(ctx context.Context, targetDir string) (map[string]any, error) {
	teamDir := filepath.Join(targetDir, "team")
	if err := os.MkdirAll(teamDir, 0o750); err != nil {
		return nil, err
	}
	leaguesDir := filepath.Join(teamDir, "leagues")
	if err := os.MkdirAll(leaguesDir, 0o750); err != nil {
		slog.Warn("failed to create leagues directory", "err", err)
	}

	now := p.common.NowISO()
	var leagues []map[string]any

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

		rosterOwner := make(map[int]string)
		for _, r := range rosters {
			rosterOwner[anyToInt(r["roster_id"])] = fmt.Sprint(r["owner_id"])
		}

		leagueInfo, err := p.common.LeagueInfo(ctx, leagueID)
		if err != nil {
			slog.Warn("failed to get league info", "league_id", leagueID, "err", err)
			continue
		}
		var starterSlots []string
		for _, rp := range toStringSlice(leagueInfo["roster_positions"]) {
			if rp != "BN" {
				starterSlots = append(starterSlots, rp)
			}
		}

		starters := toStringSlice(myRoster["starters"])
		starterSlot := make(map[string]string, len(starters))
		for i, pid := range starters {
			if pid == "" || pid == "0" || i >= len(starterSlots) {
				continue
			}
			starterSlot[pid] = starterSlots[i]
		}
		taxiSet := make(map[string]bool)
		for _, pid := range toStringSlice(myRoster["taxi"]) {
			taxiSet[pid] = true
		}
		reserveSet := make(map[string]bool)
		for _, pid := range toStringSlice(myRoster["reserve"]) {
			reserveSet[pid] = true
		}

		players := toStringSlice(myRoster["players"])
		playerRows := p.common.PlayerRows(ctx, players)
		rkRows := p.common.RankingRows(ctx, players, "Dynasty Daddy", 14)

		var roster, taxiSquad, injuredReserve []map[string]any
		for _, sid := range players {
			pr := playerRows[sid]
			rk := rkRows[sid]
			if pr == nil {
				continue
			}
			fullName := getStr(pr, "full_name")
			pos := getStr(pr, "position")
			teamAbbr := getStr(pr, "team_abbr")
			var age *int
			if v, ok := pr["age"].(*int); ok {
				age = v
			}
			injuryStatus := getStr(pr, "injury_status")

			var tradeValue *int
			if rk != nil {
				if v, ok := rk["trade_value"].(*int); ok {
					tradeValue = v
				}
			}

			slot := "BN"
			if s, ok := starterSlot[sid]; ok {
				slot = s
			}
			if taxiSet[sid] {
				slot = "TAXI"
			} else if reserveSet[sid] {
				slot = "IR"
			}

			slotRec := map[string]any{
				colSleeperPlayerID: sid,
				colFullName:        nilIfEmpty(fullName),
				colPosition:        nilIfEmpty(pos),
				"nfl_team":         nilIfEmpty(teamAbbr),
				"slot":             slot,
				"trade_value":      tradeValue,
				colAge:             age,
				colInjuryStatus:    nilIfEmpty(injuryStatus),
			}

			switch {
			case taxiSet[sid]:
				taxiSquad = append(taxiSquad, slotRec)
			case reserveSet[sid]:
				injuredReserve = append(injuredReserve, slotRec)
			default:
				roster = append(roster, slotRec)
			}
		}

		// Get future picks currently owned by Markis's roster in this league.
		markisRosterID := fmt.Sprint(myRoster["roster_id"])
		tradedPicks, err := p.common.LeagueTradedPicks(ctx, leagueID)
		if err != nil {
			slog.Warn("failed to get league traded picks", "league_id", leagueID, "err", err)
			tradedPicks = nil
		}
		var futurePicks []map[string]any
		for _, pick := range tradedPicks {
			if fmt.Sprint(pick["owner_id"]) != markisRosterID {
				continue
			}
			originalTeam := fmt.Sprint(pick["roster_id"])
			if name, ok := rosterOwner[anyToInt(pick["roster_id"])]; ok {
				originalTeam = name
			}
			currentOwner := fmt.Sprint(pick["owner_id"])
			if name, ok := rosterOwner[anyToInt(pick["owner_id"])]; ok {
				currentOwner = name
			}
			futurePicks = append(futurePicks, map[string]any{
				"season":        anyToInt(pick["season"]),
				"round":         anyToInt(pick["round"]),
				"original_team": originalTeam,
				"current_owner": currentOwner,
			})
		}

		// FAAB. The league's total waiver budget (default 100 per Sleeper's
		// own default) must come from the league settings, not be assumed —
		// leagues can configure any total.
		settings, _ := myRoster["settings"].(map[string]any) //nolint:errcheck // Type assertion returns empty map if fails
		faab := 0
		if v, ok := settings["waiver_budget_used"]; ok {
			if n, ok := v.(float64); ok {
				faab = int(n)
			}
		}
		waiverBudget := 100
		if lset, ok := leagueInfo["settings"].(map[string]any); ok {
			if v, ok := lset["waiver_budget"]; ok {
				if n, ok := v.(float64); ok {
					waiverBudget = int(n)
				}
			}
		}
		faabRemaining := max(waiverBudget-faab, 0)

		teamName := lf.Name + " (Markis)"
		if md, ok := myRoster["metadata"].(map[string]any); ok {
			if tn, ok := md["team_name"].(string); ok && tn != "" {
				teamName = tn
			}
		}

		benchCount := 0
		for _, rp := range toStringSlice(leagueInfo["roster_positions"]) {
			if rp == "BN" {
				benchCount++
			}
		}

		leagueState := map[string]any{
			colLeague: map[string]any{
				colLeagueID:       leagueID,
				colName:           lf.Name,
				"platform":        "Sleeper",
				"format":          lf.Type,
				colTeams:          lf.Teams,
				"scoring_summary": fmt.Sprintf("%d QB, PPR=%d, TEP=%s", lf.NumQbs, lf.PPR, lf.TEP),
				"roster_summary":  fmt.Sprintf("Starters: %s; Bench: %d", strings.Join(starterSlots, ","), benchCount),
				"trade_deadline":  nil,
				"notes":           "",
			},
			"team": map[string]any{
				"name":                     teamName,
				"competitive_mode":         "unknown", // would be read from TEAM_STATE.md
				"target_contention_window": nil,
				"roster":                   dfltSlots(roster),
				"taxi_squad":               dfltSlots(taxiSquad),
				"injured_reserve":          dfltSlots(injuredReserve),
				"future_picks":             dfltSlots(futurePicks),
				"faab_remaining":           faabRemaining,
				"active_trade_discussions": []string{},
				"roster_constraints":       []string{},
				"current_priorities":       []string{},
			},
			colAsOf: now,
			"data_freshness": map[string]any{
				"roster_updated_at":       now,
				"league_updated_at":       now,
				"transactions_updated_at": now,
			},
		}

		// Write per-league
		slug := slugify(lf.Name)
		if err := WriteJSON(filepath.Join(leaguesDir, slug, "team-state.json"), leagueState); err != nil {
			return nil, err
		}

		leagues = append(leagues, leagueState)
	}

	// Write multi-league team-state
	multi := map[string]any{
		colLeagues:     leagues,
		"as_of":        now,
		colGeneratedAt: now,
	}
	if err := WriteJSON(filepath.Join(teamDir, "team-state.json"), multi); err != nil {
		return nil, err
	}

	// Roster markdown
	var mdLines []string
	mdLines = append(mdLines, "# Roster", "", fmt.Sprintf("_Generated %s._", now), "")
	for _, lg := range leagues {
		l, _ := lg["league"].(map[string]any) //nolint:errcheck // Map guaranteed by buildLeagueState
		t, _ := lg["team"].(map[string]any)   //nolint:errcheck // Map guaranteed by buildLeagueState
		mdLines = append(mdLines, fmt.Sprintf("## %s", l["name"]), "")
		roster, _ := t["roster"].([]map[string]any) //nolint:errcheck // Roster guaranteed by buildLeagueState
		for _, r := range roster {
			mdLines = append(mdLines, fmt.Sprintf("- %s (%s, %s) — value: %v",
				r["full_name"], r["position"], r["nfl_team"], r["trade_value"]))
		}
		mdLines = append(mdLines, "")
	}
	if err := WriteText(filepath.Join(teamDir, "roster.md"), strings.Join(mdLines, "\n")); err != nil {
		return nil, err
	}

	// Future picks
	if err := WriteJSON(filepath.Join(teamDir, "future-picks.json"), map[string]any{
		"picks":        []any{},
		colGeneratedAt: now,
	}); err != nil {
		return nil, err
	}

	// League settings
	if err := WriteJSON(filepath.Join(teamDir, "league-settings.json"), map[string]any{
		colLeagues:     models.LeagueFormats,
		colGeneratedAt: now,
	}); err != nil {
		return nil, err
	}

	// Transaction history (empty for now)
	if err := os.WriteFile(filepath.Join(teamDir, "transaction-history.jsonl"), []byte(""), 0o600); err != nil {
		slog.Warn("failed to create transaction-history.jsonl", "err", err)
	}

	return map[string]any{colLeagues: len(leagues)}, nil
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
func (p *Publisher) renderDatasets(ctx context.Context, targetDir string) (map[string]any, error) {
	dsDir := filepath.Join(targetDir, "datasets")
	if err := os.MkdirAll(dsDir, 0o750); err != nil {
		return nil, err
	}

	watchIDs, _, ownership := p.buildWatchSet(ctx)
	pr := p.common.PlayerRows(ctx, watchIDs)
	now := p.common.NowISO()

	// players.jsonl
	playersPath := filepath.Join(dsDir, "players.jsonl")
	if err := os.WriteFile(playersPath, []byte(""), 0o600); err != nil {
		slog.Warn("failed to create players.jsonl", "err", err)
	}
	for _, sid := range sortedStringSlice(watchIDs) {
		p := pr[sid]
		if p == nil {
			p = map[string]any{}
		}
		own := ownership[sid]
		var leagues []map[string]string
		for _, pair := range own {
			leagues = append(leagues, map[string]string{"league": pair[0], "role": pair[1]})
		}
		rec := map[string]any{
			"sleeper_player_id": sid,
			"nfl_id":            "nfl:" + sid,
			"full_name":         getStr(p, "full_name"),
			"position":          getStr(p, "position"),
			"nfl_team":          getStr(p, "team_abbr"),
			"age":               getStr(p, "age"),
			colStatus:           getStr(p, colStatus),
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
	if err := os.WriteFile(sigPath, []byte(""), 0o600); err != nil {
		slog.Warn("failed to create player-signals.jsonl", "err", err)
	}
	sigCount := 0
	for _, sid := range sortedStringSlice(watchIDs) {
		p := pr[sid]
		if p == nil {
			continue
		}
		injStatus := getStr(p, "injury_status")
		if injStatus != "" {
			obs := now
			if v, ok := p["last_synced_at"].(time.Time); ok && !v.IsZero() {
				obs = v.UTC().Format("2006-01-02T15:04:05Z")
			}
			rec := map[string]any{
				"signal_id":   SignalID("nfl:"+sid, "injury", injStatus, obs),
				"player_id":   "nfl:" + sid,
				"signal_type": colInjury,
				"value": map[string]any{
					colStatus:   injStatus,
					"body_part": getStr(p, "injury_body_part"),
					"notes":     getStr(p, "injury_notes"),
				},
				"source":            "Sleeper",
				"source_url":        nil,
				"observed_at":       obs,
				colPublishedAt:      nil,
				colConfidence:       "high",
				colStatus:           colCurrent,
				colEvidenceRecordID: nil,
			}
			appendJSONLFile(sigPath, rec)
			sigCount++
		}
	}

	// valuations.jsonl
	valPath := filepath.Join(dsDir, "valuations.jsonl")
	if err := os.WriteFile(valPath, []byte(""), 0o600); err != nil {
		return nil, fmt.Errorf("writeFile valuations: %w", err)
	}
	valCount := 0
	sources := []struct {
		source string
		market int
	}{
		{"Dynasty Daddy", 14},
		{srcFantasyCalc, 1},
		{srcFantasyCalc, 2},
		{srcFantasyCalc, 3},
		{"KeepTradeCut", 0},
	}
	for _, src := range sources {
		rk := p.common.RankingRows(ctx, watchIDs, src.source, src.market)
		fmtCtxMap := map[int]string{
			1:  "12t-1QB-PPR",
			2:  "12t-SF-PPR-TEP1.0",
			3:  "10t-SF-HalfPPR",
			14: "Dynasty Daddy composite",
			0:  "KeepTradeCut composite",
		}
		fmtCtx := fmtCtxMap[src.market]
		for sid, r := range rk {
			obs := now
			if v, ok := r["data_date"].(*time.Time); ok && v != nil {
				obs = v.UTC().Format("2006-01-02T15:04:05Z")
			} else if v, ok := r["snapshot_date"].(time.Time); ok {
				obs = v.UTC().Format("2006-01-02T15:04:05Z")
			}
			if v, ok := r["trade_value"].(*int); ok && v != nil {
				rec := map[string]any{
					"valuation_id":       ValuationID("nfl:"+sid, src.source, "trade-value", fmtCtx, obs),
					"player_id":          "nfl:" + sid,
					"source":             src.source,
					"valuation_type":     "trade-value",
					"format_context":     fmtCtx,
					"value":              *v,
					"observed_at":        obs,
					"source_url":         nil,
					colConfidence:        colMedium,
					"evidence_record_id": nil,
				}
				appendJSONLFile(valPath, rec)
				valCount++
			}
		}
	}

	// news-events.jsonl (from evidence records)
	newsPath := filepath.Join(dsDir, "news-events.jsonl")
	if err := os.WriteFile(newsPath, []byte(""), 0o600); err != nil {
		slog.Warn("failed to create news-events.jsonl", "err", err)
	}
	recDir := filepath.Join(targetDir, "evidence", "records")
	entries, err := os.ReadDir(recDir)
	if err != nil {
		slog.Warn("failed to read evidence records directory", "path", recDir, "err", err)
		entries = nil
	}
	newsCount := 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(recDir, entry.Name()))
		if err != nil {
			continue
		}
		var rec map[string]any
		if json.Unmarshal(data, &rec) != nil {
			continue
		}
		if rec["status"] != "current" {
			continue
		}
		appendJSONLFile(newsPath, map[string]any{
			"evidence_record_id": rec["id"],
			colCanonicalURL:      rec[colCanonicalURL],
			colTitle:             rec[colTitle],
			colPublishedAt:       rec[colPublishedAt],
			"topic":              rec["topic"],
			"publisher":          rec["publisher"],
			"player_ids":         rec["player_ids"],
			"team_ids":           rec["team_ids"],
		})
		newsCount++
	}

	// league-transactions.jsonl (empty for now)
	if err := os.WriteFile(filepath.Join(dsDir, "league-transactions.jsonl"), []byte(""), 0o600); err != nil {
		return nil, fmt.Errorf("writeFile league-transactions: %w", err)
	}

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
	var leagueList []map[string]any
	for lid, lf := range models.LeagueFormats {
		leagueList = append(leagueList, map[string]any{
			"league_id": lid, "name": lf.Name, "format": lf.Type, "teams": lf.Teams,
		})
	}
	entities := map[string]any{
		"as_of":         now,
		"leagues":       leagueList,
		"teams":         teamList,
		"players_count": len(watchIDs),
		"players":       []any{},
	}
	if err := WriteJSON(filepath.Join(dsDir, "entities.json"), entities); err != nil {
		return nil, err
	}

	return map[string]any{
		"players":      len(watchIDs),
		"signals":      sigCount,
		"valuations":   valCount,
		"news_events":  newsCount,
		"transactions": 0,
	}, nil
}

func appendJSONLFile(path string, obj any) {
	data, err := json.Marshal(obj)
	if err != nil {
		slog.Warn("failed to marshal JSONL object", "path", path, "err", err)
		return
	}
	data = append(data, '\n')
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		slog.Warn("failed to open JSONL file", "path", path, "err", err)
		return
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		slog.Warn("failed to write JSONL data", "path", path, "err", err)
	}
}

func sortedStringSlice(ids []string) []string {
	result := make([]string, len(ids))
	copy(result, ids)
	sort.Strings(result)
	return result
}
