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

	"alocal/internal/brand"
	"alocal/internal/local"
	"alocal/internal/runtime"
)

// appStyle leaves the terminal surface untouched. All spacing is explicit so
// the same layout is easy to reason about for hit-testing and resize.
var appStyle = lipgloss.NewStyle()

const (
	minInputHeight = 1
	maxInputHeight = 6
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
	// Status is plain text shown below the wordmark. The CLI fills it with
	// the real version, hardware and runtime state before the TUI starts.
	Status  []string
	NoColor bool
}

// Model is the bubbletea model for the chat TUI.
type Model struct {
	modelPath     string
	modelLabel    string
	systemPrompt  string
	mockHardware  string
	runtimeBinary string
	settings      local.Settings
	status        []string
	noColor       bool
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
	// paletteTop is the zero-based screen line where the palette box starts.
	// View updates it so mouse coordinates can be resolved without guessing.
	paletteTop int
}

// New creates a chat TUI model.
func New(cfg Config) *Model {
	settings := cfg.Settings
	if settings.Context <= 0 {
		settings = local.DefaultSettings()
	}
	ta := textarea.New()
	ta.Prompt = "❯ "
	ta.Placeholder = "Ask anything"
	ta.Focus()
	ta.CharLimit = 0
	ta.SetWidth(80)
	ta.SetHeight(minInputHeight)
	ta.ShowLineNumbers = false
	ta.EndOfBufferCharacter = ' '
	ta.KeyMap.InsertNewline.SetKeys("shift+enter")
	if !cfg.NoColor {
		ta.FocusedStyle.Base = textStyle
		ta.FocusedStyle.Text = textStyle
		ta.FocusedStyle.Prompt = accentStyle
		ta.FocusedStyle.Placeholder = faintStyle
		ta.FocusedStyle.CursorLine = lipgloss.NewStyle().Foreground(lipgloss.Color(brand.White))
		ta.BlurredStyle = ta.FocusedStyle
	}

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(brand.Purple))

	renderer := newRenderer(cfg.NoColor, 80)

	m := &Model{
		modelPath:     cfg.ModelPath,
		modelLabel:    cfg.ModelLabel,
		systemPrompt:  cfg.SystemPrompt,
		mockHardware:  cfg.MockHardware,
		runtimeBinary: cfg.RuntimeBinary,
		settings:      settings,
		status:        append([]string(nil), cfg.Status...),
		noColor:       cfg.NoColor,
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
			m.syncInputHeight()
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

	case tea.MouseMsg:
		if m.palette.open {
			return m, m.handlePaletteMouse(msg)
		}

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
		m.syncInputHeight()
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
		return "\n  Goodbye.\n\n"
	}
	if !m.ready {
		return "\n  Preparing Alocal…\n"
	}
	var s strings.Builder
	s.WriteString(m.renderHeader())
	s.WriteString("\n\n")
	if m.palette.open {
		m.paletteTop = m.headerHeight() + 1
		s.WriteString(m.renderPalette())
		s.WriteString("\n\n")
		s.WriteString("  ")
		s.WriteString(m.paletteHelp())
		return appStyle.Render(s.String())
	}
	s.WriteString(m.viewport.View())
	s.WriteString("\n")
	s.WriteString("  ")
	s.WriteString(m.statusLine())
	s.WriteString("\n")
	if rule := m.rule(); rule != "" {
		s.WriteString(rule)
		s.WriteString("\n")
	}
	s.WriteString("  ")
	s.WriteString(m.input.View())
	s.WriteString("\n")
	s.WriteString("  ")
	s.WriteString(m.helpBar())
	return appStyle.Render(s.String())
}

// renderHeader stacks the mark, the truthful startup status, and the active
// model. It is the only place the identity is repeated.
func (m *Model) renderHeader() string {
	var b strings.Builder
	b.WriteString(brand.Logo(!m.noColor, m.width))
	b.WriteString("\n")
	for _, line := range m.status {
		b.WriteString(m.renderStatusRow(line))
		b.WriteString("\n")
	}
	b.WriteString(m.renderModelRow())
	return b.String()
}

func (m *Model) renderStatusRow(line string) string {
	key, sep, value := splitStatus(line)
	if key == "" {
		return "  " + m.st(mutedStyle, truncateStatus(line, max(8, m.width-2)))
	}
	avail := m.width - 2 - lipgloss.Width(key) - lipgloss.Width(sep)
	if avail < 8 {
		avail = 8
	}
	return "  " + m.st(faintStyle, key) + sep + m.st(mutedStyle, truncateStatus(value, avail))
}

func (m *Model) renderModelRow() string {
	label := m.st(faintStyle, fmt.Sprintf("%-10s", "Model"))
	value := m.modelLabel
	if m.settings.Context > 0 {
		value += fmt.Sprintf(" · %d ctx", m.settings.Context)
	}
	row := "  " + label + m.st(textStyle, value)
	if m.width <= 0 || lipgloss.Width(row) <= m.width {
		return row
	}
	available := m.width - 12
	return "  " + label + m.st(textStyle, truncateStatus(value, available))
}

