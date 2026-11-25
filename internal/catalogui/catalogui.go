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

// section is a sidebar destination. The synthetic "All Models" entry makes the
// full embedded catalogue the default view; the remaining sections mirror the
// discovery categories, including the locally downloaded models.
type section struct {
	ID    string
	Label string
}

var catalogueSections = func() []section {
	out := []section{{ID: "all", Label: "All Models"}}
	for _, c := range discovery.Categories {
		out = append(out, section{ID: c.ID, Label: c.Label})
	}
	return out
}()

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

	// hits are the clickable regions from the most recently rendered frame.
	hits []hitBox

	// lastDownload tracks the most recently completed download so embedders
	// (such as the chat TUI) can react to it.
	lastDownloadRepo  string
	lastDownloadFile  string
	hasNewDownload    bool
}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(brand.White))
	textStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color(brand.White))
	accentStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(brand.Purple))
	labelStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(brand.Lavender))
	mutedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color(brand.Muted))
	faintStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color(brand.Faint))
	goodStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color(brand.Success))
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color(brand.Warning))
	badStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color(brand.Danger))
	ruleStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color(brand.Rule))
	keyStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color(brand.Lavender))
	helpStyle   = faintStyle

	// Keep these names for compatibility with the download workflow helpers.
	selectedStyle = accentStyle
	dimStyle      = mutedStyle
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
	m.search.Prompt = ""
	m.search.Placeholder = "Search models, publishers, tasks or quants"
	m.search.CharLimit = 160
	m.search.Width = 48
	if opts.Color {
		m.search.TextStyle = textStyle
		m.search.PlaceholderStyle = faintStyle
		m.search.Cursor.Style = accentStyle
	} else {
		plain := lipgloss.NewStyle()
		m.search.TextStyle = plain
		m.search.PlaceholderStyle = plain
		m.search.Cursor.Style = plain
	}
	m.applyFilter()
	if opts.FetchDynamic {
		m.initialCmd = m.fetchCmd()
		m.status = "Fetching live Hugging Face catalogue…"
	} else {
		m.status = "Offline · embedded catalogue"
	}
	return m
}

// Run starts the interactive catalogue.
func Run(opts Options) error {
	m := New(opts)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	m.SetProgram(p)
	_, err := p.Run()
	return err
}

// SetProgram lets download commands stream progress back into the UI.
func (m *Model) SetProgram(p *tea.Program) { m.program = p }

// HasNewDownload reports whether a download completed since the last clear.
func (m *Model) HasNewDownload() bool { return m.hasNewDownload }

// LastDownload returns the repo and file of the most recent download.
func (m *Model) LastDownload() (string, string) { return m.lastDownloadRepo, m.lastDownloadFile }

// ClearDownload acknowledges the download so it is not processed twice.
func (m *Model) ClearDownload() { m.hasNewDownload = false }
// streams must be terminals: Bubble Tea reads keys from stdin and draws to
// stdout.
// SetSearch sets the catalogue search query and applies it.
func (m *Model) SetSearch(query string) {
	m.search.SetValue(query)
	m.applyFilter()
}

func IsTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{textinput.Blink}
	if m.initialCmd != nil {
		cmds = append(cmds, m.initialCmd)
	}
	return tea.Batch(cmds...)
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
		return m, nil
	case tea.MouseMsg:
		return m, m.handleMouse(v)
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
			m.cancelDownload()
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
		if index < len(catalogueSections) {
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
	case "enter", "backspace":
		m.detail = false
		m.showRequirements = false
	}
	return nil
}

func (m *Model) cancelDownload() {
	if m.cancel != nil {
		m.cancel()
	}
	m.downloading = false
	m.cancelRequested = true
	m.status = "Download cancelled; partial data was kept for resume."
}

// handleMouse makes the browser, detail actions and modals pointer-driven.
func (m *Model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		if msg.Action != tea.MouseActionPress {
			return nil
		}
		return m.mouseScroll(-1)
	case tea.MouseButtonWheelDown:
		if msg.Action != tea.MouseActionPress {
			return nil
		}
		return m.mouseScroll(1)
	}
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return nil
	}
	for _, hit := range m.hits {
		if msg.Y != hit.y || msg.X < hit.x0 || msg.X >= hit.x1 {
			continue
		}
		return m.activate(hit)
	}
	return nil
}

