package sleeper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/markis/fantasy-football-engine/internal/models"
)

// Client is a Sleeper Fantasy Football API client.
type Client struct {
	baseURL string
	client  *http.Client
	cache   map[string]cacheEntry
	mu      sync.RWMutex
}

type cacheEntry struct {
	data    interface{}
	expires time.Time
}

// New creates a new Sleeper API client with per-run caching.
func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 30 * time.Second},
		cache:   make(map[string]cacheEntry),
	}
}

// get fetches JSON from the Sleeper API with caching and retry.
func (c *Client) get(ctx context.Context, path string, target interface{}) error {
	c.mu.RLock()
	if entry, ok := c.cache[path]; ok && time.Now().Before(entry.expires) {
		c.mu.RUnlock()
		// Deep-copy via re-marshal is complex; for our use the cache is per-run
		// and callers don't mutate the returned slice, so this is acceptable.
		raw, _ := json.Marshal(entry.data)
		c.mu.RUnlock()
		return json.Unmarshal(raw, target)
	}
	c.mu.RUnlock()

	url := c.baseURL + "/" + path
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("sleeper GET %s: HTTP %d: %s", path, resp.StatusCode, string(body))
			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}
		err = json.NewDecoder(resp.Body).Decode(target)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("decode sleeper response %s: %w", path, err)
		}
		// Cache the result (re-marshal for storage)
		raw, _ := json.Marshal(target)
		var stored interface{}
		json.Unmarshal(raw, &stored)
		c.mu.Lock()
		c.cache[path] = cacheEntry{data: stored, expires: time.Now().Add(5 * time.Minute)}
		c.mu.Unlock()
		return nil
	}
	return fmt.Errorf("sleeper GET %s failed after 3 attempts: %w", path, lastErr)
}

// GetNFLState returns the current NFL state.
func (c *Client) GetNFLState(ctx context.Context) (*models.NFLState, error) {
	var state models.NFLState
	if err := c.get(ctx, "state/nfl", &state); err != nil {
		return nil, err
	}
	return &state, nil
}

// GetUserInfo returns user info by username or user ID.
func (c *Client) GetUserInfo(ctx context.Context, usernameOrID string) (map[string]interface{}, error) {
	var result map[string]interface{}
	if err := c.get(ctx, "user/"+usernameOrID, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetUserLeagues returns a user's leagues for a season.
func (c *Client) GetUserLeagues(ctx context.Context, userID, season string) ([]map[string]interface{}, error) {
	var result []map[string]interface{}
	if err := c.get(ctx, fmt.Sprintf("user/%s/leagues/nfl/%s", userID, season), &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetLeagueInfo returns league info.
func (c *Client) GetLeagueInfo(ctx context.Context, leagueID string) (map[string]interface{}, error) {
	var result map[string]interface{}
	if err := c.get(ctx, "league/"+leagueID, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetLeagueRosters returns rosters for a league.
func (c *Client) GetLeagueRosters(ctx context.Context, leagueID string) ([]map[string]interface{}, error) {
	var result []map[string]interface{}
	if err := c.get(ctx, "league/"+leagueID+"/rosters", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetLeagueUsers returns users/managers for a league.
func (c *Client) GetLeagueUsers(ctx context.Context, leagueID string) ([]map[string]interface{}, error) {
	var result []map[string]interface{}
	if err := c.get(ctx, "league/"+leagueID+"/users", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetLeagueMatchups returns matchups for a league/week.
func (c *Client) GetLeagueMatchups(ctx context.Context, leagueID string, week int) ([]map[string]interface{}, error) {
	var result []map[string]interface{}
	if err := c.get(ctx, fmt.Sprintf("league/%s/matchups/%d", leagueID, week), &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetLeagueTransactions returns transactions for a league/week.
func (c *Client) GetLeagueTransactions(ctx context.Context, leagueID string, week int) ([]map[string]interface{}, error) {
	var result []map[string]interface{}
	if err := c.get(ctx, fmt.Sprintf("league/%s/transactions/%d", leagueID, week), &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetLeagueDrafts returns drafts for a league.
func (c *Client) GetLeagueDrafts(ctx context.Context, leagueID string) ([]map[string]interface{}, error) {
	var result []map[string]interface{}
	if err := c.get(ctx, "league/"+leagueID+"/drafts", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetLeagueTradedPicks returns traded picks for a league.
func (c *Client) GetLeagueTradedPicks(ctx context.Context, leagueID string) ([]map[string]interface{}, error) {
	var result []map[string]interface{}
	if err := c.get(ctx, "league/"+leagueID+"/traded_picks", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetDraftInfo returns draft info.
func (c *Client) GetDraftInfo(ctx context.Context, draftID string) (map[string]interface{}, error) {
	var result map[string]interface{}
	if err := c.get(ctx, "draft/"+draftID, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetDraftPicks returns picks for a draft.
func (c *Client) GetDraftPicks(ctx context.Context, draftID string) ([]map[string]interface{}, error) {
	var result []map[string]interface{}
	if err := c.get(ctx, "draft/"+draftID+"/picks", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetTrendingPlayers returns trending players.
func (c *Client) GetTrendingPlayers(ctx context.Context, trendType string, lookbackHours, limit int) ([]map[string]interface{}, error) {
	var result []map[string]interface{}
	if err := c.get(ctx, fmt.Sprintf("players/nfl/trending/%s?lookback_hours=%d&limit=%d", trendType, lookbackHours, limit), &result); err != nil {
		return nil, err
	}
	return result, nil
}

// FetchPlayerDump fetches the full Sleeper NFL player database (~16MB JSON).
// This is NOT cached due to size.
func (c *Client) FetchPlayerDump(ctx context.Context) (map[string]map[string]interface{}, error) {
	url := c.baseURL + "/players/nfl"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create player dump request: %w", err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch player dump: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("player dump HTTP %d: %s", resp.StatusCode, string(body))
	}
	var result map[string]map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode player dump: %w", err)
	}
	return result, nil
}

// CurrentSeason returns the current NFL season string.
func (c *Client) CurrentSeason(ctx context.Context) string {
	state, err := c.GetNFLState(ctx)
	if err != nil || state.Season == "" {
		return "2026"
	}
	return state.Season
}

// GetRaw fetches a raw path from the Sleeper API and returns the JSON as-is.
// Used by the corpus publisher which needs flexible access.
func (c *Client) GetRaw(ctx context.Context, path string) (interface{}, error) {
	url := c.baseURL + "/" + path
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("sleeper GET %s: HTTP %d: %s", path, resp.StatusCode, string(body))
	}
	var result interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result, nil
}

// CurrentWeek returns the current NFL week (1-18); 0 if offseason.
func (c *Client) CurrentWeek(ctx context.Context) int {
	state, err := c.GetNFLState(ctx)
	if err != nil {
		return 0
	}
	if state.SeasonType == "regular" || state.SeasonType == "post" || state.SeasonType == "pre" {
		w := state.Week
		if w < 0 {
			w = 0
		}
		if w > 18 {
			w = 18
		}
		return w
	}
	return 0
}