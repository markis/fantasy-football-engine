package query

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"

	"ff-engine/internal/db"
	"ff-engine/internal/embed"
	"ff-engine/internal/models"
	"ff-engine/internal/sleeper"
	"ff-engine/internal/util"
)

const (
	colPlayerID       = "player_id"
	colFullName       = "full_name"
	colName           = "name"
	colPosition       = "position"
	colTeam           = "team"
	colLeagueID       = "league_id"
	colUserID         = "user_id"
	colLeagues        = "leagues"
	colPlayer         = "player"
	colTitle          = "title"
	colURL            = "url"
	colSimilarity     = "similarity"
	colSource         = "source"
	colRelevantFilter = " AND ni.is_relevant = true"
	srcFantasyCalc    = "FantasyCalc"
	statusFound       = "found"

	// Ranking column qualifiers used in SQL fragments.
	rankColTradeValue = "r.trade_value"
	rankColOverall    = "r.overall_rank"
	rankColPosRank    = "r.position_rank"
)

var errRosterNotFound = errors.New("roster not found")

// Service provides query implementations backing the MCP tools.
type Service struct {
	pool    *db.Pool
	embed   *embed.Client
	sleeper *sleeper.Client
}

// New creates a new query service.
func New(pool *db.Pool, embedClient *embed.Client, sleeperClient *sleeper.Client) *Service {
	return &Service{pool: pool, embed: embedClient, sleeper: sleeperClient}
}

// SearchNews performs semantic search over news_chunk embeddings, grouping
// results by parent news_item so an article appears once even if multiple
// chunks match. The highest-similarity chunk is used as the article's score.
func (s *Service) SearchNews(ctx context.Context, query string, limit int, days *int, relevantOnly bool) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 10
	}
	// Embed the query
	vec, err := s.embed.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	v := pgvector.NewVector(vec)

	// Build query — search chunk embeddings, dedupe by news_item.
	sql := `SELECT * FROM (
		       SELECT DISTINCT ON (ni.id)
			       ni.id, ni.title, ni.url, ni.published_at, s.name AS source_name,
			       1 - (nc.embedding <=> $1::vector) AS similarity,
			       ni.news_story
			FROM news_chunk nc
			JOIN news_item ni ON ni.id = nc.news_item_id
			JOIN source s ON s.id = ni.source_id
			WHERE nc.embedding IS NOT NULL AND COALESCE(ni.quality_score, 0) >= 0`
	params := []any{v}
	if days != nil {
		d := max(*days, 0)
		params = append(params, d)
		sql += fmt.Sprintf(" AND ni.published_at >= now() - make_interval(days => $%d)", len(params))
	}
	if relevantOnly {
		sql += colRelevantFilter
	}
	sql += " ORDER BY ni.id, 1 - (nc.embedding <=> $1::vector) DESC"
	sql += ") AS ranked ORDER BY similarity DESC"
	params = append(params, limit)
	sql += fmt.Sprintf(" LIMIT $%d", len(params))

	rows, err := s.pool.Query(ctx, sql, params...)
	if err != nil {
		return nil, fmt.Errorf("query similar news items: %w", err)
	}
	defer rows.Close()

	var result []map[string]any
	for rows.Next() {
		var id uuid.UUID
		var title, url, sourceName, newsStory *string
		var publishedAt *time.Time
		var similarity float64
		if err := rows.Scan(&id, &title, &url, &publishedAt, &sourceName, &similarity, &newsStory); err != nil {
			continue
		}
		item := map[string]any{
			"id":          id.String(),
			colTitle:      util.StrOrEmpty(title),
			colURL:        util.StrOrEmpty(url),
			colSimilarity: similarity,
			colSource:     util.StrOrEmpty(sourceName),
		}
		if publishedAt != nil {
			item["published_at"] = publishedAt.UTC().Format(time.RFC3339)
		}
		if newsStory != nil && *newsStory != "" {
			item["story"] = *newsStory
		}
		result = append(result, item)
	}
	return result, nil
}

