package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/markis/fantasy-football-engine/internal/db"
	"github.com/mmcdole/gofeed"
)

var errFeedHTTP = errors.New("feed HTTP error")

const (
	userAgent = "ZeroClawFantasyBot/0.1 (homelab)"
)

// RSSFetcher fetches and ingests RSS/Atom feeds into the database.
type RSSFetcher struct {
	pool *db.Pool
}

// NewRSSFetcher creates a new RSS fetcher.
func NewRSSFetcher(pool *db.Pool) *RSSFetcher {
	return &RSSFetcher{pool: pool}
}

// FetchResult is the result of fetching one feed.
type FetchResult struct {
	SourceID        string `json:"source_id"`
	URL             string `json:"url"`
	ItemsFetched    int    `json:"items_fetched"`
	ItemsNew        int    `json:"items_new"`
	ItemsUpdated    int    `json:"items_updated"`
	ItemsSkippedOld int    `json:"items_skipped_old"`
	HTTPStatus      int    `json:"http_status"`
	Status          string `json:"status"`
}

// Fetch fetches a single RSS feed, stores the raw document, and upserts news items.
func (f *RSSFetcher) Fetch(ctx context.Context, feedURL string, maxAgeDays int) (*FetchResult, error) {
	result := &FetchResult{URL: feedURL, Status: "ok"}

	// Look up source by URL
	var sourceID uuid.UUID
	var etag, lastModified *string
	err := f.pool.QueryRow(ctx,
		"SELECT id, etag, last_modified FROM source WHERE url = $1", feedURL,
	).Scan(&sourceID, &etag, &lastModified)
	if err != nil {
		return nil, fmt.Errorf("source not found for URL %s: %w", feedURL, err)
	}
	result.SourceID = sourceID.String()

	// Build conditional request headers
	headers := make(map[string]string)
	headers["User-Agent"] = userAgent
	if etag != nil && *etag != "" {
		headers["If-None-Match"] = *etag
	}
	if lastModified != nil && *lastModified != "" {
		headers["If-Modified-Since"] = *lastModified
	}

	// Fetch the feed
	httpClient := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, http.NoBody)
	if err != nil {
		result.Status = statusError
		return result, fmt.Errorf("create request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		result.Status = statusError
		return result, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		f.updateSourcePoll(ctx, sourceID, etag, lastModified)
		result.HTTPStatus = 304
		result.Status = "not_modified"
		slog.Info("rss not modified", "url", feedURL)
		return result, nil
	}

	if resp.StatusCode != http.StatusOK {
		result.Status = statusError
		result.HTTPStatus = resp.StatusCode
		return result, fmt.Errorf("%w (%d) for %s", errFeedHTTP, resp.StatusCode, feedURL)
	}
	result.HTTPStatus = resp.StatusCode

	// Read body
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		result.Status = statusError
		return result, fmt.Errorf("read body: %w", err)
	}
	body := string(bodyBytes)

	newETag := resp.Header.Get("etag")
	newLastModified := resp.Header.Get("Last-Modified")
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/rss+xml"
	}

	// Parse the feed
	parser := gofeed.NewParser()
	feed, err := parser.ParseString(body)
	if err != nil {
		result.Status = statusError
		return result, fmt.Errorf("parse feed: %w", err)
	}
	result.ItemsFetched = len(feed.Items)

	// Store raw document
	headersJSON, err := json.Marshal(resp.Header)
	if err != nil {
		slog.Warn("failed to marshal response headers", "err", err)
		headersJSON = []byte("{}")
	}
	var rawDocID uuid.UUID
	err = f.pool.QueryRow(ctx, `
		INSERT INTO raw_document (source_id, url, fetch_status, headers, body_text, content_type)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id
	`, sourceID, feedURL, resp.StatusCode, headersJSON, body, contentType).Scan(&rawDocID)
	if err != nil {
		result.Status = statusError
		return result, fmt.Errorf("insert raw_document: %w", err)
	}

	// Process entries
	var cutoff time.Time
	if maxAgeDays > 0 {
		cutoff = time.Now().UTC().AddDate(0, 0, -maxAgeDays)
	}

	for _, item := range feed.Items {
		// Recency filter
		if maxAgeDays > 0 && item.PublishedParsed != nil {
			if item.PublishedParsed.Before(cutoff) {
				result.ItemsSkippedOld++
				continue
			}
		}

		isNew, isUpdated, err := f.upsertNewsItem(ctx, sourceID, "rss", item, rawDocID)
		if err != nil {
			slog.Warn("upsert news item", "title", item.Title, "err", err)
			continue
		}
		if isNew {
			result.ItemsNew++
		} else if isUpdated {
			result.ItemsUpdated++
		}
	}

	// Update source poll state
	newETagPtr := &newETag
	if newETag == "" {
		newETagPtr = etag
	}
	newLMPtr := &newLastModified
	if newLastModified == "" {
		newLMPtr = lastModified
	}
	f.updateSourcePoll(ctx, sourceID, newETagPtr, newLMPtr)

	slog.Info("rss fetched", "url", feedURL, "items", result.ItemsFetched,
		"new", result.ItemsNew, "updated", result.ItemsUpdated)
	return result, nil
}

