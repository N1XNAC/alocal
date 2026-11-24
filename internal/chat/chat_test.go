package chat

import (
	"strings"
	"testing"
)

func TestNewConfig(t *testing.T) {
	m := New(Config{
		ModelName:    "qwen3:0.6b",
		OllamaURL:    "http://example.invalid:11434",
		SystemPrompt: "be brief",
	})
	if m.modelName != "qwen3:0.6b" || m.systemPrompt != "be brief" {
		t.Fatalf("bad config: %+v", m)
	}
	if m.client.BaseURL != "http://example.invalid:11434" {
		t.Fatalf("base URL = %q", m.client.BaseURL)
	}
}

func TestSlashCommands(t *testing.T) {
	m := New(Config{ModelName: "old:latest"})

	m.handleCommand("/help")
	if len(m.messages) == 0 || !strings.Contains(m.messages[len(m.messages)-1].Content, "Commands") {
		t.Fatalf("help message missing: %+v", m.messages)
	}

	m.handleCommand("/model new:latest")
	if m.modelName != "new:latest" {
		t.Errorf("model = %q", m.modelName)
	}

	m.handleCommand("/system be concise")
	if m.systemPrompt != "be concise" {
		t.Errorf("system prompt = %q", m.systemPrompt)
	}

	m.handleCommand("/clear")
	if len(m.messages) != 0 {
		t.Errorf("clear left %d messages", len(m.messages))
	}

	m.handleCommand("/quit")
	if !m.quitting {
		t.Error("quit did not mark the program as quitting")
	}
}

func TestViewBeforeReady(t *testing.T) {
	m := New(Config{ModelName: "qwen3:0.6b"})
	if got := m.View(); !strings.Contains(got, "Initializing") {
		t.Fatalf("view = %q", got)
	}
}
