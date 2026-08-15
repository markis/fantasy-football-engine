package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

var (
	errEmptyEmbedResponse = errors.New("empty embedding response")
	errEmbedHTTP          = errors.New("embedding HTTP error")
	errEmbedContextSize   = errors.New("embedding input exceeds model context window")
)

// MaxEmbedChars is the conservative per-item character cap applied before
// sending text to nomic-embed-text-v1.5. ~1800 tokens ≈ 7000 chars for typical
// English prose; the retry loop in Embed handles content with a lower
// chars/token ratio (code, URLs, non-ASCII) that still exceeds the 2048-token
// hard limit after truncation.
const MaxEmbedChars = 7000

// minEmbedChars is the floor for retry truncation; below this we give up
// rather than looping indefinitely.
const minEmbedChars = 64

// Client is an embedding client that calls a llama-server HTTP endpoint.
type Client struct {
	url    string
	model  string
	client *http.Client
}

// New creates a new embedding client.
func New(url, model string) *Client {
	return &Client{
		url:    strings.TrimRight(url, "/") + "/v1/embeddings",
		model:  model,
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

// embedRequest is the JSON body for the /v1/embeddings endpoint.
type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// embedResponse is the JSON response from the /v1/embeddings endpoint.
type embedResponse struct {
	Data []embedEntry `json:"data"`
}

// embedEntry is a single embedding result in the /v1/embeddings response.
type embedEntry struct {
	Embedding []float32 `json:"embedding"`
}

// Embed sends a single text and returns its embedding vector. If the server
// rejects the input for exceeding the model's 2048-token context window, the
// text is progressively halved and retried, so a tokenizer/char-budget
// mismatch never permanently skips an embeddable item.
func (c *Client) Embed(ctx context.Context, text string) ([]float32, error) {
	text = truncateForEmbed(text)
	for {
		vecs, err := c.embedBatch(ctx, []string{text})
		if err == nil {
			if len(vecs) == 0 {
				return nil, errEmptyEmbedResponse
			}
			return vecs[0], nil
		}
		if !errors.Is(err, errEmbedContextSize) || len(text) <= minEmbedChars {
			return nil, err
		}
		half := len(text) / 2
		if half >= len(text) {
			return nil, err
		}
		prev := len(text)
		text = truncateForEmbed(text[:half])
		slog.Warn("embed retry with shorter text", "prev_chars", prev, "chars", len(text))
	}
}

// EmbedBatch sends multiple texts in one HTTP call and returns their
// embeddings. If the server rejects the whole batch because one input exceeds
// the model context window, it falls back to embedding each text individually
// via Embed, so a single oversized item no longer poisons the rest.
func (c *Client) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	vecs, err := c.embedBatch(ctx, texts)
	if err == nil {
		return vecs, nil
	}
	if !errors.Is(err, errEmbedContextSize) {
		return nil, err
	}
	slog.Warn("embed batch rejected for context size; retrying individually", "count", len(texts))
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v, embedErr := c.Embed(ctx, t)
		if embedErr != nil {
			return nil, embedErr
		}
		out[i] = v
	}
	return out, nil
}

// embedBatch sends texts as a single array request. It returns a wrapped
// errEmbedContextSize when the server rejects the input for exceeding the
// model's context window, so callers can retry with shorter or individual
// inputs. Each input is truncated to MaxEmbedChars before sending.
func (c *Client) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	truncated := make([]string, len(texts))
	for i, t := range texts {
		truncated[i] = truncateForEmbed(t)
	}

	body := embedRequest{Model: c.model, Input: truncated}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			respBody = []byte("(unable to read error response body)")
		}
		bodyStr := string(respBody)
		// llama-server returns HTTP 400 with an "exceed_context_size_error"
		// / "larger than the max context size" message when input > 2048 tokens.
		if resp.StatusCode == http.StatusBadRequest && isContextSizeMessage(bodyStr) {
			return nil, fmt.Errorf("%w (%d): %s", errEmbedContextSize, resp.StatusCode, bodyStr)
		}
		return nil, fmt.Errorf("%w (%d): %s", errEmbedHTTP, resp.StatusCode, bodyStr)
	}

	var result embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode embed response: %w", err)
	}

	vecs := make([][]float32, len(result.Data))
	for i, d := range result.Data {
		vecs[i] = d.Embedding
	}
	dim := 0
	if len(vecs) > 0 {
		dim = len(vecs[0])
	}
	slog.Debug("embedded batch", "count", len(vecs), "dim", dim)
	return vecs, nil
}

// truncateForEmbed truncates text to MaxEmbedChars, a conservative character
// cap that fits nomic-embed-text-v1.5's 2048-token context for typical English
// prose. The retry loop in Embed handles content with a lower chars/token
// ratio (code, URLs, non-ASCII) that still exceeds the limit after truncation.
func truncateForEmbed(text string) string {
	if text == "" {
		return text
	}
	if len(text) > MaxEmbedChars {
		return text[:MaxEmbedChars]
	}
	return text
}

// isContextSizeMessage reports whether the server error body indicates the
// input exceeded the model's context window.
func isContextSizeMessage(body string) bool {
	l := strings.ToLower(body)
	return strings.Contains(l, "context size") || strings.Contains(l, "exceed_context_size")
}
