package chat

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"alocal/internal/catalog"
	"alocal/internal/hardware"
	"alocal/internal/hf"
	"alocal/internal/local"
	"alocal/internal/match"
	"alocal/internal/scoring"
)

type filesMsg struct {
	Repo  string
	Files []string
	Err   error
}

type downloadMsg struct {
	Model local.Model
	Err   error
}

type progressMsg struct {
	Done  int64
	Total int64
}

type paletteState struct {
	open     bool
	mode     string // menu, switch, repo, file, explain, mcp, mcp-add, settings
	items    []string
	selected int
	filter   string
	input    textinput.Model
	repo     string
	files    []string
}

func newPalette() paletteState {
	input := textinput.New()
	input.CharLimit = 256
	input.Width = 48
	return paletteState{mode: "menu", items: menuItems(), input: input}
}

func menuItems() []string {
	return []string{
		"Switch model",
		"Download GGUF from Hugging Face",
		"Scan hardware",
		"Recommend a model",
		"Explain a model",
		"MCP servers",
		"Settings",
		"Help",
		"Quit",
	}
}

func (p *paletteState) setMenu() {
	p.mode = "menu"
	p.items = menuItems()
	p.selected = 0
	p.filter = ""
	p.input.Reset()
}

func (p *paletteState) setItems(mode string, items []string) {
	p.mode = mode
	p.items = items
	p.selected = 0
	p.filter = ""
	p.input.Reset()
}

func (p *paletteState) visible() []string {
	if p.filter == "" {
		return p.items
	}
	needle := strings.ToLower(p.filter)
	var out []string
	for _, item := range p.items {
		if strings.Contains(strings.ToLower(item), needle) {
			out = append(out, item)
		}
	}
	return out
}

func (p *paletteState) move(delta int) {
	count := len(p.visible())
	if count == 0 {
		return
	}
	p.selected = (p.selected + delta + count) % count
}

func localModelLabels() []string {
	store, err := local.LoadModels()
	if err != nil {
		return nil
	}
	var labels []string
	for _, m := range store.Models {
		labels = append(labels, fmt.Sprintf("%s  [%s, %.1f GB]", m.ID, orDefault(m.Quant, "GGUF"), float64(m.SizeBytes)/1e9))
	}
	return labels
}

func mcpLabels() []string {
	cfg, err := local.LoadMCP()
	if err != nil {
		return nil
	}
	var labels []string
	for _, s := range cfg.Servers {
		state := "off"
		if s.Enabled {
			state = "on"
		}
		labels = append(labels, fmt.Sprintf("%s  %s  %s %s", s.Name, state, s.Command, strings.Join(s.Args, " ")))
	}
	return append(labels, "(press A to add a server, D to delete)")
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// handlePaletteKey processes input while the Ctrl+X palette is open.
// It returns a command and whether the key was consumed.
func (m *Model) handlePaletteKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	p := &m.palette
	switch msg.String() {
	case "ctrl+x", "esc":
		if p.mode != "menu" {
			p.setMenu()
			return nil, true
		}
		p.open = false
		return nil, true
	case "up", "k":
		p.move(-1)
		return nil, true
	case "down", "j":
		p.move(1)
		return nil, true
	case "enter":
		return m.paletteEnter()
	case "backspace":
		if p.textMode() {
			value := p.input.Value()
			if len(value) > 0 {
				p.input.SetValue(value[:len(value)-1])
			}
		} else if len(p.filter) > 0 {
			p.filter = p.filter[:len(p.filter)-1]
			p.selected = 0
		}
		return nil, true
	case "a":
		if p.mode == "mcp" {
			p.mode = "mcp-add"
			p.input.Reset()
			p.input.Focus()
			return nil, true
		}
	case "d":
		if p.mode == "mcp" {
			return m.mcpDelete(), true
		}
	}
	if p.textMode() {
		var cmd tea.Cmd
		p.input, cmd = p.input.Update(msg)
		return cmd, true
	}
	if len(msg.Runes) > 0 {
		p.filter += string(msg.Runes)
		p.selected = 0
	}
	return nil, true
}

func (p *paletteState) textMode() bool {
	return p.mode == "repo" || p.mode == "explain" || p.mode == "settings" || p.mode == "mcp-add"
}

