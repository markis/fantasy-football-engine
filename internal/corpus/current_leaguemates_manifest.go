package corpus

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/markis/fantasy-football-engine/internal/models"
)

// renderCurrent renders current/ markdown briefs.
func (p *Publisher) renderCurrent(ctx context.Context, targetDir string) (map[string]interface{}, error) {
	curDir := filepath.Join(targetDir, "current")
	if err := os.MkdirAll(curDir, 0o755); err != nil {
		return nil, err
	}

	recs := loadCurrentRecords(filepath.Join(targetDir, "evidence", "records"))
	now := p.common.NowISO()

	// Load team-state
	tsPath := filepath.Join(targetDir, "team", "team-state.json")
	var ts map[string]interface{}
	if data, err := os.ReadFile(tsPath); err == nil {
		json.Unmarshal(data, &ts)
	}

	// daily-brief.md
	recent24 := filterRecent(recs, 24)
	var lines []string
	lines = append(lines, "# Daily Brief", "",
		fmt.Sprintf("_Generated %s. New or materially-changed decision-relevant evidence in the last 24h (%d items)._",
			now, len(recent24)))
	if len(recent24) == 0 {
		lines = append(lines, "", "_No new decision-relevant evidence in the last 24h._")
	} else {
		lines = append(lines, "")
		for _, r := range recent24 {
			lines = append(lines, fmtRec(r))
			if s := getStr(r, "summary"); s != "" {
				if len(s) > 240 {
					s = s[:240]
				}
				lines = append(lines, "  > "+s)
			}
		}
	}
	WriteText(filepath.Join(curDir, "daily-brief.md"), strings.Join(lines, "\n"))

	// injury-and-usage.md
	var inj, usage []map[string]interface{}
	for _, r := range recs {
		if getStr(r, "topic") == "injury" {
			inj = append(inj, r)
		} else if getStr(r, "topic") == "usage" {
			usage = append(usage, r)
		}
	}
	lines = []string{
		"# Injury & Usage", "",
		fmt.Sprintf("_Generated %s._", now),
	}
	for _, pair := range []struct {
		title string
		group []map[string]interface{}
	}{
		{"## Injuries", inj},
		{"## Usage / role / depth-chart", usage},
	} {
		lines = append(lines, "", pair.title, "")
		if len(pair.group) == 0 {
			lines = append(lines, "_None in the current evidence window._")
		} else {
			for _, r := range pair.group {
				lines = append(lines, fmtRec(r))
			}
		}
	}
	WriteText(filepath.Join(curDir, "injury-and-usage.md"), strings.Join(lines, "\n"))

	// market-and-trade-watch.md
	lines = []string{
		"# Market & Trade Watch", "",
		fmt.Sprintf("_Generated %s._", now), "",
		"## Recommendation guardrails", "",
		"- Treat value movement as a **dated market signal**, not truth.",
		"- _No autonomous action: every trade is a proposed decision awaiting human confirmation._",
	}
	WriteText(filepath.Join(curDir, "market-and-trade-watch.md"), strings.Join(lines, "\n"))

	// weekly-team-review.md
	lines = []string{
		"# Weekly Team Review", "",
		fmt.Sprintf("_Generated %s._", now),
	}
	if ts != nil {
		if leagues, ok := ts["leagues"].([]interface{}); ok {
			for _, lg := range leagues {
				l, _ := lg.(map[string]interface{})
				league, _ := l["league"].(map[string]interface{})
				team, _ := l["team"].(map[string]interface{})
				if league == nil {
					continue
				}
				lines = append(lines, "",
					"## "+getStr(league, "name"), "",
					fmt.Sprintf("- **Mode:** %v", team["competitive_mode"]))
			}
		}
	}
	WriteText(filepath.Join(curDir, "weekly-team-review.md"), strings.Join(lines, "\n"))

	// rookie-draft-board.md (simplified — queries DB)
	lines = []string{
		"# Rookie Draft Board", "",
		fmt.Sprintf("_Generated %s._", now), "",
	}
	rows, err := p.common.pool.Query(ctx, `
		SELECT p.full_name, p.position, p.team_abbr, p.age, p.years_exp,
		       r.trade_value, r.overall_rank, r.position_rank
		FROM player p JOIN player_ranking r ON r.player_id = p.id
		WHERE r.source='Dynasty Daddy' AND r.market=14
		  AND (p.years_exp = 0 OR p.age <= 22)
		ORDER BY r.overall_rank ASC NULLS LAST LIMIT 40
	`)
	if err == nil {
		defer rows.Close()
		byPos := make(map[string][]string)
		for rows.Next() {
			var fullName, position, teamAbbr *string
			var age, yearsExp *int
			var tradeValue, overallRank, positionRank *int
			if err := rows.Scan(&fullName, &position, &teamAbbr, &age, &yearsExp,
				&tradeValue, &overallRank, &positionRank); err != nil {
				continue
			}
			pos := "?"
			if position != nil {
				pos = *position
			}
			name := ""
			if fullName != nil {
				name = *fullName
			}
			team := ""
			if teamAbbr != nil {
				team = *teamAbbr
			}
			ageStr := ""
			if age != nil {
				ageStr = strconv.Itoa(*age)
			}
			tvStr := ""
			if tradeValue != nil {
				tvStr = strconv.Itoa(*tradeValue)
			}
			orStr := ""
			if overallRank != nil {
				orStr = strconv.Itoa(*overallRank)
			}
			prStr := ""
			if positionRank != nil {
				prStr = strconv.Itoa(*positionRank)
			}
			byPos[pos] = append(byPos[pos], fmt.Sprintf("| %s | %s | %s | %s | %s | %s |", name, team, ageStr, tvStr, orStr, prStr))
		}
		for _, pos := range []string{"QB", "RB", "WR", "TE"} {
			if rows, ok := byPos[pos]; ok && len(rows) > 0 {
				lines = append(lines, "## "+pos, "",
					"| Player | NFL | Age | Value | OVR | PosRank |", "|---|---|---|---|---|---|")
				lines = append(lines, rows...)
			}
		}
	}
	WriteText(filepath.Join(curDir, "rookie-draft-board.md"), strings.Join(lines, "\n"))

	// upcoming-decisions.md
	lines = []string{
		"# Upcoming Decisions", "",
		fmt.Sprintf("_Generated %s._", now), "",
		"_All decisions are PROPOSED — no autonomous action._", "",
	}
	WriteText(filepath.Join(curDir, "upcoming-decisions.md"), strings.Join(lines, "\n"))

	// strategy/current-team-plan.md
	if err := os.MkdirAll(filepath.Join(targetDir, "strategy"), 0o755); err != nil {
		return nil, err
	}
	planLines := []string{
		"# Current Team Plan", "",
		fmt.Sprintf("_Regenerated %s._", now), "",
	}
	WriteText(filepath.Join(targetDir, "strategy", "current-team-plan.md"), strings.Join(planLines, "\n"))

	return map[string]interface{}{"daily_items": len(recent24), "evidence_total": len(recs)}, nil
}

