// Package chat implements an interactive terminal chat TUI with a local
// llama.cpp runtime and GGUF models downloaded from Hugging Face.
package chat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"

	"alocal/internal/local"
	"alocal/internal/runtime"
)

var (
	appStyle       = lipgloss.NewStyle().Padding(0, 1)
	statusStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Padding(0, 1)
	userStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	assistantStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("111"))
	dimStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	helpStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	errorStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("203"))
)

type chatMessage struct {
	Role    string
	Content string
}

type tokenMsg struct {
	Token  string
	Done   bool
	Tokens int
}

type errMsg struct{ Err error }

type infoMsg struct{ Text string }

type loadProgressMsg struct{ Text string }

type loadMsg struct {
	Model  local.Model
	Path   string
	Label  string
	Err    error
	Server *runtime.Server
}

// Config controls a chat session.
type Config struct {
	ModelPath     string
	ModelLabel    string
	SystemPrompt  string
	MockHardware  string
	RuntimeBinary string
	Settings      local.Settings
}

// Model is the bubbletea model for the chat TUI.
type Model struct {
	modelPath     string
	modelLabel    string
	systemPrompt  string
	mockHardware  string
	runtimeBinary string
	settings      local.Settings
	width         int
	height        int

	messages      []chatMessage
	input         textarea.Model
	viewport      viewport.Model
	spinner       spinner.Model
	streaming     bool
	loading       bool
	loadingDetail string
	initialCmd    tea.Cmd
	streamBuf     string
	cancelFunc    context.CancelFunc
	streamStart   time.Time
	err           error
	quitting      bool
	ready         bool
	lastTokPerSec float64
	lastTokens    int
	progressDone  int64
	progressTotal int64

	server   *runtime.Server
	client   *runtime.Client
	program  *tea.Program
	renderer *glamour.TermRenderer
	palette  paletteState
}

// New creates a chat TUI model.
func New(cfg Config) *Model {
	settings := cfg.Settings
	if settings.Context <= 0 {
		settings = local.DefaultSettings()
	}
	ta := textarea.New()
	ta.Placeholder = "Type a message... (Enter to send, Shift+Enter for newline)"
	ta.Focus()
	ta.CharLimit = 0
	ta.SetWidth(80)
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetKeys("shift+enter")

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	renderer, _ := glamour.NewTermRenderer(glamour.WithStandardStyle("dark"), glamour.WithWordWrap(80))

	m := &Model{
		modelPath:     cfg.ModelPath,
		modelLabel:    cfg.ModelLabel,
		systemPrompt:  cfg.SystemPrompt,
		mockHardware:  cfg.MockHardware,
		runtimeBinary: cfg.RuntimeBinary,
		settings:      settings,
		width:         100,
		height:        30,
		input:         ta,
		spinner:       sp,
		renderer:      renderer,
		palette:       newPalette(),
	}
	if m.modelLabel == "" {
		m.modelLabel = "no model loaded"
	}
	// Initialize the viewport immediately. Waiting for the first terminal
	// resize message could otherwise leave the UI showing "Initializing..."
	// forever in embedders that do not report size right away.
	m.handleResize()
	return m
}

