package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"ff-engine/internal/db"
	"ff-engine/internal/llm"
)

var errStoryValidation = errors.New("story validation failed")

// StoryGenerator generates fantasy football blurbs for news items.
type StoryGenerator struct {
	pool *db.Pool
	llm  *llm.Client
}

// NewStoryGenerator creates a new story generator.
func NewStoryGenerator(pool *db.Pool, llmClient *llm.Client) *StoryGenerator {
	return &StoryGenerator{pool: pool, llm: llmClient}
}

const (
	maxBodyCharsStory = 1500
	maxFactChars      = 220
	maxStoryChars     = 800
)

const storyPrompt = `You are writing a short fantasy football news blurb for a Discord channel.

Given a headline, a short summary, the start of the article body, and a list of
atomic facts already extracted from the article, write ONE short blurb of
2-3 sentences (about 50-90 words) for fantasy football managers.

Rules:
- Be factual. Do not invent names, dates, numbers, or stats that are not in
  the inputs.
- Lead with the fantasy takeaway (who is affected, how, and what it means for
  fantasy lineups) in the first sentence.
- Mention the player name and team when known.
- Plain, direct fantasy voice — not clickbait, not chatty. No hashtags, emojis,
  or call-to-action language.
- If the facts and summary disagree, prefer the facts (they're verified).
- Do NOT include the source URL or "according to <source>".
- Output ONLY the blurb — no JSON, no markdown fences, no preamble.

Item title: %s
Summary: %s
Body excerpt: %s
Atomic facts:
%s
`

// StoryResult is the result of a story generation batch.
type StoryResult struct {
	Processed        int    `json:"processed"`
	StoriesGenerated int    `json:"storiesGenerated"`
	Errors           int    `json:"errors"`
	Candidates       int    `json:"candidates"`
	Status           string `json:"status"`
}

// GenerateBatch generates stories for items that don't have one yet.
func (g *StoryGenerator) GenerateBatch(ctx context.Context, limit int) (*StoryResult, error) {
	rows, err := g.pool.Query(ctx, `
		SELECT id FROM news_item
		WHERE news_story IS NULL
		  AND is_relevant = true
		  AND COALESCE(is_news, true)
		  AND body_fetch_status IN ('fetched', 'skipped')
		ORDER BY published_at DESC NULLS LAST, created_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query items for stories: %w", err)
	}
	defer rows.Close()

	var itemIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan news item id: %w", err)
		}
		itemIDs = append(itemIDs, id)
	}

	result := &StoryResult{Candidates: len(itemIDs), Status: "ok"}
	for _, id := range itemIDs {
		err := g.generateOne(ctx, id)
		if err != nil {
			slog.Warn("generate story", "id", id, "err", err)
			result.Errors++
			continue
		}
		result.Processed++
		result.StoriesGenerated++
	}
	slog.Info("story generation complete", "generated", result.StoriesGenerated, "errors", result.Errors)
	return result, nil
}

func (g *StoryGenerator) generateOne(ctx context.Context, itemID uuid.UUID) error {
	var title, summary, body *string
	err := g.pool.QueryRow(ctx, `
		SELECT title, summary_short, content_text FROM news_item WHERE id = $1
	`, itemID).Scan(&title, &summary, &body)
	if err != nil {
		return fmt.Errorf("query news item %s: %w", itemID, err)
	}

	summaryStr := strings.TrimSpace(ptrStr(summary))
	bodyExcerpt := ""
	if summaryStr == "" {
		bodyExcerpt = truncate(ptrStr(body), 600)
	}
	if summaryStr == "" && bodyExcerpt == "" {
		return nil
	}

	facts := g.compactFacts(ctx, itemID)
	prompt := fmt.Sprintf(storyPrompt,
		ptrStrOr(title, "(untitled)"),
		ptrStrOr(summary, "(no summary)"),
		bodyExcerpt,
		strings.Join(facts, "\n"))

	raw, err := g.llm.Chat(ctx, prompt, 0.3)
	if err != nil {
		return fmt.Errorf("llm: %w", err)
	}

	story := validateStory(raw)
	if story == "" {
		return errStoryValidation
	}

	modelTag := g.llm.ModelTag()
	_, err = g.pool.Exec(ctx, `
		UPDATE news_item SET news_story = $1, news_story_generated_at = now(), news_story_model = $2
		WHERE id = $3
	`, story, modelTag, itemID)
	if err != nil {
		return fmt.Errorf("update news item %s: %w", itemID, err)
	}
	return nil
}

func (g *StoryGenerator) compactFacts(ctx context.Context, itemID uuid.UUID) []string {
	rows, err := g.pool.Query(ctx,
		"SELECT fact_text FROM fact WHERE news_item_id = $1 ORDER BY extracted_at", itemID)
	if err != nil {
		return []string{"- (no atomic facts extracted)"}
	}
	defer rows.Close()

	var facts []string
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			continue
		}
		facts = append(facts, "- "+truncate(text, maxFactChars))
	}
	if len(facts) == 0 {
		return []string{"- (no atomic facts extracted)"}
	}
	return facts
}

func truncate(text string, limit int) string {
	text = strings.TrimSpace(whitespaceRe.ReplaceAllString(text, " "))
	if len(text) <= limit {
		return text
	}
	return strings.TrimRight(text[:limit-1], " ") + "\u2026"
}

var (
	codeFenceStartRe = regexp.MustCompile("^```(?:json|text|markdown)?\\s*")
	codeFenceEndRe   = regexp.MustCompile("\\s*```$")
	listLineRe       = regexp.MustCompile(`^\s*[-*]\s|^\s*\d+\.\s`)
)

func validateStory(text string) string {
	text = strings.TrimSpace(text)
	text = codeFenceStartRe.ReplaceAllString(text, "")
	text = codeFenceEndRe.ReplaceAllString(text, "")
	text = strings.TrimSpace(text)
	text = strings.Trim(text, `"'`)
	text = strings.TrimSpace(text)
	if text == "" || len(text) < 40 {
		return ""
	}
	if len(text) > maxStoryChars {
		text = strings.TrimRight(text[:maxStoryChars-1], " ") + "\u2026"
	}
	// Reject if it looks like a list
	listCount := 0
	for line := range strings.SplitSeq(text, "\n") {
		if listLineRe.MatchString(line) {
			listCount++
		}
	}
	if listCount >= 2 {
		return ""
	}
	text = strings.TrimSpace(whitespaceRe.ReplaceAllString(text, " "))
	return text
}

func ptrStrOr(s *string, def string) string {
	if s == nil || *s == "" {
		return def
	}
	return *s
}
