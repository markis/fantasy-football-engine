package corpus

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ff-engine/internal/models"
)

// renderCurrent renders current/ markdown briefs.
func (p *Publisher) renderCurrent(ctx context.Context, targetDir string) (map[string]any, error) {
	curDir := filepath.Join(targetDir, "current")
	if err := os.MkdirAll(curDir, 0o750); err != nil {
		return nil, fmt.Errorf("create directory %s: %w", curDir, err)
	}

	recs := loadCurrentRecords(filepath.Join(targetDir, "evidence", "records"))
	now := p.common.NowISO()
	ts := loadTeamState(targetDir)

	recent24 := p.writeDailyBrief(curDir, now, recs)
	p.writeInjuryAndUsage(curDir, now, recs)
	p.writeMarketAndTradeWatch(curDir, now)

	if err := p.writeWeeklyTeamReview(curDir, now, ts); err != nil {
		return nil, err
	}
	if err := p.writeRookieDraftBoard(ctx, curDir, now); err != nil {
		return nil, err
	}
	if err := p.writeUpcomingDecisions(curDir, now); err != nil {
		return nil, err
	}
	if err := p.writeCurrentTeamPlan(targetDir, now); err != nil {
		return nil, err
	}

	return map[string]any{"daily_items": len(recent24), "evidence_total": len(recs)}, nil
}

// loadTeamState reads and parses team/team-state.json, returning nil on any failure.
func loadTeamState(targetDir string) map[string]any {
	tsPath := filepath.Join(targetDir, "team", "team-state.json")
	var ts map[string]any
	if data, err := readFileRooted(tsPath); err == nil {
		if json.Unmarshal(data, &ts) != nil {
			ts = nil
		}
	}
	return ts
}

// writeDailyBrief renders current/daily-brief.md and returns the evidence added/changed in the last 24h.
func (p *Publisher) writeDailyBrief(curDir, now string, recs []map[string]any) []map[string]any {
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
	if err := WriteText(filepath.Join(curDir, "daily-brief.md"), strings.Join(lines, "\n")); err != nil {
		slog.Warn("failed to write daily-brief.md", "err", err)
	}
	return recent24
}