func (m *Model) mouseScroll(direction int) tea.Cmd {
	switch {
	case m.downloading, m.confirmDownload, m.help:
		return nil
	case m.quantPicker:
		return m.handleQuantKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{scrollKey(direction)}})
	case m.downloadMenu:
		m.menuCursor = (m.menuCursor + direction + 4) % 4
		return nil
	case m.detail:
		m.scrollDetail(direction * 3)
		return nil
	case m.search.Focused():
		return nil
	default:
		m.moveCursor(direction * 3)
		return nil
	}
}

func scrollKey(direction int) rune {
	if direction < 0 {
		return 'k'
	}
	return 'j'
}

func (m *Model) activate(hit hitBox) tea.Cmd {
	switch hit.action {
	case "category":
		m.setCategory(hit.index)
	case "model":
		if hit.index < 0 || hit.index >= len(m.filtered) {
			return nil
		}
		if hit.index == m.cursor {
			m.detail = true
			m.detailScroll = 0
			m.showRequirements = false
			return nil
		}
		m.cursor = hit.index
		m.quantIndex = -1
		m.page = m.cursor / m.pageSize()
	case "search":
		m.search.Focus()
	case "detail-download":
		m.openDownloadMenu()
	case "detail-requirements":
		m.showRequirements = !m.showRequirements
	case "detail-enrich":
		return m.enrichCmd()
	case "detail-back":
		m.detail = false
		m.showRequirements = false
	case "menu":
		m.menuCursor = hit.index
		return m.handleMenuKey(tea.KeyMsg{Type: tea.KeyEnter})
	case "quant":
		m.quantIndex = hit.index
		return m.handleQuantKey(tea.KeyMsg{Type: tea.KeyEnter})
	case "confirm-yes":
		m.confirmDownload = false
		return m.beginDownload(m.pendingEntry, m.pendingFile, m.pendingDest)
	case "confirm-no":
		m.confirmDownload = false
		m.status = "Download cancelled."
	case "cancel-download":
		m.cancelDownload()
	case "help-close":
		m.help = false
	}
	return nil
}

// View implements tea.Model.
func (m *Model) View() string {
	if m.quitting {
		return "\n  Goodbye.\n\n"
	}
	w := m.width
	if w <= 0 {
		w = 110
	}
	out := &block{}
	m.renderHeader(out, w)

	switch {
	case m.help:
		m.renderHelp(out, w)
	case m.downloading:
		m.renderProgress(out, w)
	case m.confirmDownload:
		m.renderConfirmation(out, w)
	case m.detail:
		m.renderDetail(out, w)
	default:
		m.renderBrowse(out, w)
	}

	if m.height > 0 {
		for len(out.lines) < m.height-1 {
			out.add("")
		}
	}
	out.add(m.footer(w))
	// Pad every line to the terminal width so stale cells from the previous
	// frame are overwritten when the view changes (browse -> detail, etc.).
	for i := range out.lines {
		if n := lipgloss.Width(out.lines[i]); n < w {
			out.lines[i] += strings.Repeat(" ", w-n)
		}
	}
	m.hits = out.hits
	return strings.Join(out.lines, "\n")
}

func (m *Model) renderHeader(out *block, w int) {
	left := brand.Compact(m.color, w)
	right := m.st(faintStyle, "Model catalogue")
	out.add(padBetween(left, right, w))
	out.add(m.st(faintStyle, truncate(m.summaryLine(), w)))
	if m.err != nil {
		out.add(m.st(badStyle, "! "+truncate(m.err.Error(), max(20, w-4))))
	} else if m.status != "" {
		out.add(m.st(faintStyle, "· "+truncate(m.status, max(20, w-4))))
	} else {
		out.add("")
	}
	out.add("")
}

func (m *Model) summaryLine() string {
	repos := discovery.Count(m.entries)
	sized := 0
	for _, e := range m.entries {
		if e.File.SizeBytes > 0 {
			sized++
		}
	}
	return fmt.Sprintf("%d repositories · %d with real sizes · %s · %s",
		repos, sized, m.hardwareSummary(), m.runtimeSummary())
}

func (m *Model) renderBrowse(out *block, w int) {
	compact := w < 96 || m.height < 20
	if compact {
		m.renderList(out, w)
		return
	}
	side := m.sidebarBlock(24)
	list := &block{}
	m.listBlock(list, w-24-2)
	out.append(joinBlocks(*side, *list, 2))
}

