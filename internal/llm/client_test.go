package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestChat_OllamaFormat(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"  hello ollama  "}}`))
	}))
	defer srv.Close()

	c := New(ProviderOllamaCloud, srv.URL, "minimax-m3", "key-1", time.Minute)
	got, err := c.Chat(context.Background(), "prompt", 0.25)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/chat" {
		t.Errorf("path = %q, want /api/chat", gotPath)
	}
	if gotAuth != "Bearer key-1" {
		t.Errorf("auth = %q, want Bearer key-1", gotAuth)
	}
	if gotBody.Model != "minimax-m3" {
		t.Errorf("model = %q, want minimax-m3", gotBody.Model)
	}
	if len(gotBody.Messages) != 1 || gotBody.Messages[0].Content != "prompt" {
		t.Errorf("messages = %+v, want single user prompt", gotBody.Messages)
	}
	if gotBody.Options.Temperature != 0.25 {
		t.Errorf("temperature = %v, want 0.25", gotBody.Options.Temperature)
	}
	if got != "hello ollama" {
		t.Errorf("content = %q, want trimmed hello ollama", got)
	}
	if tag := c.ModelTag(); tag != "ollama:minimax-m3" {
		t.Errorf("model tag = %q, want ollama:minimax-m3", tag)
	}
}

func TestChat_AgentgatewayOpenAIFormat(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody openaiChatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":" hello gateway "}}]}`))
	}))
	defer srv.Close()

	c := New(ProviderAgentgateway, srv.URL, "agent-default", "key-2", time.Minute)
	got, err := c.Chat(context.Background(), "prompt", 0.5)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/chat/completions" {
		t.Errorf("path = %q, want /v1/chat/completions", gotPath)
	}
	if gotAuth != "Bearer key-2" {
		t.Errorf("auth = %q, want Bearer key-2", gotAuth)
	}
	if gotBody.Model != "agent-default" {
		t.Errorf("model = %q, want agent-default", gotBody.Model)
	}
	if gotBody.Stream {
		t.Error("stream = true, want false")
	}
	if gotBody.Temperature != 0.5 {
		t.Errorf("temperature = %v, want 0.5", gotBody.Temperature)
	}
	if got != "hello gateway" {
		t.Errorf("content = %q, want trimmed hello gateway", got)
	}
	if tag := c.ModelTag(); tag != "agentgateway:agent-default" {
		t.Errorf("model tag = %q, want agentgateway:agent-default", tag)
	}
}

func TestChat_AnonymousKeyOmitted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "" {
			t.Errorf("unexpected Authorization header %q", auth)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()

	c := New(ProviderOpenAI, srv.URL, "m", "", time.Minute)
	if _, err := c.Chat(context.Background(), "prompt", 0); err != nil {
		t.Fatal(err)
	}
}

func TestChat_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"Unauthorized"}`))
	}))
	defer srv.Close()

	for _, provider := range []string{ProviderOllamaCloud, ProviderAgentgateway} {
		c := New(provider, srv.URL, "m", "k", time.Minute)
		_, err := c.Chat(context.Background(), "prompt", 0)
		if err == nil {
			t.Fatalf("%s: expected error", provider)
		}
		if !strings.Contains(err.Error(), "401") {
			t.Errorf("%s: error = %v, want status 401", provider, err)
		}
	}
}
