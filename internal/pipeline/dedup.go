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
	embedding *string
	published *any
	createdAt any
}

// CheckBatch runs dedup on items that haven't been checked yet.
func (d *DedupChecker) CheckBatch(ctx context.Context, limit int) (*DedupResult, error) {
	rows, err := d.pool.Query(ctx, `
		SELECT id, source_id, canonical_url_hash, simhash, embedding::text,
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
			&it.embedding, &it.published, &it.createdAt); err != nil {
			return nil, err
		}
		items = append(items, it)
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

		// Semantic dedup (embedding cosine)
		if it.embedding != nil && *it.embedding != "" {
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

// checkSemanticDup checks if an item is a semantic duplicate using embeddings.
func (d *DedupChecker) checkSemanticDup(ctx context.Context, it *dedupItem, result *DedupResult) bool {
	var semanticID uuid.UUID
	var distance float64
	err := d.pool.QueryRow(ctx, `
		SELECT id, embedding <=> $1::vector AS distance
		FROM news_item
		WHERE source_id != $2 AND embedding IS NOT NULL AND id != $3
		AND created_at < $4
		ORDER BY distance LIMIT 1
	`, *it.embedding, it.sourceID, it.id, it.createdAt).Scan(&semanticID, &distance)
	if err != nil {
		return false
	}
	cosineSim := 1 - distance
	if cosineSim >= cosineThreshold {
		if _, err := d.pool.Exec(ctx, "UPDATE news_item SET quality_score = -0.3 WHERE id = $1", it.id); err != nil {
			slog.Warn("mark semantic dup", "err", err)
		}
		result.SemanticDups++
		return true
	}
	return false
}
