package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"

	"ff-engine/internal/db"
	"ff-engine/internal/embed"
	"ff-engine/internal/llm"
)

var errNoFactBody = errors.New("no fact body available")

// FactExtractor extracts atomic fantasy football facts from news items.
type FactExtractor struct {
	pool  *db.Pool
	llm   *llm.Client
	embed *embed.Client
}

// NewFactExtractor creates a new fact extractor.
func NewFactExtractor(pool *db.Pool, llmClient *llm.Client, embedClient *embed.Client) *FactExtractor {
	return &FactExtractor{pool: pool, llm: llmClient, embed: embedClient}
}

const (
	maxFactsPerItem = 8
	maxBodyChars    = 6000
)

const factsPrompt = `You are extracting atomic fantasy football facts from a news item.

Rules:
- Extract ONLY factual statements that tell fantasy managers something actionable:
  injury status, depth chart changes, snap counts, target share, touch counts,
  trades, signings, releases, coaching changes, workload trends, quarterback
  battles, practice participation, and player performance data.
- Each fact must be ONE standalone sentence, understandable without the article.
- Include the player name and team in each fact when known (e.g. "Saquon Barkley
  (PHI) was limited in practice with a knee issue").
- Skip: off-field legal issues with no fantasy impact, stadium news, ownership
  disputes, league business, broadcast news, Hall of Fame ceremonies, general
  NFL news with no player/team fantasy impact.
- At most %d facts. Prefer fewer, higher-quality facts.
- occurred_at: set to the article's published_at (RSS) so facts are filterable
  by when the news occurred (the LLM event date is no longer used for this field).
  else null.
- entities: 0-4 key named entities (players, teams) in the fact.
- topics: 0-2 short topic labels (e.g. "injury", "depth chart", "transaction",
  "snap count", "coaching", "performance").

Title: %s
Text:
"""%s"""

Return ONLY a JSON array (no markdown fences), each element:
{"fact": "...", "confidence": "high",
  "occurred_at": null, "entities": [], "topics": []}`

type llmFact struct {
	Fact       string   `json:"fact"`
	Confidence string   `json:"confidence"`
	OccurredAt *string  `json:"occurredAt"`
	Entities   []string `json:"entities"`
	Topics     []string `json:"topics"`
}

// FactsResult is the result of a fact extraction batch.
type FactsResult struct {
	Processed      int    `json:"processed"`
	FactsExtracted int    `json:"factsExtracted"`
	Errors         int    `json:"errors"`
	Status         string `json:"status"`
}

