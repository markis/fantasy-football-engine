package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/markis/fantasy-football-engine/internal/db"
	"github.com/markis/fantasy-football-engine/internal/llm"
	"github.com/markis/fantasy-football-engine/internal/models"
	"golang.org/x/sync/errgroup"
)

// Enricher classifies fantasy relevance and extracts entities/topics.
type Enricher struct {
	pool           *db.Pool
	llm            *llm.Client
	maxConcurrency int
}

// NewEnricher creates a new enricher. maxConcurrency bounds how many items
// EnrichBatch classifies via the LLM at once; values <= 0 fall back to 1
// (sequential).
func NewEnricher(pool *db.Pool, llmClient *llm.Client, maxConcurrency int) *Enricher {
	if maxConcurrency <= 0 {
		maxConcurrency = 1
	}
	return &Enricher{pool: pool, llm: llmClient, maxConcurrency: maxConcurrency}
}

var fantasyPositions = []string{"QB", "RB", "WR", "TE", "K", "DEF", "DST", "DL", "LB", "DB"}

var capitalizedNameRe = regexp.MustCompile(`\b([A-Z][a-z]+(?:\s+[A-Z][a-z]+)+)\b`)

const fantasyPrompt = `Is this news story relevant for fantasy football? ` +
	`A story is relevant if it mentions specific NFL players, teams, or fantasy-relevant ` +
	`topics such as: injuries, depth chart changes, trades, signings, releases, ` +
	`snap counts, targets, touches, coaching changes, quarterback battles, ` +
	`player performance, waiver wire targets, start/sit advice, or sleeper picks.

Relevant: player injury updates, depth chart moves, trade rumors involving ` +
	`named players, coaching firings/hirings that affect fantasy, practice reports, ` +
	`snap count analysis, target share data, player rankings, DFS advice.

NOT relevant: off-field legal issues with no on-field impact, stadium news, ` +
	`ownership disputes, league-wide CBA/labor news, Super Bowl halftime show, ` +
	`draft broadcast coverage, Hall of Fame ceremonies, general NFL business news ` +
	`with no player/team fantasy impact.

When in doubt, answer YES if any NFL player or team is named and the story ` +
	`could affect their fantasy value. Answer only YES or NO.

Title: %s
Summary: %s`

// EnrichResult is the result of enriching items in batch.
type EnrichResult struct {
	Enriched        int    `json:"enriched"`
	FantasyRelevant int    `json:"fantasy_relevant"`
	NotRelevant     int    `json:"not_relevant"`
	Status          string `json:"status"`
}

