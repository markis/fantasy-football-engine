package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"ff-engine/internal/telemetry"
)

var errChatHTTP = errors.New("chat HTTP error")

// maxErrorBodyBytes caps how much of a non-200 response body is read for
// the error message — error paths must not become unbounded allocations.
const maxErrorBodyBytes = 64 << 10 // 64 KiB

// Provider identifiers recognized by the chat client. "ollama-cloud" is the
// default and speaks Ollama's native /api/chat protocol. "agentgateway"
// (and the generic "openai") speak the OpenAI chat-completions protocol at
// {url}/v1/chat/completions — used for gateways that expose model aliases
// (e.g. the cluster's agentgateway LLM proxy).
const (
	ProviderOllamaCloud  = "ollama-cloud"
	ProviderAgentgateway = "agentgateway"
	ProviderOpenAI       = "openai"
)

// Client is a chat completion client whose wire format is selected by the
// configured provider.
type Client struct {
	provider string
	url      string
	model    string
	apiKey   string
	client   *http.Client
}

// New creates a new chat client for the given provider, base URL, and model.
func New(provider string, url, model, apiKey string, timeout time.Duration) *Client {
	return &Client{
		provider: strings.ToLower(strings.TrimSpace(provider)),
		url:      strings.TrimRight(url, "/"),
		model:    model,
		apiKey:   apiKey,
		client:   telemetry.NewHTTPClient(timeout),
	}
}

// openaiCompatible reports whether the provider speaks the OpenAI
// chat-completions wire format instead of Ollama's native /api/chat protocol.
func (c *Client) openaiCompatible() bool {
	return c.provider == ProviderAgentgateway || c.provider == ProviderOpenAI
}

// chatMessage is shared by both wire formats.
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatRequest is the JSON body for the Ollama /api/chat endpoint.
type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
	Think    bool          `json:"think"`
	Options  chatOptions   `json:"options"`
}

type chatOptions struct {
	Temperature float64 `json:"temperature"`
}

// chatResponse is the JSON response from the Ollama /api/chat endpoint.
type chatResponse struct {
	Message chatMessage `json:"message"`
}

// openaiChatRequest is the JSON body for the OpenAI
// /v1/chat/completions endpoint.
type openaiChatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Stream      bool          `json:"stream"`
	Temperature float64       `json:"temperature"`
}

// openaiChatResponse is the JSON response from the OpenAI
// /v1/chat/completions endpoint.
type openaiChatResponse struct {
	Choices []openaiChatChoice `json:"choices"`
}

type openaiChatChoice struct {
	Message chatMessage `json:"message"`
}

// Chat sends a single-turn user message and returns the assistant's response.
func (c *Client) Chat(ctx context.Context, prompt string, temperature float64) (string, error) {
	var (
		endpoint string
		body     any
	)
	if c.openaiCompatible() {
		endpoint = c.url + "/v1/chat/completions"
		body = openaiChatRequest{
			Model:       c.model,
			Messages:    []chatMessage{{Role: "user", Content: prompt}},
			Stream:      false,
			Temperature: temperature,
		}
	} else {
		endpoint = c.url + "/api/chat"
		body = chatRequest{
			Model:    c.model,
			Messages: []chatMessage{{Role: "user", Content: prompt}},
			Stream:   false,
			Think:    false,
			Options:  chatOptions{Temperature: temperature},
		}
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal chat request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("create chat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("chat request: %w", err)
	}
	defer resp.Body.Close()

	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	if readErr != nil {
		respBody = []byte("(unable to read response body)")
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w (%d): %s", errChatHTTP, resp.StatusCode, string(respBody))
	}

	content, err := c.decodeContent(respBody)
	if err != nil {
		return "", err
	}
	return content, nil
}

// decodeContent extracts the assistant message text from the response body
// for the configured wire format.
func (c *Client) decodeContent(respBody []byte) (string, error) {
	if c.openaiCompatible() {
		var result openaiChatResponse
		if err := json.Unmarshal(respBody, &result); err != nil {
			return "", fmt.Errorf("decode chat response: %w", err)
		}
		if len(result.Choices) == 0 {
			return "", fmt.Errorf("chat response has no choices")
		}
		return strings.TrimSpace(result.Choices[0].Message.Content), nil
	}

	var result chatResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("decode chat response: %w", err)
	}
	return strings.TrimSpace(result.Message.Content), nil
}

// ModelTag returns the model tag string for storage (e.g.
// "ollama:minimax-m3" or "agentgateway:agent-default").
func (c *Client) ModelTag() string {
	if c.openaiCompatible() {
		return c.provider + ":" + c.model
	}
	return "ollama:" + c.model
}