// renderLeaguemates renders the leaguemate brief.
func (p *Publisher) renderLeaguemates(ctx context.Context, targetDir string) (map[string]interface{}, error) {
	curDir := filepath.Join(targetDir, "current")
	dsDir := filepath.Join(targetDir, "datasets")
	os.MkdirAll(curDir, 0o755)
	os.MkdirAll(dsDir, 0o755)

	var snap *time.Time
	_ = p.common.pool.QueryRow(ctx, "SELECT max(snapshot_date) FROM leaguemate_signal").Scan(&snap)

	gen := p.common.NowISO()
	var lines []string
	lines = append(lines, "# Leaguemate Tendencies", "")
	if snap != nil {
		lines = append(lines, fmt.Sprintf("*_Generated %s from the %s snapshot._*", gen, snap.Format("2006-01-02")))
	} else {
		lines = append(lines, fmt.Sprintf("*_Generated %s._*", gen))
	}
	lines = append(lines, "")

	// Query leaguemate signals
	var profiles int
	if snap != nil {
		rows, err := p.common.pool.Query(ctx, `
			SELECT user_id, COALESCE(username, ''), COALESCE(display_name, ''),
			       leagues_count, win_pct, contender_score, trade_count, trade_count_30d,
			       net_firsts, dossier
			FROM leaguemate_signal WHERE snapshot_date = $1
			ORDER BY contender_score DESC NULLS LAST
		`, *snap)
		if err == nil {
			defer rows.Close()
			jpath := filepath.Join(dsDir, "leaguemate-profiles.jsonl")
			os.WriteFile(jpath, []byte(""), 0o644)
			for rows.Next() {
				var uid, uname, dname string
				var leagues, contScore, tc, tc30, nf *int
				var winPct *float32
				var dossier *string
				if err := rows.Scan(&uid, &uname, &dname, &leagues, &winPct, &contScore,
					&tc, &tc30, &nf, &dossier); err != nil {
					continue
				}
				name := uname
				if name == "" {
					name = dname
				}
				if name == "" {
					name = uid
				}
				lines = append(lines, "## "+name, "")
				dossierStr := ""
				if dossier != nil {
					dossierStr = strings.TrimSpace(*dossier)
				}
				if dossierStr == "" {
					dossierStr = "_No dossier generated._"
				}
				lines = append(lines, dossierStr, "")
				appendJSONLFile(jpath, map[string]interface{}{
					"id":              "leaguemate:" + uname,
					"snapshot_date":   snap.Format("2006-01-02"),
					"user_id":         uid,
					"username":        uname,
					"display_name":    dname,
					"leagues_count":   leagues,
					"win_pct":         winPct,
					"contender_score": contScore,
					"trade_count":     tc,
					"trade_count_30d": tc30,
					"net_firsts":      nf,
					"dossier":         dossierStr,
					"generated_at":    gen,
				})
				profiles++
			}
		}
	}

	WriteText(filepath.Join(curDir, "leaguemate-brief.md"), strings.Join(lines, "\n"))
	return map[string]interface{}{"profiles": profiles}, nil
}

