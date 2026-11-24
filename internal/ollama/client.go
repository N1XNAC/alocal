// Package ollama provides a streaming HTTP client for the Ollama API.
package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// DefaultBaseURL is the default Ollama API endpoint.
const DefaultBaseURL = "http://localhost:11434"

// Message is a chat message in the Ollama format.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest is the request body for /api/chat.
type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
	Options  *Options  `json:"options,omitempty"`
}

// Options controls generation parameters.
type Options struct {
	Temperature   *float64 `json:"temperature,omitempty"`
	TopP          *float64 `json:"top_p,omitempty"`
	TopK          *int     `json:"top_k,omitempty"`
	NumCtx        *int     `json:"num_ctx,omitempty"`
	RepeatPenalty *float64 `json:"repeat_penalty,omitempty"`
	Seed          *int     `json:"seed,omitempty"`
	Stop          []string `json:"stop,omitempty"`
}

// ChatResponse is one chunk from streaming /api/chat.
type ChatResponse struct {
	Model     string  `json:"model"`
	Message   Message `json:"message"`
	Done      bool    `json:"done"`
	CreatedAt string  `json:"created_at"`

	// Final chunk fields
	TotalDuration      int64 `json:"total_duration,omitempty"`
	LoadDuration       int64 `json:"load_duration,omitempty"`
	PromptEvalCount    int   `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64 `json:"prompt_eval_duration,omitempty"`
	EvalCount          int   `json:"eval_count,omitempty"`
	EvalDuration       int64 `json:"eval_duration,omitempty"`
}

// ModelInfo is a model from /api/tags.
type ModelInfo struct {
	Name       string    `json:"name"`
	Model      string    `json:"model"`
	ModifiedAt time.Time `json:"modified_at"`
	Size       int64     `json:"size"`
}

// TagsResponse is the response from /api/tags.
type TagsResponse struct {
	Models []ModelInfo `json:"models"`
}

// Client communicates with Ollama.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

// New creates a Client. If baseURL is empty, uses OLLAMA_HOST or the default.
func New(baseURL string) *Client {
	if baseURL == "" {
		baseURL = os.Getenv("OLLAMA_HOST")
	}
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	return &Client{
		BaseURL:    baseURL,
		HTTPClient: &http.Client{Timeout: 0}, // no global timeout for streaming
	}
}

// Ping checks if Ollama is reachable.
func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.BaseURL, nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach Ollama at %s: %w", c.BaseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("ollama returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// ListModels returns locally available models.
func (c *Client) ListModels(ctx context.Context) ([]ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.BaseURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot list models from Ollama: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama /api/tags returned %d: %s", resp.StatusCode, string(body))
	}
	var tags TagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return nil, fmt.Errorf("parse /api/tags: %w", err)
	}
	return tags.Models, nil
}

// StreamCallback is called for each streaming token chunk.
type StreamCallback func(token string, done bool, resp *ChatResponse)

// ChatStream sends a chat request and streams the response token-by-token.
func (c *Client) ChatStream(ctx context.Context, cr ChatRequest, cb StreamCallback) error {
	cr.Stream = true
	body, err := json.Marshal(cr)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("chat request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(respBody))
	}

	scanner := bufio.NewScanner(resp.Body)
	// Allow large lines (some models produce big chunks)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var chunk ChatResponse
		if err := json.Unmarshal(line, &chunk); err != nil {
			continue // skip malformed lines
		}
		if cb != nil {
			cb(chunk.Message.Content, chunk.Done, &chunk)
		}
		if chunk.Done {
			break
		}
	}
	return scanner.Err()
}

// Chat sends a non-streaming chat request and returns the full response.
func (c *Client) Chat(ctx context.Context, cr ChatRequest) (*ChatResponse, error) {
	cr.Stream = false
	body, err := json.Marshal(cr)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("chat request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(respBody))
	}

	var cr2 ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr2); err != nil {
		return nil, err
	}
	return &cr2, nil
}