func (m *Model) sidebarBlock(width int) *block {
	b := &block{}
	b.add(m.st(faintStyle, "LIBRARY"))
	b.add(m.st(faintStyle, "click a section"))
	b.add("")
	b.add(m.st(ruleStyle, strings.Repeat("─", width)))
	for i, cat := range catalogueSections {
		count := m.categoryCount[cat.ID]
		labelW := width - 6
		if labelW < 8 {
			labelW = 8
		}
		label := padRight(truncate(cat.Label, labelW), labelW)
		countText := fmt.Sprintf("%3d", count)
		var line string
		if i == m.categoryIdx {
			line = m.st(accentStyle, "▸ ") + m.st(titleStyle, label) + m.st(mutedStyle, countText)
		} else {
			line = "  " + m.st(mutedStyle, label) + m.st(faintStyle, countText)
		}
		y := b.add(line)
		b.hit(y, 0, width, "category", i)
	}
	return b
}

func (m *Model) renderList(out *block, w int) {
	list := &block{}
	m.listBlock(list, w)
	out.append(*list)
}

func (m *Model) listBlock(b *block, w int) {
	cat := m.section()
	title := m.st(titleStyle, cat.Label)
	count := fmt.Sprintf("%d models", len(m.filtered))
	b.add(padBetween(title, m.st(faintStyle, count), w))

	m.search.Width = max(12, w-6)
	searchLine := m.st(accentStyle, "/ ") + m.search.View()
	y := b.add(searchLine)
	b.hit(y, 0, w, "search", 0)
	b.add("")

	cols := columnsFor(w)
	header := "  " + m.cell("MODEL", cols.name) + "  "
	if cols.publisher > 0 {
		header += m.cell("PUBLISHER", cols.publisher) + "  "
	}
	if cols.params > 0 {
		header += m.cell("PARAMS", cols.params) + "  "
	}
	if cols.size > 0 {
		header += m.cell("SIZE", cols.size) + "  "
	}
	header += m.cell("FIT", cols.fit)
	b.add(m.st(faintStyle, header))

	if len(m.filtered) == 0 {
		b.add("")
		b.add(m.st(mutedStyle, "  No models match this section and search."))
		b.add(m.st(faintStyle, "  Clear the search with Esc, or switch section with ← →."))
		return
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
	for i := start; i < end; i++ {
		e := m.filtered[i]
		fit := m.compatResult(e)
		badge := m.fitBadge(fit, cols.compact)
		name := e.DisplayName
		if name == "" {
			name = e.Repo
		}
		marker := "  "
		nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(brand.White))
		if i == m.cursor {
			marker = m.st(accentStyle, "▸ ")
			nameStyle = titleStyle
		}
		line := marker + m.st(nameStyle, m.cell(name, cols.name)) + "  "
		if cols.publisher > 0 {
			line += m.st(mutedStyle, m.cell(e.Publisher, cols.publisher)) + "  "
		}
		if cols.params > 0 {
			line += m.st(faintStyle, m.cell(paramsLabel(e.ParamsB), cols.params)) + "  "
		}
		if cols.size > 0 {
			line += m.st(mutedStyle, m.cell(sizeLabel(e), cols.size)) + "  "
		}
		line += badge
		y := b.add(line)
		b.hit(y, 0, w, "model", i)
	}

	pages := (len(m.filtered) + pageSize - 1) / pageSize
	if pages < 1 {
		pages = 1
	}
	b.add("")
	b.add(m.st(faintStyle, fmt.Sprintf("  %d–%d of %d  ·  page %d/%d",
		start+1, end, len(m.filtered), m.page+1, pages)))
}

type columns struct {
	name      int
	publisher int
	params    int
	size      int
	fit       int
	compact   bool
}

func columnsFor(w int) columns {
	switch {
	case w >= 86:
		return columns{name: w - 52, publisher: 14, params: 6, size: 9, fit: 13}
	case w >= 64:
		return columns{name: w - 26, publisher: 0, params: 0, size: 9, fit: 9, compact: true}
	default:
		return columns{name: max(10, w-13), publisher: 0, params: 0, size: 0, fit: 8, compact: true}
	}
}

func (m *Model) cell(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return padRight(truncate(s, width), width)
}