func (m *Model) paletteEnter() (tea.Cmd, bool) {
	p := &m.palette
	visible := p.visible()
	switch p.mode {
	case "menu":
		if len(visible) == 0 {
			return nil, true
		}
		action := visible[p.selected]
		p.open = false
		switch action {
		case "Switch model":
			p.setItems("switch", localModelLabels())
			p.open = true
		case "Download GGUF from Hugging Face":
			p.setItems("repo", nil)
			p.input.Reset()
			p.input.Focus()
			p.open = true
		case "Scan hardware":
			return m.scanInfoCmd(), true
		case "Recommend a model":
			return m.recommendInfoCmd(), true
		case "Explain a model":
			p.setItems("explain", nil)
			p.input.Reset()
			p.input.Focus()
			p.open = true
		case "MCP servers":
			p.setItems("mcp", mcpLabels())
			p.open = true
		case "Settings":
			p.setItems("settings", nil)
			p.input.SetValue(fmt.Sprintf("%d %d %.2f", m.settings.Context, m.settings.GPULayers, m.settings.Temperature))
			p.input.Focus()
			p.open = true
		case "Help":
			return func() tea.Msg {
				return infoMsg{Text: "Ctrl+X opens this palette. In chat: Enter sends, Shift+Enter adds a newline, Esc stops generation. Slash commands: /help /clear /model /system /info /models /quit."}
			}, true
		case "Quit":
			m.quitting = true
			return tea.Quit, true
		}

	case "switch":
		if len(visible) == 0 {
			return nil, true
		}
		id := strings.Fields(visible[p.selected])[0]
		store, err := local.LoadModels()
		if err == nil {
			if lm, ok := store.Find(id); ok {
				p.open = false
				m.appendSystem(fmt.Sprintf("Loading %s ...", lm.ID))
				return m.loadModelCmd(lm), true
			}
		}
		m.appendSystem("Could not resolve that local model.")

	case "repo":
		repo := hf.NormalizeRepo(p.input.Value())
		if repo == "" {
			m.appendSystem("Enter a repository such as bartowski/Qwen2.5-7B-Instruct-GGUF")
			return nil, true
		}
		p.repo = repo
		return func() tea.Msg {
			files, err := hf.New().GGUFFiles(context.Background(), repo)
			return filesMsg{Repo: repo, Files: files, Err: err}
		}, true

	case "file":
		if len(visible) == 0 {
			return nil, true
		}
		file := visible[p.selected]
		repo := p.repo
		p.open = false
		m.appendSystem(fmt.Sprintf("Downloading %s from %s ...", file, repo))
		return m.downloadCmd(repo, file), true

	case "explain":
		id := strings.TrimSpace(p.input.Value())
		p.open = false
		return m.explainInfoCmd(id), true

	case "mcp-add":
		fields := strings.Fields(p.input.Value())
		if len(fields) < 2 {
			m.appendSystem("Usage in palette: A then `name command [args...]`")
			p.setItems("mcp", mcpLabels())
			p.open = true
			return nil, true
		}
		cfg, err := local.LoadMCP()
		if err == nil {
			err = cfg.AddServer(local.MCPServer{Name: fields[0], Command: fields[1], Args: fields[2:], Enabled: true})
		}
		if err != nil {
			m.appendSystem("MCP save error: " + err.Error())
		} else {
			m.appendSystem(fmt.Sprintf("Added MCP server %s.", fields[0]))
		}
		p.setItems("mcp", mcpLabels())
		p.open = true

	case "settings":
		fields := strings.Fields(p.input.Value())
		if len(fields) < 3 {
			m.appendSystem("Settings format: context gpu_layers temperature")
			return nil, true
		}
		ctxVal, err1 := strconv.Atoi(fields[0])
		layers, err2 := strconv.Atoi(fields[1])
		temp, err3 := strconv.ParseFloat(fields[2], 64)
		if err1 != nil || err2 != nil || err3 != nil || ctxVal < 512 {
			m.appendSystem("Invalid settings. Use: 8192 -1 0.7")
			return nil, true
		}
		m.settings.Context = ctxVal
		m.settings.GPULayers = layers
		m.settings.Temperature = temp
		if err := m.settings.Save(); err != nil {
			m.appendSystem("Settings save error: " + err.Error())
		} else {
			m.appendSystem(fmt.Sprintf("Settings saved: context=%d layers=%d temperature=%.2f", ctxVal, layers, temp))
		}
	}
	return nil, true
}

func (m *Model) mcpDelete() tea.Cmd {
	p := &m.palette
	visible := p.visible()
	if len(visible) == 0 || len(visible) == 1 {
		p.setItems("mcp", mcpLabels())
		return nil
	}
	name := strings.Fields(visible[p.selected])[0]
	cfg, err := local.LoadMCP()
	if err == nil {
		_, err = cfg.RemoveServer(name)
	}
	if err != nil {
		m.appendSystem("MCP delete error: " + err.Error())
	} else {
		m.appendSystem(fmt.Sprintf("Removed MCP server %s.", name))
	}
	p.setItems("mcp", mcpLabels())
	return nil
}

func (m *Model) downloadCmd(repo, file string) tea.Cmd {
	client := hf.New()
	return func() tea.Msg {
		ctx := context.Background()
		dest := filepath.Join(local.ModelsDir(), strings.ReplaceAll(repo, "/", "_"), filepath.Base(file))
		err := client.Download(ctx, repo, file, dest, func(done, total int64) {
			if m.program != nil {
				m.program.Send(progressMsg{Done: done, Total: total})
			}
		})
		if err != nil {
			return downloadMsg{Err: err}
		}
		info, err := os.Stat(dest)
		if err != nil {
			return downloadMsg{Err: err}
		}
		model := local.Model{
			ID:        repo + "/" + file,
			Repo:      repo,
			File:      file,
			Path:      dest,
			SizeBytes: info.Size(),
			Quant:     guessQuant(file),
		}
		store, err := local.LoadModels()
		if err == nil {
			err = store.Add(model)
		}
		if err != nil {
			return downloadMsg{Err: err}
		}
		return downloadMsg{Model: model}
	}
}