// ExtractBatch extracts facts for enriched fantasy items that have no facts yet.
func (f *FactExtractor) ExtractBatch(ctx context.Context, limit int) (*FactsResult, error) {
	rows, err := f.pool.Query(ctx, `
		SELECT ni.id FROM news_item ni
		WHERE ni.is_relevant = true
		  AND COALESCE(ni.is_news, true)
		  AND ni.quality_score IS NOT NULL
		  AND ni.body_fetch_status IN ('fetched', 'skipped')
		  AND NOT EXISTS (SELECT 1 FROM fact f WHERE f.news_item_id = ni.id)
		ORDER BY ni.published_at DESC NULLS LAST, ni.created_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query items for fact extraction: %w", err)
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

	result := &FactsResult{Status: "ok"}
	for _, id := range itemIDs {
		n, err := f.extractOne(ctx, id)
		if err != nil {
			slog.Warn("extract facts", "id", id, "err", err)
			result.Errors++
			continue
		}
		result.Processed++
		result.FactsExtracted += n
	}
	slog.Info("fact extraction complete", "processed", result.Processed, "facts", result.FactsExtracted)
	return result, nil
}

// factSource holds the title/body and timestamps used to extract facts for
// one news item.
type factSource struct {
	title       string
	body        string
	publishedAt *time.Time
	createdAt   *time.Time
}

// validFact pairs an LLM-extracted fact with its trimmed, validated text.
//
// Valid facts are collected before embedding so they can be embedded in one
// batched call instead of one HTTP round trip per fact (EmbedBatch exists
// precisely to avoid the one-call-per-text pattern the Python pipeline used).
type validFact struct {
	fact llmFact
	text string
}

// loadFactSource fetches the news item's title/body and timestamps needed
// for fact extraction. A nil factSource (with nil error) means there is no
// usable body text and extraction should be skipped.
func (f *FactExtractor) loadFactSource(ctx context.Context, itemID uuid.UUID) (*factSource, error) {
	var title, content, summary *string
	var publishedAt, createdAt *time.Time
	err := f.pool.QueryRow(ctx, `
		SELECT title, content_text, summary_short, published_at, created_at
		FROM news_item WHERE id = $1
	`, itemID).Scan(&title, &content, &summary, &publishedAt, &createdAt)
	if err != nil {
		return nil, fmt.Errorf("query news item %s: %w", itemID, err)
	}

	body := ptrStr(content)
	if body == "" {
		body = ptrStr(summary)
	}
	if body == "" {
		body = ptrStr(title)
	}
	if strings.TrimSpace(body) == "" {
		return nil, errNoFactBody
	}
	if len(body) > maxBodyChars {
		body = body[:maxBodyChars]
	}

	return &factSource{title: ptrStr(title), body: body, publishedAt: publishedAt, createdAt: createdAt}, nil
}

// filterValidFacts trims fact text and drops facts that are too short or
// beyond the per-item cap.
func filterValidFacts(facts []llmFact) []validFact {
	var valid []validFact
	for _, fact := range facts {
		if len(valid) >= maxFactsPerItem {
			break
		}
		text := strings.TrimSpace(fact.Fact)
		if len(text) < 15 {
			continue
		}
		valid = append(valid, validFact{fact: fact, text: text})
	}
	return valid
}

// resolveOccurredAt picks the fact's occurred_at timestamp: article
// published_at, else created_at, else the LLM-provided value, else now.
func resolveOccurredAt(fact *llmFact, publishedAt, createdAt *time.Time) time.Time {
	switch {
	case publishedAt != nil:
		return *publishedAt
	case createdAt != nil:
		return *createdAt
	case fact.OccurredAt != nil:
		t, parseErr := time.Parse(time.RFC3339, *fact.OccurredAt)
		if parseErr == nil {
			return t
		}
		return time.Now().UTC()
	default:
		return time.Now().UTC()
	}
}

// normalizeConfidence returns a pointer to confidence if it's one of the
// accepted values, else nil.
func normalizeConfidence(confidence string) *string {
	if confidence != "high" && confidence != "medium" && confidence != "low" {
		return nil
	}
	return &confidence
}

// insertFacts stores each valid fact (with its embedding) and returns the
// count of facts actually inserted.
func (f *FactExtractor) insertFacts(
	ctx context.Context, itemID uuid.UUID, valid []validFact, vecs [][]float32, publishedAt, createdAt *time.Time,
) int {
	inserted := 0
	for i, vf := range valid {
		if i >= len(vecs) {
			break
		}
		fact, text := vf.fact, vf.text

		occurredAt := resolveOccurredAt(&fact, publishedAt, createdAt)

		entities := fact.Entities
		if entities == nil {
			entities = []string{}
		}
		topics := fact.Topics
		if topics == nil {
			topics = []string{}
		}

		confPtr := normalizeConfidence(fact.Confidence)

		v := pgvector.NewVector(vecs[i])
		_, err := f.pool.Exec(ctx, `
			INSERT INTO fact (news_item_id, fact_text, entities, topics,
			                  occurred_at, confidence, embedding)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (news_item_id, md5(fact_text)) DO NOTHING
		`, itemID, text, entities, topics, occurredAt, confPtr, v)
		if err != nil {
			slog.Warn("store fact", "err", err)
			continue
		}
		inserted++
	}
	return inserted
}

func (f *FactExtractor) extractOne(ctx context.Context, itemID uuid.UUID) (int, error) {
	src, err := f.loadFactSource(ctx, itemID)
	if err != nil {
		if errors.Is(err, errNoFactBody) {
			return 0, nil
		}
		return 0, err
	}

	prompt := fmt.Sprintf(factsPrompt, maxFactsPerItem, src.title, src.body)
	resp, err := f.llm.Chat(ctx, prompt, 0)
	if err != nil {
		return 0, fmt.Errorf("llm: %w", err)
	}

	facts := parseFactsJSON(resp)
	valid := filterValidFacts(facts)
	if len(valid) == 0 {
		return 0, nil
	}

	texts := make([]string, len(valid))
	for i, vf := range valid {
		texts[i] = vf.text
	}
	vecs, err := f.embed.EmbedBatch(ctx, texts)
	if err != nil {
		return 0, fmt.Errorf("embed facts: %w", err)
	}

	return f.insertFacts(ctx, itemID, valid, vecs, src.publishedAt, src.createdAt), nil
}

func parseFactsJSON(content string) []llmFact {
	start := strings.Index(content, "[")
	end := strings.LastIndex(content, "]")
	if start == -1 || end == -1 || end <= start {
		return nil
	}
	var facts []llmFact
	if err := json.Unmarshal([]byte(content[start:end+1]), &facts); err != nil {
		return nil
	}
	var result []llmFact
	for _, f := range facts {
		if f.Fact != "" {
			result = append(result, f)
		}
	}
	return result
}