func paramsLabel(v float64) string {
	if v <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.0fB", v)
}

func sizeLabel(e discovery.Entry) string {
	if e.File.SizeBytes <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f GB", e.SizeGB())
}

func (m *Model) fitBadge(r compat.Result, compact bool) string {
	var text string
	switch r.Level {
	case compat.Recommended:
		if compact {
			text = "● Fits"
		} else {
			text = "● Recommended"
		}
		return m.st(goodStyle, text)
	case compat.Warning:
		if compact {
			text = "◐ Tight"
		} else {
			text = "◐ Tight fit"
		}
		return m.st(warnStyle, text)
	case compat.Incompatible:
		if compact {
			text = "▲ Large"
		} else {
			text = "▲ Too large"
		}
		return m.st(badStyle, text)
	default:
		return m.st(faintStyle, "? Unknown")
	}
}

func (m *Model) renderDetail(out *block, w int) {
	e := m.selected()
	if e.Repo == "" {
		m.detail = false
		m.renderBrowse(out, w)
		return
	}
	out.add(m.st(faintStyle, "Model details"))
	out.add(m.st(titleStyle, e.DisplayName))
	out.add(m.st(mutedStyle, e.Repo))
	out.add("")

	// Action row: every action is also a mouse target. The row shares the
	// two-column content indent used by the rest of the detail pane.
	actionY := out.add("  ")
	x := 2
	addAction := func(key, label, action string) {
		if x > 2 {
			out.lines[actionY] += m.st(faintStyle, "   ")
			x += 3
		}
		seg := m.st(keyStyle, key) + " " + m.st(mutedStyle, label)
		out.lines[actionY] += seg
		out.hit(actionY, x, x+lipgloss.Width(seg), action, 0)
		x += lipgloss.Width(seg)
	}
	addAction("d", "Download", "detail-download")
	addAction("r", "Requirements", "detail-requirements")
	addAction("e", "Fetch sizes", "detail-enrich")
	addAction("Esc", "Back", "detail-back")

	menuH := 0
	if m.downloadMenu {
		menuH = 6
	}
	if m.quantPicker {
		menuH = min(len(e.Quantizations)+3, 10)
	}
	avail := m.height - len(out.lines) - menuH - 2
	if avail < 5 {
		avail = 5
	}
	content := m.detailContent(e, w)
	if m.detailScroll > len(content)-avail {
		m.detailScroll = len(content) - avail
	}
	if m.detailScroll < 0 {
		m.detailScroll = 0
	}
	start := m.detailScroll
	end := start + avail
	if end > len(content) {
		end = len(content)
	}
	for _, line := range content[start:end] {
		out.add(line)
	}
	if len(content) > avail {
		out.add(m.st(faintStyle, fmt.Sprintf("  %d–%d of %d · j/k or scroll", start+1, end, len(content))))
	}
	if m.downloadMenu {
		out.add("")
		m.renderDownloadMenu(out)
	}
	if m.quantPicker {
		out.add("")
		m.renderQuantPicker(out)
	}
}

func (m *Model) detailContent(e discovery.Entry, w int) []string {
	labelW := 15
	valueW := max(20, w-labelW-4)
	field := func(label, value string) string {
		return "  " + m.st(faintStyle, padRight(label, labelW)) +
			m.st(textStyle, truncate(value, valueW))
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
		size = fmt.Sprintf("%.2f GB · %s", float64(selectedFile.SizeBytes)/1e9, selectedFile.Name)
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
		field("Publisher", or(e.Publisher, "not published")),
		field("Architecture", arch),
	}
	if e.BaseModel != "" && !strings.EqualFold(e.BaseModel, arch) {
		lines = append(lines, field("Base model", e.BaseModel))
	}
	lines = append(lines,
		field("Parameters", params),
		field("File size", size),
		field("Context", ctx),
		field("License", or(e.License, "not published")),
	)
	if len(e.Languages) > 0 {
		lines = append(lines, field("Languages", strings.Join(e.Languages, ", ")))
	}
	if e.CreatedAt != "" {
		created := e.CreatedAt
		if len(created) > 10 {
			created = created[:10]
		}
		lines = append(lines, field("Created", created))
	}
	lines = append(lines, field("Downloads", fmt.Sprintf("%d · %d likes", e.Downloads, e.Likes)))
	quants := strings.Join(e.Quantizations, ", ")
	if quants == "" {
		quants = "not published"
	}
	lines = append(lines, field("Quantizations", truncate(quants, valueW)))
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
	lines = append(lines, field("Features", truncate(strings.Join(features, ", "), valueW)))
	if e.URL != "" {
		lines = append(lines, field("Repository", e.URL))
	}
	lines = append(lines, "")
	lines = append(lines, "  "+m.st(labelStyle, "Compatibility"))
	lines = append(lines, "  "+m.badge(compatResult))
	if m.showRequirements {
		for _, reason := range compatResult.Reasons {
			for _, line := range wrap(reason, max(30, w-4)) {
				lines = append(lines, "  "+m.st(mutedStyle, line))
			}
		}
	} else {
		lines = append(lines, "  "+m.st(faintStyle, "Press r or click Requirements to see the memory calculation."))
	}
	return lines
}

