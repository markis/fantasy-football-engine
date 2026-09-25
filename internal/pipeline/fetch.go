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
	"github.com/mmcdole/gofeed"

	"ff-engine/internal/db"
	"ff-engine/internal/telemetry"
	"ff-engine/internal/util"
)

var errFeedHTTP = errors.New("feed HTTP error")

const (
	userAgent = "ZeroClawFantasyBot/0.1 (homelab)"

	// maxFeedBytes caps how much of an RSS/Atom response body is read into
	// memory. Feeds are fetched 8-at-a-time, so without a cap a single huge
	// or hostile response balloons the daemon's heap.
	maxFeedBytes = 10 << 20 // 10 MiB
)

// RSSFetcher fetches and ingests RSS/Atom feeds into the database.
type RSSFetcher struct {
	pool      *db.Pool
	evergreen []string // canonical-URL substrings whose items are de-flagged as stale/evergreen
}

// NewRSSFetcher creates a new RSS fetcher. evergreen is a list of canonical-URL
// substrings whose articles are evergreen aggregators (republished with a fresh
// pubDate but stale body); matching items are ingested with is_news=false,
// is_relevant=false so they never surface as current news.
func NewRSSFetcher(pool *db.Pool, evergreen []string) *RSSFetcher {
	return &RSSFetcher{pool: pool, evergreen: evergreen}
}

// isEvergreen reports whether the item's canonical URL matches an evergreen
// blocklist pattern.
func (f *RSSFetcher) isEvergreen(canonicalURL string) bool {
	if canonicalURL == "" {
		return false
	}
	for _, p := range f.evergreen {
		if p != "" && strings.Contains(canonicalURL, p) {
			return true
		}
	}
	return false
}

// FetchResult is the result of fetching one feed.
type FetchResult struct {
	SourceID        string `json:"sourceId"`
	URL             string `json:"url"`
	ItemsFetched    int    `json:"itemsFetched"`
	ItemsNew        int    `json:"itemsNew"`
	ItemsUpdated    int    `json:"itemsUpdated"`
	ItemsSkippedOld int    `json:"itemsSkippedOld"`
	HTTPStatus      int    `json:"httpStatus"`
	Status          string `json:"status"`
}

// sourceLookup holds a source's id and conditional-request cache validators.
type sourceLookup struct {
	sourceID     uuid.UUID
	etag         *string
	lastModified *string
}

// lookupSource looks up a source's id and conditional-request cache
// validators by feed URL.
func (f *RSSFetcher) lookupSource(ctx context.Context, feedURL string) (sourceLookup, error) {
	var sl sourceLookup
	err := f.pool.QueryRow(ctx,
		"SELECT id, etag, last_modified FROM source WHERE url = $1", feedURL,
	).Scan(&sl.sourceID, &sl.etag, &sl.lastModified)
	if err != nil {
		return sl, fmt.Errorf("query source %s: %w", feedURL, err)
	}
	return sl, nil
}

// doConditionalFetch issues a GET request for the feed URL with conditional
// request headers (If-None-Match / If-Modified-Since) set when available.
func (f *RSSFetcher) doConditionalFetch(ctx context.Context, feedURL string, etag, lastModified *string) (*http.Response, error) {
	headers := map[string]string{"User-Agent": userAgent}
	if etag != nil && *etag != "" {
		headers["If-None-Match"] = *etag
	}
	if lastModified != nil && *lastModified != "" {
		headers["If-Modified-Since"] = *lastModified
	}

	httpClient := telemetry.NewHTTPClient(30 * time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	return resp, nil
}

// readAndParseFeed reads the response body and parses it as an RSS/Atom
// feed, also returning the marshaled response headers and content type for
// raw-document storage.
func (f *RSSFetcher) readAndParseFeed(
	resp *http.Response,
) (string, *gofeed.Feed, []byte, string, error) {
	var (
		body        string
		feed        *gofeed.Feed
		headersJSON []byte
		contentType string
	)
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedBytes))
	if err != nil {
		return "", nil, nil, "", fmt.Errorf("read body: %w", err)
	}
	body = string(bodyBytes)

	contentType = resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/rss+xml"
	}

	parser := gofeed.NewParser()
	feed, err = parser.ParseString(body)
	if err != nil {
		return "", nil, nil, "", fmt.Errorf("parse feed: %w", err)
	}

	headersJSON, err = json.Marshal(resp.Header)
	if err != nil {
		slog.Warn("failed to marshal response headers", "err", err)
		headersJSON = []byte("{}")
	}
	return body, feed, headersJSON, contentType, nil
}

