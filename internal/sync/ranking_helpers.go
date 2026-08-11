package sync

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"ff-engine/internal/db"
)

// sid2pidMap builds a sleeper_player_id -> player.id lookup used to match
// externally-sourced rankings/values against the local player table.
func sid2pidMap(ctx context.Context, pool *db.Pool) (map[string]uuid.UUID, error) {
	sid2pid := make(map[string]uuid.UUID)
	rows, err := pool.Query(ctx, "SELECT id, sleeper_player_id FROM player")
	if err != nil {
		return nil, fmt.Errorf("query players: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var sid string
		if err := rows.Scan(&id, &sid); err != nil {
			continue
		}
		if sid != "" {
			sid2pid[sid] = id
		}
	}
	return sid2pid, nil
}

// upsertParts holds the SQL fragments produced by buildUpsertParts.
type upsertParts struct {
	colNames     string
	placeholders string
	updates      string
}

// buildUpsertParts builds the column-list, placeholder-list, and
// "col = EXCLUDED.col" fragments for a dynamic set of columns, with
// placeholders numbered starting at $offset.
func buildUpsertParts(cols []string, offset int) upsertParts {
	var colNamesSb, placeholdersSb, updatesSb strings.Builder
	for i, col := range cols {
		if i > 0 {
			colNamesSb.WriteString(", ")
			placeholdersSb.WriteString(", ")
			updatesSb.WriteString(", ")
		}
		colNamesSb.WriteString(col)
		placeholdersSb.WriteString("$" + strconv.Itoa(i+offset))
		updatesSb.WriteString(col + " = EXCLUDED." + col)
	}
	return upsertParts{
		colNames:     colNamesSb.String(),
		placeholders: placeholdersSb.String(),
		updates:      updatesSb.String(),
	}
}

// deleteStaleRankings removes player_ranking rows for the given source/market
// that are not present in freshIDs. It is a no-op when freshIDs is empty, to
// avoid wiping existing rows on an empty or failed sync.
func deleteStaleRankings(ctx context.Context, pool *db.Pool, source string, market int, freshIDs []uuid.UUID) (int, error) {
	if len(freshIDs) == 0 {
		return 0, nil
	}
	ct, err := pool.Exec(ctx,
		"DELETE FROM player_ranking WHERE source = $1 AND market = $2 AND NOT (player_id = ANY($3))",
		source, market, freshIDs)
	if err != nil {
		return 0, fmt.Errorf("delete stale rankings for %s/%d: %w", source, market, err)
	}
	return int(ct.RowsAffected()), nil
}
