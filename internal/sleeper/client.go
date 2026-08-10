package sleeper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/markis/fantasy-football-engine/internal/models"
)

var errSleeperHTTP = errors.New("sleeper HTTP error")

// Client is a Sleeper Fantasy Football API client.
type Client struct {
	baseURL string
	client  *http.Client
	cache   map[string]cacheEntry
	mu      sync.RWMutex
}

type cacheEntry struct {
	data    any
	expires time.Time
}

// cacheSweepThreshold triggers an expired-entry sweep once the cache grows
// past this many entries, so paths that are cached once but never looked up
// again (a league that stops being synced, say) don't accumulate forever.
const cacheSweepThreshold = 200

// New creates a new Sleeper API client. Response caching lives for the
// whole process — this client is constructed once at daemon startup and
// kept for the daemon's entire lifetime, not just a single run.
func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 30 * time.Second},
		cache:   make(map[string]cacheEntry),
	}
}

// get fetches JSON from the Sleeper API with caching and retry.
func (c *Client) get(ctx context.Context, path string, target any) error {
	c.mu.RLock()
	entry, ok := c.cache[path]
	c.mu.RUnlock()
	if ok {
		if time.Now().Before(entry.expires) {
			// Deep-copy via re-marshal is complex; for our use the cache is per-run
			// and callers don't mutate the returned slice, so this is acceptable.
			raw, _ := json.Marshal(entry.data)
			return json.Unmarshal(raw, target)
		}
		// Expired — evict now rather than leaving it for the size-triggered sweep.
		c.mu.Lock()
		delete(c.cache, path)
		c.mu.Unlock()
	}

	resp, err := c.doGet(ctx, path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return fmt.Errorf("decode sleeper response %s: %w", path, err)
	}
	// Cache the result (re-marshal for storage)
	raw, _ := json.Marshal(target)
	var stored any
	json.Unmarshal(raw, &stored)
	c.mu.Lock()
	c.cache[path] = cacheEntry{data: stored, expires: time.Now().Add(5 * time.Minute)}
	if len(c.cache) > cacheSweepThreshold {
		c.sweepExpiredLocked()
	}
	c.mu.Unlock()
	return nil
}

// sweepExpiredLocked removes expired cache entries. Callers must hold c.mu
// for writing.
func (c *Client) sweepExpiredLocked() {
	now := time.Now()
	for k, v := range c.cache {
		if now.After(v.expires) {
			delete(c.cache, k)
		}
	}
}