// SetProgram provides the program reference used for async token delivery.
func (m *Model) SetProgram(p *tea.Program) { m.program = p }

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{textarea.Blink, m.spinner.Tick}
	if m.initialCmd != nil {
		cmds = append(cmds, m.initialCmd)
	}
	return tea.Batch(cmds...)
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.palette.open {
			if cmd, consumed := m.handlePaletteKey(msg); consumed {
				return m, cmd
			}
		}
		switch msg.String() {
		case "ctrl+x":
			if m.streaming {
				m.cancelStream()
			}
			if !m.palette.open {
				m.palette.setMenu()
			}
			m.palette.open = true
			return m, nil
		case "ctrl+c":
			if m.streaming {
				m.cancelStream()
				return m, nil
			}
			m.quitting = true
			return m, tea.Quit
		case "ctrl+d":
			if m.streaming {
				m.cancelStream()
			}
			m.quitting = true
			return m, tea.Quit
		case "esc":
			if m.streaming {
				m.cancelStream()
				return m, nil
			}
		case "enter":
			if m.streaming || m.palette.open {
				return m, nil
			}
			content := strings.TrimSpace(m.input.Value())
			if content == "" {
				return m, nil
			}
			if strings.HasPrefix(content, "/") {
				return m.handleSlash(content)
			}
			m.messages = append(m.messages, chatMessage{Role: "user", Content: content})
			m.input.Reset()
			m.streaming = true
			m.streamBuf = ""
			m.err = nil
			m.streamStart = time.Now()
			m.updateViewport()
			return m, m.startStream()
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.handleResize()
		return m, nil

	case tokenMsg:
		if msg.Done {
			m.streaming = false
			m.cancelFunc = nil
			if m.streamBuf != "" {
				m.messages = append(m.messages, chatMessage{Role: "assistant", Content: m.streamBuf})
				m.streamBuf = ""
			}
			m.lastTokens = msg.Tokens
			if m.lastTokens > 0 && !m.streamStart.IsZero() {
				m.lastTokPerSec = float64(m.lastTokens) / time.Since(m.streamStart).Seconds()
			}
			m.updateViewport()
			return m, nil
		}
		m.streamBuf += msg.Token
		m.updateViewport()
		return m, nil

	case errMsg:
		m.streaming = false
		m.cancelFunc = nil
		m.err = msg.Err
		if m.streamBuf != "" {
			m.messages = append(m.messages, chatMessage{Role: "assistant", Content: m.streamBuf + "\n\n*[error]*"})
			m.streamBuf = ""
		}
		m.updateViewport()
		return m, nil

	case infoMsg:
		m.appendSystem(msg.Text)
		m.updateViewport()
		return m, nil

	case loadProgressMsg:
		m.loadingDetail = msg.Text
		return m, nil

	case progressMsg:
		m.progressDone, m.progressTotal = msg.Done, msg.Total
		return m, nil

	case filesMsg:
		if msg.Err != nil {
			m.appendSystem("Could not list GGUF files: " + msg.Err.Error())
			m.palette.setMenu()
		} else if len(msg.Files) == 0 {
			m.appendSystem(fmt.Sprintf("No GGUF files found in %s.", msg.Repo))
			m.palette.setMenu()
		} else {
			m.palette.setItems("file", msg.Files)
			m.palette.open = true
		}
		m.updateViewport()
		return m, nil

	case downloadMsg:
		if msg.Err != nil {
			m.appendSystem("Download failed: " + msg.Err.Error())
		} else {
			m.appendSystem(fmt.Sprintf("Downloaded %s (%.1f GB).", msg.Model.ID, float64(msg.Model.SizeBytes)/1e9))
			m.appendSystem("Loading it now ...")
			cmds = append(cmds, m.loadModelCmd(msg.Model))
		}
		m.updateViewport()
		return m, tea.Batch(cmds...)

	case loadMsg:
		m.loading = false
		m.loadingDetail = ""
		if msg.Err != nil {
			m.appendSystem("Model load failed: " + msg.Err.Error())
		} else {
			if m.server != nil {
				_ = m.server.Close()
			}
			m.server = msg.Server
			m.client = runtime.NewClient(msg.Server.BaseURL())
			m.modelPath = msg.Path
			m.modelLabel = msg.Label
			m.settings.LastModelID = msg.Model.ID
			_ = m.settings.Save()
			m.appendSystem("Loaded " + msg.Label + " — chatting locally via llama.cpp.")
		}
		m.updateViewport()
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)
	}

	if !m.streaming && !m.palette.open && m.input.Focused() {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
	}
	if m.palette.open && m.palette.textMode() {
		var cmd tea.Cmd
		m.palette.input, cmd = m.palette.input.Update(msg)
		cmds = append(cmds, cmd)
	}
	var vpCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)
	cmds = append(cmds, vpCmd)
	return m, tea.Batch(cmds...)
}

// View implements tea.Model.
func (m *Model) View() string {
	if m.quitting {
		return "\n  Goodbye!\n\n"
	}
	if !m.ready {
		return "\n  Preparing Alocal interface...\n"
	}
	var s strings.Builder
	s.WriteString(renderLogo(m.width))
	s.WriteString("\n")
	s.WriteString(dimStyle.Render(fmt.Sprintf("  %s · llama.cpp · %d ctx", m.modelLabel, m.settings.Context)))
	s.WriteString("\n\n")
	if m.palette.open {
		s.WriteString(m.renderPalette())
		s.WriteString("\n")
		s.WriteString(helpStyle.Render(m.paletteHelp()))
		return appStyle.Render(s.String())
	}
	s.WriteString(m.viewport.View())
	s.WriteString("\n")
	s.WriteString(m.statusLine())
	s.WriteString("\n")
	s.WriteString(m.input.View())
	s.WriteString("\n")
	s.WriteString(helpStyle.Render("Enter send · Shift+Enter newline · Ctrl+X commands · Esc stop · Ctrl+C quit"))
	return appStyle.Render(s.String())
}

