package pipeline

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/markis/fantasy-football-engine/internal/db"
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
	simhashThreshold   = 3
	cosineThreshold    = 0.88
	timeWindowHours    = 48
)

// DedupResult is the result of a dedup check run.
type DedupResult struct {
	Checked       int `json:"checked"`
	ExactDups     int `json:"exact_dups"`
	NearDups      int `json:"near_dups"`
	SemanticDups  int `json:"semantic_dups"`
	NewItems      int `json:"new_items"`
	Status        string `json:"status"`
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

	type item struct {
		id          uuid.UUID
		sourceID    uuid.UUID
		urlHash     *string
		simhash     *int64
		embedding   *string
		published   *interface{}
		createdAt   interface{}
	}
	var items []item
	for rows.Next() {
		var it item
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
				_, _ = d.pool.Exec(ctx, "UPDATE news_item SET quality_score = -1 WHERE id = $1", it.id)
				result.ExactDups++
				continue
			}
		}

		// Near dedup (simhash)
		if it.simhash != nil {
			nearRows, err := d.pool.Query(ctx, `
				SELECT id, simhash FROM news_item
				WHERE source_id = $1 AND simhash IS NOT NULL AND id != $2
				AND created_at < $3
			`, it.sourceID, it.id, it.createdAt)
			if err == nil {
				found := false
				for nearRows.Next() {
					var candID uuid.UUID
					var candSimhash int64
					nearRows.Scan(&candID, &candSimhash)
					if HammingDistance(*it.simhash, candSimhash) <= simhashThreshold {
						_, _ = d.pool.Exec(ctx, "UPDATE news_item SET quality_score = -0.5 WHERE id = $1", it.id)
						result.NearDups++
						found = true
						break
					}
				}
				nearRows.Close()
				if found {
					continue
				}
			}
		}

		// Semantic dedup (embedding cosine)
		if it.embedding != nil && *it.embedding != "" {
			var semanticID uuid.UUID
			var distance float64
			err := d.pool.QueryRow(ctx, `
				SELECT id, embedding <=> $1::vector AS distance
				FROM news_item
				WHERE source_id != $2 AND embedding IS NOT NULL AND id != $3
				AND created_at < $4
				ORDER BY distance LIMIT 1
			`, *it.embedding, it.sourceID, it.id, it.createdAt).Scan(&semanticID, &distance)
			if err == nil {
				cosineSim := 1 - distance
				if cosineSim >= cosineThreshold {
					_, _ = d.pool.Exec(ctx, "UPDATE news_item SET quality_score = -0.3 WHERE id = $1", it.id)
					result.SemanticDups++
					continue
				}
			}
		}
	}

	result.NewItems = result.Checked - result.ExactDups - result.NearDups - result.SemanticDups
	slog.Info("dedup complete", "checked", result.Checked, "exact", result.ExactDups,
		"near", result.NearDups, "semantic", result.SemanticDups)
	return result, nil
}