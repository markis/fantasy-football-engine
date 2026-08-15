package embed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubEmbedServer returns the given status/body for every request. If the
// returned body is empty it sends a minimal valid OpenAI-shape embed response.
func stubEmbedServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		if body != "" {
			_, _ = w.Write([]byte(body))
			return
		}
		resp := embedResponse{Data: []embedEntry{{Embedding: []float32{0.1, 0.2, 0.3}}}}
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestTruncateForEmbed_ShortText(t *testing.T) {
	if got := truncateForEmbed("hello"); got != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestTruncateForEmbed_LongTextTruncatedToCap(t *testing.T) {
	long := strings.Repeat("a", MaxEmbedChars+5000)
	got := truncateForEmbed(long)
	if len(got) != MaxEmbedChars {
		t.Fatalf("len = %d, want %d", len(got), MaxEmbedChars)
	}
}

func TestTruncateForEmbed_Empty(t *testing.T) {
	if got := truncateForEmbed(""); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestEmbed_OK(t *testing.T) {
	srv := stubEmbedServer(t, http.StatusOK, "")
	defer srv.Close()
	c := New(srv.URL, "nomic-embed-text-v1.5")
	vec, err := c.Embed(context.Background(), "some text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vec) != 3 {
		t.Fatalf("vec len = %d, want 3", len(vec))
	}
}

func TestEmbed_RetriesOnContextSizeError(t *testing.T) {
	// First call rejects with context-size 400; subsequent calls succeed.
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"input (3000 tokens) is larger than the max context size (2048 tokens)"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(embedResponse{Data: []embedEntry{{Embedding: []float32{0.5, 0.6}}}})
	}))
	defer srv.Close()

	c := New(srv.URL, "nomic-embed-text-v1.5")
	vec, err := c.Embed(context.Background(), strings.Repeat("x", 8000))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vec) != 2 {
		t.Fatalf("vec len = %d, want 2", len(vec))
	}
	if calls < 2 {
		t.Fatalf("expected retry, got %d calls", calls)
	}
}

func TestEmbed_PropagatesNonContextError(t *testing.T) {
	srv := stubEmbedServer(t, http.StatusInternalServerError, "boom")
	defer srv.Close()
	c := New(srv.URL, "nomic-embed-text-v1.5")
	_, err := c.Embed(context.Background(), "text")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected 500 in error, got %v", err)
	}
}

func TestEmbedBatch_FallbackToIndividual(t *testing.T) {
	// First batch call returns context-size 400; individual calls succeed.
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"input (5000 tokens) is larger than the max context size (2048 tokens)"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(embedResponse{Data: []embedEntry{{Embedding: []float32{0.7, 0.8, 0.9}}}})
	}))
	defer srv.Close()

	c := New(srv.URL, "nomic-embed-text-v1.5")
	vecs, err := c.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vecs) != 3 {
		t.Fatalf("vecs len = %d, want 3", len(vecs))
	}
	// 1 batch call + 3 individual calls = 4 total.
	if calls != 4 {
		t.Fatalf("calls = %d, want 4", calls)
	}
}

func TestEmbedBatch_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embedRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		resp := embedResponse{}
		for range req.Input {
			resp.Data = append(resp.Data, embedEntry{Embedding: []float32{0.1, 0.2}})
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := New(srv.URL, "nomic-embed-text-v1.5")
	vecs, err := c.EmbedBatch(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vecs) != 2 {
		t.Fatalf("vecs len = %d, want 2", len(vecs))
	}
}

func TestEmbedBatch_Empty(t *testing.T) {
	c := New("http://localhost", "m")
	vecs, err := c.EmbedBatch(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vecs != nil {
		t.Fatalf("expected nil vecs")
	}
}

func TestIsContextSizeMessage(t *testing.T) {
	cases := []struct {
		body string
		want bool
	}{
		{`{"error":"input (2529 tokens) is larger than the max context size (2048 tokens)"}`, true},
		{`{"error":"exceed_context_size_error"}`, true},
		{`{"error":"some other error"}`, false},
		{``, false},
	}
	for _, tc := range cases {
		if got := isContextSizeMessage(tc.body); got != tc.want {
			t.Errorf("isContextSizeMessage(%q) = %v, want %v", tc.body, got, tc.want)
		}
	}
}
