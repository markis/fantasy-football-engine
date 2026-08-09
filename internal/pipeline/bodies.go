package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/markis/fantasy-football-engine/internal/db"
	"github.com/markis/fantasy-football-engine/internal/htmlx"
)

// BodyFetcher downloads full article bodies for news items.
type BodyFetcher struct {
	pool *db.Pool
}

// NewBodyFetcher creates a new body fetcher.
func NewBodyFetcher(pool *db.Pool) *BodyFetcher {
	return &BodyFetcher{pool: pool}
}

const (
	maxBodyAttempts      = 3
	staleFetchingMinutes = 10
	minBodyChars         = 200
)

var botBlockedHosts = map[string]bool{
	"www.espn.com": true,
}

const bodyUserAgent = "Mozilla/5.0 (X11; Linux x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36"

// BodyFetchResult is the result of a body fetch batch.
type BodyFetchResult struct {
	Checked int `json:"checked"`
	Fetched int `json:"fetched"`
	Skipped int `json:"skipped"`
	Status  string `json:"status"`
}

// FetchBatch fetches bodies for pending items.
func (b *BodyFetcher) FetchBatch(ctx context.Context, limit int) (*BodyFetchResult, error) {
	// Reset stale 'fetching' items
	_, _ = b.pool.Exec(ctx, `
		UPDATE news_item SET body_fetch_status = 'pending'
		WHERE body_fetch_status = 'fetching'
		  AND body_fetched_at < now() - interval '10 minutes'
	`)

	// Get pending items
	rows, err := b.pool.Query(ctx, `
		SELECT id, url, title, summary_short, content_text
		FROM news_item
		WHERE body_fetch_status = 'pending'
		  AND url IS NOT NULL AND url != ''
		  AND body_fetch_attempts < $1
		ORDER BY published_at DESC NULLS LAST, created_at DESC
		LIMIT $2
	`, maxBodyAttempts, limit)
	if err != nil {
		return nil, fmt.Errorf("query pending bodies: %w", err)
	}
	defer rows.Close()

	type pendingItem struct {
		id       uuid.UUID
		url      string
		title    string
		summary  string
		content  string
	}
	var items []pendingItem
	for rows.Next() {
		var it pendingItem
		var url, title, summary, content *string
		if err := rows.Scan(&it.id, &url, &it.title, &it.summary, &it.content); err != nil {
			return nil, err
		}
		if url != nil {
			it.url = *url
		}
		if title != nil {
			it.title = *title
		}
		if summary != nil {
			it.summary = *summary
		}
		if content != nil {
			it.content = *content
		}
		items = append(items, it)
	}

	result := &BodyFetchResult{Checked: len(items), Status: "ok"}

	for _, item := range items {
		// Mark as fetching
		_, _ = b.pool.Exec(ctx, `
			UPDATE news_item SET body_fetch_status = 'fetching',
				body_fetch_attempts = body_fetch_attempts + 1, body_fetched_at = now()
			WHERE id = $1 AND body_fetch_status = 'pending'
		`, item.id)

		status := b.processItem(ctx, item)
		if status == "skipped" {
			_, _ = b.pool.Exec(ctx,
				"UPDATE news_item SET body_fetch_status = 'skipped', body_fetched_at = now() WHERE id = $1",
				item.id)
			result.Skipped++
		} else {
			result.Fetched++
		}
	}

	slog.Info("body fetch complete", "checked", result.Checked, "fetched", result.Fetched, "skipped", result.Skipped)
	return result, nil
}

func (b *BodyFetcher) processItem(ctx context.Context, item struct {
	id      uuid.UUID
	url     string
	title   string
	summary string
	content string
}) string {
	parsed, err := url.Parse(item.url)
	if err != nil {
		return "skipped"
	}
	if botBlockedHosts[parsed.Host] {
		return "skipped"
	}

	httpClient := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", item.url, nil)
	if err != nil {
		return "skipped"
	}
	req.Header.Set("User-Agent", bodyUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "skipped"
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "skipped"
	}

	bodyBytes, err := ioReadAll(resp.Body)
	if err != nil || len(bodyBytes) < 500 {
		return "skipped"
	}

	htmlStr := string(bodyBytes)
	contentText := htmlx.ExtractText(htmlStr)
	if len(contentText) < minBodyChars {
		return "skipped"
	}

	// Only update if fetched body is longer than existing
	if len(contentText) <= len(item.content) {
		return "fetched"
	}

	h := sha256.Sum256([]byte(contentText))
	cHash := hex.EncodeToString(h[:])

	contentHTML := htmlx.ExtractMainHTML(htmlStr)

	_, err = b.pool.Exec(ctx, `
		UPDATE news_item SET
			content_text = $1, content_html = $2, content_hash = $3,
			body_fetch_status = 'fetched', body_fetched_at = now()
		WHERE id = $4
	`, contentText, contentHTML, cHash, item.id)
	if err != nil {
		return "skipped"
	}
	return "fetched"
}

// Ensure strings import is used
var _ = strings.TrimSpace