func (m *Model) statusLine() string {
	if m.loading {
		detail := m.loadingDetail
		if detail == "" {
			detail = "Starting local llama.cpp runtime..."
		}
		return statusStyle.Render(fmt.Sprintf("%s %s", m.spinner.View(), detail))
	}
	if m.progressTotal > 0 && m.progressDone < m.progressTotal {
		pct := float64(m.progressDone) / float64(m.progressTotal) * 100
		return statusStyle.Render(fmt.Sprintf("Downloading GGUF %.1f%% (%.1f / %.1f GB)", pct,
			float64(m.progressDone)/1e9, float64(m.progressTotal)/1e9))
	}
	if m.streaming {
		return statusStyle.Render(fmt.Sprintf("%s Generating... (Esc to stop)", m.spinner.View()))
	}
	if m.err != nil {
		return errorStyle.Render(fmt.Sprintf("✗ %s", m.err))
	}
	if m.lastTokens > 0 {
		return statusStyle.Render(fmt.Sprintf("✓ %d tokens · %.1f tok/s", m.lastTokens, m.lastTokPerSec))
	}
	return statusStyle.Render("Ready · local GGUF runtime")
}

func (m *Model) appendSystem(text string) {
	m.messages = append(m.messages, chatMessage{Role: "system", Content: text})
}

func (m *Model) cancelStream() {
	if m.cancelFunc != nil {
		m.cancelFunc()
		m.cancelFunc = nil
	}
	m.streaming = false
	if m.streamBuf != "" {
		m.messages = append(m.messages, chatMessage{Role: "assistant", Content: m.streamBuf + "\n\n*[interrupted]*"})
		m.streamBuf = ""
	}
	m.updateViewport()
}

func (m *Model) handleResize() {
	headerH := 6
	inputH := 5
	statusH := 1
	helpH := 1
	vpH := m.height - headerH - inputH - statusH - helpH - 2
	if vpH < 3 {
		vpH = 3
	}
	vpW := m.width - 2
	if vpW < 20 {
		vpW = 20
	}
	if !m.ready {
		m.viewport = viewport.New(vpW, vpH)
		m.viewport.SetContent(m.renderWelcome())
		m.ready = true
	} else {
		m.viewport.Width = vpW
		m.viewport.Height = vpH
	}
	m.input.SetWidth(vpW - 2)
	if m.width > 10 {
		m.renderer, _ = glamour.NewTermRenderer(glamour.WithStandardStyle("dark"), glamour.WithWordWrap(m.width-6))
	}
	m.updateViewport()
}

func (m *Model) updateViewport() {
	if !m.ready {
		return
	}
	m.viewport.SetContent(m.renderMessages())
	m.viewport.GotoBottom()
}

func (m *Model) renderWelcome() string {
	if m.client == nil {
		return "  Alocal is ready. No GGUF model is loaded yet.\n\n" +
			"  Press " + lipgloss.NewStyle().Bold(true).Render("Ctrl+X") + " → Download GGUF from Hugging Face\n" +
			"  or run: alocal pull bartowski/Qwen2.5-7B-Instruct-GGUF\n\n" +
			"  Then press Ctrl+X → Switch model to start chatting.\n"
	}
	return "  Local GGUF runtime ready.\n\n" +
		"  Type a message and press Enter to start.\n" +
		"  Press " + lipgloss.NewStyle().Bold(true).Render("Ctrl+X") + " for models, Hugging Face downloads, MCP and settings.\n"
}

func (m *Model) renderMessages() string {
	if len(m.messages) == 0 && m.streamBuf == "" {
		return m.renderWelcome()
	}
	var sb strings.Builder
	for _, msg := range m.messages {
		sb.WriteString(m.renderOneMessage(msg))
		sb.WriteString("\n")
	}
	if m.streamBuf != "" {
		sb.WriteString(assistantStyle.Render("┃ Assistant") + "\n")
		sb.WriteString(m.renderMarkdown(m.streamBuf))
		sb.WriteString(dimStyle.Render(" ▊") + "\n")
	}
	return sb.String()
}

func (m *Model) renderOneMessage(msg chatMessage) string {
	var sb strings.Builder
	switch msg.Role {
	case "user":
		sb.WriteString(userStyle.Render("┃ You") + "\n")
		for _, line := range strings.Split(msg.Content, "\n") {
			sb.WriteString("  " + line + "\n")
		}
	case "assistant":
		sb.WriteString(assistantStyle.Render("┃ Assistant") + "\n")
		sb.WriteString(m.renderMarkdown(msg.Content))
	case "system":
		sb.WriteString(dimStyle.Render("─── system ───") + "\n")
		sb.WriteString(m.renderMarkdown(msg.Content))
	}
	return sb.String()
}

func (m *Model) renderMarkdown(content string) string {
	if m.renderer == nil {
		return "  " + content + "\n"
	}
	rendered, err := m.renderer.Render(content)
	if err != nil {
		return "  " + content + "\n"
	}
	return rendered
}

