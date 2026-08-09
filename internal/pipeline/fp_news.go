package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/markis/fantasy-football-engine/internal/config"
	"github.com/markis/fantasy-football-engine/internal/db"
)

// FPNewsFetcher fetches FantasyPros player news via API.
type FPNewsFetcher struct {
	pool    *db.Pool
	cfg     *config.Config
	client  *http.Client
}

// NewFPNewsFetcher creates a new FantasyPros news fetcher.
func NewFPNewsFetcher(pool *db.Pool, cfg *config.Config) *FPNewsFetcher {
	return &FPNewsFetcher{
		pool:   pool,
		cfg:    cfg,
		client: &http.Client{Timeout: 20 * time.Second},
	}
}

const fpNewsURL = "https://api.fantasypros.com/public/v2/json/nfl/news"
const fpNewsSourceName = "FantasyPros Player News"

// FPNewsResult is the result of a FantasyPros news fetch.
type FPNewsResult struct {
	Ingested int    `json:"ingested"`
	Status   string `json:"status"`
}

// Fetch fetches the latest FantasyPros player news.
func (f *FPNewsFetcher) Fetch(ctx context.Context) (*FPNewsResult, error) {
	result := &FPNewsResult{Status: "ok"}

	apiKey, err := config.PassShow(f.cfg.FantasyPros.APIKeyPass)
	if err != nil {
		return nil, fmt.Errorf("get FP API key: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", fpNewsURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("Accept", "application/json")
	q := req.URL.Query()
	q.Add("limit", "500")
	req.URL.RawQuery = q.Encode()

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("FP news request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := ioReadAll(resp.Body)
		return nil, fmt.Errorf("FP news HTTP %d: %s", resp.StatusCode, string(body))
	}

	var apiResp struct {
		Injuries []map[string]interface{} `json:"injuries"`
		News     []map[string]interface{} `json:"news"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("decode FP news: %w", err)
	}

	// Ensure source exists
	sourceID, err := f.ensureSource(ctx)
	if err != nil {
		return nil, err
	}

	items := apiResp.News
	for _, item := range items {
		f.upsertNews(ctx, sourceID, item)
		result.Ingested++
	}

	slog.Info("FP news fetched", "ingested", result.Ingested)
	return result, nil
}

func (f *FPNewsFetcher) ensureSource(ctx context.Context) (uuid.UUID, error) {
	var sourceID uuid.UUID
	err := f.pool.QueryRow(ctx, "SELECT id FROM source WHERE name = $1", fpNewsSourceName).Scan(&sourceID)
	if err == nil {
		return sourceID, nil
	}
	err = f.pool.QueryRow(ctx, `
		INSERT INTO source (name, type, url, fetch_method, poll_interval_sec, is_active)
		VALUES ($1, 'api', $2, 'http', 3600, true) RETURNING id
	`, fpNewsSourceName, fpNewsURL).Scan(&sourceID)
	if err != nil {
		return uuid.Nil, err
	}
	return sourceID, nil
}

func (f *FPNewsFetcher) upsertNews(ctx context.Context, sourceID uuid.UUID, item map[string]interface{}) {
	id := getStr(item, "id")
	if id == "" {
		return
	}

	title := getStr(item, "title")
	desc := getStr(item, "desc")
	impact := getStr(item, "impact")
	created := getStr(item, "created")

	// Content: desc + impact
	content := desc
	if impact != "" {
		content = desc + "\n\n" + impact
	}

	// Published at
	var published *time.Time
	if created != "" {
		if t, err := time.Parse("2006-01-02 15:04:05", created); err == nil {
			t = t.UTC()
			published = &t
		}
	}

	cHash := contentHash(content)
	sh := SimhashCompute(content)

	// Check existing
	var existingID *uuid.UUID
	_ = f.pool.QueryRow(ctx,
		"SELECT id FROM news_item WHERE source_id = $1 AND external_id = $2",
		sourceID, id).Scan(&existingID)

	if existingID != nil {
		_, _ = f.pool.Exec(ctx, `
			UPDATE news_item SET title = $1, summary_short = $2, content_text = $3,
				content_hash = $4, simhash = $5, published_at = COALESCE($6, published_at),
				body_fetch_status = 'fetched', is_relevant = true, is_news = true,
				quality_score = 0.9, fetched_at = now()
			WHERE id = $7
		`, title, desc, content, cHash, sh, published, *existingID)
		return
	}

	_, err := f.pool.Exec(ctx, `
		INSERT INTO news_item
			(source_id, source_type, external_id, title, summary_short, content_text,
			 content_hash, simhash, published_at, fetched_at, body_fetch_status,
			 is_relevant, is_news, quality_score)
		VALUES ($1, 'api', $2, $3, $4, $5, $6, $7, $8, now(), 'fetched', true, true, 0.9)
	`, sourceID, id, title, desc, content, cHash, sh, published)
	if err != nil {
		slog.Warn("FP news upsert", "id", id, "err", err)
	}
}

func getStr(m map[string]interface{}, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}