// storeRawDocument inserts the fetched raw feed document and returns its id.
func (f *RSSFetcher) storeRawDocument(
	ctx context.Context, sourceID uuid.UUID, feedURL string, statusCode int, headersJSON []byte, body, contentType string,
) (uuid.UUID, error) {
	var rawDocID uuid.UUID
	err := f.pool.QueryRow(ctx, `
		INSERT INTO raw_document (source_id, url, fetch_status, headers, body_text, content_type)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id
	`, sourceID, feedURL, statusCode, headersJSON, body, contentType).Scan(&rawDocID)
	if err != nil {
		return rawDocID, fmt.Errorf("insert raw document for %s: %w", feedURL, err)
	}
	return rawDocID, nil
}

// processFeedEntries upserts each feed entry (skipping ones older than the
// max age cutoff) and tallies new/updated/skipped counts into result.
func (f *RSSFetcher) processFeedEntries(
	ctx context.Context, sourceID uuid.UUID, feed *gofeed.Feed, rawDocID uuid.UUID, maxAgeDays int, result *FetchResult,
) {
	var cutoff time.Time
	if maxAgeDays > 0 {
		cutoff = time.Now().UTC().AddDate(0, 0, -maxAgeDays)
	}

	for _, item := range feed.Items {
		// Recency filter
		if maxAgeDays > 0 && item.PublishedParsed != nil && item.PublishedParsed.Before(cutoff) {
			result.ItemsSkippedOld++
			continue
		}

		// Skip non-football sports articles at ingest so they never enter the pipeline.
		if isNonFootballSport(item.Title, item.Description) {
			continue
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
}

// Fetch fetches a single RSS feed, stores the raw document, and upserts news items.
func (f *RSSFetcher) Fetch(ctx context.Context, feedURL string, maxAgeDays int) (*FetchResult, error) {
	result := &FetchResult{URL: feedURL, Status: "ok"}

	src, err := f.lookupSource(ctx, feedURL)
	if err != nil {
		return nil, fmt.Errorf("source not found for URL %s: %w", feedURL, err)
	}
	sourceID, etag, lastModified := src.sourceID, src.etag, src.lastModified
	result.SourceID = sourceID.String()

	resp, err := f.doConditionalFetch(ctx, feedURL, etag, lastModified)
	if err != nil {
		result.Status = statusError
		return result, err
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

	body, feed, headersJSON, contentType, err := f.readAndParseFeed(resp)
	if err != nil {
		result.Status = statusError
		return result, err
	}
	result.ItemsFetched = len(feed.Items)

	rawDocID, err := f.storeRawDocument(ctx, sourceID, feedURL, resp.StatusCode, headersJSON, body, contentType)
	if err != nil {
		result.Status = statusError
		return result, fmt.Errorf("insert raw_document: %w", err)
	}

	f.processFeedEntries(ctx, sourceID, feed, rawDocID, maxAgeDays, result)

	// Update source poll state
	newETag := resp.Header.Get("etag")
	newLastModified := resp.Header.Get("Last-Modified")
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

// normalizedItem holds a feed entry's fields after normalization, ready to
// be inserted or used to update an existing news item.
type normalizedItem struct {
	link         string
	guid         string
	title        string
	author       string
	contentHTML  string
	contentText  string
	summaryShort string
	cHash        string
	cURL         string
	cURLHash     string
	published    *time.Time
	bodyStatus   string
	simhash      int64
}

// extractAuthor picks the entry's author name from the Author or Authors field.
func extractAuthor(item *gofeed.Item) string {
	author := ""
	if item.Author != nil {
		author = item.Author.Name
	}
	if len(item.Authors) > 0 && author == "" {
		author = item.Authors[0].Name
	}
	return author
}

// extractContentFields derives HTML content, a short plain-text summary, and
// plain-text content from a feed entry's content:encoded/description fields.
func extractContentFields(item *gofeed.Item) (string, string, string) {
	var contentHTML, summaryShort, contentText string
	// content:encoded (RSS 2.0) or Atom content
	if item.Content != "" {
		contentHTML = item.Content
	}
	if contentHTML == "" && item.Description != "" {
		contentHTML = item.Description
	}

	summaryShort = item.Description
	if summaryShort == "" && contentHTML != "" {
		summaryShort = stripHTML(contentHTML)
		if len(summaryShort) > 500 {
			summaryShort = util.TruncateRunes(summaryShort, 500)
		}
	}

	if contentHTML != "" && contentHTML != summaryShort {
		contentText = stripHTML(contentHTML)
	} else if summaryShort != "" {
		contentText = stripHTML(summaryShort)
	}
	return contentHTML, summaryShort, contentText
}

// resolvePublished picks the entry's published timestamp, falling back to
// its updated timestamp.
func resolvePublished(item *gofeed.Item) *time.Time {
	if item.PublishedParsed != nil {
		t := item.PublishedParsed.UTC()
		return &t
	}
	if item.UpdatedParsed != nil {
		t := item.UpdatedParsed.UTC()
		return &t
	}
	return nil
}

// normalizeFeedItem converts a raw feed entry into a normalizedItem with all
// derived fields (content, hashes, body status, simhash) computed.
func normalizeFeedItem(item *gofeed.Item) normalizedItem {
	link := item.Link
	guid := link
	if item.GUID != "" {
		guid = item.GUID
	}
	title := item.Title
	author := extractAuthor(item)

	contentHTML, summaryShort, contentText := extractContentFields(item)

	cHash := contentHash(summaryShort)
	if cHash == "" && contentHTML != "" {
		cHash = contentHash(contentHTML)
	}
	cURL := canonicalizeURL(link)
	cURLHash := urlHash(link)

	published := resolvePublished(item)

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

	return normalizedItem{
		link: link, guid: guid, title: title, author: author,
		contentHTML: contentHTML, contentText: contentText, summaryShort: summaryShort,
		cHash: cHash, cURL: cURL, cURLHash: cURLHash, published: published,
		bodyStatus: bodyStatus, simhash: sh,
	}
}

// findExistingItem looks up an existing news item by GUID, falling back to
// canonical URL hash when the GUID doesn't match anything (e.g. a feed
// changed its GUID format for an already-ingested URL).
func (f *RSSFetcher) findExistingItem(ctx context.Context, sourceID uuid.UUID, guid, cURLHash string) *uuid.UUID {
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
	return existingID
}

// insertNewNewsItem inserts a brand-new news item row for a normalized feed entry.
func (f *RSSFetcher) insertNewNewsItem(
	ctx context.Context, sourceID uuid.UUID, sourceType, guid string, rawDocID uuid.UUID, n *normalizedItem,
) error {
	// Evergreen aggregator articles (republished with a fresh pubDate but stale
	// body — e.g. CBS's "training camp injuries tracker") must never surface as
	// current news. De-flag them at ingest.
	isNews, isRelevant := true, false
	if f.isEvergreen(n.cURL) {
		isNews, isRelevant = false, false
	}

	_, err := f.pool.Exec(ctx, `
		INSERT INTO news_item
			(source_id, source_type, external_id, raw_document_id,
			 url, canonical_url, canonical_url_hash, title, author,
			 published_at, content_html, content_text, content_markdown, summary_short,
			 content_hash, simhash, fetched_at, body_fetch_status, is_news, is_relevant)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, now(), $17, $18, $19)
	`, sourceID, sourceType, guid, rawDocID,
		n.link, n.cURL, n.cURLHash, n.title, n.author,
		n.published, n.contentHTML, n.contentText, n.contentHTML, n.summaryShort,
		n.cHash, n.simhash, n.bodyStatus, isNews, isRelevant)
	if err != nil {
		return fmt.Errorf("insert news item: %w", err)
	}
	return nil
}

func (f *RSSFetcher) upsertNewsItem(
	ctx context.Context, sourceID uuid.UUID, sourceType string, item *gofeed.Item, rawDocID uuid.UUID,
) (bool, bool, error) {
	n := normalizeFeedItem(item)

	existingID := f.findExistingItem(ctx, sourceID, n.guid, n.cURLHash)
	if existingID != nil {
		changed, uerr := f.updateExistingItem(ctx, existingID, &n.link, &n.cURL, &n.cURLHash, &n.title, &n.author, n.published,
			&n.contentHTML, &n.contentText, &n.summaryShort, &n.cHash, &n.simhash, rawDocID, n.bodyStatus)
		// Evergreen items must stay retired even on re-ingest (a fresh pubDate
		// update would otherwise leave a previously-de-flagged row flagged again
		// if it was ever re-enriched). Force de-flag.
		if uerr == nil && f.isEvergreen(n.cURL) {
			if _, dErr := f.pool.Exec(ctx,
				"UPDATE news_item SET is_news = false, is_relevant = false, news_story = NULL WHERE id = $1",
				*existingID); dErr != nil {
				slog.Warn("de-flag evergreen item", "id", *existingID, "err", dErr)
			}
		}
		return false, changed, uerr
	}

	if insertErr := f.insertNewNewsItem(ctx, sourceID, sourceType, n.guid, rawDocID, &n); insertErr != nil {
		return false, false, insertErr
	}
	return true, false, nil
}

// updateExistingItem updates an existing news item if new content is longer.
func (f *RSSFetcher) updateExistingItem(ctx context.Context, existingID *uuid.UUID,
	link, cURL, cURLHash, title, author *string, published any,
	contentHTML, contentText, summaryShort, cHash *string, sh *int64, rawDocID uuid.UUID, bodyStatus string,
) (bool, error) {
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
			return false, fmt.Errorf("update news item %s: %w", *existingID, err)
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
			return false, fmt.Errorf("update news item %s: %w", *existingID, err)
		}
	}
	return true, nil
}