// renderManifest renders corpus-manifest.json + change-log.jsonl.
func (p *Publisher) renderManifest(ctx context.Context, targetDir, prevDir string, evidenceSummary map[string]interface{}) (map[string]interface{}, error) {
	ts := p.common.NowISO()

	// Carry forward prior change-log
	clPath := filepath.Join(targetDir, "datasets", "change-log.jsonl")
	clPrev := filepath.Join(prevDir, "datasets", "change-log.jsonl")
	if data, err := os.ReadFile(clPrev); err == nil {
		os.WriteFile(clPath, data, 0o644)
	} else {
		os.WriteFile(clPath, []byte(""), 0o644)
	}

	// Append change-log entries for evidence added/superseded this run.
	recDir := filepath.Join(targetDir, "evidence", "records")
	var lastChangeID string
	appendEvidenceChanges := func(ids []string, operation string) {
		for _, rid := range ids {
			filename := strings.Replace(rid, "sha256:", "", 1) + ".json"
			contentHash := ""
			if data, err := os.ReadFile(filepath.Join(recDir, filename)); err == nil {
				var rec map[string]interface{}
				if json.Unmarshal(data, &rec) == nil {
					contentHash = getStr(rec, "content_hash")
				}
			}
			if contentHash == "" {
				contentHash = ContentHash(rid)
			}
			changeID := ChangeID(ts, rid, operation)
			entry := map[string]interface{}{
				"change_id":    changeID,
				"timestamp":    ts,
				"operation":    operation,
				"entity_type":  "evidence",
				"entity_id":    rid,
				"paths":        []string{filepath.Join("evidence", "records", filename)},
				"content_hash": contentHash,
				"summary":      operation + " evidence record " + rid,
			}
			if err := AppendJSONL(clPath, entry); err == nil {
				lastChangeID = changeID
			}
		}
	}
	added, _ := evidenceSummary["added"].([]string)
	superseded, _ := evidenceSummary["superseded"].([]string)
	appendEvidenceChanges(added, "added")
	appendEvidenceChanges(superseded, "superseded")
	changesThisRun := len(added) + len(superseded)

	// Walk files and build manifest
	tFiles, err := walkFilesForManifest(targetDir)
	if err != nil {
		return nil, fmt.Errorf("walk target directory: %w", err)
	}
	var filesMeta []map[string]interface{}
	for rel, full := range tFiles {
		if rel == "datasets/change-log.jsonl" {
			continue
		}
		hash, _ := FileSHA256Bytes(full)
		info, _ := os.Stat(full)
		filesMeta = append(filesMeta, map[string]interface{}{
			"path":   rel,
			"sha256": hash,
			"bytes":  info.Size(),
		})
	}

	// Count evidence
	currentEvidence := 0
	if entries, err := os.ReadDir(recDir); err == nil {
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(recDir, e.Name()))
			if err != nil {
				continue
			}
			var rec map[string]interface{}
			if json.Unmarshal(data, &rec) == nil && rec["status"] == "current" {
				currentEvidence++
			}
		}
	}

	teamStateHash := ""
	teamStateCount := 0
	if hash, err := FileSHA256Bytes(filepath.Join(targetDir, "team", "team-state.json")); err == nil {
		teamStateHash = hash
		teamStateCount = 1
	}

	counts := map[string]interface{}{
		"evidence":           currentEvidence,
		"player_signal":      countJSONLFile(targetDir, "datasets/player-signals.jsonl"),
		"valuation":          countJSONLFile(targetDir, "datasets/valuations.jsonl"),
		"news_event":         countJSONLFile(targetDir, "datasets/news-events.jsonl"),
		"league_transaction": countJSONLFile(targetDir, "datasets/league-transactions.jsonl"),
		"team_state":         teamStateCount,
		"change":             countJSONLFile(targetDir, "datasets/change-log.jsonl"),
	}

	var leagueList []map[string]interface{}
	for lid, lf := range models.LeagueFormats {
		leagueList = append(leagueList, map[string]interface{}{"league_id": lid, "name": lf.Name})
	}

	var cursor interface{}
	if lastChangeID != "" {
		cursor = lastChangeID
	}

	manifest := map[string]interface{}{
		"generated_at":       ts,
		"schema_version":     1,
		"change_log_cursor":  cursor,
		"as_of_window_hours": evidenceWindowDays * 24,
		"counts":             counts,
		"files":              filesMeta,
		"leagues":            leagueList,
	}
	if teamStateHash != "" {
		manifest["team_state_hash"] = teamStateHash
	}
	WriteJSON(filepath.Join(targetDir, "corpus-manifest.json"), manifest)

	return map[string]interface{}{
		"changes": changesThisRun,
		"cursor":  cursor,
		"files":   len(filesMeta),
		"counts":  counts,
	}, nil
}