// GetStories returns top story clusters by time window.
func (s *Service) GetStories(ctx context.Context, hours, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 5
	}
	if hours <= 0 {
		hours = 24
	}
	rows, err := s.pool.Query(ctx, `
		SELECT sc.id, sc.representative_title, sc.importance_score,
		       sc.first_seen_at, sc.last_seen_at,
		       count(ni.id) AS item_count
		FROM story_cluster sc
		JOIN news_item ni ON ni.cluster_id = sc.id
		WHERE ni.published_at >= now() - make_interval(hours => $1)
		  AND ni.is_relevant = true AND ni.is_news = true
		GROUP BY sc.id, sc.representative_title, sc.importance_score,
		         sc.first_seen_at, sc.last_seen_at
		ORDER BY sc.importance_score DESC NULLS LAST, count(ni.id) DESC
		LIMIT $2
	`, hours, limit)
	if err != nil {
		return nil, fmt.Errorf("query stories: %w", err)
	}
	defer rows.Close()

	var result []map[string]any
	for rows.Next() {
		var id uuid.UUID
		var repTitle *string
		var importance *float64
		var firstSeen, lastSeen time.Time
		var itemCount int
		if err := rows.Scan(&id, &repTitle, &importance, &firstSeen, &lastSeen, &itemCount); err != nil {
			continue
		}
		item := map[string]any{
			"id":            id.String(),
			"title":         util.StrOrEmpty(repTitle),
			"item_count":    itemCount,
			"first_seen_at": firstSeen.UTC().Format(time.RFC3339),
			"last_seen_at":  lastSeen.UTC().Format(time.RFC3339),
		}
		if importance != nil {
			item["importance_score"] = *importance
		}
		result = append(result, item)
	}
	return result, nil
}

// SearchFacts performs semantic search over extracted facts.
func (s *Service) SearchFacts(ctx context.Context, query string, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 10
	}
	vec, err := s.embed.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	v := pgvector.NewVector(vec)

	rows, err := s.pool.Query(ctx, `
		SELECT f.id, f.fact_text, f.confidence, f.occurred_at,
		       1 - (f.embedding <=> $1::vector) AS similarity
		FROM fact f
		WHERE f.embedding IS NOT NULL
		ORDER BY f.embedding <=> $1::vector
		LIMIT $2
	`, v, limit)
	if err != nil {
		return nil, fmt.Errorf("query similar facts: %w", err)
	}
	defer rows.Close()

	var result []map[string]any
	for rows.Next() {
		var id uuid.UUID
		var factText string
		var confidence *string
		var occurredAt time.Time
		var similarity float64
		if err := rows.Scan(&id, &factText, &confidence, &occurredAt, &similarity); err != nil {
			continue
		}
		item := map[string]any{
			"id":          id.String(),
			"fact_text":   factText,
			"similarity":  similarity,
			"occurred_at": occurredAt.UTC().Format(time.RFC3339),
		}
		if confidence != nil {
			item["confidence"] = *confidence
		}
		result = append(result, item)
	}
	return result, nil
}

// GetRecentNews returns the latest N news items.
func (s *Service) GetRecentNews(ctx context.Context, limit int, relevantOnly bool) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 10
	}
	sql := `SELECT ni.id, ni.title, ni.url, ni.published_at, ni.news_story,
		       s.name AS source_name, ni.is_relevant
		FROM news_item ni JOIN source s ON s.id = ni.source_id`
	sql += " WHERE ni.is_news = true AND COALESCE(ni.quality_score, 0) >= 0"
	if relevantOnly {
		sql += " AND ni.is_relevant = true"
	}
	sql += " ORDER BY ni.published_at DESC NULLS LAST LIMIT $1"
	rows, err := s.pool.Query(ctx, sql, limit)
	if err != nil {
		return nil, fmt.Errorf("query recent news: %w", err)
	}
	defer rows.Close()

	var result []map[string]any
	for rows.Next() {
		var id uuid.UUID
		var title, url, newsStory, sourceName *string
		var publishedAt *time.Time
		var isRelevant bool
		if err := rows.Scan(&id, &title, &url, &publishedAt, &newsStory, &sourceName, &isRelevant); err != nil {
			continue
		}
		item := map[string]any{
			"id":          id.String(),
			"title":       util.StrOrEmpty(title),
			"url":         util.StrOrEmpty(url),
			"source":      util.StrOrEmpty(sourceName),
			"is_relevant": isRelevant,
		}
		if publishedAt != nil {
			item["published_at"] = publishedAt.UTC().Format(time.RFC3339)
		}
		if newsStory != nil && *newsStory != "" {
			item["story"] = *newsStory
		}
		result = append(result, item)
	}
	return result, nil
}

