package pipeline

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"ff-engine/internal/db"
)

// DedupChecker runs staged dedup (exact -> simhash -> semantic) on items.
type DedupChecker struct {
	pool *db.Pool
}

// NewDedupChecker creates a new dedup checker.
func NewDedupChecker(pool *db.Pool) *DedupChecker {
	return &DedupChecker{pool: pool}
}

const (
	simhashThreshold = 3
	cosineThreshold  = 0.88
	timeWindowHours  = 48
)

// DedupResult is the result of a dedup check run.
type DedupResult struct {
	Checked      int    `json:"checked"`
	ExactDups    int    `json:"exactDups"`
	NearDups     int    `json:"nearDups"`
	SemanticDups int    `json:"semanticDups"`
	NewItems     int    `json:"newItems"`
	Status       string `json:"status"`
}

// dedupItem is a news item pending dedup checks.
type dedupItem struct {
	id        uuid.UUID
	sourceID  uuid.UUID
	urlHash   *string
	simhash   *int64
	chunkVecs []string // embedding::text per news_chunk, for semantic dedup
	published *any
	createdAt any
}

// CheckBatch runs dedup on items that haven't been checked yet.
func (d *DedupChecker) CheckBatch(ctx context.Context, limit int) (*DedupResult, error) {
	rows, err := d.pool.Query(ctx, `
		SELECT id, source_id, canonical_url_hash, simhash,
		       published_at, created_at
		FROM news_item WHERE quality_score IS NULL
		ORDER BY created_at DESC LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query items for dedup: %w", err)
	}
	defer rows.Close()

	var items []dedupItem
	for rows.Next() {
		var it dedupItem
		if err := rows.Scan(&it.id, &it.sourceID, &it.urlHash, &it.simhash,
			&it.published, &it.createdAt); err != nil {
			return nil, fmt.Errorf("scan dedup item: %w", err)
		}
		items = append(items, it)
	}

	// Load chunk embedding vectors per item for semantic dedup.
	for i := range items {
		vecs, err := d.fetchChunkVectors(ctx, items[i].id)
		if err != nil {
			slog.Warn("fetch chunk vectors", "id", items[i].id, "err", err)
			continue
		}
		items[i].chunkVecs = vecs
	}

	result := &DedupResult{Checked: len(items), Status: "ok"}

	for _, it := range items {
		// Exact dedup
		if it.urlHash != nil && *it.urlHash != "" {
			var existing uuid.UUID
			err := d.pool.QueryRow(ctx, `
				SELECT id FROM news_item
				WHERE source_id = $1 AND canonical_url_hash = $2 AND id != $3
				AND created_at < $4
			`, it.sourceID, *it.urlHash, it.id, it.createdAt).Scan(&existing)
			if err == nil {
				if _, err := d.pool.Exec(ctx, "UPDATE news_item SET quality_score = -1 WHERE id = $1", it.id); err != nil {
					slog.Warn("mark exact dup", "err", err)
				}
				result.ExactDups++
				continue
			}
		}

		// Near dedup (simhash)
		if it.simhash != nil {
			if d.checkNearDup(ctx, &it, result) {
				continue
			}
		}

		// Semantic dedup (chunk embeddings)
		if len(it.chunkVecs) > 0 {
			if d.checkSemanticDup(ctx, &it, result) {
				continue
			}
		}
	}

	result.NewItems = result.Checked - result.ExactDups - result.NearDups - result.SemanticDups
	slog.Info("dedup complete", "checked", result.Checked, "exact", result.ExactDups,
		"near", result.NearDups, "semantic", result.SemanticDups)
	return result, nil
}

// checkNearDup checks if an item is a near duplicate using simhash.
func (d *DedupChecker) checkNearDup(ctx context.Context, it *dedupItem, result *DedupResult) bool {
	nearRows, err := d.pool.Query(ctx, `
		SELECT id, simhash FROM news_item
		WHERE source_id = $1 AND simhash IS NOT NULL AND id != $2
		AND created_at < $3
	`, it.sourceID, it.id, it.createdAt)
	if err != nil {
		return false
	}
	defer nearRows.Close()

	for nearRows.Next() {
		var candID uuid.UUID
		var candSimhash int64
		if err := nearRows.Scan(&candID, &candSimhash); err != nil {
			continue
		}
		if HammingDistance(*it.simhash, candSimhash) <= simhashThreshold {
			if _, err := d.pool.Exec(ctx, "UPDATE news_item SET quality_score = -0.5 WHERE id = $1", it.id); err != nil {
				slog.Warn("mark near dup", "err", err)
			}
			result.NearDups++
			return true
		}
	}
	return false
}

// fetchChunkVectors loads the embedding vectors for an item's chunks as text.
func (d *DedupChecker) fetchChunkVectors(ctx context.Context, itemID uuid.UUID) ([]string, error) {
	rows, err := d.pool.Query(ctx,
		"SELECT embedding::text FROM news_chunk WHERE news_item_id = $1 AND embedding IS NOT NULL",
		itemID)
	if err != nil {
		return nil, fmt.Errorf("query item chunk vectors: %w", err)
	}
	defer rows.Close()
	var vecs []string
	for rows.Next() {
		var v *string
		if err := rows.Scan(&v); err != nil {
			continue
		}
		if v != nil && *v != "" {
			vecs = append(vecs, *v)
		}
	}
	return vecs, nil
}

// checkSemanticDup checks if an item is a semantic duplicate using chunk
// embeddings. Each of the item's chunk vectors is searched against the HNSW
// index on news_chunk.embedding (excluding chunks from the same item/source);
// the smallest distance is used.
func (d *DedupChecker) checkSemanticDup(ctx context.Context, it *dedupItem, result *DedupResult) bool {
	bestDistance := 1 - cosineThreshold // only flag if closer than threshold
	var semanticID uuid.UUID
	for _, vec := range it.chunkVecs {
		var candID uuid.UUID
		var distance float64
		err := d.pool.QueryRow(ctx, `
			SELECT ni.id, nc.embedding <=> $1::vector AS distance
			FROM news_chunk nc
			JOIN news_item ni ON ni.id = nc.news_item_id
			WHERE ni.source_id != $2 AND ni.id != $3
			  AND nc.embedding IS NOT NULL
			  AND ni.created_at < $4
			ORDER BY nc.embedding <=> $1::vector
			LIMIT 1
		`, vec, it.sourceID, it.id, it.createdAt).Scan(&candID, &distance)
		if err != nil {
			continue
		}
		if distance < bestDistance {
			bestDistance = distance
			semanticID = candID
		}
	}
	if bestDistance >= 1-cosineThreshold {
		return false
	}
	_ = semanticID
	if _, err := d.pool.Exec(ctx, "UPDATE news_item SET quality_score = -0.3 WHERE id = $1", it.id); err != nil {
		slog.Warn("mark semantic dup", "err", err)
	}
	result.SemanticDups++
	return true
}
