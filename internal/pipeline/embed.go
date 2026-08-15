package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
	"golang.org/x/sync/errgroup"

	"ff-engine/internal/db"
	"ff-engine/internal/embed"
)

// Embedder generates and stores embeddings for news chunks.
type Embedder struct {
	pool           *db.Pool
	embedClient    *embed.Client
	batchSize      int
	maxConcurrency int
}

// NewEmbedder creates a new embedder. batchSize is the max number of chunks
// processed per EmbedChunkBatch run; maxConcurrency caps in-flight embed requests.
func NewEmbedder(pool *db.Pool, embedClient *embed.Client, batchSize, maxConcurrency int) *Embedder {
	if maxConcurrency <= 0 {
		maxConcurrency = 8
	}
	return &Embedder{pool: pool, embedClient: embedClient, batchSize: batchSize, maxConcurrency: maxConcurrency}
}

// EmbedResult is the result of embedding chunks in batch.
type EmbedResult struct {
	Embedded int    `json:"embedded"`
	Errors   int    `json:"errors"`
	Total    int    `json:"total"`
	Status   string `json:"status"`
}

// EmbedChunkBatch embeds news_chunk rows that lack embeddings, using the
// same bounded-concurrency pattern as EmbedBatch.
func (e *Embedder) EmbedChunkBatch(ctx context.Context, limit int) (*EmbedResult, error) {
	rows, err := e.pool.Query(ctx, `
		SELECT id, chunk_text FROM news_chunk
		WHERE embedding IS NULL
		ORDER BY created_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query chunks for embedding: %w", err)
	}
	defer rows.Close()

	type chunkItem struct {
		id   uuid.UUID
		text string
	}
	var items []chunkItem
	for rows.Next() {
		var it chunkItem
		if err := rows.Scan(&it.id, &it.text); err != nil {
			return nil, fmt.Errorf("scan chunk: %w", err)
		}
		items = append(items, it)
	}

	result := &EmbedResult{Status: "ok", Total: len(items)}
	if len(items) == 0 {
		slog.Info("chunk embed batch complete", "embedded", 0, "errors", 0)
		return result, nil
	}

	var mu sync.Mutex
	var g errgroup.Group
	g.SetLimit(e.maxConcurrency)

	for _, item := range items {
		g.Go(func() error {
			vec, err := e.embedClient.Embed(ctx, item.text)
			if err != nil {
				slog.Warn("embed chunk error", "id", item.id, "err", err)
				mu.Lock()
				result.Errors++
				mu.Unlock()
				return nil
			}
			v := pgvector.NewVector(vec)
			if _, err := e.pool.Exec(ctx,
				"UPDATE news_chunk SET embedding = $1 WHERE id = $2",
				v, item.id); err != nil {
				slog.Warn("store chunk embedding", "id", item.id, "err", err)
				mu.Lock()
				result.Errors++
				mu.Unlock()
				return nil
			}
			mu.Lock()
			result.Embedded++
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("chunk embed batch: %w", err)
	}

	slog.Info("chunk embed batch complete", "embedded", result.Embedded, "errors", result.Errors)
	return result, nil
}