// writeInjuryAndUsage renders current/injury-and-usage.md.
func (p *Publisher) writeInjuryAndUsage(curDir, now string, recs []map[string]any) {
	var inj, usage []map[string]any
	for _, r := range recs {
		if getStr(r, "topic") == colInjury {
			inj = append(inj, r)
		} else if getStr(r, "topic") == colUsage {
			usage = append(usage, r)
		}
	}
	lines := []string{
		"# Injury & Usage", "",
		fmt.Sprintf("_Generated %s._", now),
	}
	for _, pair := range []struct {
		title string
		group []map[string]any
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
	if err := WriteText(filepath.Join(curDir, "injury-and-usage.md"), strings.Join(lines, "\n")); err != nil {
		slog.Warn("failed to write injury-and-usage.md", "err", err)
	}
}

// writeMarketAndTradeWatch renders current/market-and-trade-watch.md.
func (p *Publisher) writeMarketAndTradeWatch(curDir, now string) {
	lines := []string{
		"# Market & Trade Watch", "",
		fmt.Sprintf("_Generated %s._", now), "",
		"## Recommendation guardrails", "",
		"- Treat value movement as a **dated market signal**, not truth.",
		"- _No autonomous action: every trade is a proposed decision awaiting human confirmation._",
	}
	if err := WriteText(filepath.Join(curDir, "market-and-trade-watch.md"), strings.Join(lines, "\n")); err != nil {
		slog.Warn("failed to write market-and-trade-watch.md", "err", err)
	}
}

// writeWeeklyTeamReview renders current/weekly-team-review.md.
func (p *Publisher) writeWeeklyTeamReview(curDir, now string, ts map[string]any) error {
	lines := []string{
		"# Weekly Team Review", "",
		fmt.Sprintf("_Generated %s._", now),
	}
	dst := filepath.Join(curDir, "weekly-team-review.md")
	if ts == nil {
		return WriteText(dst, strings.Join(lines, "\n"))
	}
	leagues, ok := ts["leagues"].([]any)
	if !ok {
		return WriteText(dst, strings.Join(lines, "\n"))
	}
	for _, lg := range leagues {
		l, ok := lg.(map[string]any)
		if !ok {
			continue
		}
		league, ok := l["league"].(map[string]any)
		if !ok {
			continue
		}
		var team map[string]any
		if v, ok := l["team"].(map[string]any); ok {
			team = v
		}
		lines = append(lines, "",
			"## "+getStr(league, "name"), "",
			fmt.Sprintf("- **Mode:** %v", team["competitive_mode"]))
	}
	return WriteText(dst, strings.Join(lines, "\n"))
}

// writeRookieDraftBoard renders current/rookie-draft-board.md (simplified — queries DB).
func (p *Publisher) writeRookieDraftBoard(ctx context.Context, curDir, now string) error {
	lines := []string{
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
	if err != nil {
		return fmt.Errorf("query rookie draft board: %w", err)
	}
	defer rows.Close()
	p.appendRookieDraftLines(rows, &lines)
	return WriteText(filepath.Join(curDir, "rookie-draft-board.md"), strings.Join(lines, "\n"))
}

// writeUpcomingDecisions renders current/upcoming-decisions.md.
func (p *Publisher) writeUpcomingDecisions(curDir, now string) error {
	lines := []string{
		"# Upcoming Decisions", "",
		fmt.Sprintf("_Generated %s._", now), "",
		"_All decisions are PROPOSED — no autonomous action._", "",
	}
	return WriteText(filepath.Join(curDir, "upcoming-decisions.md"), strings.Join(lines, "\n"))
}

// writeCurrentTeamPlan renders strategy/current-team-plan.md.
func (p *Publisher) writeCurrentTeamPlan(targetDir, now string) error {
	if err := os.MkdirAll(filepath.Join(targetDir, "strategy"), 0o750); err != nil {
		return fmt.Errorf("create strategy directory: %w", err)
	}
	planLines := []string{
		"# Current Team Plan", "",
		fmt.Sprintf("_Regenerated %s._", now), "",
	}
	return WriteText(filepath.Join(targetDir, "strategy", "current-team-plan.md"), strings.Join(planLines, "\n"))
}

// renderLeaguemates renders the leaguemate brief.
func (p *Publisher) renderLeaguemates(ctx context.Context, targetDir string) (map[string]any, error) {
	curDir := filepath.Join(targetDir, "current")
	dsDir := filepath.Join(targetDir, "datasets")
	if err := os.MkdirAll(curDir, 0o750); err != nil {
		return nil, fmt.Errorf("create directory %s: %w", curDir, err)
	}
	if err := os.MkdirAll(dsDir, 0o750); err != nil {
		return nil, fmt.Errorf("create directory %s: %w", dsDir, err)
	}

	var snap *time.Time
	if err := p.common.pool.QueryRow(ctx, "SELECT max(snapshot_date) FROM leaguemate_signal").Scan(&snap); err != nil {
		slog.Warn("failed to query leaguemate snapshot date", "err", err)
	}

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
		profiles = p.renderLeaguemateProfiles(ctx, dsDir, snap, gen, &lines)
	}

	if err := WriteText(filepath.Join(curDir, "leaguemate-brief.md"), strings.Join(lines, "\n")); err != nil {
		return nil, err
	}
	return map[string]any{"profiles": profiles}, nil
}

// renderManifest renders corpus-manifest.json + change-log.jsonl.
func (p *Publisher) renderManifest(_ context.Context, targetDir, prevDir string, evidenceSummary map[string]any) (map[string]any, error) {
	ts := p.common.NowISO()

	clPath := filepath.Join(targetDir, "datasets", "change-log.jsonl")
	clPrev := filepath.Join(prevDir, "datasets", "change-log.jsonl")
	carryForwardChangeLog(clPath, clPrev)

	recDir := filepath.Join(targetDir, "evidence", "records")
	lastChangeID, changesThisRun := appendEvidenceChangeLog(clPath, recDir, ts, evidenceSummary)

	tFiles, err := walkFilesForManifest(targetDir)
	if err != nil {
		return nil, fmt.Errorf("walk target directory: %w", err)
	}
	filesMeta := buildManifestFilesMeta(tFiles)

	currentEvidence := countCurrentEvidence(recDir)

	teamStateHash := ""
	teamStateCount := 0
	if hash, err := FileSHA256Bytes(filepath.Join(targetDir, "team", "team-state.json")); err == nil {
		teamStateHash = hash
		teamStateCount = 1
	}

	counts := buildManifestCounts(targetDir, currentEvidence, teamStateCount)
	leagueList := buildManifestLeagueList()

	var cursor any
	if lastChangeID != "" {
		cursor = lastChangeID
	}

	manifest := map[string]any{
		colGeneratedAt:       ts,
		"schema_version":     1,
		"change_log_cursor":  cursor,
		"as_of_window_hours": evidenceWindowDays * 24,
		"counts":             counts,
		"files":              filesMeta,
		colLeagues:           leagueList,
	}
	if teamStateHash != "" {
		manifest["team_state_hash"] = teamStateHash
	}
	if err := WriteJSON(filepath.Join(targetDir, "corpus-manifest.json"), manifest); err != nil {
		return nil, err
	}

	return map[string]any{
		"changes": changesThisRun,
		"cursor":  cursor,
		"files":   len(filesMeta),
		"counts":  counts,
	}, nil
}

// carryForwardChangeLog copies the prior run's change-log.jsonl forward, or creates an empty one.
func carryForwardChangeLog(clPath, clPrev string) {
	if data, err := readFileRooted(clPrev); err == nil {
		if err := os.WriteFile(clPath, data, 0o600); err != nil {
			slog.Warn("failed to write change-log", "err", err)
		}
	} else {
		if err := os.WriteFile(clPath, []byte(""), 0o600); err != nil {
			slog.Warn("failed to create change-log", "err", err)
		}
	}
}

// appendEvidenceChangeLog appends change-log entries for evidence added/superseded this run,
// returning the last change ID written and the total number of changes.
//

func appendEvidenceChangeLog(clPath, recDir, ts string, evidenceSummary map[string]any) (string, int) {
	var lastChangeID string
	recRoot, recErr := os.OpenRoot(recDir)
	if recErr != nil {
		slog.Warn("failed to open records dir for change log", "dir", recDir, "err", recErr)
	}
	if recRoot != nil {
		defer recRoot.Close()
	}
	appendEvidenceChanges := func(ids []string, operation string) {
		for _, rid := range ids {
			filename := strings.Replace(rid, "sha256:", "", 1) + ".json"
			contentHash := ""
			if recRoot != nil {
				if data, rErr := rootReadAll(recRoot, filename); rErr == nil {
					var rec map[string]any
					if json.Unmarshal(data, &rec) == nil {
						contentHash = getStr(rec, colContentHash)
					}
				}
			}
			if contentHash == "" {
				contentHash = ContentHash(rid)
			}
			changeID := ChangeID(ts, rid, operation)
			entry := map[string]any{
				"change_id":    changeID,
				"timestamp":    ts,
				"operation":    operation,
				"entity_type":  "evidence",
				"entity_id":    rid,
				"paths":        []string{filepath.Join("evidence", "records", filename)},
				colContentHash: contentHash,
				"summary":      operation + " evidence record " + rid,
			}
			if err := AppendJSONL(clPath, entry); err == nil {
				lastChangeID = changeID
			}
		}
	}
	var added []string
	if v, ok := evidenceSummary["added"].([]string); ok {
		added = v
	}
	var superseded []string
	if v, ok := evidenceSummary["superseded"].([]string); ok {
		superseded = v
	}
	appendEvidenceChanges(added, "added")
	appendEvidenceChanges(superseded, "superseded")
	return lastChangeID, len(added) + len(superseded)
}

// buildManifestFilesMeta computes path/hash/size metadata for each file in the manifest walk.
func buildManifestFilesMeta(tFiles map[string]string) []map[string]any {
	var filesMeta []map[string]any
	for rel, full := range tFiles {
		if rel == "datasets/change-log.jsonl" {
			continue
		}
		hash, err := FileSHA256Bytes(full)
		if err != nil {
			slog.Warn("failed to hash file for manifest", "path", rel, "err", err)
			continue
		}
		info, err := os.Stat(full)
		if err != nil {
			slog.Warn("failed to stat file for manifest", "path", rel, "err", err)
			continue
		}
		filesMeta = append(filesMeta, map[string]any{
			"path":   rel,
			"sha256": hash,
			"bytes":  info.Size(),
		})
	}
	return filesMeta
}

// countCurrentEvidence counts evidence records with status "current" in recDir.
func countCurrentEvidence(recDir string) int {
	currentEvidence := 0
	root, err := os.OpenRoot(recDir)
	if err != nil {
		return currentEvidence
	}
	defer root.Close()
	entries, err := os.ReadDir(recDir)
	if err != nil {
		return currentEvidence
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := rootReadAll(root, e.Name())
		if err != nil {
			continue
		}
		var rec map[string]any
		if json.Unmarshal(data, &rec) == nil && rec[colStatus] == colCurrent {
			currentEvidence++
		}
	}
	return currentEvidence
}

// buildManifestCounts assembles the manifest's dataset counts.
func buildManifestCounts(targetDir string, currentEvidence, teamStateCount int) map[string]any {
	return map[string]any{
		"evidence":           currentEvidence,
		"player_signal":      countJSONLFile(targetDir, "datasets/player-signals.jsonl"),
		"valuation":          countJSONLFile(targetDir, "datasets/valuations.jsonl"),
		"news_event":         countJSONLFile(targetDir, "datasets/news-events.jsonl"),
		"league_transaction": countJSONLFile(targetDir, "datasets/league-transactions.jsonl"),
		"team_state":         teamStateCount,
		"change":             countJSONLFile(targetDir, "datasets/change-log.jsonl"),
	}
}

// buildManifestLeagueList builds the manifest's league summary list.
func buildManifestLeagueList() []map[string]any {
	leagueList := make([]map[string]any, 0, len(models.LeagueFormats))
	for lid, lf := range models.LeagueFormats {
		leagueList = append(leagueList, map[string]any{colLeagueID: lid, colName: lf.Name})
	}
	return leagueList
}

func loadCurrentRecords(recordsDir string) []map[string]any {
	var result []map[string]any
	root, err := os.OpenRoot(recordsDir)
	if err != nil {
		return result
	}
	defer root.Close()
	entries, err := os.ReadDir(recordsDir)
	if err != nil {
		return result
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := rootReadAll(root, e.Name())
		if err != nil {
			continue
		}
		var rec map[string]any
		if json.Unmarshal(data, &rec) == nil && rec["status"] == "current" {
			result = append(result, rec)
		}
	}
	return result
}

func filterRecent(recs []map[string]any, hours int) []map[string]any {
	var result []map[string]any
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

func fmtRec(rec map[string]any) string {
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
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if base == fileGitignore || base == "corpus-manifest.json" || base == ".publish.log" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("relativize path %s: %w", path, err)
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
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", root, err)
	}
	return files, nil
}

func countJSONLFile(targetDir, rel string) int {
	data, err := readFileRooted(filepath.Join(targetDir, rel))
	if err != nil {
		return 0
	}
	count := 0
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

// appendRookieDraftLines builds lines for rookie draft board by querying player rankings.
func (p *Publisher) appendRookieDraftLines(rows interface {
	Close()
	Next() bool
	Scan(dest ...any) error
}, lines *[]string,
) {
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
		pos := derefStr(position, "?")
		name := derefStr(fullName, "")
		team := derefStr(teamAbbr, "")
		ageStr := derefInt(age)
		tvStr := derefInt(tradeValue)
		orStr := derefInt(overallRank)
		prStr := derefInt(positionRank)
		byPos[pos] = append(byPos[pos], fmt.Sprintf("| %s | %s | %s | %s | %s | %s |", name, team, ageStr, tvStr, orStr, prStr))
	}
	for _, pos := range []string{"QB", "RB", "WR", "TE"} {
		if posRows, ok := byPos[pos]; ok && len(posRows) > 0 {
			*lines = append(*lines, "## "+pos, "",
				"| Player | NFL | Age | Value | OVR | PosRank |", "|---|---|---|---|---|---|")
			*lines = append(*lines, posRows...)
		}
	}
}

// derefStr dereferences a pointer to string or returns default.
func derefStr(s *string, def string) string {
	if s != nil {
		return *s
	}
	return def
}

// derefInt dereferences a pointer to int and converts to string, or returns empty string.
func derefInt(i *int) string {
	if i != nil {
		return strconv.Itoa(*i)
	}
	return ""
}

// renderLeaguemateProfiles queries and renders leaguemate signal profiles.
func (p *Publisher) renderLeaguemateProfiles(ctx context.Context, dsDir string, snap *time.Time, gen string, lines *[]string) int {
	rows, err := p.common.pool.Query(ctx, `
		SELECT user_id, COALESCE(username, ''), COALESCE(display_name, ''),
		       leagues_count, win_pct, contender_score, trade_count, trade_count_30d,
		       net_firsts, dossier
		FROM leaguemate_signal WHERE snapshot_date = $1
		ORDER BY contender_score DESC NULLS LAST
	`, *snap)
	if err != nil {
		slog.Warn("failed to query leaguemate signals", "err", err)
		return 0
	}
	defer rows.Close()
	jpath := filepath.Join(dsDir, "leaguemate-profiles.jsonl")
	if err := os.WriteFile(jpath, []byte(""), 0o600); err != nil {
		slog.Warn("failed to create leaguemate profiles file", "err", err)
	}
	profiles := 0
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
		*lines = append(*lines, "## "+name, "")
		dossierStr := ""
		if dossier != nil {
			dossierStr = strings.TrimSpace(*dossier)
		}
		if dossierStr == "" {
			dossierStr = "_No dossier generated._"
		}
		*lines = append(*lines, dossierStr, "")
		appendJSONLFile(jpath, map[string]any{
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
			colGeneratedAt:    gen,
		})
		profiles++
	}
	return profiles
}
