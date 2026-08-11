package sync

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"ff-engine/internal/config"
	"ff-engine/internal/db"
)

// FPInjuriesSyncer syncs FantasyPros injury data.
type FPInjuriesSyncer struct {
	pool   *db.Pool
	cfg    *config.Config
	client *http.Client
}

// NewFPInjuriesSyncer creates a new FP injuries syncer.
func NewFPInjuriesSyncer(pool *db.Pool, cfg *config.Config) *FPInjuriesSyncer {
	return &FPInjuriesSyncer{pool: pool, cfg: cfg, client: &http.Client{Timeout: 20 * time.Second}}
}

const (
	fpInjuriesURL = "https://api.fantasypros.com/public/v2/json/nfl/injuries"
)

// FPInjuriesResult is the result of an injury sync.
type FPInjuriesResult struct {
	InjuriesFetched int    `json:"injuriesFetched"`
	PlayersUpdated  int    `json:"playersUpdated"`
	Unmatched       int    `json:"unmatched"`
	Status          string `json:"status"`
}

// Sync fetches FantasyPros injury data and updates the player table.
func (s *FPInjuriesSyncer) Sync(ctx context.Context) (*FPInjuriesResult, error) {
	result := &FPInjuriesResult{Status: "ok"}

	items, err := s.fetchFPInjuriesData(ctx)
	if err != nil {
		return nil, err
	}
	result.InjuriesFetched = len(items)

	lookups, err := s.playerLookupMaps(ctx)
	if err != nil {
		return nil, err
	}

	for _, it := range items {
		pid, ok := matchInjuryPlayer(it, lookups.byYahoo, lookups.byNameTeam)
		if !ok {
			result.Unmatched++
			continue
		}
		if err := s.updatePlayerInjury(ctx, it, pid); err != nil {
			slog.Warn("update injury", "err", err)
			continue
		}
		result.PlayersUpdated++
	}

	slog.Info("FP injuries sync complete", "fetched", result.InjuriesFetched, "updated", result.PlayersUpdated)
	return result, nil
}

// fetchFPInjuriesData fetches and decodes the raw FantasyPros injuries
// payload.
func (s *FPInjuriesSyncer) fetchFPInjuriesData(ctx context.Context) ([]map[string]any, error) {
	var apiResp struct {
		Injuries []map[string]any `json:"injuries"`
	}
	if err := fetchFPJSON(ctx, s.client, s.cfg, fpInjuriesURL, errFPInjuriesHTTP,
		"FP injuries request", "decode FP injuries", &apiResp); err != nil {
		return nil, err
	}
	return apiResp.Injuries, nil
}

// injuryPlayerLookups holds the lookups built by playerLookupMaps.
type injuryPlayerLookups struct {
	byYahoo    map[string]any
	byNameTeam map[string]any
}

// playerLookupMaps builds the yahoo-id and (lower(name)|team) lookups used to
// match FantasyPros injury records against the local player table.
func (s *FPInjuriesSyncer) playerLookupMaps(ctx context.Context) (injuryPlayerLookups, error) {
	rows, err := s.pool.Query(ctx,
		"SELECT id, sleeper_player_id, yahoo_id, lower(full_name), team FROM player WHERE active = true")
	if err != nil {
		return injuryPlayerLookups{}, fmt.Errorf("query active players: %w", err)
	}
	defer rows.Close()

	lookups := injuryPlayerLookups{
		byYahoo:    make(map[string]any),
		byNameTeam: make(map[string]any),
	}
	for rows.Next() {
		var id any
		var sleeperID, yahooID, fullName, team *string
		if err := rows.Scan(&id, &sleeperID, &yahooID, &fullName, &team); err != nil {
			continue
		}
		if yahooID != nil && *yahooID != "" {
			lookups.byYahoo[*yahooID] = id
		}
		if fullName != nil && team != nil {
			lookups.byNameTeam[*fullName+"|"+*team] = id
		}
	}
	return lookups, nil
}

// matchInjuryPlayer resolves an injury record to a player id, preferring a
// yahoo-id match and falling back to a (lower(name)|team) match.
func matchInjuryPlayer(it, byYahoo, byNameTeam map[string]any) (any, bool) {
	yid := fmt.Sprint(it["yahoo_id"])
	if yid != "" && yid != nilStr {
		if v, ok := byYahoo[yid]; ok {
			return v, true
		}
	}
	nameKey := strings.ToLower(fmt.Sprint(it["name"])) + "|" + fmt.Sprint(it["team_id"])
	if v, ok := byNameTeam[nameKey]; ok {
		return v, true
	}
	return nil, false
}

// updatePlayerInjury writes a single injury record's status fields onto the
// matched player row.
func (s *FPInjuriesSyncer) updatePlayerInjury(ctx context.Context, it map[string]any, pid any) error {
	status := fmt.Sprint(it["status"])
	injuryType := fmt.Sprint(it["injury_type"])
	if injuryType == nilStr {
		injuryType = fmt.Sprint(it["practice_report_injury_type"])
	}
	if injuryType == nilStr {
		injuryType = ""
	}
	comment := fmt.Sprint(it["comment"])
	if comment == nilStr {
		comment = ""
	}
	practice := latestPractice(it)
	practiceDesc := fmt.Sprint(it["practice_report_injury_type"])
	if practiceDesc == nilStr {
		practiceDesc = ""
	}

	_, err := s.pool.Exec(ctx, `
		UPDATE player SET
			injury_status = NULLIF($1, ''),
			injury_body_part = NULLIF($2, ''),
			injury_notes = NULLIF($3, ''),
			practice_participation = NULLIF($4, ''),
			practice_description = NULLIF($5, ''),
			updated_at = now()
		WHERE id = $6
	`, status, injuryType, comment, practice, practiceDesc, pid)
	if err != nil {
		return fmt.Errorf("update player %s injury status: %w", pid, err)
	}
	return nil
}

func latestPractice(item map[string]any) string {
	for _, k := range []string{"practice_3", "practice_2", "practice_1"} {
		if v, ok := item[k]; ok && v != nil {
			s := fmt.Sprint(v)
			if s != "" && s != nilStr {
				return s
			}
		}
	}
	return ""
}