func loadCurrentRecords(recordsDir string) []map[string]interface{} {
	var result []map[string]interface{}
	entries, err := os.ReadDir(recordsDir)
	if err != nil {
		return result
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(recordsDir, e.Name()))
		if err != nil {
			continue
		}
		var rec map[string]interface{}
		if json.Unmarshal(data, &rec) == nil && rec["status"] == "current" {
			result = append(result, rec)
		}
	}
	return result
}

func filterRecent(recs []map[string]interface{}, hours int) []map[string]interface{} {
	var result []map[string]interface{}
	cutoff := time.Now().UTC().Add(-time.Duration(hours) * time.Hour)
	for _, r := range recs {
		pubStr := getStr(r, "published_at")
		if pubStr == "" {
			continue
		}
		t, err := time.Parse("2006-01-02T15:04:05Z", pubStr)
		if err != nil {
			continue
		}
		if t.After(cutoff) {
			result = append(result, r)
		}
	}
	return result
}

func fmtRec(rec map[string]interface{}) string {
	pub := getStr(rec, "published_at")
	if len(pub) > 10 {
		pub = pub[:10]
	}
	players := ""
	if pids, ok := rec["player_ids"].([]string); ok {
		var playersSb452 strings.Builder
		for i, pid := range pids {
			if i >= 4 {
				break
			}
			if i > 0 {
				playersSb452.WriteString(", ")
			}
			playersSb452.WriteString(strings.TrimPrefix(pid, "nfl:"))
		}
		players += playersSb452.String()
	}
	return fmt.Sprintf("- [%s] `%s…` %s — %s (%s) [%s](%s)",
		pub, truncateID(getStr(rec, "id")), getStr(rec, "topic"), getStr(rec, "title"),
		getStr(rec, "publisher"), players, getStr(rec, "canonical_url"))
}

func truncateID(s string) string {
	if len(s) > 24 {
		return s[:24]
	}
	return s
}

func walkFilesForManifest(root string) (map[string]string, error) {
	files := make(map[string]string)
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if base == ".gitignore" || base == "corpus-manifest.json" || base == ".publish.log" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if strings.HasPrefix(rel, ".git") || strings.HasPrefix(rel, ".staging") {
			return nil
		}
		if strings.HasPrefix(rel, "schemas/") {
			return nil
		}
		files[rel] = path
		return nil
	})
	return files, err
}

func countJSONLFile(targetDir, rel string) int {
	path := filepath.Join(targetDir, rel)
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}
