package corpus

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/markis/fantasy-football-engine/internal/models"
)

const (
	evidenceWindowDays = 14
	evidenceMaxRecords = 600
)

// renderEvidence renders evidence/ from decision-relevant news items.
func (p *Publisher) renderEvidence(ctx context.Context, targetDir, prevDir string) (map[string]any, error) {
	recDir := filepath.Join(targetDir, "evidence", "records")
	if err := os.MkdirAll(recDir, 0o750); err != nil {
		return nil, err
	}

	// Build watch set
	watchIDs, nameIndex, ownership := p.buildWatchSet(ctx)

	// Query relevant items
	items := p.queryRelevantItems(ctx, watchIDs, nameIndex)

	// Build current records
	current := make(map[string]map[string]any)
	for _, item := range items {
		rec := p.buildEvidenceRecord(ctx, item, ownership)
		current[rec["id"].(string)] = rec //nolint:errcheck // buildEvidenceRecord guarantees "id" key exists and is string
	}

	// Load previous records
	prevRecords := loadExistingRecords(filepath.Join(prevDir, "evidence", "records"))

	// Write current records
	var added, superseded []string
	for rid, rec := range current {
		filename := strings.Replace(rid, "sha256:", "", 1) + ".json"
		if err := WriteJSON(filepath.Join(recDir, filename), rec); err != nil {
			slog.Warn("write evidence record", "err", err)
		}
		if _, ok := prevRecords[rid]; !ok {
			added = append(added, rid)
		}
	}

	// Preserve prior records as superseded
	for rid, prev := range prevRecords {
		if _, ok := current[rid]; ok {
			continue
		}
		prev["status"] = "superseded"
		filename := strings.Replace(rid, "sha256:", "", 1) + ".json"
		if err := WriteJSON(filepath.Join(recDir, filename), prev); err != nil {
			return nil, err
		}
		superseded = append(superseded, rid)
	}

	// Write index.md
	var lines []string
	lines = append(lines, "# Evidence Index", "",
		fmt.Sprintf("_Current records: %d · superseded preserved: %d · window: last %d days. Generated %s._",
			len(current), len(superseded), evidenceWindowDays, p.common.NowISO()), "",
		"| Published | Topic | Title | Players | Source |", "|---|---|---|---|---|")
	// Sort by published_at desc
	var sortedRecs []map[string]any
	for _, rec := range current {
		sortedRecs = append(sortedRecs, rec)
	}
	// Sort by published_at, newest first.
	sort.SliceStable(sortedRecs, func(i, j int) bool {
		return getStr(sortedRecs[i], "published_at") > getStr(sortedRecs[j], "published_at")
	})
	for _, rec := range sortedRecs {
		pub := getStr(rec, "published_at")
		if len(pub) > 10 {
			pub = pub[:10]
		}
		players := ""
		if pids, ok := rec["player_ids"].([]string); ok {
			var playersSb89 strings.Builder
			for i, pid := range pids {
				if i >= 4 {
					break
				}
				if i > 0 {
					playersSb89.WriteString(", ")
				}
				playersSb89.WriteString(strings.TrimPrefix(pid, "nfl:"))
			}
			players += playersSb89.String()
		}
		title := getStr(rec, "title")
		if len(title) > 60 {
			title = title[:60]
		}
		title = strings.ReplaceAll(title, "|", "/")
		lines = append(lines, fmt.Sprintf("| %s | %s | %s | %s | %s |",
			pub, getStr(rec, "topic"), title, players, getStr(rec, "publisher")))
	}
	if writeErr := WriteText(filepath.Join(targetDir, "evidence", "index.md"), strings.Join(lines, "\n")); writeErr != nil {
		return nil, writeErr
	}

	return map[string]any{
		"current_count":    len(current),
		"superseded_count": len(superseded),
		"added":            added,
		"superseded":       superseded,
	}, nil
}

func (p *Publisher) buildWatchSet(ctx context.Context) (watchIDs []string, nameIndex map[string][]string, ownership map[string][][2]string) {
	watchIDsSet := make(map[string]bool)
	ownership = make(map[string][][2]string)

	for leagueID, lf := range models.LeagueFormats {
		rosters, err := p.common.LeagueRosters(ctx, leagueID)
		if err != nil {
			slog.Warn("get rosters for watch set", "league", leagueID, "err", err)
			continue
		}
		myRoster := p.common.MyRoster(rosters)
		myPlayers := make(map[string]bool)
		if myRoster != nil {
			for _, pid := range toStringSlice(myRoster["players"]) {
				myPlayers[pid] = true
			}
		}
		for _, pid := range p.common.AllRosterPlayerIDs(rosters) {
			watchIDsSet[pid] = true
			role := "rival"
			if myPlayers[pid] {
				role = "owned"
			}
			ownership[pid] = append(ownership[pid], [2]string{lf.Name, role})
		}
	}

	ids := make([]string, 0, len(watchIDsSet))
	for id := range watchIDsSet {
		ids = append(ids, id)
	}
	playerRows := p.common.PlayerRows(ctx, ids)
	nameIndex = BuildNameIndex(playerRows)
	watchIDs = ids
	return
}

