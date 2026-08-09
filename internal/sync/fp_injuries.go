package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/markis/fantasy-football-engine/internal/config"
	"github.com/markis/fantasy-football-engine/internal/db"
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

const fpInjuriesURL = "https://api.fantasypros.com/public/v2/json/nfl/injuries"

// FPInjuriesResult is the result of an injury sync.
type FPInjuriesResult struct {
	InjuriesFetched  int    `json:"injuries_fetched"`
	PlayersUpdated   int    `json:"players_updated"`
	Unmatched        int    `json:"unmatched"`
	Status           string `json:"status"`
}

// Sync fetches FantasyPros injury data and updates the player table.
func (s *FPInjuriesSyncer) Sync(ctx context.Context) (*FPInjuriesResult, error) {
	result := &FPInjuriesResult{Status: "ok"}

	apiKey, err := config.PassShow(s.cfg.FantasyPros.APIKeyPass)
	if err != nil {
		return nil, fmt.Errorf("get FP API key: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", fpInjuriesURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("Accept", "application/json")
	q := req.URL.Query()
	q.Add("limit", "500")
	req.URL.RawQuery = q.Encode()

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("FP injuries request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("FP injuries HTTP %d: %s", resp.StatusCode, string(body))
	}

	var apiResp struct {
		Injuries []map[string]interface{} `json:"injuries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("decode FP injuries: %w", err)
	}
	items := apiResp.Injuries
	result.InjuriesFetched = len(items)

	// Build player lookup maps
	rows, err := s.pool.Query(ctx,
		"SELECT id, sleeper_player_id, yahoo_id, lower(full_name), team FROM player WHERE active = true")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byYahoo := make(map[string]interface{})
	byNameTeam := make(map[string]interface{})
	for rows.Next() {
		var id interface{}
		var sleeperID, yahooID, fullName, team *string
		if err := rows.Scan(&id, &sleeperID, &yahooID, &fullName, &team); err != nil {
			continue
		}
		if yahooID != nil && *yahooID != "" {
			byYahoo[*yahooID] = id
		}
		if fullName != nil && team != nil {
			byNameTeam[*fullName+"|"+*team] = id
		}
	}

	for _, it := range items {
		var pid interface{}
		yid := fmt.Sprint(it["yahoo_id"])
		if yid != "" && yid != "<nil>" {
			if v, ok := byYahoo[yid]; ok {
				pid = v
			}
		}
		if pid == nil {
			nameKey := strings.ToLower(fmt.Sprint(it["name"])) + "|" + fmt.Sprint(it["team_id"])
			if v, ok := byNameTeam[nameKey]; ok {
				pid = v
			}
		}
		if pid == nil {
			result.Unmatched++
			continue
		}

		status := fmt.Sprint(it["status"])
		injuryType := fmt.Sprint(it["injury_type"])
		if injuryType == "<nil>" {
			injuryType = fmt.Sprint(it["practice_report_injury_type"])
		}
		if injuryType == "<nil>" {
			injuryType = ""
		}
		comment := fmt.Sprint(it["comment"])
		if comment == "<nil>" {
			comment = ""
		}
		practice := latestPractice(it)
		practiceDesc := fmt.Sprint(it["practice_report_injury_type"])
		if practiceDesc == "<nil>" {
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
			slog.Warn("update injury", "err", err)
			continue
		}
		result.PlayersUpdated++
	}

	slog.Info("FP injuries sync complete", "fetched", result.InjuriesFetched, "updated", result.PlayersUpdated)
	return result, nil
}

func latestPractice(item map[string]interface{}) string {
	for _, k := range []string{"practice_3", "practice_2", "practice_1"} {
		if v, ok := item[k]; ok && v != nil {
			s := fmt.Sprint(v)
			if s != "" && s != "<nil>" {
				return s
			}
		}
	}
	return ""
}