// EnrichBatch enriches news items that haven't been enriched yet.
func (e *Enricher) EnrichBatch(ctx context.Context, limit int) (*EnrichResult, error) {
	rows, err := e.pool.Query(ctx,
		`SELECT id FROM news_item WHERE quality_score IS NULL ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("query unenriched items: %w", err)
	}
	defer rows.Close()

	var itemIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		itemIDs = append(itemIDs, id)
	}

	result := &EnrichResult{Status: "ok"}
	var mu sync.Mutex
	var g errgroup.Group
	g.SetLimit(e.maxConcurrency)
	for _, id := range itemIDs {
		g.Go(func() error {
			relevant, err := e.enrichOne(ctx, id)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				slog.Warn("enrich item", "id", id, "err", err)
				return nil
			}
			result.Enriched++
			if relevant {
				result.FantasyRelevant++
			} else {
				result.NotRelevant++
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("enrich batch: %w", err)
	}
	slog.Info("enrich batch complete", "enriched", result.Enriched, "relevant", result.FantasyRelevant)
	return result, nil
}

func (e *Enricher) enrichOne(ctx context.Context, itemID uuid.UUID) (bool, error) {
	var title, summary, content, contentHTML, url *string
	var sourceID uuid.UUID
	var sourceName string
	err := e.pool.QueryRow(ctx, `
		SELECT ni.title, ni.summary_short, ni.content_text, ni.content_html,
		       ni.source_id, s.name, ni.url
		FROM news_item ni JOIN source s ON s.id = ni.source_id
		WHERE ni.id = $1
	`, itemID).Scan(&title, &summary, &content, &contentHTML, &sourceID, &sourceName, &url)
	if err != nil {
		return false, err
	}

	titleStr := ptrStr(title)
	summaryStr := ptrStr(summary)
	contentStr := ptrStr(content)
	text := contentStr
	if text == "" {
		text = summaryStr
	}
	if text == "" {
		text = titleStr
	}

	// Classify relevance via LLM
	isRelevant := e.classifyRelevance(ctx, titleStr, summaryStr)

	entities := extractEntities(text, titleStr)
	topics := extractTopics(text, titleStr)
	quality := computeQuality(contentStr, summaryStr)

	_, err = e.pool.Exec(ctx, `
		UPDATE news_item SET
			is_relevant = $1, is_news = true, entities = $2, topics = $3,
			language = 'en', quality_score = $4
		WHERE id = $5
	`, isRelevant, entities, topics, quality, itemID)
	if err != nil {
		return false, err
	}
	return isRelevant, nil
}

func (e *Enricher) classifyRelevance(ctx context.Context, title, summary string) bool {
	summaryTrunc := summary
	if len(summaryTrunc) > 500 {
		summaryTrunc = summaryTrunc[:500]
	}
	prompt := fmt.Sprintf(fantasyPrompt, title, summaryTrunc)
	answer, err := e.llm.Chat(ctx, prompt, 0)
	if err != nil {
		slog.Warn("classify relevance LLM error", "err", err)
		return false
	}
	return strings.HasPrefix(strings.ToUpper(strings.TrimSpace(answer)), "YES")
}

func extractEntities(text, title string) []string {
	combined := title + " " + text
	entities := make(map[string]bool)

	// NFL team detection
	for abbr, fullName := range models.NFLTeams {
		parts := strings.Split(fullName, " ")
		nickname := parts[len(parts)-1]
		city := strings.Join(parts[:len(parts)-1], " ")

		nickRe := regexp.MustCompile(`\b` + regexp.QuoteMeta(nickname) + `\b`)
		cityRe := regexp.MustCompile(`\b` + regexp.QuoteMeta(city) + `\b`)
		abbrRe := regexp.MustCompile(`\b` + abbr + `\b`)

		if nickRe.MatchString(combined) {
			entities[fullName] = true
		}
		if cityRe.MatchString(combined) {
			entities[fullName] = true
		}
		if abbrRe.MatchString(combined) {
			entities[fullName] = true
		}
	}

	// Position detection
	for _, pos := range fantasyPositions {
		posRe := regexp.MustCompile(`\b` + pos + `\b`)
		if posRe.MatchString(combined) {
			entities[pos] = true
		}
	}

	// Capitalized multi-word phrases (player names)
	for _, m := range capitalizedNameRe.FindAllString(combined, -1) {
		entities[m] = true
	}

	result := make([]string, 0, len(entities))
	for e := range entities {
		result = append(result, e)
	}
	if len(result) > 15 {
		result = result[:15]
	}
	return result
}

var topicKeywords = map[string][]string{
	"injury":      {"injury", "injured", "hurt", "concussion", "hamstring", "ankle", "knee", "shoulder", "questionable", "doubtful", "out", "ir", "injured reserve", "physically unable", "pup", "dnp", "limited"},
	"depth chart": {"depth chart", "starter", "backup", "benched", "demoted", "promoted", "number one", "number 1", "rb1", "rb2", "wr1", "wr2", "te1", "starting"},
	"transaction": {"trade", "traded", "signing", "signed", "released", "cut", "waived", "claimed", "free agent", "free agency", "contract", "extension", "retire", "retirement", "suspended", "suspension"},
	"performance": {"snap count", "snaps", "targets", "touchdown", "td", "yards", "receptions", "carries", "rush", "receiving", "passing", "fantasy points", "ppr", "half ppr"},
	"matchup":     {"matchup", "vs", "versus", "against", "defense", "defense", "secondary", "pass rush", "blitz"},
	"coaching":    {"coach", "coordinator", "offensive coordinator", "dc", "head coach", "fired", "hired", "playcaller", "play caller"},
	"practice":    {"practice", "mini camp", "minicamp", "ota", "training camp", "preseason", "walk-through"},
	"dfs":         {"dfs", "draftkings", "fanduel", "salary", "lineup", "cash game", "tournament", "gpp", "stack", "value"},
	"waiver wire": {"waiver", "waivers", "add", "drop", "pickup", "claim"},
	"draft":       {"draft", "drafted", "pick", "first round", "round 1", "rookie", "combine", "pro day"},
}

func extractTopics(text, title string) []string {
	textLower := strings.ToLower(text) + " " + strings.ToLower(title)
	topics := make(map[string]bool)
	for topic, keywords := range topicKeywords {
		for _, kw := range keywords {
			if strings.Contains(textLower, kw) {
				topics[topic] = true
				break
			}
		}
	}
	result := make([]string, 0, len(topics))
	for t := range topics {
		result = append(result, t)
	}
	return result
}

func computeQuality(contentText, summaryShort string) float64 {
	textLen := len(contentText)
	if textLen == 0 {
		textLen = len(summaryShort)
	}
	switch {
	case textLen > 5000:
		return 0.9
	case textLen > 1000:
		return 0.75
	case textLen > 200:
		return 0.5
	case textLen > 50:
		return 0.3
	default:
		return 0.1
	}
}

func ptrStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
