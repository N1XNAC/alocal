// Package runtime starts llama.cpp and speaks its OpenAI-compatible API.
package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Message is one chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// GenOptions controls generation for a request.
type GenOptions struct {
	Temperature float64
	MaxTokens   int
}

// Client talks to a running llama-server.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// NewClient returns a client for a llama-server base URL.
func NewClient(baseURL string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: &http.Client{}}
}

// Health checks the server's /health endpoint.
func (c *Client) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("llama.cpp health returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// ChatStream sends a chat request and streams tokens via cb.
func (c *Client) ChatStream(ctx context.Context, messages []Message, opts GenOptions, cb func(token string, done bool, tokens int)) error {
	body := map[string]any{
		"model":       "local-model",
		"messages":    messages,
		"stream":      true,
		"temperature": opts.Temperature,
	}
	if opts.MaxTokens > 0 {
		body["max_tokens"] = opts.MaxTokens
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("llama.cpp returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			cb("", true, 0)
			return nil
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		if choice.Delta.Content != "" {
			cb(choice.Delta.Content, false, 0)
		}
		if choice.FinishReason != "" {
			cb("", true, chunk.Usage.CompletionTokens)
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	cb("", true, 0)
	return nil
}