// SearchPlayers searches the player table by name.
func (s *Service) SearchPlayers(ctx context.Context, query string, position *string, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 25
	}
	q := "%" + strings.ToLower(query) + "%"
	sql := `SELECT sleeper_player_id, full_name, position, team, team_abbr,
		       age, status, active, injury_status, depth_chart_position
		FROM player WHERE lower(full_name) LIKE $1 OR lower(search_full_name) LIKE $1
		       OR lower(last_name) LIKE $1`
	params := []any{q}
	if position != nil && *position != "" {
		sql += " AND position = $2"
		params = append(params, *position)
	}
	sql += fmt.Sprintf(" LIMIT %d", limit)
	rows, err := s.pool.Query(ctx, sql, params...)
	if err != nil {
		return nil, fmt.Errorf("query players: %w", err)
	}
	defer rows.Close()

	var result []map[string]any
	for rows.Next() {
		var sleeperID string
		var fullName, pos, team, teamAbbr, status, injStatus, dcPos *string
		var age *int
		var active bool
		if err := rows.Scan(&sleeperID, &fullName, &pos, &team, &teamAbbr, &age, &status, &active, &injStatus, &dcPos); err != nil {
			continue
		}
		item := map[string]any{
			colPlayerID: sleeperID,
			colFullName: util.StrOrEmpty(fullName),
			colPosition: util.StrOrEmpty(pos),
			colTeam:     util.StrOrEmpty(teamAbbr),
			"active":    active,
		}
		if age != nil {
			item["age"] = *age
		}
		if injStatus != nil {
			item["injury_status"] = *injStatus
		}
		result = append(result, item)
	}
	return result, nil
}

// GetPlayer returns full player profile.
func (s *Service) GetPlayer(ctx context.Context, playerID string) (map[string]any, error) {
	var sleeperID, fullName, pos, team, teamAbbr, status, injStatus, injBodyPart, injNotes *string
	var age, yearsExp *int
	var active bool
	err := s.pool.QueryRow(ctx, `
		SELECT sleeper_player_id, full_name, position, team, team_abbr, status,
		       active, age, years_exp, injury_status, injury_body_part, injury_notes
		FROM player WHERE sleeper_player_id = $1
	`, playerID).Scan(&sleeperID, &fullName, &pos, &team, &teamAbbr, &status,
		&active, &age, &yearsExp, &injStatus, &injBodyPart, &injNotes)
	if err != nil {
		return nil, fmt.Errorf("query player %s: %w", playerID, err)
	}
	item := map[string]any{
		colPlayerID: playerID,
		colFullName: util.StrOrEmpty(fullName),
		colPosition: util.StrOrEmpty(pos),
		colTeam:     util.StrOrEmpty(teamAbbr),
		"active":    active,
	}
	if age != nil {
		item["age"] = *age
	}
	if injStatus != nil {
		item["injury_status"] = *injStatus
	}
	if injBodyPart != nil {
		item["injury_body_part"] = *injBodyPart
	}
	return item, nil
}

