package cli

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"alocal/internal/ollama"
)

func TestChatUnreachable(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "http://127.0.0.1:1")
	_, _, err := run(t, "", "chat", "qwen3:0.6b")
	var ce *CodedError
	if !errors.As(err, &ce) || ce.Code != 1 || !strings.Contains(ce.Msg, "Cannot reach Ollama") {
		t.Fatalf("expected unreachable error, got %v", err)
	}
}

func TestChatWithoutModelUnreachable(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "http://127.0.0.1:1")
	_, _, err := run(t, "", "chat")
	var ce *CodedError
	if !errors.As(err, &ce) || ce.Code != 1 || !strings.Contains(ce.Msg, "No model specified") {
		t.Fatalf("expected no-model error, got %v", err)
	}
}

func TestChatModelNotInstalled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_ = json.NewEncoder(w).Encode(ollama.TagsResponse{})
		default:
			_, _ = w.Write([]byte("Ollama is running"))
		}
	}))
	defer srv.Close()
	t.Setenv("OLLAMA_HOST", srv.URL)

	_, _, err := run(t, "", "chat", "missing:latest")
	var ce *CodedError
	if !errors.As(err, &ce) || ce.Code != 1 || !strings.Contains(ce.Msg, "not installed") {
		t.Fatalf("expected not-installed error, got %v", err)
	}
	if !strings.Contains(ce.Msg, "ollama pull missing:latest") {
		t.Fatalf("missing pull hint: %s", ce.Msg)
	}
}

func TestChatAutoFallsBackToInstalledModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_ = json.NewEncoder(w).Encode(ollama.TagsResponse{Models: []ollama.ModelInfo{
				{Name: "qwen3:0.6b"},
			}})
		default:
			_, _ = w.Write([]byte("Ollama is running"))
		}
	}))
	defer srv.Close()
	t.Setenv("OLLAMA_HOST", srv.URL)

	_, errOut, err := run(t, "", "chat", "--auto", "--mock-hardware", fixturePath("rtx3060"))
	if err == nil {
		t.Fatal("expected non-TTY chat error in tests")
	}
	if !strings.Contains(errOut, "not pulled; using qwen3:0.6b instead") {
		t.Fatalf("missing auto fallback note: %s", errOut)
	}
	if !strings.Contains(errOut, "alocal pull") {
		t.Fatalf("missing pull hint: %s", errOut)
	}
}

func TestHasOllamaModel(t *testing.T) {
	models := []ollama.ModelInfo{
		{Name: "qwen3:0.6b"},
		{Name: "phi4-mini:latest"},
	}
	for _, tag := range []string{"qwen3:0.6b", "phi4-mini", "phi4-mini:latest"} {
		if !hasOllamaModel(models, tag) {
			t.Errorf("hasOllamaModel(%q) = false", tag)
		}
	}
	if hasOllamaModel(models, "not-installed:latest") {
		t.Error("unknown model reported as installed")
	}
}
