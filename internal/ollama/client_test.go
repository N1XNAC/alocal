package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewUsesEnvironment(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "http://example.invalid:12345/")
	if got := New("").BaseURL; got != "http://example.invalid:12345" {
		t.Fatalf("BaseURL = %q", got)
	}
	if got := New("http://override:1").BaseURL; got != "http://override:1" {
		t.Fatalf("explicit URL was overridden: %q", got)
	}
}

func TestPing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("Ollama is running"))
	}))
	defer srv.Close()

	if err := New(srv.URL).Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer bad.Close()
	if err := New(bad.URL).Ping(context.Background()); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected 500 error, got %v", err)
	}
}

func TestListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(TagsResponse{Models: []ModelInfo{
			{Name: "qwen3:0.6b", Size: 522000000},
			{Name: "phi4-mini:latest", Size: 2500000000},
		}})
	}))
	defer srv.Close()

	models, err := New(srv.URL).ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 || models[0].Name != "qwen3:0.6b" {
		t.Fatalf("models = %+v", models)
	}
}

func TestChatStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var req ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if !req.Stream {
			t.Error("stream must be true")
		}
		chunks := []ChatResponse{
			{Message: Message{Role: "assistant", Content: "Hello"}},
			{Message: Message{Role: "assistant", Content: " world"}},
			{Done: true, EvalCount: 2, EvalDuration: 1_000_000},
		}
		for _, chunk := range chunks {
			if err := json.NewEncoder(w).Encode(chunk); err != nil {
				t.Errorf("encode chunk: %v", err)
			}
		}
	}))
	defer srv.Close()

	var tokens []string
	var final *ChatResponse
	err := New(srv.URL).ChatStream(context.Background(), ChatRequest{
		Model:    "qwen3:0.6b",
		Messages: []Message{{Role: "user", Content: "hi"}},
	}, func(token string, done bool, resp *ChatResponse) {
		if done {
			final = resp
			return
		}
		tokens = append(tokens, token)
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if strings.Join(tokens, "") != "Hello world" {
		t.Errorf("tokens = %q", tokens)
	}
	if final == nil || final.EvalCount != 2 {
		t.Fatalf("final stats = %+v", final)
	}
}

func TestChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Stream {
			t.Error("stream must be false")
		}
		_ = json.NewEncoder(w).Encode(ChatResponse{
			Message: Message{Role: "assistant", Content: "Hi there"},
			Done:    true,
		})
	}))
	defer srv.Close()

	resp, err := New(srv.URL).Chat(context.Background(), ChatRequest{
		Model:    "qwen3:0.6b",
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Message.Content != "Hi there" {
		t.Errorf("content = %q", resp.Message.Content)
	}
}

func TestChatStreamHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"model not found"}`, http.StatusNotFound)
	}))
	defer srv.Close()

	err := New(srv.URL).ChatStream(context.Background(), ChatRequest{Model: "missing"}, nil)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected 404 error, got %v", err)
	}
}