func (p *Publisher) queryRelevantItems(ctx context.Context, _ []string, nameIndex map[string][]string) []map[string]any {
	cutoff := time.Now().UTC().AddDate(0, 0, -evidenceWindowDays)
	rows, err := p.common.pool.Query(ctx, `
		SELECT id, canonical_url, url, title, summary_short, content_hash,
		       entities, topics, author, published_at, fetched_at, updated_at, news_story
		FROM news_item
		WHERE is_relevant = true AND COALESCE(is_news, true)
		  AND quality_score >= 0
		  AND published_at >= $1
		ORDER BY published_at DESC
		LIMIT $2
	`, cutoff, evidenceMaxRecords*2)
	if err != nil {
		slog.Warn("query relevant items", "err", err)
		return nil
	}
	defer rows.Close()

	cols := []string{
		"id", "canonical_url", "url", "title", "summary_short", "content_hash",
		"entities", "topics", "author", "published_at", "fetched_at", "updated_at", "news_story",
	}
	var result []map[string]any
	seenURLs := make(map[string]bool)
	for rows.Next() {
		var id, canonicalURL, url, contentHash any
		var title, summaryShort, author, newsStory *string
		var entities, topics []string
		var publishedAt, fetchedAt, updatedAt *time.Time
		if err := rows.Scan(&id, &canonicalURL, &url, &title, &summaryShort, &contentHash,
			&entities, &topics, &author, &publishedAt, &fetchedAt, &updatedAt, &newsStory); err != nil {
			continue
		}
		d := map[string]any{
			"id":            id,
			"canonical_url": canonicalURL,
			"url":           url,
			"title":         title,
			"summary_short": summaryShort,
			"content_hash":  contentHash,
			"entities":      entities,
			"topics":        topics,
			"author":        author,
			"published_at":  publishedAt,
			"fetched_at":    fetchedAt,
			"updated_at":    updatedAt,
			"news_story":    newsStory,
		}
		_ = cols
		hits := MatchEntitiesToPlayers(entities, nameIndex)
		if len(hits) == 0 {
			continue
		}
		urlStr := fmt.Sprint(canonicalURL)
		if urlStr == "<nil>" || urlStr == "" {
			urlStr = fmt.Sprint(url)
		}
		if urlStr == "<nil>" || urlStr == "" {
			continue
		}
		if seenURLs[urlStr] {
			continue
		}
		seenURLs[urlStr] = true
		d["matched_players"] = hits
		result = append(result, d)
		if len(result) >= evidenceMaxRecords {
			break
		}
	}
	return result
}