func (m *Model) handleSlash(input string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(input)
	cmd := strings.ToLower(parts[0])
	m.input.Reset()
	switch cmd {
	case "/help":
		m.appendSystem("Commands: /help, /clear, /model <id>, /system <prompt>, /info, /models, /quit. Ctrl+X opens the command palette.")
	case "/clear":
		m.messages = nil
		m.lastTokens, m.lastTokPerSec, m.err = 0, 0, nil
	case "/model":
		if len(parts) < 2 {
			m.appendSystem("Usage: /model <local model id>")
		} else {
			store, err := local.LoadModels()
			if err != nil {
				m.appendSystem("Model store error: " + err.Error())
			} else if lm, ok := store.Find(parts[1]); ok {
				m.appendSystem("Loading " + lm.ID + " ...")
				return m, m.loadModelCmd(lm)
			} else {
				m.appendSystem("Unknown local model. Open Ctrl+X → Switch model.")
			}
		}
	case "/system":
		if len(parts) < 2 {
			m.appendSystem("System prompt: " + orDefault(m.systemPrompt, "(none)"))
		} else {
			m.systemPrompt = strings.Join(parts[1:], " ")
			m.settings.SystemPrompt = m.systemPrompt
			_ = m.settings.Save()
			m.appendSystem("System prompt set: " + m.systemPrompt)
		}
	case "/info":
		m.appendSystem(fmt.Sprintf("Model: %s\nPath: %s\nRuntime: llama.cpp\nContext: %d\nGPU layers: %d\nTemperature: %.2f\nMCP config: %s",
			m.modelLabel, m.modelPath, m.settings.Context, m.settings.GPULayers, m.settings.Temperature, local.MCPFile()))
	case "/models":
		labels := localModelLabels()
		if len(labels) == 0 {
			m.appendSystem("No GGUFs downloaded yet. Ctrl+X → Download GGUF from Hugging Face.")
		} else {
			m.appendSystem("Local GGUFs:\n  " + strings.Join(labels, "\n  "))
		}
	case "/quit":
		m.quitting = true
		return m, tea.Quit
	default:
		m.appendSystem("Unknown command: " + cmd + ". Type /help.")
	}
	m.updateViewport()
	return m, nil
}

func (m *Model) startStream() tea.Cmd {
	if m.client == nil {
		return func() tea.Msg { return errMsg{Err: errors.New("no local model is loaded")} }
	}
	var msgs []runtime.Message
	if m.systemPrompt != "" {
		msgs = append(msgs, runtime.Message{Role: "system", Content: m.systemPrompt})
	}
	for _, cm := range m.messages {
		if cm.Role == "system" {
			continue
		}
		msgs = append(msgs, runtime.Message{Role: cm.Role, Content: cm.Content})
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFunc = cancel
	prog := m.program
	return func() tea.Msg {
		var tokens int
		err := m.client.ChatStream(ctx, msgs, runtime.GenOptions{
			Temperature: m.settings.Temperature,
			MaxTokens:   m.settings.MaxTokens,
		}, func(token string, done bool, n int) {
			if done {
				tokens = n
				return
			}
			if prog != nil {
				prog.Send(tokenMsg{Token: token})
			}
		})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return tokenMsg{Done: true}
			}
			return errMsg{Err: err}
		}
		return tokenMsg{Done: true, Tokens: tokens}
	}
}

func (m *Model) loadModelCmd(lm local.Model) tea.Cmd {
	m.loading = true
	m.loadingDetail = "Preparing local runtime..."
	report := func(text string) {
		if m.program != nil {
			m.program.Send(loadProgressMsg{Text: truncateStatus(text, 90)})
		}
	}
	return func() tea.Msg {
		binary := m.runtimeBinary
		if binary == "" {
			var err error
			binary, err = runtime.EnsureWithProgress(context.Background(), report)
			if err != nil {
				return loadMsg{Err: err, Model: lm}
			}
		}
		srv, err := runtime.StartWithProgress(context.Background(), binary, lm.Path, runtime.Options{
			Context: m.settings.Context, GPULayers: m.settings.GPULayers,
		}, report)
		return loadMsg{Server: srv, Model: lm, Path: lm.Path, Label: lm.ID, Err: err}
	}
}

func truncateStatus(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		return s[:max-1] + "…"
	}
	return s
}

// Run starts the chat TUI with a local llama.cpp server.
func Run(cfg Config) error {
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return errors.New("chat requires an interactive terminal (TTY); run it directly in your terminal, not through a pipe")
	}
	model := New(cfg)

	// Show the UI immediately, then load the model in the background. This
	// avoids a long silent wait while llama.cpp is downloaded or a large GGUF
	// is loaded, and lets Ctrl+X stay usable during that time.
	if cfg.ModelPath != "" {
		model.initialCmd = model.loadModelCmd(local.Model{
			ID:    cfg.ModelLabel,
			Path:  cfg.ModelPath,
			Quant: guessQuant(cfg.ModelPath),
		})
	}

	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	model.SetProgram(p)
	_, err := p.Run()
	if model.server != nil {
		_ = model.server.Close()
	}
	return err
}
