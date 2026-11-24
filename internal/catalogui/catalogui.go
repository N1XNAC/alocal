// Package catalogui implements the interactive model catalogue. It is TTY-only
// and reuses Alocal's existing hardware, compatibility and HF download code.
package catalogui

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"

	"alocal/internal/brand"
	"alocal/internal/compat"
	"alocal/internal/discovery"
	"alocal/internal/hardware"
	"alocal/internal/hf"
)

// Options configures a catalogue session.
type Options struct {
	Entries      []discovery.Entry
	Profile      *hardware.Profile
	Client       *hf.Client
	Version      string
	Color        bool
	FetchDynamic bool
}

// Model is the bubbletea model for the catalogue.
type Model struct {
	entries       []discovery.Entry
	filtered      []discovery.Entry
	categoryIdx   int
	categoryCount map[string]int
	cursor        int
	page          int
	search        textinput.Model
	profile       *hardware.Profile
	client        *hf.Client
	version       string
	color         bool
	width         int
	height        int

	detail           bool
	detailScroll     int
	showRequirements bool
	downloadMenu     bool
	menuCursor       int
	quantPicker      bool
	quantIndex       int
	confirmDownload  bool
	pendingLevel     compat.Level
	pendingEntry     discovery.Entry
	pendingFile      discovery.File
	pendingDest      string
	help             bool
	quitting         bool

	downloading     bool
	cancelRequested bool
	progressFile    string
	progressDone    int64
	progressTotal   int64
	status          string
	err             error
	cancel          func()
	initialCmd      tea.Cmd
	program         *tea.Program
}

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("225"))
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("141"))
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	goodStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	warnStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	badStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("203"))
	helpStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	boxStyle      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
)

// New builds a catalogue model. Dynamic Hub discovery starts on Init.
func New(opts Options) *Model {
	m := &Model{
		profile:       opts.Profile,
		client:        opts.Client,
		version:       opts.Version,
		color:         opts.Color,
		width:         110,
		height:        32,
		categoryCount: map[string]int{},
		quantIndex:    -1,
	}
	m.entries = append([]discovery.Entry(nil), opts.Entries...)
	sort.SliceStable(m.entries, func(i, j int) bool {
		if m.entries[i].Downloaded != m.entries[j].Downloaded {
			return m.entries[i].Downloaded
		}
		return m.entries[i].Downloads > m.entries[j].Downloads
	})
	m.search = textinput.New()
	m.search.Placeholder = "Search name, publisher, task or quant..."
	m.search.CharLimit = 160
	m.search.Width = 48
	m.rebuildCounts()
	m.applyFilter()
	if opts.FetchDynamic {
		m.initialCmd = m.fetchCmd()
		m.status = "Fetching live Hugging Face catalog…"
	} else {
		m.status = fmt.Sprintf("Embedded catalogue: %d repositories", discovery.Count(m.entries))
	}
	return m
}

// Run starts the interactive catalogue.
func Run(opts Options) error {
	m := New(opts)
	p := tea.NewProgram(m, tea.WithAltScreen())
	m.SetProgram(p)
	_, err := p.Run()
	return err
}

// SetProgram lets download commands stream progress back into the UI.
func (m *Model) SetProgram(p *tea.Program) { m.program = p }

// IsTerminal reports whether the catalogue can use a full-screen UI. Both
// streams must be terminals: Bubble Tea reads keys from stdin and draws to
// stdout.
func IsTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd { return m.initialCmd }

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
		return m, nil
	case fetchedMsg:
		m.onFetched(v)
		return m, nil
	case enrichedMsg:
		m.onEnriched(v)
		return m, nil
	case progressMsg:
		m.progressDone, m.progressTotal = v.done, v.total
		if v.file != "" {
			m.progressFile = v.file
		}
		return m, nil
	case downloadDoneMsg:
		m.onDownloadDone(v)
		return m, nil
	case statusMsg:
		m.status = v.text
		if v.err != nil {
			m.err = v.err
		}
		return m, nil
	}

	if key, ok := msg.(tea.KeyMsg); ok {
		return m.handleKey(key)
	}
	if m.search.Focused() {
		var cmd tea.Cmd
		m.search, cmd = m.search.Update(msg)
		m.applyFilter()
		return m, cmd
	}
	return m, nil
}

