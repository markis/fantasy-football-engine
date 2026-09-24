package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math"

	"github.com/google/uuid"

	"ff-engine/internal/db"
	"ff-engine/internal/util"
)

// Clusterer matches items to existing story clusters or creates new ones.
type Clusterer struct {
	pool *db.Pool
}

// NewClusterer creates a new clusterer.
func NewClusterer(pool *db.Pool) *Clusterer {
	return &Clusterer{pool: pool}
}

const clusterCosineThreshold = 0.78

const (
	statusCreated     = "created"
	statusNotFound    = "not_found"
	statusNotAssigned = "not_assigned"
	statusReused      = "reused"
)

// ClusterResult is the result of a clustering run.
type ClusterResult struct {
	ClustersCreated int    `json:"clustersCreated"`
	ClustersReused  int    `json:"clustersReused"`
	ItemsAssigned   int    `json:"itemsAssigned"`
	Status          string `json:"status"`
}

// AssignBatch clusters up to limit unclustered items. The query is bounded:
// each item triggers a nearest-neighbor scan over every embedding in the
// table, so an unbounded run (a backlog after an outage, an initial
// backfill) pins both CPU and memory for hours.
func (c *Clusterer) AssignBatch(ctx context.Context, limit int) (*ClusterResult, error) {
	if limit <= 0 {
		limit = defaultClusterBatch
	}
	rows, err := c.pool.Query(ctx,
		"SELECT id FROM news_item WHERE cluster_id IS NULL ORDER BY created_at DESC LIMIT $1", limit)
	if err != nil {
		return nil, fmt.Errorf("query unclustered items: %w", err)
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

	result := &ClusterResult{Status: "ok"}
	for _, id := range itemIDs {
		action, err := c.processItem(ctx, id)
		if err != nil {
			slog.Warn("cluster item", "id", id, "err", err)
			continue
		}
		result.ItemsAssigned++
		if action == statusCreated {
			result.ClustersCreated++
		} else {
			result.ClustersReused++
		}
	}
	slog.Info("cluster batch complete", "created", result.ClustersCreated,
		"reused", result.ClustersReused, "assigned", result.ItemsAssigned)
	return result, nil
}

func (c *Clusterer) processItem(ctx context.Context, itemID uuid.UUID) (string, error) {
	var title *string
	var sourceID uuid.UUID
	err := c.pool.QueryRow(ctx,
		"SELECT title, source_id FROM news_item WHERE id = $1", itemID,
	).Scan(&title, &sourceID)
	if err != nil {
		return statusNotFound, fmt.Errorf("scan news item %s: %w", itemID, err)
	}

	chunkVecs, err := c.fetchItemChunkVectors(ctx, itemID)
	if err != nil {
		return statusNotAssigned, err
	}
	if len(chunkVecs) > 0 {
		reused, err := c.tryReuseCluster(ctx, itemID, chunkVecs)
		if err != nil {
			return statusNotAssigned, err
		}
		if reused {
			return statusReused, nil
		}
	}

	// Create new cluster
	if err := c.createCluster(ctx, itemID, util.StrOrEmpty(title), sourceID); err != nil {
		return statusNotAssigned, err
	}
	return statusCreated, nil
}

// fetchItemChunkVectors loads the embedding vectors for an item's chunks as
// text, suitable for casting to vector in a similarity query.
func (c *Clusterer) fetchItemChunkVectors(ctx context.Context, itemID uuid.UUID) ([]string, error) {
	rows, err := c.pool.Query(ctx,
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

// tryReuseCluster searches already-clustered news_chunk embeddings for the
// nearest match to any of the item's chunk vectors. Uses the HNSW index on
// news_chunk.embedding (one lookup per chunk vector). The best (smallest
// distance) cluster is assigned when cosine similarity meets the threshold.
func (c *Clusterer) tryReuseCluster(ctx context.Context, itemID uuid.UUID, chunkVecs []string) (bool, error) {
	var bestClusterID uuid.UUID
	bestDistance := math.MaxFloat64
	for _, vec := range chunkVecs {
		var clusterID uuid.UUID
		var distance float64
		err := c.pool.QueryRow(ctx, `
			SELECT sc.id, nc.embedding <=> $1::vector AS distance
			FROM news_chunk nc
			JOIN news_item ni ON ni.id = nc.news_item_id
			JOIN story_cluster sc ON sc.id = ni.cluster_id
			WHERE nc.embedding IS NOT NULL
			ORDER BY nc.embedding <=> $1::vector
			LIMIT 1
		`, vec).Scan(&clusterID, &distance)
		if err != nil {
			continue // no rows / scan error — try next chunk
		}
		if distance < bestDistance {
			bestDistance = distance
			bestClusterID = clusterID
		}
	}
	if bestDistance == math.MaxFloat64 {
		return false, nil // no existing clusters found
	}
	cosineSim := 1 - bestDistance
	if cosineSim < clusterCosineThreshold {
		return false, nil // no match
	}
	if err := c.assignToCluster(ctx, itemID, bestClusterID); err != nil {
		return false, err
	}
	return true, nil
}

func (c *Clusterer) assignToCluster(ctx context.Context, itemID, clusterID uuid.UUID) error {
	if _, err := c.pool.Exec(ctx, "UPDATE news_item SET cluster_id = $1 WHERE id = $2", clusterID, itemID); err != nil {
		return fmt.Errorf("assign item to cluster: %w", err)
	}
	if _, err := c.pool.Exec(ctx, `
		UPDATE story_cluster SET last_seen_at = now(),
			item_ids = array_append(item_ids, $1)
		WHERE id = $2 AND NOT $1 = ANY(item_ids)
	`, itemID, clusterID); err != nil {
		slog.Warn("update cluster items", "err", err)
	}
	c.updateClusterScore(ctx, clusterID)
	return nil
}

func (c *Clusterer) createCluster(ctx context.Context, itemID uuid.UUID, title string, _ uuid.UUID) error {
	clusterKey := clusterKeyHash(title, itemID.String())
	var clusterID uuid.UUID
	err := c.pool.QueryRow(ctx, `
		INSERT INTO story_cluster (cluster_key, representative_title, item_ids)
		VALUES ($1, $2, ARRAY[$3]::uuid[])
		ON CONFLICT (cluster_key) DO UPDATE
			SET item_ids = array_append(story_cluster.item_ids, EXCLUDED.item_ids[1])
		RETURNING id
	`, clusterKey, title, itemID).Scan(&clusterID)
	if err != nil {
		return fmt.Errorf("create cluster: %w", err)
	}
	if _, err := c.pool.Exec(ctx, "UPDATE news_item SET cluster_id = $1 WHERE id = $2", clusterID, itemID); err != nil {
		return fmt.Errorf("assign item to new cluster: %w", err)
	}
	if _, err := c.pool.Exec(ctx, `
		UPDATE story_cluster SET last_seen_at = now(),
			item_ids = array_append(item_ids, $1)
		WHERE id = $2 AND NOT $1 = ANY(item_ids)
	`, itemID, clusterID); err != nil {
		slog.Warn("update cluster items", "err", err)
	}
	c.updateClusterScore(ctx, clusterID)
	return nil
}

func (c *Clusterer) updateClusterScore(ctx context.Context, clusterID uuid.UUID) {
	score := c.computeImportanceScore(ctx, clusterID)
	if _, err := c.pool.Exec(ctx, "UPDATE story_cluster SET importance_score = $1 WHERE id = $2",
		score, clusterID); err != nil {
		slog.Warn("update cluster score", "err", err)
	}
}

func (c *Clusterer) computeImportanceScore(ctx context.Context, clusterID uuid.UUID) float64 {
	var itemCount *int
	var ageSecs *float64
	var sourceCount *int
	if err := c.pool.QueryRow(ctx, `
		SELECT array_length(sc.item_ids, 1),
		       EXTRACT(EPOCH FROM (now() - sc.first_seen_at))::float,
		       (SELECT COUNT(DISTINCT ni2.source_id)
		          FROM news_item ni2
		          WHERE ni2.cluster_id = sc.id)
		FROM story_cluster sc WHERE sc.id = $1
	`, clusterID).Scan(&itemCount, &ageSecs, &sourceCount); err != nil {
		slog.Warn("compute importance score", "err", err)
	}

	ic := 1
	if itemCount != nil {
		ic = *itemCount
	}
	as := 0.0
	if ageSecs != nil {
		as = *ageSecs
	}
	sc := 1
	if sourceCount != nil {
		sc = *sourceCount
	}

	sizeScore := math.Min(1.0, 0.3+0.2*math.Log2(math.Max(1, float64(ic))))
	ageDays := as / 86400
	recencyScore := math.Max(0.0, 1.0-(ageDays/7.0))
	diversityScore := math.Min(1.0, 0.3+0.25*math.Log2(math.Max(1, float64(sc))))

	return roundTo(0.4*sizeScore+0.3*recencyScore+0.3*diversityScore, 4)
}

func clusterKeyHash(title, itemID string) string {
	text := title
	if text == "" {
		text = itemID
	}
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:])[:16]
}

func roundTo(v float64, places int) float64 {
	mult := math.Pow(10, float64(places))
	return math.Round(v*mult) / mult
}