func (m *Model) renderDownloadMenu(out *block) {
	e := m.selected()
	file, _ := m.selectedFile()
	label := "Download model"
	if file.Name != "" {
		label += " (" + or(file.Quant, "default") + ")"
	}
	switch m.compatResult(e).Level {
	case compat.Incompatible:
		label = "Download model anyway — incompatible"
	case compat.Unknown:
		label = "Download model anyway — compatibility unknown"
	}
	out.add(m.st(labelStyle, "Download"))
	items := []string{label, "Select quantization", "Hardware requirements", "Cancel"}
	for i, item := range items {
		var line string
		if i == m.menuCursor {
			line = m.st(accentStyle, "▸ ") + m.st(titleStyle, item)
		} else {
			line = "  " + m.st(mutedStyle, item)
		}
		y := out.add(line)
		out.hit(y, 0, lipgloss.Width(line), "menu", i)
	}
}

func (m *Model) renderQuantPicker(out *block) {
	e := m.selected()
	out.add(m.st(labelStyle, "Select quantization"))
	if len(e.Quantizations) == 0 {
		out.add("  " + m.st(mutedStyle, "No quantizations published for this repository."))
		return
	}
	active := m.quantIndex
	if active < 0 {
		active = quantIndexFor(e, e.File.Quant)
	}
	for i, q := range e.Quantizations {
		var line string
		if i == active {
			line = m.st(accentStyle, "▸ ") + m.st(titleStyle, q)
		} else {
			line = "  " + m.st(mutedStyle, q)
		}
		y := out.add(line)
		out.hit(y, 0, lipgloss.Width(line), "quant", i)
	}
}

func (m *Model) renderProgress(out *block, w int) {
	out.add(m.st(titleStyle, "Downloading"))
	out.add("")
	out.add("  " + m.st(textStyle, m.progressFile))
	out.add("")
	pct := 0.0
	if m.progressTotal > 0 {
		pct = float64(m.progressDone) / float64(m.progressTotal) * 100
	}
	bar := m.progressBar(pct, w)
	out.add("  " + bar + "  " + m.st(titleStyle, fmt.Sprintf("%.0f%%", pct)))
	if m.progressTotal > 0 {
		out.add(fmt.Sprintf("  %.2f / %.2f GB", float64(m.progressDone)/1e9, float64(m.progressTotal)/1e9))
	} else {
		out.add(fmt.Sprintf("  %.2f GB received (size not published)", float64(m.progressDone)/1e9))
	}
	out.add("")
	cancelY := len(out.lines)
	line := "  " + m.st(keyStyle, "Esc") + " " + m.st(mutedStyle, "Cancel")
	out.add(line)
	out.hit(cancelY, 0, lipgloss.Width(line), "cancel-download", 0)
	out.add("  " + m.st(faintStyle, "Partial data is kept so the download can resume."))
}