func (m *Model) handleKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	name := key.String()

	if m.downloading {
		switch name {
		case "esc", "ctrl+c":
			if m.cancel != nil {
				m.cancel()
			}
			m.downloading = false
			m.cancelRequested = true
			m.status = "Download cancelled; partial data was kept for resume."
			return m, nil
		}
		return m, nil
	}

	if m.confirmDownload {
		switch strings.ToLower(name) {
		case "y", "enter":
			m.confirmDownload = false
			return m, m.beginDownload(m.pendingEntry, m.pendingFile, m.pendingDest)
		case "n", "esc", "q":
			m.confirmDownload = false
			m.status = "Download cancelled."
		}
		return m, nil
	}

	if m.search.Focused() {
		switch name {
		case "esc", "enter":
			m.search.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.search, cmd = m.search.Update(key)
		m.applyFilter()
		return m, cmd
	}

	switch name {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "q":
		if m.downloadMenu || m.quantPicker || m.detail || m.help {
			m.downloadMenu, m.quantPicker, m.detail, m.help = false, false, false, false
			return m, nil
		}
		m.quitting = true
		return m, tea.Quit
	case "?":
		m.help = !m.help
		return m, nil
	case "esc":
		switch {
		case m.help:
			m.help = false
		case m.quantPicker:
			m.quantPicker = false
		case m.downloadMenu:
			m.downloadMenu = false
		case m.detail:
			m.detail = false
			m.showRequirements = false
		case m.search.Value() != "":
			m.search.SetValue("")
			m.applyFilter()
		}
		return m, nil
	}

	if m.help {
		return m, nil
	}
	if m.quantPicker {
		return m, m.handleQuantKey(key)
	}
	if m.downloadMenu {
		return m, m.handleMenuKey(key)
	}
	if m.detail {
		return m, m.handleDetailKey(key)
	}

	if len(name) == 1 && name[0] >= '0' && name[0] <= '9' {
		index := int(name[0] - '0')
		if index == 0 {
			index = len(discovery.Categories) // 0 jumps to Downloaded Models
		} else {
			index--
		}
		if index < len(discovery.Categories) {
			m.setCategory(index)
		}
		return m, nil
	}

	switch name {
	case "/":
		m.search.Focus()
		return m, nil
	case "left", "h", "shift+tab":
		m.setCategory(m.categoryIdx - 1)
	case "right", "l", "tab":
		m.setCategory(m.categoryIdx + 1)
	case "j", "down":
		m.moveCursor(1)
	case "k", "up":
		m.moveCursor(-1)
	case "[", "pgup":
		m.movePage(-1)
	case "]", "pgdown":
		m.movePage(1)
	case "enter":
		if m.selected().Repo != "" {
			m.detail = true
			m.detailScroll = 0
			m.showRequirements = false
		}
	case "d":
		if m.selected().Repo != "" {
			m.detail = true
			m.openDownloadMenu()
		}
	case "e":
		if m.selected().Repo != "" {
			return m, m.enrichCmd()
		}
	}
	return m, nil
}

func (m *Model) handleMenuKey(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "j", "down":
		m.menuCursor = (m.menuCursor + 1) % 4
	case "k", "up":
		m.menuCursor = (m.menuCursor + 3) % 4
	case "enter":
		switch m.menuCursor {
		case 0:
			m.downloadMenu = false
			return m.requestDownloadCmd()
		case 1:
			m.quantPicker = true
			e := m.selected()
			m.quantIndex = quantIndexFor(e, e.File.Quant)
		case 2:
			m.downloadMenu = false
			m.showRequirements = true
		case 3:
			m.downloadMenu = false
		}
	}
	return nil
}

func (m *Model) handleQuantKey(key tea.KeyMsg) tea.Cmd {
	e := m.selected()
	n := len(e.Quantizations)
	if n == 0 {
		m.quantPicker = false
		m.status = "This repository has no published quantization labels."
		return nil
	}
	switch key.String() {
	case "j", "down":
		if m.quantIndex < 0 {
			m.quantIndex = 0
		} else {
			m.quantIndex = (m.quantIndex + 1) % n
		}
	case "k", "up":
		if m.quantIndex < 0 {
			m.quantIndex = n - 1
		} else {
			m.quantIndex = (m.quantIndex + n - 1) % n
		}
	case "enter":
		if m.quantIndex < 0 {
			m.quantIndex = 0
		}
		m.quantPicker = false
		m.status = fmt.Sprintf("Selected quantization %s.", e.Quantizations[m.quantIndex])
	}
	return nil
}