// doGet performs an HTTP GET against the Sleeper API with the same
// retry-with-backoff behavior as get(), but returns the raw response
// instead of decoding+caching it — used for endpoints too large to cache
// (FetchPlayerDump's ~16MB player database).
func (c *Client) doGet(ctx context.Context, path string) (*http.Response, error) {
	url := c.baseURL + "/" + path
	var lastErr error
	for attempt := range 3 {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
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
			lastErr = fmt.Errorf("%s: %w (%d): %s", path, errSleeperHTTP, resp.StatusCode, string(body))
			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}
		return resp, nil
	}
	return nil, fmt.Errorf("sleeper GET %s failed after 3 attempts: %w", path, lastErr)
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
func (c *Client) GetUserInfo(ctx context.Context, usernameOrID string) (map[string]any, error) {
	var result map[string]any
	if err := c.get(ctx, "user/"+usernameOrID, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetUserLeagues returns a user's leagues for a season.
func (c *Client) GetUserLeagues(ctx context.Context, userID, season string) ([]map[string]any, error) {
	var result []map[string]any
	if err := c.get(ctx, fmt.Sprintf("user/%s/leagues/nfl/%s", userID, season), &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetLeagueInfo returns league info.
func (c *Client) GetLeagueInfo(ctx context.Context, leagueID string) (map[string]any, error) {
	var result map[string]any
	if err := c.get(ctx, "league/"+leagueID, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetLeagueRosters returns rosters for a league.
func (c *Client) GetLeagueRosters(ctx context.Context, leagueID string) ([]map[string]any, error) {
	var result []map[string]any
	if err := c.get(ctx, "league/"+leagueID+"/rosters", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetLeagueUsers returns users/managers for a league.
func (c *Client) GetLeagueUsers(ctx context.Context, leagueID string) ([]map[string]any, error) {
	var result []map[string]any
	if err := c.get(ctx, "league/"+leagueID+"/users", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetLeagueMatchups returns matchups for a league/week.
func (c *Client) GetLeagueMatchups(ctx context.Context, leagueID string, week int) ([]map[string]any, error) {
	var result []map[string]any
	if err := c.get(ctx, fmt.Sprintf("league/%s/matchups/%d", leagueID, week), &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetLeagueTransactions returns transactions for a league/week.
func (c *Client) GetLeagueTransactions(ctx context.Context, leagueID string, week int) ([]map[string]any, error) {
	var result []map[string]any
	if err := c.get(ctx, fmt.Sprintf("league/%s/transactions/%d", leagueID, week), &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetLeagueDrafts returns drafts for a league.
func (c *Client) GetLeagueDrafts(ctx context.Context, leagueID string) ([]map[string]any, error) {
	var result []map[string]any
	if err := c.get(ctx, "league/"+leagueID+"/drafts", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetLeagueTradedPicks returns traded picks for a league.
func (c *Client) GetLeagueTradedPicks(ctx context.Context, leagueID string) ([]map[string]any, error) {
	var result []map[string]any
	if err := c.get(ctx, "league/"+leagueID+"/traded_picks", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetDraftInfo returns draft info.
func (c *Client) GetDraftInfo(ctx context.Context, draftID string) (map[string]any, error) {
	var result map[string]any
	if err := c.get(ctx, "draft/"+draftID, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetDraftPicks returns picks for a draft.
func (c *Client) GetDraftPicks(ctx context.Context, draftID string) ([]map[string]any, error) {
	var result []map[string]any
	if err := c.get(ctx, "draft/"+draftID+"/picks", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetTrendingPlayers returns trending players.
func (c *Client) GetTrendingPlayers(ctx context.Context, trendType string, lookbackHours, limit int) ([]map[string]any, error) {
	var result []map[string]any
	if err := c.get(ctx, fmt.Sprintf("players/nfl/trending/%s?lookback_hours=%d&limit=%d", trendType, lookbackHours, limit), &result); err != nil {
		return nil, err
	}
	return result, nil
}

// FetchPlayerDump fetches the full Sleeper NFL player database (~16MB JSON).
// This is NOT cached due to size.
func (c *Client) FetchPlayerDump(ctx context.Context) (map[string]map[string]any, error) {
	resp, err := c.doGet(ctx, "players/nfl")
	if err != nil {
		return nil, fmt.Errorf("fetch player dump: %w", err)
	}
	defer resp.Body.Close()
	var result map[string]map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode player dump: %w", err)
	}
	return result, nil
}

// CurrentSeason returns the current NFL season string.
func (c *Client) CurrentSeason(ctx context.Context) string {
	state, err := c.GetNFLState(ctx)
	if err != nil || state.Season == "" {
		fallback := nflSeasonFromDate(time.Now())
		slog.Warn("sleeper: falling back to date-derived season", "err", err, "season", fallback)
		return fallback
	}
	return state.Season
}

// nflSeasonFromDate estimates the NFL season year for a given date. The NFL
// season runs roughly March-February, so January/February dates belong to
// the season that started the previous calendar year.
func nflSeasonFromDate(t time.Time) string {
	year := t.Year()
	if t.Month() < time.March {
		year--
	}
	return strconv.Itoa(year)
}

// GetRaw fetches a raw path from the Sleeper API and returns the JSON as-is.
// Used by the corpus publisher which needs flexible access.
func (c *Client) GetRaw(ctx context.Context, path string) (any, error) {
	url := c.baseURL + "/" + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
		return nil, fmt.Errorf("%s: %w (%d): %s", path, errSleeperHTTP, resp.StatusCode, string(body))
	}
	var result any
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
		w := max(state.Week, 0)
		if w > 18 {
			w = 18
		}
		return w
	}
	return 0
}