func (p *Publisher) buildEvidenceRecord(ctx context.Context, item map[string]any, ownership map[string][][2]string) map[string]any {
	urlStr := fmt.Sprint(item["canonical_url"])
	if urlStr == "<nil>" || urlStr == "" {
		urlStr = fmt.Sprint(item["url"])
	}
	summary := ""
	if v, ok := item["news_story"].(*string); ok && v != nil {
		summary = *v
	}
	if summary == "" {
		if v, ok := item["summary_short"].(*string); ok && v != nil {
			summary = *v
		}
	}
	if summary == "" {
		if v, ok := item["title"].(*string); ok && v != nil {
			summary = *v
		}
	}
	if summary == "" {
		summary = urlStr
	}
	if len(summary) > 1200 {
		summary = summary[:1200]
	}

	chStr := fmt.Sprint(item["content_hash"])
	if chStr == "<nil>" || chStr == "" {
		chStr = ContentHash(summary)
	}

	recID := EvidenceID(urlStr, chStr)

	matchedPlayers, _ := item["matched_players"].([]string) //nolint:errcheck // External data may not have field
	playerIDs := make([]string, 0, len(matchedPlayers))
	for _, sid := range matchedPlayers {
		playerIDs = append(playerIDs, "nfl:"+sid)
	}

	// Team IDs: entities store full team names (e.g. "Philadelphia Eagles"),
	// so resolve each against the canonical name->abbreviation table.
	entities, _ := item["entities"].([]string) //nolint:errcheck // External data may not have field
	teamIDs := make([]string, 0, len(entities))
	for _, e := range entities {
		if abbr, ok := models.TeamAbbrForName(e); ok {
			teamIDs = append(teamIDs, "nfl:"+abbr)
		}
	}

	publisher := ""
	if v, ok := item["author"].(*string); ok && v != nil {
		publisher = *v
	}
	if publisher == "" {
		publisher = "unknown"
	}

	titleStr := ""
	if v, ok := item["title"].(*string); ok && v != nil {
		titleStr = *v
	}
	if titleStr == "" {
		titleStr = urlStr
	}

	topics := item["topics"].([]string) //nolint:errcheck // External data may not have field
	topic := TopicFromTopics(topics)

	var publishedAt any
	if v, ok := item["published_at"].(*time.Time); ok && v != nil {
		publishedAt = v.UTC().Format("2006-01-02T15:04:05Z")
	}
	fetchedAt := ""
	if v, ok := item["fetched_at"].(*time.Time); ok && v != nil {
		fetchedAt = v.UTC().Format("2006-01-02T15:04:05Z")
	}
	if fetchedAt == "" {
		fetchedAt = p.common.NowISO()
	}
	var updatedAt any
	if v, ok := item["updated_at"].(*time.Time); ok && v != nil {
		updatedAt = v.UTC().Format("2006-01-02T15:04:05Z")
	}

	// Facts
	claims := p.factsForItem(ctx, item["id"])

	// Owner reasons
	var ownerReasons []string
	for _, sid := range matchedPlayers {
		for _, pair := range ownership[sid] {
			ownerReasons = append(ownerReasons, fmt.Sprintf("%s in %s", pair[1], pair[0]))
		}
	}
	reason := "decision-relevant"
	if len(ownerReasons) > 0 {
		// Dedupe
		seen := make(map[string]bool)
		var unique []string
		for _, r := range ownerReasons {
			if !seen[r] {
				seen[r] = true
				unique = append(unique, r)
			}
		}
		reason = "Mentions " + strings.Join(unique, ", ")
	}

	relevantToRoster := false
	relevantToTradeTarget := false
	for _, sid := range matchedPlayers {
		for _, pair := range ownership[sid] {
			if pair[1] == "owned" {
				relevantToRoster = true
			}
			if pair[1] == "rival" {
				relevantToTradeTarget = true
			}
		}
	}

	return map[string]any{
		"id":            recID,
		"canonical_url": urlStr,
		"title":         titleStr,
		"player_ids":    playerIDs,
		"team_ids":      teamIDs,
		"topic":         topic,
		"publisher":     publisher,
		"source_type":   "secondary",
		"published_at":  publishedAt,
		"retrieved_at":  fetchedAt,
		"updated_at":    updatedAt,
		"summary":       summary,
		"claims":        claims,
		"decision_relevance": map[string]any{
			"relevant_to_roster":       relevantToRoster,
			"relevant_to_trade_target": relevantToTradeTarget,
			"relevant_to_pick_value":   false,
			"reason":                   reason,
		},
		"status":       "current",
		"content_hash": ContentHash(summary),
		"supersedes":   []any{},
	}
}

func (p *Publisher) factsForItem(ctx context.Context, itemID any) []map[string]any {
	claims := make([]map[string]any, 0)
	rows, err := p.common.pool.Query(ctx,
		"SELECT fact_text, confidence FROM fact WHERE news_item_id = $1 ORDER BY occurred_at DESC",
		itemID)
	if err != nil {
		return claims
	}
	defer rows.Close()
	for rows.Next() {
		var text string
		var conf *string
		if err := rows.Scan(&text, &conf); err != nil {
			continue
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		confStr := "medium"
		if conf != nil && (*conf == "high" || *conf == "medium" || *conf == "low") {
			confStr = *conf
		}
		claims = append(claims, map[string]any{
			"id":         ClaimID(text),
			"text":       text,
			"confidence": confStr,
		})
	}
	return claims
}

func loadExistingRecords(recordsDir string) map[string]map[string]any {
	result := make(map[string]map[string]any)
	entries, err := os.ReadDir(recordsDir)
	if err != nil {
		return result
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(recordsDir, entry.Name()))
		if err != nil {
			continue
		}
		var rec map[string]any
		if json.Unmarshal(data, &rec) == nil {
			if id, ok := rec["id"].(string); ok {
				result[id] = rec
			}
		}
	}
	return result
}

func getStr(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	// fmt.Sprint prints the hex address for a non-nil *string/*int rather
	// than the pointed-to value, so those need an explicit dereference.
	switch p := v.(type) {
	case *string:
		if p == nil {
			return ""
		}
		return *p
	case *int:
		if p == nil {
			return ""
		}
		return strconv.Itoa(*p)
	}
	s := fmt.Sprint(v)
	if s == "<nil>" {
		return ""
	}
	return s
}
