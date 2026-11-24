// Package chat implements an interactive terminal chat TUI using bubbletea.
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

	"alocal/internal/ollama"
)

// Styles
var (
	appStyle = lipgloss.NewStyle().Padding(0, 1)

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("205")).
			Padding(0, 1)

	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244")).
			Padding(0, 1)

	userStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("42"))

	assistantStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("111"))

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244"))

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	errorStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("203"))
)

// chatMessage is a message in the conversation.
type chatMessage struct {
	Role    string
	Content string
}

// tokenMsg carries a streaming token from the Ollama callback.
type tokenMsg struct {
	Token string
	Done  bool
	Stats *ollama.ChatResponse
}

// errMsg carries an error from Ollama.
type errMsg struct{ Err error }

// modelsMsg carries the list of available models.
type modelsMsg struct {
	Models []ollama.ModelInfo
	Err    error
}

// Model is the bubbletea model for the chat TUI.
type Model struct {
	// Config
	modelName    string
	systemPrompt string
	width        int
	height       int

	// State
	messages  []chatMessage
	input     textarea.Model
	viewport  viewport.Model
	spinner   spinner.Model
	streaming bool
	streamBuf string
	err       error
	quitting  bool
	ready     bool

	// Cancel streaming
	cancelFunc context.CancelFunc

	// Stats from last response
	lastTokensPerSec float64
	lastEvalCount    int

	// Ollama
	client *ollama.Client

	// Markdown renderer
	renderer *glamour.TermRenderer

	// Program reference for async sends (set externally)
	program *tea.Program
}

// Config controls the chat session.
type Config struct {
	ModelName    string
	OllamaURL    string
	SystemPrompt string
}

// New creates a new chat TUI model.
func New(cfg Config) *Model {
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

	client := ollama.New(cfg.OllamaURL)

	renderer, _ := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(80),
	)

	return &Model{
		modelName:    cfg.ModelName,
		systemPrompt: cfg.SystemPrompt,
		input:        ta,
		spinner:      sp,
		client:       client,
		renderer:     renderer,
	}
}

// SetProgram sets the tea.Program reference for async message delivery.
func (m *Model) SetProgram(p *tea.Program) {
	m.program = p
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		m.spinner.Tick,
	)
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
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
			if m.streaming {
				return m, nil
			}
			content := strings.TrimSpace(m.input.Value())
			if content == "" {
				return m, nil
			}

			// Handle slash commands
			if strings.HasPrefix(content, "/") {
				return m.handleCommand(content)
			}

			// Add user message
			m.messages = append(m.messages, chatMessage{Role: "user", Content: content})
			m.input.Reset()
			m.streaming = true
			m.streamBuf = ""
			m.err = nil
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
			if msg.Stats != nil && msg.Stats.EvalDuration > 0 {
				m.lastEvalCount = msg.Stats.EvalCount
				m.lastTokensPerSec = float64(msg.Stats.EvalCount) / (float64(msg.Stats.EvalDuration) / 1e9)
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

	case modelsMsg:
		if msg.Err != nil {
			m.messages = append(m.messages, chatMessage{Role: "system",
				Content: fmt.Sprintf("Error listing models: %s", msg.Err)})
		} else {
			var sb strings.Builder
			sb.WriteString("**Locally available models:**\n\n")
			if len(msg.Models) == 0 {
				sb.WriteString("No models found. Pull one with `ollama pull <model>`\n")
			}
			for _, mi := range msg.Models {
				sizeGB := float64(mi.Size) / (1024 * 1024 * 1024)
				sb.WriteString(fmt.Sprintf("- `%s` (%.1f GB)\n", mi.Name, sizeGB))
			}
			m.messages = append(m.messages, chatMessage{Role: "system", Content: sb.String()})
		}
		m.updateViewport()
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)
	}

	// Update input when not streaming
	if !m.streaming {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
	}

	// Update viewport
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

// View implements tea.Model.
func (m *Model) View() string {
	if m.quitting {
		return "\n  Goodbye! 👋\n\n"
	}
	if !m.ready {
		return "\n  Initializing..."
	}

	var s strings.Builder

	// Header
	header := headerStyle.Render(fmt.Sprintf("🤖 Alocal Chat · %s", m.modelName))
	s.WriteString(header + "\n")

	// Messages viewport
	s.WriteString(m.viewport.View() + "\n")

	// Status
	s.WriteString(m.statusLine() + "\n")

	// Input
	s.WriteString(m.input.View() + "\n")

	// Help bar
	s.WriteString(helpStyle.Render("Enter: send · Shift+Enter: newline · /help: commands · Ctrl+C: quit"))

	return appStyle.Render(s.String())
}

func (m *Model) statusLine() string {
	if m.streaming {
		return statusStyle.Render(fmt.Sprintf("%s Generating... (Esc to stop)", m.spinner.View()))
	}
	if m.err != nil {
		return errorStyle.Render(fmt.Sprintf("✗ %s", m.err))
	}
	if m.lastTokensPerSec > 0 {
		return statusStyle.Render(fmt.Sprintf("✓ %d tokens · %.1f tok/s", m.lastEvalCount, m.lastTokensPerSec))
	}
	return statusStyle.Render("Ready")
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
	headerH := 2
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
		m.renderer, _ = glamour.NewTermRenderer(
			glamour.WithAutoStyle(),
			glamour.WithWordWrap(m.width-6),
		)
	}

	m.updateViewport()
}

