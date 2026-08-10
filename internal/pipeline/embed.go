package pipeline

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/markis/fantasy-football-engine/internal/db"
	"github.com/markis/fantasy-football-engine/internal/embed"
	"github.com/pgvector/pgvector-go"
)

// Embedder generates and stores embeddings for news items.
type Embedder struct {
	pool        *db.Pool
	embedClient *embed.Client
	batchSize   int
}

// NewEmbedder creates a new embedder.
func NewEmbedder(pool *db.Pool, embedClient *embed.Client, batchSize int) *Embedder {
	return &Embedder{pool: pool, embedClient: embedClient, batchSize: batchSize}
}

// EmbedResult is the result of embedding items in batch.
type EmbedResult struct {
	Embedded int    `json:"embedded"`
	Errors   int    `json:"errors"`
	Total    int    `json:"total"`
	Status   string `json:"status"`
}

// embedItem is a news item pending embedding.
type embedItem struct {
	id   uuid.UUID
	text string
}

// fetchPendingEmbedItems loads news items that don't have embeddings yet.
func (e *Embedder) fetchPendingEmbedItems(ctx context.Context, limit int) ([]embedItem, error) {
	rows, err := e.pool.Query(ctx, `
		SELECT id, title, summary_short, content_text FROM news_item
		WHERE embedding IS NULL
		  AND (content_text IS NOT NULL OR summary_short IS NOT NULL OR title IS NOT NULL)
		ORDER BY created_at DESC LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query items for embedding: %w", err)
	}
	defer rows.Close()

	var items []embedItem
	for rows.Next() {
		var id uuid.UUID
		var title, summary, content *string
		if err := rows.Scan(&id, &title, &summary, &content); err != nil {
			return nil, err
		}
		text := ptrStr(content)
		if text == "" {
			text = ptrStr(summary)
		}
		if text == "" {
			text = ptrStr(title)
		}
		if text != "" {
			items = append(items, embedItem{id: id, text: text})
		}
	}
	return items, nil
}

// embedAndStoreBatch embeds one batch of items and stores the resulting
// vectors, tallying successes and failures into result.
func (e *Embedder) embedAndStoreBatch(ctx context.Context, batch []embedItem, batchStart int, result *EmbedResult) {
	texts := make([]string, len(batch))
	for j, it := range batch {
		texts[j] = it.text
	}

	vecs, err := e.embedClient.EmbedBatch(ctx, texts)
	if err != nil {
		slog.Warn("embed batch error", "err", err, "batch_start", batchStart)
		result.Errors += len(batch)
		return
	}

	if len(vecs) < len(batch) {
		slog.Warn("embed batch returned fewer vectors than requested",
			"requested", len(batch), "received", len(vecs), "batch_start", batchStart)
	}

	for j, vec := range vecs {
		if j >= len(batch) {
			break
		}
		v := pgvector.NewVector(vec)
		_, err := e.pool.Exec(ctx,
			"UPDATE news_item SET embedding = $1 WHERE id = $2",
			v, batch[j].id)
		if err != nil {
			slog.Warn("store embedding", "id", batch[j].id, "err", err)
			result.Errors++
		} else {
			result.Embedded++
		}
	}
	if len(vecs) < len(batch) {
		result.Errors += len(batch) - len(vecs)
	}
}

// EmbedBatch embeds all news items without embeddings, using batched HTTP calls.
func (e *Embedder) EmbedBatch(ctx context.Context, limit int) (*EmbedResult, error) {
	items, err := e.fetchPendingEmbedItems(ctx, limit)
	if err != nil {
		return nil, err
	}

	result := &EmbedResult{Status: "ok", Total: len(items)}

	// Process in batches — key improvement over Python (which did 1-at-a-time)
	bs := e.batchSize
	if bs <= 0 {
		bs = 32
	}
	for i := 0; i < len(items); i += bs {
		end := min(i+bs, len(items))
		e.embedAndStoreBatch(ctx, items[i:end], i, result)
	}

	slog.Info("embed batch complete", "embedded", result.Embedded, "errors", result.Errors)
	return result, nil
}
