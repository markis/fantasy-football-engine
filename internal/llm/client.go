package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is an Ollama Cloud chat completion client.
type Client struct {
	url       string
	model     string
	apiKey    string
	client    *http.Client
}

// New creates a new LLM client for Ollama Cloud.
func New(url, model, apiKey string, timeout time.Duration) *Client {
	return &Client{
		url:    strings.TrimRight(url, "/") + "/api/chat",
		model:  model,
		apiKey: apiKey,
		client: &http.Client{Timeout: timeout},
	}
}

// chatRequest is the JSON body for the Ollama /api/chat endpoint.
type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
	Think    bool          `json:"think"`
	Options  chatOptions   `json:"options"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatOptions struct {
	Temperature float64 `json:"temperature"`
}

// chatResponse is the JSON response from the Ollama /api/chat endpoint.
type chatResponse struct {
	Message chatMessage `json:"message"`
}

// Chat sends a single-turn user message and returns the assistant's response.
func (c *Client) Chat(ctx context.Context, prompt string, temperature float64) (string, error) {
	body := chatRequest{
		Model:    c.model,
		Messages: []chatMessage{{Role: "user", Content: prompt}},
		Stream:   false,
		Think:    false,
		Options:  chatOptions{Temperature: temperature},
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal chat request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.url, bytes.NewReader(bodyBytes))
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

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("chat HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode chat response: %w", err)
	}

	return strings.TrimSpace(result.Message.Content), nil
}

// ModelTag returns the model tag string for storage (e.g. "ollama:minimax-m3").
func (c *Client) ModelTag() string {
	return "ollama:" + c.model
}