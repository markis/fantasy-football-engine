package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/markis/fantasy-football-engine/internal/db"
	"github.com/markis/fantasy-football-engine/internal/embed"
	"github.com/markis/fantasy-football-engine/internal/llm"
	"github.com/pgvector/pgvector-go"
)

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
			return nil, err
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

func (f *FactExtractor) extractOne(ctx context.Context, itemID uuid.UUID) (int, error) {
	var title, content, summary *string
	var publishedAt, createdAt *time.Time
	err := f.pool.QueryRow(ctx, `
		SELECT title, content_text, summary_short, published_at, created_at
		FROM news_item WHERE id = $1
	`, itemID).Scan(&title, &content, &summary, &publishedAt, &createdAt)
	if err != nil {
		return 0, err
	}

	body := ptrStr(content)
	if body == "" {
		body = ptrStr(summary)
	}
	if body == "" {
		body = ptrStr(title)
	}
	if strings.TrimSpace(body) == "" {
		return 0, nil
	}
	if len(body) > maxBodyChars {
		body = body[:maxBodyChars]
	}

	prompt := fmt.Sprintf(factsPrompt, maxFactsPerItem, ptrStr(title), body)
	resp, err := f.llm.Chat(ctx, prompt, 0)
	if err != nil {
		return 0, fmt.Errorf("llm: %w", err)
	}

	facts := parseFactsJSON(resp)

	// Collect valid fact texts first so they embed in one batched call
	// instead of one HTTP round trip per fact (EmbedBatch exists precisely
	// to avoid the one-call-per-text pattern the Python pipeline used).
	type validFact struct {
		fact llmFact
		text string
	}
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

	inserted := 0
	for i, vf := range valid {
		if i >= len(vecs) {
			break
		}
		fact, text := vf.fact, vf.text

		// occurred_at = article published_at, else created_at, else LLM value
		var occurredAt time.Time
		switch {
		case publishedAt != nil:
			occurredAt = *publishedAt
		case createdAt != nil:
			occurredAt = *createdAt
		case fact.OccurredAt != nil:
			t, parseErr := time.Parse(time.RFC3339, *fact.OccurredAt)
			if parseErr == nil {
				occurredAt = t
			} else {
				occurredAt = time.Now().UTC()
			}
		default:
			occurredAt = time.Now().UTC()
		}

		entities := fact.Entities
		if entities == nil {
			entities = []string{}
		}
		topics := fact.Topics
		if topics == nil {
			topics = []string{}
		}

		confidence := fact.Confidence
		if confidence != "high" && confidence != "medium" && confidence != "low" {
			confidence = ""
		}
		var confPtr *string
		if confidence != "" {
			confPtr = &confidence
		}

		v := pgvector.NewVector(vecs[i])
		_, err = f.pool.Exec(ctx, `
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
	return inserted, nil
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