// GetRankings returns dynasty trade values.
func (s *Service) GetRankings(
	ctx context.Context,
	position *string,
	limit int,
	source string,
	market int,
	superflex bool,
) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 15
	}
	if source == "" {
		source = srcFantasyCalc
	}

	valueCol, overallCol, posRankCol := rankingColumns(source, superflex)

	sql := fmt.Sprintf(`
		SELECT p.sleeper_player_id, p.full_name, p.position, r.team,
		       %s, %s, %s
		FROM player p JOIN player_ranking r ON r.player_id = p.id
		WHERE r.source = $1 AND r.market = $2
	`, valueCol, overallCol, posRankCol)
	params := []any{source, market}
	if position != nil && *position != "" {
		sql += " AND p.position = $3"
		params = append(params, *position)
	}
	sql += fmt.Sprintf(" ORDER BY %s DESC NULLS LAST LIMIT %d", valueCol, limit)

	rows, err := s.pool.Query(ctx, sql, params...)
	if err != nil {
		return nil, fmt.Errorf("query rankings: %w", err)
	}
	defer rows.Close()

	result := []map[string]any{}
	for rows.Next() {
		var sleeperID string
		var fullName, pos, team *string
		var tradeValue, overallRank, posRank *int
		if err := rows.Scan(&sleeperID, &fullName, &pos, &team, &tradeValue, &overallRank, &posRank); err != nil {
			continue
		}
		item := map[string]any{
			colPlayerID: sleeperID,
			colFullName: util.StrOrEmpty(fullName),
			colPosition: util.StrOrEmpty(pos),
			colTeam:     util.StrOrEmpty(team),
		}
		if tradeValue != nil {
			item["trade_value"] = *tradeValue
		}
		if overallRank != nil {
			item["overall_rank"] = *overallRank
		}
		if posRank != nil {
			item["position_rank"] = *posRank
		}
		result = append(result, item)
	}
	return result, nil
}

// GetNFLState returns the current NFL state.
func (s *Service) GetNFLState(ctx context.Context) (*models.NFLState, error) {
	return s.sleeper.GetNFLState(ctx)
}

// GetStudyMaterial returns a digest for agent self-study.
func (s *Service) GetStudyMaterial(ctx context.Context, hours int) (map[string]any, error) {
	if hours <= 0 {
		hours = 24
	}
	stories, err := s.GetStories(ctx, hours, 10)
	if err != nil {
		return nil, err
	}
	recentNews, err := s.GetRecentNews(ctx, 20, true)
	if err != nil {
		return nil, err
	}

	var md strings.Builder
	md.WriteString("# Self-Study Material\n\n")
	fmt.Fprintf(&md, "_Generated for the last %d hours._\n\n", hours)

	md.WriteString("## Top Stories\n\n")
	for _, story := range stories {
		fmt.Fprintf(&md, "### %s\n", story["title"])
		fmt.Fprintf(&md, "- Items: %v | Importance: %v\n\n",
			story["item_count"], story["importance_score"])
	}

	md.WriteString("## Recent News\n\n")
	for _, news := range recentNews {
		fmt.Fprintf(&md, "- **%s** (%s)\n", news["title"], news["source"])
		if story, ok := news["story"].(string); ok && story != "" {
			fmt.Fprintf(&md, "  > %s\n", story)
		}
	}

	return map[string]any{
		"digest":      md.String(),
		"stories":     stories,
		"recent_news": recentNews,
	}, nil
}

// GetTrendingPlayers returns trending players from Sleeper.
func (s *Service) GetTrendingPlayers(ctx context.Context, trendType string, limit int) ([]sleeper.TrendingPlayer, error) {
	if limit <= 0 {
		limit = 25
	}
	if trendType == "" {
		trendType = "add"
	}
	return s.sleeper.GetTrendingPlayers(ctx, trendType, 24, limit)
}