func (f *RSSFetcher) updateSourcePoll(ctx context.Context, sourceID uuid.UUID, etag, lastModified *string) {
	_, err := f.pool.Exec(ctx, `
		UPDATE source SET etag = $1, last_modified = $2,
			last_poll_at = now(), next_poll_at = now() + (poll_interval_sec || ' seconds')::interval
		WHERE id = $3
	`, etag, lastModified, sourceID)
	if err != nil {
		slog.Warn("update source poll", "err", err)
	}
}

func stripHTML(s string) string {
	s = htmlTagRe.ReplaceAllString(s, " ")
	s = whitespaceRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func canonicalizeURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	path := strings.TrimRight(parsed.Path, "/")
	if path == "" {
		path = "/"
	}
	scheme := parsed.Scheme
	if scheme == "" {
		scheme = "https"
	}
	netloc := strings.ToLower(parsed.Host)
	if parsed.RawQuery != "" {
		return fmt.Sprintf("%s://%s%s?%s", scheme, netloc, path, parsed.RawQuery)
	}
	return fmt.Sprintf("%s://%s%s", scheme, netloc, path)
}

func urlHash(rawURL string) string {
	h := sha256.Sum256([]byte(canonicalizeURL(rawURL)))
	return hex.EncodeToString(h[:])
}

func contentHash(text string) string {
	if text == "" {
		return ""
	}
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:])
}