// handleCommand processes slash commands.
func (m *Model) handleCommand(input string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(input)
	cmd := strings.ToLower(parts[0])
	m.input.Reset()

	switch cmd {
	case "/help":
		m.messages = append(m.messages, chatMessage{Role: "system", Content: `**Commands:**
- ` + "`/help`" + ` — Show this help
- ` + "`/clear`" + ` — Clear conversation history
- ` + "`/model <name>`" + ` — Switch model (e.g. ` + "`/model llama3.2:3b`" + `)
- ` + "`/system <prompt>`" + ` — Set system prompt
- ` + "`/info`" + ` — Show session info
- ` + "`/models`" + ` — List locally available Ollama models
- ` + "`/quit`" + ` — Exit`})
		m.updateViewport()

	case "/clear":
		m.messages = nil
		m.lastTokensPerSec = 0
		m.lastEvalCount = 0
		m.err = nil
		m.updateViewport()

	case "/model":
		if len(parts) < 2 {
			m.messages = append(m.messages, chatMessage{Role: "system",
				Content: "Usage: `/model <name>` — e.g. `/model llama3.2:3b`"})
		} else {
			m.modelName = parts[1]
			m.messages = append(m.messages, chatMessage{Role: "system",
				Content: fmt.Sprintf("Switched to model **%s**", m.modelName)})
		}
		m.updateViewport()

	case "/system":
		if len(parts) < 2 {
			p := "(none)"
			if m.systemPrompt != "" {
				p = m.systemPrompt
			}
			m.messages = append(m.messages, chatMessage{Role: "system",
				Content: fmt.Sprintf("System prompt: %s", p)})
		} else {
			m.systemPrompt = strings.Join(parts[1:], " ")
			m.messages = append(m.messages, chatMessage{Role: "system",
				Content: fmt.Sprintf("System prompt set: *%s*", m.systemPrompt)})
		}
		m.updateViewport()

	case "/info":
		m.messages = append(m.messages, chatMessage{Role: "system",
			Content: fmt.Sprintf("**Session Info**\n- Model: `%s`\n- Ollama: `%s`\n- Messages: %d\n- System prompt: %s",
				m.modelName, m.client.BaseURL, len(m.messages),
				func() string {
					if m.systemPrompt == "" {
						return "(none)"
					}
					return m.systemPrompt
				}())})
		m.updateViewport()

	case "/models":
		return m, m.fetchModels()

	case "/quit", "/exit":
		m.quitting = true
		return m, tea.Quit

	default:
		m.messages = append(m.messages, chatMessage{Role: "system",
			Content: fmt.Sprintf("Unknown command: `%s`. Type `/help` for help.", cmd)})
		m.updateViewport()
	}

	return m, nil
}

// startStream launches the Ollama streaming request in a goroutine.
func (m *Model) startStream() tea.Cmd {
	var msgs []ollama.Message

	if m.systemPrompt != "" {
		msgs = append(msgs, ollama.Message{Role: "system", Content: m.systemPrompt})
	}
	for _, cm := range m.messages {
		if cm.Role == "system" {
			continue
		}
		msgs = append(msgs, ollama.Message{Role: cm.Role, Content: cm.Content})
	}

	req := ollama.ChatRequest{
		Model:    m.modelName,
		Messages: msgs,
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFunc = cancel

	prog := m.program

	// Return a Cmd that launches the streaming goroutine
	return func() tea.Msg {
		var finalResp *ollama.ChatResponse
		err := m.client.ChatStream(ctx, req, func(token string, done bool, resp *ollama.ChatResponse) {
			if done {
				finalResp = resp
				return
			}
			// Send each token as a message to the program
			if prog != nil {
				prog.Send(tokenMsg{Token: token, Done: false})
			}
		})

		if err != nil {
			// Esc/Ctrl+C while generating is a deliberate interruption, not a failure.
			if errors.Is(err, context.Canceled) {
				return tokenMsg{Done: true}
			}
			return errMsg{Err: err}
		}
		return tokenMsg{Done: true, Stats: finalResp}
	}
}

// fetchModels lists locally available Ollama models.
func (m *Model) fetchModels() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		models, err := m.client.ListModels(ctx)
		return modelsMsg{Models: models, Err: err}
	}
}

func (m *Model) updateViewport() {
	if !m.ready {
		return
	}
	m.viewport.SetContent(m.renderMessages())
	m.viewport.GotoBottom()
}

func (m *Model) renderWelcome() string {
	return fmt.Sprintf(`
  Welcome to %s Chat!

  Chatting with %s via Ollama.
  %s

  Type a message and press Enter to start.
  Type %s for commands.
`,
		headerStyle.Render("Alocal"),
		lipgloss.NewStyle().Bold(true).Render(m.modelName),
		dimStyle.Render("Responses stream in real-time from your local LLM."),
		lipgloss.NewStyle().Bold(true).Render("/help"),
	)
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

	// Streaming buffer
	if m.streamBuf != "" {
		sb.WriteString(assistantStyle.Render("┃ Assistant") + "\n")
		sb.WriteString(m.renderMarkdown(m.streamBuf))
		sb.WriteString(dimStyle.Render(" ▊") + "\n") // cursor
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

// Run starts the chat TUI. This is the main entry point.
func Run(cfg Config) error {
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return errors.New("chat requires an interactive terminal (TTY); run it directly in your terminal, not through a pipe")
	}

	model := New(cfg)

	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	model.SetProgram(p)

	_, err := p.Run()
	return err
}