func (m *Model) progressBar(pct float64, w int) string {
	width := 32
	if w < 70 {
		width = 18
	}
	if width > w-14 {
		width = max(8, w-14)
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

func (m *Model) renderConfirmation(out *block, w int) {
	e := m.selected()
	file, _ := m.selectedFile()
	res := m.compatResult(e)
	out.add(m.st(labelStyle, "Attention"))
	out.add("")
	if m.pendingLevel == compat.Unknown {
		out.add(m.st(warnStyle, "Unknown memory fit"))
		out.add("")
		out.add("  " + m.st(textStyle, fmt.Sprintf("%s (%s)", e.DisplayName, file.Name)))
		out.add("  " + m.st(mutedStyle, "This repository has no published GGUF size, so memory fit cannot be checked."))
		out.add("  " + m.st(mutedStyle, "Press e in the details view to fetch real sizes before downloading."))
	} else {
		out.add(m.st(badStyle, "Incompatible model"))
		out.add("")
		out.add("  " + m.st(textStyle, fmt.Sprintf("%s (%s)", e.DisplayName, file.Name)))
		out.add("  " + m.st(mutedStyle, fmt.Sprintf("Needs ~%.1f GB, but only %.1f GB is usable.", res.Need.RequiredGB, res.EffectiveGB)))
	}
	out.add("")
	out.add("  " + m.st(mutedStyle, "Alocal will not start an unresolved download silently."))
	out.add("")
	y := len(out.lines)
	yes := "[" + m.st(accentStyle, " Download anyway ") + "]"
	no := "[" + m.st(mutedStyle, " Cancel ") + "]"
	line := "  " + yes + "   " + no
	out.add(line)
	yesW := lipgloss.Width(yes)
	noW := lipgloss.Width(no)
	out.hit(y, 2, 2+yesW, "confirm-yes", 0)
	out.hit(y, 2+yesW+3, 2+yesW+3+noW, "confirm-no", 0)
	out.add("")
	out.add("  " + m.hintLine([2]string{"y", "download anyway"}, [2]string{"n", "cancel"}))
	_ = w
}

func (m *Model) renderHelp(out *block, w int) {
	out.add(m.st(labelStyle, "Catalogue controls"))
	out.add("")
	rows := [][2]string{
		{"↑↓ / j k", "move between models"},
		{"←→ / h l", "switch section"},
		{"0–9", "jump to section (0 = all models)"},
		{"/", "search"},
		{"Enter / click", "model details (click again to open)"},
		{"d", "download workflow"},
		{"r", "hardware requirements"},
		{"e", "fetch real file sizes from the Hub"},
		{"[ / ]", "previous / next page"},
		{"Esc", "back or clear search"},
		{"? ", "close this help"},
		{"q / Ctrl+C", "quit"},
	}
	for _, row := range rows {
		out.add("  " + m.st(keyStyle, padRight(row[0], 14)) + m.st(mutedStyle, row[1]))
	}
	out.add("")
	out.add("  " + m.st(mutedStyle, "Mouse: scroll to move, click a row to select, click again to open."))
	y := len(out.lines)
	line := "  " + m.st(keyStyle, "?") + " " + m.st(mutedStyle, "close")
	out.add(line)
	out.hit(y, 0, lipgloss.Width(line), "help-close", 0)
	_ = w
}

// footer shows only the shortcuts that apply to the current surface. Hints are
// added in priority order and dropped when the terminal is too narrow.
func (m *Model) footer(w int) string {
	type part struct{ key, label string }
	var parts []part
	switch {
	case m.help:
		parts = []part{{"?", "close"}, {"q", "quit"}}
	case m.downloading:
		parts = []part{{"Esc", "cancel"}, {"click", "cancel"}}
	case m.confirmDownload:
		parts = []part{{"y", "download anyway"}, {"n", "cancel"}}
	case m.quantPicker:
		parts = []part{{"↑↓", "choose"}, {"Enter", "select"}, {"Esc", "back"}}
	case m.downloadMenu:
		parts = []part{{"↑↓", "choose"}, {"Enter", "select"}, {"Esc", "back"}}
	case m.detail:
		parts = []part{
			{"↑↓", "scroll"}, {"d", "download"}, {"r", "requirements"},
			{"e", "sizes"}, {"Esc", "back"}, {"click", "action"},
		}
	case m.search.Focused():
		parts = []part{{"type", "to filter"}, {"Enter", "done"}, {"Esc", "clear"}}
	default:
		parts = []part{
			{"↑↓", "move"}, {"/", "search"}, {"Enter", "details"},
			{"d", "download"}, {"q", "quit"}, {"?", "help"},
			{"←→", "section"}, {"click", "select"}, {"scroll", "move"},
		}
	}
	var rendered []string
	used := 2
	for _, p := range parts {
		seg := m.st(keyStyle, p.key) + " " + m.st(faintStyle, p.label)
		extra := lipgloss.Width(seg)
		if len(rendered) > 0 {
			extra += 5
		}
		if used+extra > w && len(rendered) > 0 {
			continue
		}
		rendered = append(rendered, seg)
		used += extra
	}
	return "  " + strings.Join(rendered, m.st(faintStyle, "  ·  "))
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

func (m *Model) badge(r compat.Result) string {
	switch r.Level {
	case compat.Recommended:
		return m.st(goodStyle, "● Recommended")
	case compat.Warning:
		return m.st(warnStyle, "◐ Tight fit")
	case compat.Incompatible:
		return m.st(badStyle, "▲ Too large")
	default:
		return m.st(faintStyle, "? Unknown")
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

func (m *Model) section() section {
	if m.categoryIdx < 0 || m.categoryIdx >= len(catalogueSections) {
		return catalogueSections[0]
	}
	return catalogueSections[m.categoryIdx]
}

func (m *Model) inSection(e discovery.Entry, id string) bool {
	switch id {
	case "all":
		return true
	case discovery.CategoryDownloaded:
		return e.Downloaded
	default:
		return e.HasCategory(id)
	}
}

func (m *Model) setCategory(index int) {
	n := len(catalogueSections)
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
	cat := m.section()
	query := m.search.Value()
	m.filtered = m.filtered[:0]
	for _, e := range m.entries {
		if !m.inSection(e, cat.ID) || !e.Matches(query) {
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
	m.categoryCount = map[string]int{"all": discovery.Count(m.entries)}
	for _, e := range m.entries {
		for _, id := range e.Categories {
			m.categoryCount[id]++
		}
		if e.Downloaded && !e.HasCategory(discovery.CategoryDownloaded) {
			m.categoryCount[discovery.CategoryDownloaded]++
		}
	}
}

func (m *Model) pageSize() int {
	if m.height <= 0 {
		return 12
	}
	n := m.height - 11
	if n < 5 {
		return 5
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

// ---- rendering primitives -------------------------------------------------

type hitBox struct {
	y, x0, x1 int
	action    string
	index     int
}

type block struct {
	lines []string
	hits  []hitBox
}

func (b *block) add(line string) int {
	b.lines = append(b.lines, line)
	return len(b.lines) - 1
}

func (b *block) hit(y, x0, x1 int, action string, index int) {
	if x1 <= x0 {
		return
	}
	b.hits = append(b.hits, hitBox{y: y, x0: x0, x1: x1, action: action, index: index})
}

func (b *block) append(other block) {
	offset := len(b.lines)
	b.lines = append(b.lines, other.lines...)
	for _, h := range other.hits {
		h.y += offset
		b.hits = append(b.hits, h)
	}
}

func joinBlocks(left, right block, gap int) block {
	lines := make([]string, 0, max(len(left.lines), len(right.lines)))
	leftW := 0
	for _, line := range left.lines {
		if w := lipgloss.Width(line); w > leftW {
			leftW = w
		}
	}
	rightW := 0
	for _, line := range right.lines {
		if w := lipgloss.Width(line); w > rightW {
			rightW = w
		}
	}
	for i := 0; i < max(len(left.lines), len(right.lines)); i++ {
		l, r := "", ""
		if i < len(left.lines) {
			l = left.lines[i]
		}
		if i < len(right.lines) {
			r = right.lines[i]
		}
		lines = append(lines, padRight(l, leftW)+strings.Repeat(" ", gap)+r)
	}
	out := block{lines: lines}
	out.hits = append(out.hits, left.hits...)
	for _, h := range right.hits {
		h.x0 += leftW + gap
		h.x1 += leftW + gap
		out.hits = append(out.hits, h)
	}
	return out
}

func (m *Model) st(style lipgloss.Style, text string) string {
	if !m.color {
		return text
	}
	return style.Render(text)
}

func (m *Model) hintLine(pairs ...[2]string) string {
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		parts = append(parts, m.st(keyStyle, p[0])+" "+m.st(faintStyle, p[1]))
	}
	return strings.Join(parts, m.st(faintStyle, "  ·  "))
}

func padBetween(left, right string, width int) string {
	lw := lipgloss.Width(left)
	rw := lipgloss.Width(right)
	if lw+rw+2 > width {
		return left
	}
	return left + strings.Repeat(" ", width-lw-rw) + right
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