func (m *Model) handleDetailKey(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "j", "down":
		m.scrollDetail(1)
	case "k", "up":
		m.scrollDetail(-1)
	case "d":
		m.openDownloadMenu()
	case "r":
		m.showRequirements = !m.showRequirements
	case "e":
		return m.enrichCmd()
	case "enter":
		m.detail = false
		m.showRequirements = false
	}
	return nil
}

// View implements tea.Model.
func (m *Model) View() string {
	if m.quitting {
		return "\n  Goodbye!\n\n"
	}
	w := m.width
	if w <= 0 {
		w = 110
	}
	var b strings.Builder
	b.WriteString(brand.Compact(m.color, w))
	b.WriteString("\n")
	if m.version != "" {
		b.WriteString(dimStyle.Render("Model catalogue · v" + m.version))
	} else {
		b.WriteString(dimStyle.Render("Model catalogue"))
	}
	b.WriteString("\n")
	b.WriteString(dimStyle.Render(fmt.Sprintf("%d repositories · %s · %s",
		discovery.Count(m.entries), m.hardwareSummary(), m.runtimeSummary())))
	b.WriteString("\n\n")

	if m.help {
		b.WriteString(m.renderHelp())
		return boxStyle.Width(w - 2).Render(b.String())
	}
	if m.downloading {
		b.WriteString(m.renderProgress())
		return boxStyle.Width(w - 2).Render(b.String())
	}
	if m.confirmDownload {
		b.WriteString(m.renderConfirmation())
		return boxStyle.Width(w - 2).Render(b.String())
	}

	bodyWidth := w - 4
	if m.detail {
		b.WriteString(m.renderDetail(bodyWidth))
	} else {
		b.WriteString(m.renderBrowse(bodyWidth))
	}
	b.WriteString("\n")
	b.WriteString(helpStyle.Render(m.footerHelp()))
	return boxStyle.Width(w - 2).Render(b.String())
}

func (m *Model) renderBrowse(w int) string {
	sidebarWidth := 28
	compact := w < 92
	if compact {
		sidebarWidth = 0
	}
	var side string
	if !compact {
		side = m.renderCategories()
	}
	listWidth := w - sidebarWidth - 2
	if listWidth < 30 {
		listWidth = 30
	}
	list := m.renderList(listWidth)
	if side == "" {
		return list
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, side, "  ", list)
}

