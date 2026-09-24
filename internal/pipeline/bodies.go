package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"

	"ff-engine/internal/db"
	"ff-engine/internal/htmlx"
	"ff-engine/internal/telemetry"
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
	bodyUserAgent        = "Mozilla/5.0 (X11; Linux x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36"

	// maxBodyFetchBytes caps how much of an article page is read into
	// memory; anything larger can't be a usable article body.
	maxBodyFetchBytes = 5 << 20 // 5 MiB
)

var botBlockedHosts = map[string]bool{
	"www.espn.com": true,
}

// BodyFetchResult is the result of a body fetch batch.
type BodyFetchResult struct {
	Checked int    `json:"checked"`
	Fetched int    `json:"fetched"`
	Skipped int    `json:"skipped"`
	Status  string `json:"status"`
}

// pendingItem is a news_item row awaiting a full-body fetch.
type pendingItem struct {
	id      uuid.UUID
	url     string
	title   string
	summary string
	content string
}

// FetchBatch fetches bodies for pending items.
func (b *BodyFetcher) FetchBatch(ctx context.Context, limit int) (*BodyFetchResult, error) {
	// Reset stale 'fetching' items
	if _, err := b.pool.Exec(ctx, `
		UPDATE news_item SET body_fetch_status = 'pending'
		WHERE body_fetch_status = 'fetching'
		  AND body_fetched_at < now() - interval '10 minutes'
	`); err != nil {
		slog.Warn("reset stale fetch status", "err", err)
	}

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

	var items []pendingItem
	for rows.Next() {
		var it pendingItem
		var urlStr, title, summary, content *string
		if err := rows.Scan(&it.id, &urlStr, &title, &summary, &content); err != nil {
			return nil, fmt.Errorf("scan pending body row: %w", err)
		}
		if urlStr != nil {
			it.url = *urlStr
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

	for i := range items {
		item := &items[i]
		// Mark as fetching
		if _, err := b.pool.Exec(ctx, `
			UPDATE news_item SET body_fetch_status = 'fetching',
				body_fetch_attempts = body_fetch_attempts + 1, body_fetched_at = now()
			WHERE id = $1 AND body_fetch_status = 'pending'
		`, item.id); err != nil {
			slog.Warn("mark fetching", "id", item.id, "err", err)
		}

		status := b.processItem(ctx, item)
		if status == statusSkipped {
			if _, err := b.pool.Exec(ctx,
				"UPDATE news_item SET body_fetch_status = 'skipped', body_fetched_at = now() WHERE id = $1",
				item.id); err != nil {
				slog.Warn("mark skipped", "id", item.id, "err", err)
			}
			result.Skipped++
		} else {
			result.Fetched++
		}
	}

	slog.Info("body fetch complete", "checked", result.Checked, "fetched", result.Fetched, "skipped", result.Skipped)
	return result, nil
}

func (b *BodyFetcher) processItem(ctx context.Context, item *pendingItem) string {
	parsed, err := url.Parse(item.url)
	if err != nil {
		return statusSkipped
	}
	if botBlockedHosts[parsed.Host] {
		return statusSkipped
	}

	httpClient := telemetry.NewHTTPClient(20 * time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, item.url, http.NoBody)
	if err != nil {
		return statusSkipped
	}
	req.Header.Set("User-Agent", bodyUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := httpClient.Do(req)
	if err != nil {
		return statusSkipped
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return statusSkipped
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyFetchBytes))
	if err != nil || len(bodyBytes) < 500 {
		return statusSkipped
	}

	htmlStr := string(bodyBytes)
	contentText := htmlx.ExtractText(htmlStr)
	if len(contentText) < minBodyChars {
		return statusSkipped
	}

	// Only overwrite content if the newly fetched body is longer than what
	// we already have — but the status update must still happen, or the row
	// is left stuck at 'fetching' (set by FetchBatch before this call) until
	// the staleness reset kicks it back to 'pending' and it's retried again.
	if len(contentText) <= len(item.content) {
		_, execErr := b.pool.Exec(ctx,
			"UPDATE news_item SET body_fetch_status = 'fetched', body_fetched_at = now() WHERE id = $1",
			item.id)
		if execErr != nil {
			return statusSkipped
		}
		return statusFetched
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
		return statusSkipped
	}
	return statusFetched
}