// statusLine reports only transient work. The steady state lives on the model
// row, so the line stays quiet when nothing is happening.
func (m *Model) statusLine() string {
	if m.loading {
		detail := m.loadingDetail
		if detail == "" {
			detail = "Starting local llama.cpp runtime..."
		}
		return m.st(accentStyle, m.spinner.View()) + " " + m.st(mutedStyle, detail)
	}
	if m.progressTotal > 0 && m.progressDone < m.progressTotal {
		pct := float64(m.progressDone) / float64(m.progressTotal) * 100
		return m.progressBar(pct) + "  " + m.st(faintStyle, fmt.Sprintf("%.0f%%", pct))
	}
	if m.streaming {
		return m.st(accentStyle, m.spinner.View()) + " " +
			m.st(mutedStyle, "Generating") + "  " + m.st(faintStyle, "Esc to stop")
	}
	if m.err != nil {
		return m.st(dangerStyle, "!") + " " + m.st(dangerStyle, m.err.Error())
	}
	if m.lastTokens > 0 {
		return m.st(successStyle, "✓") + " " + m.st(mutedStyle,
			fmt.Sprintf("%d tokens · %.1f tok/s", m.lastTokens, m.lastTokPerSec))
	}
	if m.client == nil {
		return m.st(faintStyle, "○") + " " + m.st(faintStyle, "no model loaded")
	}
	return m.st(successStyle, "●") + " " + m.st(faintStyle, "ready · local")
}

// progressBar is a quiet, determinate bar. The numeric percentage follows it
// in statusLine so the state never depends on colour or shape alone.
func (m *Model) progressBar(pct float64) string {
	width := 24
	if m.width > 0 && m.width < 60 {
		width = 12
	}
	filled := int(pct / 100 * float64(width))
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	return m.st(accentStyle, strings.Repeat("━", filled)) +
		m.st(ruleStyle, strings.Repeat("━", width-filled))
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

func (m *Model) headerHeight() int {
	logo := brand.Logo(!m.noColor, m.width)
	return strings.Count(logo, "\n") + 1 + len(m.status) + 1
}

func (m *Model) handleResize() {
	headerH := m.headerHeight()
	inputH := m.input.Height()
	if inputH < minInputHeight {
		inputH = minInputHeight
	}
	ruleH := 0
	if m.width >= 26 {
		ruleH = 1
	}
	vpH := m.height - headerH - inputH - ruleH - 3
	if vpH < 3 {
		vpH = 3
	}
	vpW := m.width - 4
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
		m.renderer = newRenderer(m.noColor, m.width-8)
	}
	m.updateViewport()
}

func (m *Model) syncInputHeight() {
	lines := strings.Count(m.input.Value(), "\n") + 1
	if lines < minInputHeight {
		lines = minInputHeight
	}
	if lines > maxInputHeight {
		lines = maxInputHeight
	}
	if m.input.Height() == lines {
		return
	}
	m.input.SetHeight(lines)
	m.handleResize()
}

func newRenderer(noColor bool, wrap int) *glamour.TermRenderer {
	style := "dark"
	if noColor {
		style = "notty"
	}
	if wrap < 20 {
		wrap = 20
	}
	r, _ := glamour.NewTermRenderer(glamour.WithStandardStyle(style), glamour.WithWordWrap(wrap))
	return r
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
		var b strings.Builder
		b.WriteString("  " + m.st(titleStyle, "No model loaded") + "\n\n")
		b.WriteString("  " + m.st(mutedStyle, "Alocal runs GGUF models locally with llama.cpp.") + "\n")
		b.WriteString("  " + m.st(mutedStyle, "Choose one to begin.") + "\n\n")
		b.WriteString("  " + m.hint("Ctrl+X", "download or switch models") + "\n")
		b.WriteString("  " + m.st(faintStyle, "alocal pull bartowski/Qwen2.5-7B-Instruct-GGUF") + "\n")
		return b.String()
	}
	var b strings.Builder
	b.WriteString("  " + m.st(titleStyle, "Ready") + "\n\n")
	b.WriteString("  " + m.st(mutedStyle, "Type a message below. Everything stays on this machine.") + "\n\n")
	b.WriteString("  " + m.hint("Ctrl+X", "models · downloads · settings") + "\n")
	return b.String()
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
		sb.WriteString(m.st(assistantStyle, "Alocal") + "\n")
		sb.WriteString(m.renderMarkdown(m.streamBuf))
		sb.WriteString(m.st(accentStyle, "▊") + "\n")
	}
	return sb.String()
}

func (m *Model) renderOneMessage(msg chatMessage) string {
	var sb strings.Builder
	switch msg.Role {
	case "user":
		sb.WriteString(m.st(userStyle, "You") + "\n")
		for _, line := range strings.Split(msg.Content, "\n") {
			sb.WriteString("  " + m.st(textStyle, line) + "\n")
		}
	case "assistant":
		sb.WriteString(m.st(assistantStyle, "Alocal") + "\n")
		sb.WriteString(m.renderMarkdown(msg.Content))
	case "system":
		sb.WriteString(m.st(faintStyle, "System") + "\n")
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
	return strings.Trim(rendered, "\n") + "\n"
}

// helpBar groups the keys that matter in the composer, dropping lower-priority
// hints when the terminal is narrow.
func (m *Model) helpBar() string {
	if m.width > 0 && m.width < 72 {
		return m.hintLine(
			[2]string{"Enter", "send"},
			[2]string{"Ctrl+X", "commands"},
			[2]string{"Esc", "stop"},
		)
	}
	return m.hintLine(
		[2]string{"Enter", "send"},
		[2]string{"Shift+Enter", "newline"},
		[2]string{"Ctrl+X", "commands"},
		[2]string{"Esc", "stop"},
	)
}

// splitStatus separates "Hardware  RTX 4090" into its label, the original
// spacing and the value. Keeping the spacing makes plain-text status rows
// stable while the TUI can style the label quietly.
func splitStatus(line string) (key, sep, value string) {
	i := strings.Index(line, "  ")
	if i < 0 {
		return "", "", line
	}
	j := i
	for j < len(line) && line[j] == ' ' {
		j++
	}
	return line[:i], line[i:j], line[j:]
}

func (m *Model) handleSlash(input string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(input)
	cmd := strings.ToLower(parts[0])
	m.input.Reset()
	m.syncInputHeight()
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
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	return string(runes[:max-1]) + "…"
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
