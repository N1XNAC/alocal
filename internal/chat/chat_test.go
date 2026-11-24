package chat

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNewConfig(t *testing.T) {
	m := New(Config{
		ModelPath:    "/tmp/model.gguf",
		ModelLabel:   "test-model",
		SystemPrompt: "be brief",
	})
	if m.modelPath != "/tmp/model.gguf" || m.modelLabel != "test-model" || m.systemPrompt != "be brief" {
		t.Fatalf("bad config: %+v", m)
	}
	if m.client != nil {
		t.Fatal("client should be created when the runtime server starts, not in New")
	}
}

func TestNewWithoutModel(t *testing.T) {
	m := New(Config{})
	if m.modelLabel != "no model loaded" {
		t.Fatalf("label = %q", m.modelLabel)
	}
	if !strings.Contains(m.renderWelcome(), "Ctrl+X") {
		t.Fatalf("welcome should point to Ctrl+X: %s", m.renderWelcome())
	}
}

func TestSlashCommands(t *testing.T) {
	t.Setenv("ALOCAL_DATA_DIR", t.TempDir())
	t.Setenv("ALOCAL_CONFIG_DIR", t.TempDir())
	m := New(Config{ModelLabel: "local.gguf"})

	m.handleSlash("/help")
	if len(m.messages) == 0 || !strings.Contains(m.messages[len(m.messages)-1].Content, "Ctrl+X") {
		t.Fatalf("help message missing: %+v", m.messages)
	}

	m.handleSlash("/system be concise")
	if m.systemPrompt != "be concise" {
		t.Errorf("system prompt = %q", m.systemPrompt)
	}

	m.handleSlash("/info")
	if len(m.messages) == 0 || !strings.Contains(m.messages[len(m.messages)-1].Content, "llama.cpp") {
		t.Fatalf("info should mention the runtime: %+v", m.messages)
	}

	m.handleSlash("/clear")
	if len(m.messages) != 0 {
		t.Errorf("clear left %d messages", len(m.messages))
	}

	m.handleSlash("/quit")
	if !m.quitting {
		t.Error("quit did not mark the program as quitting")
	}
}

func TestCtrlXOpensPalette(t *testing.T) {
	m := New(Config{})
	if m.palette.open {
		t.Fatal("palette should start closed")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	m.ready = true // render the palette instead of the initialization screen
	if !m.palette.open {
		t.Fatal("Ctrl+X did not open the palette")
	}
	if len(m.palette.items) == 0 || m.palette.items[0] != "Switch model" {
		t.Fatalf("unexpected palette: %+v", m.palette.items)
	}
	if !strings.Contains(m.View(), "ALOCAL COMMANDS") {
		t.Fatalf("palette not rendered: %s", m.View())
	}
}

func TestViewBeforeReady(t *testing.T) {
	m := New(Config{ModelLabel: "local.gguf"})
	if got := m.View(); !strings.Contains(got, "Initializing") {
		t.Fatalf("view = %q", got)
	}
}

func TestLogoUsesBlockGlyphs(t *testing.T) {
	if !strings.Contains(logoLines, "▄") || !strings.Contains(logoLines, "▀") {
		t.Fatal("Alocal wordmark must use terminal block glyphs")
	}
	if strings.ContainsAny(logoLines, "🤖🐙🚀") {
		t.Fatal("wordmark must not use emoji")
	}
}
