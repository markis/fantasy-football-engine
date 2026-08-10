package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math"

	"github.com/google/uuid"
	"github.com/markis/fantasy-football-engine/internal/db"
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

// ClusterResult is the result of a clustering run.
type ClusterResult struct {
	ClustersCreated int    `json:"clusters_created"`
	ClustersReused  int    `json:"clusters_reused"`
	ItemsAssigned   int    `json:"items_assigned"`
	Status          string `json:"status"`
}

// AssignBatch clusters all unclustered items.
func (c *Clusterer) AssignBatch(ctx context.Context) (*ClusterResult, error) {
	rows, err := c.pool.Query(ctx,
		"SELECT id FROM news_item WHERE cluster_id IS NULL ORDER BY created_at DESC")
	if err != nil {
		return nil, fmt.Errorf("query unclustered items: %w", err)
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

	result := &ClusterResult{Status: "ok"}
	for _, id := range itemIDs {
		action, err := c.processItem(ctx, id)
		if err != nil {
			slog.Warn("cluster item", "id", id, "err", err)
			continue
		}
		result.ItemsAssigned++
		if action == "created" {
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
	var embeddingText *string
	var sourceID uuid.UUID
	err := c.pool.QueryRow(ctx,
		"SELECT title, embedding::text, source_id FROM news_item WHERE id = $1", itemID,
	).Scan(&title, &embeddingText, &sourceID)
	if err != nil {
		return "not_found", err
	}

	if embeddingText != nil && *embeddingText != "" {
		// Find matching cluster
		var clusterID uuid.UUID
		var repTitle *string
		var distance float64
		err := c.pool.QueryRow(ctx, `
			SELECT sc.id, sc.representative_title,
			       ni.embedding <=> $1::vector AS distance
			FROM story_cluster sc
			JOIN news_item ni ON ni.cluster_id = sc.id
			WHERE ni.embedding IS NOT NULL
			ORDER BY distance LIMIT 1
		`, *embeddingText).Scan(&clusterID, &repTitle, &distance)
		if err == nil {
			cosineSim := 1 - distance
			if cosineSim >= clusterCosineThreshold {
				if err := c.assignToCluster(ctx, itemID, clusterID); err != nil {
					return "not_assigned", err
				}
				return "reused", nil
			}
		}
	}

	// Create new cluster
	if err := c.createCluster(ctx, itemID, ptrStr(title), sourceID); err != nil {
		return "not_assigned", err
	}
	return "created", nil
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