func (m *Model) renderCategories() string {
	var b strings.Builder
	for i, cat := range discovery.Categories {
		marker := "  "
		if i == m.categoryIdx {
			marker = "▸ "
		}
		label := fmt.Sprintf("%-18s %3d", cat.Label, m.categoryCount[cat.ID])
		line := marker + fmt.Sprintf("%d ", i+1) + label
		if i == m.categoryIdx {
			line = selectedStyle.Render(line)
		} else {
			line = dimStyle.Render(line)
		}
		b.WriteString(line)
		if i < len(discovery.Categories)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (m *Model) renderList(w int) string {
	var b strings.Builder
	cat := discovery.Categories[m.categoryIdx]
	b.WriteString(titleStyle.Render(cat.Label))
	b.WriteString("  ")
	b.WriteString(dimStyle.Render(fmt.Sprintf("%d models", len(m.filtered))))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("Search: " + m.search.View()))
	b.WriteString("\n\n")
	if len(m.filtered) == 0 {
		b.WriteString(dimStyle.Render("No models match this category and search."))
		return b.String()
	}
	pageSize := m.pageSize()
	start := m.page * pageSize
	if start >= len(m.filtered) {
		start = 0
		m.page = 0
	}
	end := start + pageSize
	if end > len(m.filtered) {
		end = len(m.filtered)
	}
	nameWidth := w - 50
	if nameWidth < 20 {
		nameWidth = 20
	}
	for i := start; i < end; i++ {
		e := m.filtered[i]
		marker := "  "
		if i == m.cursor {
			marker = "▸ "
		}
		name := truncate(e.DisplayName, nameWidth)
		meta := m.compactMeta(e)
		line := marker + padRight(name, nameWidth) + "  " + dimStyle.Render(meta)
		if i == m.cursor {
			line = selectedStyle.Render(line)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	pages := (len(m.filtered) + pageSize - 1) / pageSize
	b.WriteString("\n")
	b.WriteString(dimStyle.Render(fmt.Sprintf("Page %d/%d · [%d–%d] of %d", m.page+1, pages, start+1, end, len(m.filtered))))
	return b.String()
}

func (m *Model) renderDetail(w int) string {
	e := m.selected()
	if e.Repo == "" {
		m.detail = false
		return dimStyle.Render("No model selected.")
	}
	lines := m.detailLines(e, w)
	pageSize := m.height - 12
	if pageSize < 8 {
		pageSize = 8
	}
	if m.detailScroll > len(lines)-pageSize {
		m.detailScroll = len(lines) - pageSize
	}
	if m.detailScroll < 0 {
		m.detailScroll = 0
	}
	end := m.detailScroll + pageSize
	if end > len(lines) {
		end = len(lines)
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("Model details"))
	b.WriteString("  ")
	b.WriteString(dimStyle.Render("Esc back · d download · r requirements · e fetch sizes"))
	b.WriteString("\n\n")
	for _, line := range lines[m.detailScroll:end] {
		b.WriteString(line)
		b.WriteString("\n")
	}
	if m.downloadMenu {
		b.WriteString("\n")
		b.WriteString(m.renderDownloadMenu())
	}
	if m.quantPicker {
		b.WriteString("\n")
		b.WriteString(m.renderQuantPicker())
	}
	return b.String()
}

func (m *Model) detailLines(e discovery.Entry, w int) []string {
	labelWidth := 15
	field := func(label, value string) string {
		return padRight(label, labelWidth) + value
	}
	params := "not published"
	if e.ParamsB > 0 {
		params = fmt.Sprintf("%.2fB", e.ParamsB)
		if e.ActiveParamsB > 0 {
			params += fmt.Sprintf(" total / %.2fB active", e.ActiveParamsB)
		}
	}
	selectedFile, hasSelectedFile := m.selectedFileFor(e)
	size := "not published"
	if hasSelectedFile && selectedFile.SizeBytes > 0 {
		size = fmt.Sprintf("%.2f GB (%s)", float64(selectedFile.SizeBytes)/1e9, selectedFile.Name)
	}
	ctx := "not published"
	if e.MaxContext > 0 {
		ctx = fmt.Sprintf("%d tokens", e.MaxContext)
	}
	arch := e.Architecture
	if arch == "" {
		arch = e.BaseModel
	}
	if arch == "" {
		arch = "not published"
	}
	compatResult := m.compatResult(e)
	lines := []string{
		field("Name", e.DisplayName),
		field("Publisher", e.Publisher),
		field("Repository", e.Repo),
		field("Architecture", arch),
		field("Parameters", params),
		field("File size", size),
		field("Context", ctx),
		field("License", or(e.License, "not published")),
		field("Downloads", fmt.Sprintf("%d · %d likes", e.Downloads, e.Likes)),
	}
	quants := strings.Join(e.Quantizations, ", ")
	if quants == "" {
		quants = "not published"
	}
	lines = append(lines, field("Quantizations", truncate(quants, max(20, w-labelWidth-4))))
	if quant := m.selectedQuantFor(e); quant != "" {
		lines = append(lines, field("Selected quant", quant))
	}
	features := append([]string(nil), e.Tasks...)
	if e.Pipeline != "" {
		features = append([]string{e.Pipeline}, features...)
	}
	if len(features) == 0 {
		features = []string{"not published"}
	}
	lines = append(lines, field("Features", truncate(strings.Join(features, ", "), max(20, w-labelWidth-4))))
	lines = append(lines, field("Compatibility", m.badge(compatResult)))
	lines = append(lines, "")
	lines = append(lines, "Hardware requirements:")
	for _, reason := range compatResult.Reasons {
		lines = append(lines, wrap(reason, max(30, w-4))...)
	}
	return lines
}

func (m *Model) renderDownloadMenu() string {
	e := m.selected()
	file, _ := m.selectedFile()
	label := "Download model"
	if file.Name != "" {
		label += " (" + or(file.Quant, "default") + ")"
	}
	switch m.compatResult(e).Level {
	case compat.Incompatible:
		label = "Download model anyway - incompatible"
	case compat.Unknown:
		label = "Download model anyway - compatibility unknown"
	}
	items := []string{label, "Select quantization", "View hardware requirements", "Cancel"}
	var b strings.Builder
	b.WriteString(dimStyle.Render("Download workflow"))
	b.WriteString("\n")
	for i, item := range items {
		marker := "  "
		if i == m.menuCursor {
			marker = "▸ "
		}
		line := marker + item
		if i == m.menuCursor {
			line = selectedStyle.Render(line)
		}
		b.WriteString(line)
		if i < len(items)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (m *Model) renderQuantPicker() string {
	e := m.selected()
	var b strings.Builder
	b.WriteString(dimStyle.Render("Select quantization (j/k, Enter)"))
	b.WriteString("\n")
	if len(e.Quantizations) == 0 {
		b.WriteString(dimStyle.Render("No quantizations published for this repository."))
		return b.String()
	}
	active := m.quantIndex
	if active < 0 {
		active = quantIndexFor(e, e.File.Quant)
	}
	for i, q := range e.Quantizations {
		marker := "  "
		if i == active {
			marker = "▸ "
		}
		line := marker + q
		if i == active {
			line = selectedStyle.Render(line)
		}
		b.WriteString(line)
		if i < len(e.Quantizations)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (m *Model) renderProgress() string {
	pct := ""
	if m.progressTotal > 0 {
		pct = fmt.Sprintf(" %.1f%%", float64(m.progressDone)/float64(m.progressTotal)*100)
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("Downloading GGUF"))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("  %s%s\n", m.progressFile, pct))
	if m.progressTotal > 0 {
		b.WriteString(fmt.Sprintf("  %.2f / %.2f GB\n", float64(m.progressDone)/1e9, float64(m.progressTotal)/1e9))
	} else {
		b.WriteString(fmt.Sprintf("  %.2f GB received (size not published)\n", float64(m.progressDone)/1e9))
	}
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("Esc or Ctrl+C to cancel. Partial data is kept so the download can resume."))
	return b.String()
}

func (m *Model) renderConfirmation() string {
	e := m.selected()
	file, _ := m.selectedFile()
	res := m.compatResult(e)
	var b strings.Builder
	if m.pendingLevel == compat.Unknown {
		b.WriteString(warnStyle.Render("Unknown memory fit"))
	} else {
		b.WriteString(badStyle.Render("Incompatible model"))
	}
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("  %s (%s)\n", e.DisplayName, file.Name))
	if m.pendingLevel == compat.Unknown {
		b.WriteString("  This repository has no published GGUF size, so memory fit cannot be checked.\n")
		b.WriteString("  Press e in the details view to fetch real sizes before downloading.\n")
	} else {
		b.WriteString(fmt.Sprintf("  Needs ~%.1f GB, but only %.1f GB is usable.\n", res.Need.RequiredGB, res.EffectiveGB))
	}
	b.WriteString("\n")
	b.WriteString("Alocal will not download a model with unresolved compatibility silently.\n")
	b.WriteString(helpStyle.Render("Press y to download anyway, n or Esc to cancel."))
	return b.String()
}

func (m *Model) renderHelp() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Catalogue controls"))
	b.WriteString("\n\n")
	for _, line := range []string{
		"↑/↓ or j/k      move between models",
		"←/→ or h/l      switch category",
		"1–9, 0          jump to category (0 = downloaded)",
		"/               search",
		"Enter           model details",
		"d               download workflow",
		"r               hardware requirements",
		"e               fetch real file sizes from Hub",
		"[ / ]           previous / next page",
		"Esc             back / cancel",
		"q or Ctrl+C     quit",
	} {
		b.WriteString("  " + line + "\n")
	}
	return b.String()
}

func (m *Model) footerHelp() string {
	parts := []string{"↑/↓ navigate", "←/→ category", "/ search", "Enter details", "d download", "? help", "q quit"}
	if m.status != "" {
		return m.status + "\n" + strings.Join(parts, " · ")
	}
	return strings.Join(parts, " · ")
}

func (m *Model) hardwareSummary() string {
	if m.profile == nil {
		return "hardware unavailable"
	}
	if gpus := m.profile.DiscreteGPUs(); len(gpus) > 0 {
		return fmt.Sprintf("%s %.0f GB VRAM", gpus[0].Vendor, gpus[0].VRAMTotalGB)
	}
	if m.profile.RAM.TotalGB > 0 {
		return fmt.Sprintf("%.1f GB RAM", m.profile.RAM.TotalGB)
	}
	return "hardware detected"
}

func (m *Model) runtimeSummary() string { return "llama.cpp" }

func (m *Model) compactMeta(e discovery.Entry) string {
	var parts []string
	if e.Publisher != "" {
		parts = append(parts, e.Publisher)
	}
	if e.ParamsB > 0 {
		parts = append(parts, fmt.Sprintf("%.2fB", e.ParamsB))
	}
	if e.File.SizeBytes > 0 {
		parts = append(parts, fmt.Sprintf("%.2f GB", e.SizeGB()))
	} else {
		parts = append(parts, "size n/a")
	}
	if e.Downloaded {
		parts = append(parts, "downloaded")
	}
	return strings.Join(parts, " · ")
}

func (m *Model) badge(r compat.Result) string {
	switch r.Level {
	case compat.Recommended:
		return goodStyle.Render(string(r.Level))
	case compat.Warning:
		return warnStyle.Render(string(r.Level))
	case compat.Incompatible:
		return badStyle.Render(string(r.Level))
	default:
		return dimStyle.Render(string(r.Level))
	}
}

func (m *Model) compatResult(e discovery.Entry) compat.Result {
	spec := e.Spec()
	if quant := m.selectedQuantFor(e); quant != "" {
		spec.Quant = quant
	}
	if file, ok := m.selectedFileFor(e); ok && file.Name != "" {
		spec.ID = e.Repo + "/" + file.Name
		if file.Quant != "" {
			spec.Quant = file.Quant
		}
		spec.FileSizeGB = float64(file.SizeBytes) / 1e9
	}
	return compat.Check(m.profile, spec, 8192)
}

func (m *Model) selected() discovery.Entry {
	if m.cursor < 0 || m.cursor >= len(m.filtered) {
		return discovery.Entry{}
	}
	return m.filtered[m.cursor]
}

func (m *Model) moveCursor(delta int) {
	if len(m.filtered) == 0 {
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}
	m.quantIndex = -1
	m.page = m.cursor / m.pageSize()
}

func (m *Model) movePage(delta int) {
	pages := (len(m.filtered) + m.pageSize() - 1) / m.pageSize()
	if pages <= 0 {
		return
	}
	m.page += delta
	if m.page < 0 {
		m.page = 0
	}
	if m.page >= pages {
		m.page = pages - 1
	}
	m.cursor = m.page * m.pageSize()
	m.quantIndex = -1
}

func (m *Model) setCategory(index int) {
	n := len(discovery.Categories)
	m.categoryIdx = ((index % n) + n) % n
	m.cursor = 0
	m.page = 0
	m.quantIndex = -1
	m.detail = false
	m.downloadMenu = false
	m.quantPicker = false
	m.applyFilter()
}

func (m *Model) applyFilter() {
	cat := discovery.Categories[m.categoryIdx]
	query := m.search.Value()
	m.filtered = m.filtered[:0]
	for _, e := range m.entries {
		if !e.HasCategory(cat.ID) || !e.Matches(query) {
			continue
		}
		m.filtered = append(m.filtered, e)
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.quantIndex = -1
	m.page = m.cursor / m.pageSize()
	m.rebuildCounts()
}

func (m *Model) rebuildCounts() {
	m.categoryCount = map[string]int{}
	for _, e := range m.entries {
		for _, id := range e.Categories {
			m.categoryCount[id]++
		}
	}
}

func (m *Model) pageSize() int {
	if m.height <= 0 {
		return 10
	}
	n := m.height - 14
	if n < 5 {
		return 5
	}
	if n > 16 {
		return 16
	}
	return n
}

func (m *Model) scrollDetail(delta int) { m.detailScroll += delta }

func (m *Model) openDownloadMenu() {
	m.downloadMenu = true
	m.quantPicker = false
	m.menuCursor = 0
	e := m.selected()
	m.quantIndex = quantIndexFor(e, e.File.Quant)
}

func (m *Model) selectedQuantFor(e discovery.Entry) string {
	if m.quantIndex >= 0 && m.quantIndex < len(e.Quantizations) {
		quant := e.Quantizations[m.quantIndex]
		if _, ok := e.SelectFile(quant); ok {
			return quant
		}
	}
	return e.File.Quant
}

func (m *Model) selectedFileFor(e discovery.Entry) (discovery.File, bool) {
	quant := m.selectedQuantFor(e)
	if quant != "" {
		if f, ok := e.SelectFile(quant); ok {
			return f, true
		}
	}
	return e.File, e.File.Name != ""
}

func (m *Model) selectedFile() (discovery.File, bool) {
	return m.selectedFileFor(m.selected())
}

func quantIndexFor(e discovery.Entry, quant string) int {
	for i, q := range e.Quantizations {
		if strings.EqualFold(q, quant) {
			return i
		}
	}
	return 0
}

func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}

func padRight(s string, width int) string {
	n := lipgloss.Width(s)
	if n >= width {
		return s
	}
	return s + strings.Repeat(" ", width-n)
}

func wrap(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := words[0]
		for _, word := range words[1:] {
			if lipgloss.Width(line)+1+lipgloss.Width(word) > width {
				out = append(out, line)
				line = word
			} else {
				line += " " + word
			}
		}
		out = append(out, line)
	}
	return out
}

func or(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