// GetFreeAgents returns top ranked free agents in a league.
func (s *Service) GetFreeAgents(
	ctx context.Context,
	leagueID string,
	position *string,
	limit int,
	superflex bool,
) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 15
	}
	// Get rosters
	rosters, err := s.sleeper.GetLeagueRosters(ctx, leagueID)
	if err != nil {
		return nil, err
	}
	// Build owned set
	owned := make(map[string]bool)
	for i := range rosters {
		r := &rosters[i]
		for _, p := range r.Players {
			owned[p] = true
		}
		for _, p := range r.Taxi {
			owned[p] = true
		}
		for _, p := range r.Reserve {
			owned[p] = true
		}
	}

	valueCol := tradeValueColumn("Dynasty Daddy", superflex)

	sql := fmt.Sprintf(`
		SELECT p.sleeper_player_id, p.full_name, p.position, p.team_abbr, %s
		FROM player p JOIN player_ranking r ON r.player_id = p.id
		WHERE r.source = 'Dynasty Daddy' AND r.market = 14
	`, valueCol)
	params := []any{}
	if position != nil && *position != "" {
		sql += " AND p.position = $1"
		params = append(params, *position)
	}
	sql += fmt.Sprintf(" ORDER BY %s DESC NULLS LAST LIMIT 200", valueCol)

	rows, err := s.pool.Query(ctx, sql, params...)
	if err != nil {
		return nil, fmt.Errorf("query dynasty values: %w", err)
	}
	defer rows.Close()

	var result []map[string]any
	count := 0
	for rows.Next() {
		var sleeperID string
		var fullName, pos, teamAbbr *string
		var tradeValue *int
		if err := rows.Scan(&sleeperID, &fullName, &pos, &teamAbbr, &tradeValue); err != nil {
			continue
		}
		if owned[sleeperID] {
			continue
		}
		item := map[string]any{
			colPlayerID: sleeperID,
			colFullName: util.StrOrEmpty(fullName),
			colPosition: util.StrOrEmpty(pos),
			colTeam:     util.StrOrEmpty(teamAbbr),
		}
		if tradeValue != nil {
			item["trade_value"] = *tradeValue
		}
		result = append(result, item)
		count++
		if count >= limit {
			break
		}
	}
	return result, nil
}

// EvaluateTrade returns structured data for a trade proposal.
func (s *Service) EvaluateTrade(
	ctx context.Context,
	giveNames, getNames []string,
	leagueID string,
	superflex bool,
) (map[string]any, error) {
	lf, hasLF := models.LeagueFormats[leagueID]
	source := "Dynasty Daddy"
	market := 14
	if hasLF && lf.Market != nil {
		source = srcFantasyCalc
		market = *lf.Market
	}

	valueCol := tradeValueColumn(source, superflex)

	valuePlayers := func(names []string) ([]map[string]any, int) {
		var items []map[string]any
		total := 0
		for _, name := range names {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			// Search by name
			q := "%" + strings.ToLower(name) + "%"
			var sleeperID string
			var fullName *string
			var tradeValue *int
			err := s.pool.QueryRow(ctx, fmt.Sprintf(`
				SELECT p.sleeper_player_id, p.full_name, %s
				FROM player p JOIN player_ranking r ON r.player_id = p.id
				WHERE r.source = $1 AND r.market = $2
				  AND (lower(p.full_name) LIKE $3 OR lower(p.last_name) LIKE $3)
				ORDER BY %s DESC NULLS LAST LIMIT 1
			`, valueCol, valueCol), source, market, q).Scan(&sleeperID, &fullName, &tradeValue)
			if err != nil {
				items = append(items, map[string]any{colName: name, statusFound: false})
				continue
			}
			val := 0
			if tradeValue != nil {
				val = *tradeValue
				total += val
			}
			items = append(items, map[string]any{
				colPlayerID:   sleeperID,
				"full_name":   util.StrOrEmpty(fullName),
				"trade_value": val,
				statusFound:   true,
			})
		}
		return items, total
	}

	giveItems, giveTotal := valuePlayers(giveNames)
	getItems, getTotal := valuePlayers(getNames)

	delta := giveTotal - getTotal
	recommendation := "fair"
	if delta > 500 {
		recommendation = "you're overpaying"
	} else if delta < -500 {
		recommendation = "good deal for you"
	}

	return map[string]any{
		"give":           giveItems,
		"get":            getItems,
		"give_total":     giveTotal,
		"get_total":      getTotal,
		"value_delta":    delta,
		"recommendation": recommendation,
		colLeagueID:      leagueID,
	}, nil
}

