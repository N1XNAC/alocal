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

func TestViewIsReadyImmediately(t *testing.T) {
	m := New(Config{ModelLabel: "local.gguf"})
	if !m.ready {
		t.Fatal("model should be ready before the first resize message")
	}
	got := m.View()
	if strings.Contains(got, "Initializing") {
		t.Fatalf("view stuck on initializing screen: %q", got)
	}
	if !strings.Contains(got, "local.gguf") {
		t.Fatalf("view missing model label: %q", got)
	}
}

func TestLoadingStatus(t *testing.T) {
	m := New(Config{ModelLabel: "local.gguf"})
	m.loading = true
	if got := m.statusLine(); !strings.Contains(got, "Starting local llama.cpp runtime") {
		t.Fatalf("status = %q", got)
	}
}

func TestLoadProgressUpdatesStatus(t *testing.T) {
	m := New(Config{ModelLabel: "local.gguf"})
	m.loading = true
	next, _ := m.Update(loadProgressMsg{Text: "Fetching bin-ubuntu-x64.tar.gz"})
	got := next.(*Model).statusLine()
	if !strings.Contains(got, "Fetching bin-ubuntu-x64.tar.gz") {
		t.Fatalf("status did not show progress: %q", got)
	}
}

func TestTruncateStatus(t *testing.T) {
	if got := truncateStatus("  a   b  ", 20); got != "a b" {
		t.Fatalf("truncate = %q", got)
	}
	got := truncateStatus("abcdefghij", 5)
	if !strings.HasSuffix(got, "…") || len(got) >= len("abcdefghij") {
		t.Fatalf("truncate long = %q", got)
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

func TestLogoIsGreyAWithWhiteLocal(t *testing.T) {
	got := renderLogo(100)
	if !strings.Contains(got, "▄") || !strings.Contains(got, "▀") {
		t.Fatalf("grey block A missing from logo: %q", got)
	}
	if !strings.Contains(got, "local") {
		t.Fatalf("white local text missing from logo: %q", got)
	}
	if strings.ContainsAny(got, "🤖🐙🚀") {
		t.Fatal("logo must not use emoji")
	}
}

func TestPaletteTextModeAcceptsNavigationLetters(t *testing.T) {
	m := New(Config{})
	m.palette.setItems("repo", nil)
	m.palette.input.Focus()
	m.palette.open = true
	for _, r := range "hf:jk" {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := m.palette.input.Value(); got != "hf:jk" {
		t.Fatalf("j/k were swallowed as navigation, input = %q", got)
	}
}

func TestPaletteHelpDependsOnMode(t *testing.T) {
	m := New(Config{})
	if got := m.paletteHelp(); !strings.Contains(got, "filter") {
		t.Fatalf("menu help = %q", got)
	}
	m.palette.mode = "repo"
	if got := m.paletteHelp(); !strings.Contains(got, "submit") {
		t.Fatalf("text-mode help = %q", got)
	}
}