func (f *RSSFetcher) upsertNewsItem(ctx context.Context, sourceID uuid.UUID, sourceType string, item *gofeed.Item, rawDocID uuid.UUID) (bool, bool, error) {
	link := item.Link
	guid := link
	if item.GUID != "" {
		guid = item.GUID
	}
	title := item.Title
	author := ""
	if item.Author != nil {
		author = item.Author.Name
	}
	if len(item.Authors) > 0 && author == "" {
		author = item.Authors[0].Name
	}

	// content:encoded (RSS 2.0) or Atom content
	contentHTML := ""
	if item.Content != "" {
		contentHTML = item.Content
	}
	if contentHTML == "" && item.Description != "" {
		contentHTML = item.Description
	}

	summaryShort := item.Description
	if summaryShort == "" && contentHTML != "" {
		summaryShort = stripHTML(contentHTML)
		if len(summaryShort) > 500 {
			summaryShort = summaryShort[:500]
		}
	}

	contentText := ""
	if contentHTML != "" && contentHTML != summaryShort {
		contentText = stripHTML(contentHTML)
	} else if summaryShort != "" {
		contentText = stripHTML(summaryShort)
	}

	cHash := contentHash(summaryShort)
	if cHash == "" && contentHTML != "" {
		cHash = contentHash(contentHTML)
	}
	cURL := canonicalizeURL(link)
	cURLHash := urlHash(link)

	var published *time.Time
	if item.PublishedParsed != nil {
		t := item.PublishedParsed.UTC()
		published = &t
	} else if item.UpdatedParsed != nil {
		t := item.UpdatedParsed.UTC()
		published = &t
	}

	// Body fetch status: RSS-only pipeline
	bodyStatus := statusSkipped
	if len(contentText) > 2000 {
		bodyStatus = statusFetched
	} else if link != "" {
		bodyStatus = "pending"
	}

	sh := SimhashCompute(contentText)
	if sh == 0 && summaryShort != "" {
		sh = SimhashCompute(summaryShort)
	}
	if sh == 0 {
		sh = SimhashCompute(title)
	}

	// Check for existing item by GUID, falling back to canonical URL hash
	// when the GUID doesn't match anything (e.g. a feed changed its GUID
	// format for an already-ingested URL).
	var existingID *uuid.UUID
	if err := f.pool.QueryRow(ctx,
		"SELECT id FROM news_item WHERE source_id = $1 AND external_id = $2",
		sourceID, guid,
	).Scan(&existingID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		slog.Warn("lookup existing item by guid", "err", err)
	}
	if existingID == nil && cURLHash != "" {
		if err := f.pool.QueryRow(ctx,
			"SELECT id FROM news_item WHERE source_id = $1 AND canonical_url_hash = $2",
			sourceID, cURLHash,
		).Scan(&existingID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("lookup existing item by canonical url hash", "err", err)
		}
	}

	if existingID != nil {
		return f.updateExistingItem(ctx, existingID, &link, &cURL, &cURLHash, &title, &author, published,
			&contentHTML, &contentText, &summaryShort, &cHash, &sh, rawDocID, bodyStatus)
	}

	// Insert new item
	_, err := f.pool.Exec(ctx, `
		INSERT INTO news_item
			(source_id, source_type, external_id, raw_document_id,
			 url, canonical_url, canonical_url_hash, title, author,
			 published_at, content_html, content_text, content_markdown, summary_short,
			 content_hash, simhash, fetched_at, body_fetch_status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, now(), $17)
	`, sourceID, sourceType, guid, rawDocID,
		link, cURL, cURLHash, title, author,
		published, contentHTML, contentText, contentHTML, summaryShort,
		cHash, sh, bodyStatus)
	if err != nil {
		return false, false, err
	}
	return true, false, nil
}

// updateExistingItem updates an existing news item if new content is longer.
//
//nolint:nonamedreturns // Multiple bool returns benefit from naming
func (f *RSSFetcher) updateExistingItem(ctx context.Context, existingID *uuid.UUID,
	link, cURL, cURLHash, title, author *string, published any,
	contentHTML, contentText, summaryShort, cHash *string, sh *int64, rawDocID uuid.UUID, bodyStatus string,
) (updated, changed bool, err error) {
	// Query existing content length
	var existingTextLen int
	q := "SELECT COALESCE(length(content_text), 0) FROM news_item WHERE id = $1"
	if err := f.pool.QueryRow(ctx, q, *existingID).Scan(&existingTextLen); err != nil {
		slog.Warn("query existing content length", "err", err)
		existingTextLen = 0
	}
	newTextLen := len(*contentText)

	if newTextLen > existingTextLen {
		// Update with content
		_, err := f.pool.Exec(ctx, `
			UPDATE news_item SET
				url = $1, canonical_url = $2, canonical_url_hash = $3,
				title = $4, author = $5, published_at = COALESCE($6, published_at),
				content_html = $7, content_text = $8, content_markdown = $9,
				summary_short = $10, content_hash = $11, simhash = $12,
				raw_document_id = $13, body_fetch_status = $14, fetched_at = now()
			WHERE id = $15
		`, link, cURL, cURLHash, title, author, published,
			contentHTML, contentText, contentHTML, summaryShort, cHash, sh, rawDocID, bodyStatus, *existingID)
		if err != nil {
			return false, false, err
		}
	} else {
		// Update without content
		_, err := f.pool.Exec(ctx, `
			UPDATE news_item SET
				url = $1, canonical_url = $2, canonical_url_hash = $3,
				title = $4, author = $5, published_at = COALESCE($6, published_at),
				summary_short = $7, content_hash = $8, simhash = $9,
				raw_document_id = $10, body_fetch_status = $11, fetched_at = now()
			WHERE id = $12
		`, link, cURL, cURLHash, title, author, published,
			summaryShort, cHash, sh, rawDocID, bodyStatus, *existingID)
		if err != nil {
			return false, false, err
		}
	}
	return false, true, nil
}
