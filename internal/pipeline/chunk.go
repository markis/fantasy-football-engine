package pipeline

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"ff-engine/internal/db"
	"ff-engine/internal/htmlx"
)

// Chunker splits news items into section-level chunks by HTML headings.
type Chunker struct {
	pool *db.Pool
}

// NewChunker creates a new chunker.
func NewChunker(pool *db.Pool) *Chunker {
	return &Chunker{pool: pool}
}

// ChunkResult is the result of a chunk batch.
type ChunkResult struct {
	ItemsChecked  int    `json:"itemsChecked"`
	ChunksCreated int    `json:"chunksCreated"`
	Errors        int    `json:"errors"`
	Status        string `json:"status"`
}

// chunkRow is a chunk to insert, derived from splitting a news item.
type chunkRow struct {
	newsItemID uuid.UUID
	chunkIndex int
	heading    string
	chunkText  string
}

// ChunkBatch finds news items with content_html that haven't been chunked yet
// (or whose content_hash has changed since last chunking), splits them by
// heading boundaries, and inserts the resulting chunks into news_chunk
// (without embeddings — embeddings are added by the Embedder).
func (c *Chunker) ChunkBatch(ctx context.Context, limit int) (*ChunkResult, error) {
	rows, err := c.pool.Query(ctx, `
		SELECT ni.id, ni.title, ni.content_html, ni.content_text, ni.content_hash
		FROM news_item ni
		WHERE ni.content_html IS NOT NULL AND ni.content_html != ''
		  AND (
		    NOT EXISTS (SELECT 1 FROM news_chunk nc WHERE nc.news_item_id = ni.id)
		    OR (ni.chunked_content_hash IS DISTINCT FROM ni.content_hash)
		  )
		ORDER BY ni.created_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query items for chunking: %w", err)
	}
	defer rows.Close()

	result := &ChunkResult{Status: "ok"}

	for rows.Next() {
		var id uuid.UUID
		var title, contentHTML, contentText, contentHash *string
		if err := rows.Scan(&id, &title, &contentHTML, &contentText, &contentHash); err != nil {
			slog.Warn("scan chunk item", "err", err)
			result.Errors++
			continue
		}

		articleTitle := ""
		if title != nil {
			articleTitle = *title
		}

		htmlStr := ""
		if contentHTML != nil {
			htmlStr = *contentHTML
		}

		chunks := htmlx.SplitByHeadings(htmlStr, articleTitle)
		if len(chunks) == 0 {
			if contentText != nil && *contentText != "" {
				chunks = []htmlx.Chunk{{Heading: "", Text: articleTitle + " " + *contentText}}
			} else {
				result.ItemsChecked++
				continue
			}
		}

		chunkRows := make([]chunkRow, len(chunks))
		for i, ch := range chunks {
			chunkRows[i] = chunkRow{
				newsItemID: id,
				chunkIndex: i,
				heading:    ch.Heading,
				chunkText:  ch.Text,
			}
		}

		if err := c.replaceChunks(ctx, chunkRows, id, contentHash); err != nil {
			slog.Warn("replace chunks", "id", id, "err", err)
			result.Errors++
			continue
		}
		result.ItemsChecked++
		result.ChunksCreated += len(chunkRows)
	}

	slog.Info("chunk batch complete", "items", result.ItemsChecked, "chunks", result.ChunksCreated, "errors", result.Errors)
	return result, nil
}

func (c *Chunker) replaceChunks(ctx context.Context, rows []chunkRow, newsItemID uuid.UUID, contentHash *string) error {
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, "DELETE FROM news_chunk WHERE news_item_id = $1", newsItemID)
	if err != nil {
		return fmt.Errorf("delete old chunks: %w", err)
	}

	for _, r := range rows {
		_, err := tx.Exec(ctx, `
			INSERT INTO news_chunk (news_item_id, chunk_index, heading, chunk_text)
			VALUES ($1, $2, $3, $4)
		`, r.newsItemID, r.chunkIndex, r.heading, r.chunkText)
		if err != nil {
			return fmt.Errorf("insert chunk %d: %w", r.chunkIndex, err)
		}
	}

	_, err = tx.Exec(ctx, "UPDATE news_item SET chunked_content_hash = $1 WHERE id = $2", contentHash, newsItemID)
	if err != nil {
		return fmt.Errorf("update chunked_content_hash: %w", err)
	}

	return tx.Commit(ctx)
}