// EvaluateRoster returns structured roster data.
func (s *Service) EvaluateRoster(ctx context.Context, leagueID, userID string, superflex bool) (map[string]any, error) {
	rosters, err := s.sleeper.GetLeagueRosters(ctx, leagueID)
	if err != nil {
		return nil, err
	}

	// Find user's roster
	var myRoster *sleeper.Roster
	for i := range rosters {
		if util.StrOrEmpty(rosters[i].OwnerID.Ptr()) == userID {
			myRoster = &rosters[i]
			break
		}
	}
	if myRoster == nil {
		return nil, fmt.Errorf("%w for user %s in league %s", errRosterNotFound, userID, leagueID)
	}

	players := myRoster.Players
	lf, hasLF := models.LeagueFormats[leagueID]
	source := "Dynasty Daddy"
	market := 14
	if hasLF && lf.Market != nil {
		source = srcFantasyCalc
		market = *lf.Market
	}

	valueCol := tradeValueColumn(source, superflex)

	// One batched query instead of one QueryRow per roster player — the
	// LEFT JOIN still means an unranked player gets a row with a nil
	// trade_value, and a player_id absent from the player table entirely
	// (checked below via the `found` map) still reports found:false.
	type rosterRow struct {
		fullName, pos, teamAbbr *string
		age                     *int
		tradeValue              *int
	}
	found := make(map[string]rosterRow, len(players))
	if len(players) > 0 {
		rows, err := s.pool.Query(ctx, fmt.Sprintf(`
			SELECT p.sleeper_player_id, p.full_name, p.position, p.team_abbr, p.age, %s
			FROM player p LEFT JOIN player_ranking r ON r.player_id = p.id AND r.source = $1 AND r.market = $2
			WHERE p.sleeper_player_id = ANY($3)
		`, valueCol), source, market, players)
		if err != nil {
			return nil, fmt.Errorf("query roster values: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var sid string
			var r rosterRow
			if err := rows.Scan(&sid, &r.fullName, &r.pos, &r.teamAbbr, &r.age, &r.tradeValue); err != nil {
				continue
			}
			found[sid] = r
		}
	}

	var rosterItems []map[string]any
	totalValue := 0
	for _, sid := range players {
		r, ok := found[sid]
		if !ok {
			rosterItems = append(rosterItems, map[string]any{colPlayerID: sid, statusFound: false})
			continue
		}
		val := 0
		if r.tradeValue != nil {
			val = *r.tradeValue
			totalValue += val
		}
		item := map[string]any{
			colPlayerID:   sid,
			"full_name":   util.StrOrEmpty(r.fullName),
			colPosition:   util.StrOrEmpty(r.pos),
			colTeam:       util.StrOrEmpty(r.teamAbbr),
			"trade_value": val,
		}
		if r.age != nil {
			item["age"] = *r.age
		}
		rosterItems = append(rosterItems, item)
	}

	return map[string]any{
		colLeagueID:    leagueID,
		colUserID:      userID,
		"roster":       rosterItems,
		"total_value":  totalValue,
		"player_count": len(rosterItems),
	}, nil
}

// tradeValueColumn returns the player_ranking trade-value column to use
// for a superflex vs. standard query. FantasyCalc encodes the format in the
// market (each market row holds that format's dynasty value in trade_value),
// so the value is ALWAYS trade_value regardless of the superflex flag —
// sf_trade_value is NULL for FantasyCalc. For Dynasty Daddy / KeepTradeCut
// (single 1QB+SF row), superflex selects the sf_ column.
func tradeValueColumn(source string, superflex bool) string {
	if source == srcFantasyCalc {
		return rankColTradeValue
	}
	if superflex {
		return "r.sf_trade_value"
	}
	return rankColTradeValue
}

// rankingColumns returns the (value, overall_rank, position_rank) SQL column
// expressions for a GetRankings query. For FantasyCalc the trade_value is
// always r.trade_value (format is encoded in the market), but when superflex
// is requested the sf rank columns are used with a COALESCE fallback to the
// standard ranks for rows where sf data is NULL. For other sources, superflex
// selects the sf_ columns outright.
func rankingColumns(source string, superflex bool) (string, string, string) {
	vCol := rankColTradeValue
	oCol := rankColOverall
	pCol := rankColPosRank
	if !superflex {
		return vCol, oCol, pCol
	}
	if source == srcFantasyCalc {
		oCol = "COALESCE(r.sf_overall_rank, r.overall_rank)"
		pCol = "COALESCE(r.sf_position_rank, r.position_rank)"
		return vCol, oCol, pCol
	}
	vCol = "r.sf_trade_value"
	oCol = "r.sf_overall_rank"
	pCol = "r.sf_position_rank"
	return vCol, oCol, pCol
}
