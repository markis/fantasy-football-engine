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
)

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
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// Embed sends a single text and returns its embedding vector.
func (c *Client) Embed(ctx context.Context, text string) ([]float32, error) {
	text = truncateForEmbed(text)
	vecs, err := c.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, errEmptyEmbedResponse
	}
	return vecs[0], nil
}

// EmbedBatch sends multiple texts in one HTTP call and returns their embeddings.
// This is the key improvement over the Python pipeline (which sent one text
// per call). The llama-server /v1/embeddings endpoint accepts an array of inputs.
func (c *Client) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	// Truncate each text to fit the model's context window. Copy into a new
	// slice so we don't mutate the caller's backing array.
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
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			respBody = []byte("(unable to read error response body)")
		}
		return nil, fmt.Errorf("%w (%d): %s", errEmbedHTTP, resp.StatusCode, string(respBody))
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

// MaxEmbedTokens is the content-token cap for nomic-embed-text-v1.5.
// Server adds 2 special tokens ([CLS]+[SEP]) -> 1802 total, leaving ~246 tokens
// of margin under the 2048 hard limit.
const MaxEmbedTokens = 1800

// FallbackMaxChars is the conservative character cap if we can't tokenize.
const FallbackMaxChars = 7000

// truncateForEmbed truncates text to fit nomic-embed-text-v1.5's 2048-token
// context. Without a tokenizer library, we use a conservative character cap.
// The llama-server will handle tokenization; if it returns a 400 for
// exceeding context, the caller should retry with shorter text.
func truncateForEmbed(text string) string {
	if text == "" {
		return text
	}
	// Conservative character-based truncation. ~1800 tokens ≈ 7000 chars for English.
	// This is a fallback; a proper tokenizer (Candle/HF tokenizers) would be more precise.
	if len(text) > FallbackMaxChars {
		return text[:FallbackMaxChars]
	}
	return text
}