func guessQuant(file string) string {
	upper := strings.ToUpper(file)
	for _, q := range []string{"Q3_K_M", "Q4_K_M", "Q5_K_M", "Q6_K", "Q8_0", "FP16", "F16", "BF16"} {
		if strings.Contains(upper, q) {
			return q
		}
	}
	return ""
}

func (m *Model) scanInfoCmd() tea.Cmd {
	return func() tea.Msg {
		p, err := m.detectHardware()
		if err != nil {
			return infoMsg{Text: "Hardware scan failed: " + err.Error()}
		}
		b := scoring.ScoreHardware(p)
		return infoMsg{Text: fmt.Sprintf("Hardware: %.1f / 100 %s · %.1f GB usable %s", b.Score, b.Tier, b.EffectiveMemoryGB, b.MemoryKind)}
	}
}

func (m *Model) recommendInfoCmd() tea.Cmd {
	return func() tea.Msg {
		p, err := m.detectHardware()
		if err != nil {
			return infoMsg{Text: "Hardware scan failed: " + err.Error()}
		}
		cat, err := catalog.Load()
		if err != nil {
			return infoMsg{Text: "Catalog load failed: " + err.Error()}
		}
		res := match.Recommend(p, cat, match.Options{Top: 3, Context: m.settings.Context})
		if len(res.Recommendations) == 0 {
			return infoMsg{Text: "No model fits this machine. " + res.Message}
		}
		var b strings.Builder
		b.WriteString("Recommended for this machine:\n")
		for i, r := range res.Recommendations {
			fmt.Fprintf(&b, "  %d. %s — %.1f GB, score %.1f, ~%.1f tok/s\n", i+1,
				r.Model.DisplayName, r.Need.RequiredGB, r.Need.Score, r.Throughput.TokensPerSec)
		}
		b.WriteString("Press Ctrl+X → Download GGUF from Hugging Face to get one.")
		return infoMsg{Text: b.String()}
	}
}

func (m *Model) explainInfoCmd(id string) tea.Cmd {
	return func() tea.Msg {
		id = strings.TrimSpace(id)
		cat, err := catalog.Load()
		if err != nil {
			return infoMsg{Text: "Catalog load failed: " + err.Error()}
		}
		model, ok := cat.Find(id)
		if !ok {
			return infoMsg{Text: fmt.Sprintf("Unknown model %q. Try `alocal models`.", id)}
		}
		need := model.Need(m.settings.Context)
		return infoMsg{Text: fmt.Sprintf("%s (%s): %.2f GB weights + %.2f GB KV = %.2f GB required; model score %.1f. Hugging Face repo: %s",
			model.DisplayName, model.Quant, need.ActiveWeightGB, need.KVGB, need.RequiredGB, need.Score,
			orDefault(model.RuntimeTag("llamacpp"), "see catalog"))}
	}
}

func (m *Model) detectHardware() (*hardware.Profile, error) {
	if m.mockHardware != "" {
		return hardware.MockDetector{Path: m.mockHardware}.Detect(context.Background())
	}
	return hardware.SystemDetector{Options: hardware.Options{Verbose: false}}.Detect(context.Background())
}

var paletteBox = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(lipgloss.Color("205")).
	Padding(0, 1)

func (m *Model) renderPalette() string {
	p := &m.palette
	lines := []string{lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")).Render("ALOCAL COMMANDS") + "  " + lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render("Ctrl+X / Esc to close")}
	items := p.visible()
	switch p.mode {
	case "repo", "explain", "settings", "mcp-add":
		label := map[string]string{
			"repo":     "Hugging Face repo (owner/name):",
			"explain":  "Model id:",
			"settings": "context gpu_layers temperature:",
			"mcp-add":  "name command [args...]:",
		}[p.mode]
		lines = append(lines, label, p.input.View())
	case "file":
		lines = append(lines, "Files in "+p.repo+":")
		for i, item := range items {
			lines = append(lines, renderPaletteItem(item, i == p.selected))
		}
	default:
		for i, item := range items {
			lines = append(lines, renderPaletteItem(item, i == p.selected))
		}
	}
	if len(items) == 0 && !p.textMode() {
		lines = append(lines, dimStyle.Render("(no entries)"))
	}
	return paletteBox.Width(m.width - 4).Render(strings.Join(lines, "\n"))
}

func renderPaletteItem(item string, selected bool) string {
	if selected {
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")).Render("▸ " + item)
	}
	return "  " + item
}
