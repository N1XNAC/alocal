package catalogui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"alocal/internal/discovery"
	"alocal/internal/hardware"
	"alocal/internal/hf"
	"alocal/internal/local"
)

func testEntry(repo, name, quant string, size int64, cats ...string) discovery.Entry {
	alt := strings.Replace(strings.ToUpper(name), "Q4_K_M", "Q8_0", 1)
	if alt == strings.ToUpper(name) {
		alt = "tiny-Q8_0.gguf"
	}
	return discovery.Entry{
		ID: repo, Repo: repo, Publisher: strings.Split(repo, "/")[0],
		DisplayName: "Tiny 1B Instruct", ParamsB: 1, File: discovery.File{Name: name, Quant: quant, SizeBytes: size},
		Quantizations: []string{quant, "Q8_0"},
		Files: []discovery.File{
			{Name: name, Quant: quant, SizeBytes: size},
			{Name: alt, Quant: "Q8_0", SizeBytes: size * 2},
		},
		Categories: cats, Downloads: 100,
	}
}

func sendKeys(m *Model, keys ...string) tea.Cmd {
	var last tea.Cmd
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "up":
			msg = tea.KeyMsg{Type: tea.KeyUp}
		case "right":
			msg = tea.KeyMsg{Type: tea.KeyRight}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		_, cmd := m.Update(msg)
		if cmd != nil {
			last = cmd
		}
	}
	return last
}

func TestAllModelsIsTheDefaultSection(t *testing.T) {
	entries := []discovery.Entry{
		testEntry("acme/Tiny-Coder", "tiny.gguf", "Q4_K_M", 1e9, discovery.CategoryCoding, discovery.CategoryGeneralChat),
		testEntry("acme/Tiny-Chat", "tiny.gguf", "Q4_K_M", 1e9, discovery.CategoryGeneralChat),
	}
	m := New(Options{Entries: entries, Color: false})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	if m.section().ID != "all" {
		t.Fatalf("default section = %s, want all", m.section().ID)
	}
	if len(m.filtered) != 2 || m.categoryCount["all"] != 2 {
		t.Fatalf("all section filtered=%d count=%d, want 2/2", len(m.filtered), m.categoryCount["all"])
	}
	if view := m.View(); !strings.Contains(view, "All Models") || !strings.Contains(view, "2 models") {
		t.Fatalf("all models view missing: %s", view)
	}
	sendKeys(m, "1")
	if m.section().ID != discovery.CategoryGeneralChat || len(m.filtered) != 2 {
		t.Fatalf("section 1 = %s, filtered=%d", m.section().ID, len(m.filtered))
	}
	sendKeys(m, "2")
	if m.section().ID != discovery.CategoryCoding || len(m.filtered) != 1 {
		t.Fatalf("section 2 = %s, filtered=%d", m.section().ID, len(m.filtered))
	}
}

func clickHit(t *testing.T, m *Model, action string, index int) {
	t.Helper()
	m.View()
	for _, h := range m.hits {
		if h.action == action && h.index == index {
			m.Update(tea.MouseMsg{
				X:      (h.x0 + h.x1) / 2,
				Y:      h.y,
				Button: tea.MouseButtonLeft,
				Action: tea.MouseActionPress,
			})
			return
		}
	}
	t.Fatalf("no clickable %q/%d in %d hit boxes: %+v", action, index, len(m.hits), m.hits)
}

func TestMouseSelectsRowsOpensDetailsAndSwitchesSections(t *testing.T) {
	entries := []discovery.Entry{
		testEntry("acme/One", "one.gguf", "Q4_K_M", 1e9, discovery.CategoryGeneralChat),
		testEntry("acme/Two", "two.gguf", "Q4_K_M", 1e9, discovery.CategoryCoding),
		testEntry("acme/Three", "three.gguf", "Q4_K_M", 1e9, discovery.CategoryVision),
	}
	m := New(Options{Entries: entries, Color: false})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	m.View()

	clickHit(t, m, "model", 1)
	if m.cursor != 1 || m.detail {
		t.Fatalf("first click cursor=%d detail=%v, want 1/false", m.cursor, m.detail)
	}
	clickHit(t, m, "model", 1)
	if !m.detail {
		t.Fatal("second click did not open details")
	}
	sendKeys(m, "esc")
	if m.detail {
		t.Fatal("esc did not leave details")
	}

	// Clicking a sidebar section should switch the list immediately.
	clickHit(t, m, "category", 2)
	if m.section().ID != discovery.CategoryCoding {
		t.Fatalf("category click selected %s, want coding", m.section().ID)
	}
}

func TestMouseWheelMovesTheSelection(t *testing.T) {
	entries := []discovery.Entry{
		testEntry("acme/One", "one.gguf", "Q4_K_M", 1e9, discovery.CategoryGeneralChat),
		testEntry("acme/Two", "two.gguf", "Q4_K_M", 1e9, discovery.CategoryGeneralChat),
		testEntry("acme/Three", "three.gguf", "Q4_K_M", 1e9, discovery.CategoryGeneralChat),
	}
	m := New(Options{Entries: entries, Color: false})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	m.View()
	m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if m.cursor != 2 {
		t.Fatalf("wheel down cursor = %d, want 2", m.cursor)
	}
	m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	if m.cursor != 0 {
		t.Fatalf("wheel up cursor = %d, want 0", m.cursor)
	}
}

func TestCategoriesSearchAndPagination(t *testing.T) {
	entries := []discovery.Entry{
		testEntry("acme/Tiny-Coder", "tiny.gguf", "Q4_K_M", 1e9, discovery.CategoryCoding, discovery.CategoryGeneralChat),
		testEntry("acme/Tiny-Chat", "tiny.gguf", "Q4_K_M", 1e9, discovery.CategoryGeneralChat),
	}
	m := New(Options{Entries: entries, Color: false})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 22})
	if !strings.Contains(m.View(), "Coding") || !strings.Contains(m.View(), "Downloaded Models") {
		t.Fatalf("missing categories: %s", m.View())
	}
	sendKeys(m, "/")
	for _, r := range "coder" {
		sendKeys(m, string(r))
	}
	if len(m.filtered) != 1 || m.filtered[0].Repo != "acme/Tiny-Coder" {
		t.Fatalf("search filtered = %+v", m.filtered)
	}
	sendKeys(m, "esc", "esc", "2")
	if m.section().ID != discovery.CategoryCoding {
		t.Fatalf("category = %s", m.section().ID)
	}
	if len(m.filtered) != 1 || m.filtered[0].Repo != "acme/Tiny-Coder" {
		t.Fatalf("coding filter = %+v", m.filtered)
	}
}

func TestDetailDownloadMenuAndQuantPicker(t *testing.T) {
	m := New(Options{Entries: []discovery.Entry{testEntry("acme/Tiny", "tiny.gguf", "Q4_K_M", 1e9, discovery.CategoryGeneralChat)}, Color: false})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	sendKeys(m, "enter")
	if !m.detail || !strings.Contains(m.View(), "Model details") {
		t.Fatalf("detail view missing: %s", m.View())
	}
	sendKeys(m, "d")
	if !m.downloadMenu || !strings.Contains(m.View(), "Download model") {
		t.Fatalf("download menu missing: %s", m.View())
	}
	sendKeys(m, "down", "enter")
	if !m.quantPicker || !strings.Contains(m.View(), "Select quantization") {
		t.Fatalf("quant picker missing: %s", m.View())
	}
	sendKeys(m, "down", "enter")
	if m.quantIndex != 1 {
		t.Fatalf("quant index = %d, want 1", m.quantIndex)
	}
}

func TestDownloadStreamsVerifiesAndRegisters(t *testing.T) {
	t.Setenv("ALOCAL_DATA_DIR", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/resolve/main/") {
			w.Header().Set("Content-Type", "application/octet-stream")
			fmt.Fprint(w, "GGUF-tiny-fake-bytes")
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	p, err := hardware.LoadProfile("../../tests/fixtures/rtx4090.json")
	if err != nil {
		t.Fatal(err)
	}
	client := &hf.Client{BaseURL: srv.URL, HTTP: srv.Client()}
	e := testEntry("acme/Tiny", "tiny-Q4_K_M.gguf", "Q4_K_M", 20, discovery.CategoryGeneralChat)
	m := New(Options{Entries: []discovery.Entry{e}, Profile: p, Client: client, Color: false})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	cmd := m.requestDownloadCmd()
	if cmd == nil {
		t.Fatalf("download did not start: %s", m.status)
	}
	msg := cmd()
	if bad, ok := msg.(downloadDoneMsg); ok && bad.err != nil {
		t.Fatalf("download error: %v", bad.err)
	}
	m.Update(msg)
	store, err := local.LoadModels()
	if err != nil || len(store.Models) != 1 {
		t.Fatalf("registry = %+v, err = %v", store, err)
	}
	if store.Models[0].ID != "acme/Tiny/tiny-Q4_K_M.gguf" {
		t.Fatalf("bad model id: %s", store.Models[0].ID)
	}
	if !m.selected().Downloaded || !strings.Contains(m.status, "alocal chat") {
		t.Fatalf("download not reflected in UI: %+v / %s", m.selected(), m.status)
	}
	if m.section().ID != discovery.CategoryDownloaded {
		t.Fatalf("category = %s, want downloaded", m.section().ID)
	}
}

func TestQuantSelectionChangesSizeAndCompatibility(t *testing.T) {
	p, err := hardware.LoadProfile("../../tests/fixtures/rtx3060.json")
	if err != nil {
		t.Fatal(err)
	}
	e := testEntry("acme/Tiny", "tiny-Q4_K_M.gguf", "Q4_K_M", 1_000_000_000, discovery.CategoryGeneralChat)
	m := New(Options{Entries: []discovery.Entry{e}, Profile: p, Color: false})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	before := m.compatResult(e).Need.RequiredGB
	sendKeys(m, "enter", "d", "down", "enter", "down", "enter")
	file, ok := m.selectedFile()
	if !ok || file.Quant != "Q8_0" || file.SizeBytes != 2_000_000_000 {
		t.Fatalf("selected file = %+v, ok = %v", file, ok)
	}
	after := m.compatResult(m.selected()).Need.RequiredGB
	if after <= before {
		t.Fatalf("quant selection did not change memory estimate: before %.2f, after %.2f", before, after)
	}
	if !strings.Contains(m.View(), "Selected quant") {
		t.Fatalf("detail view does not show selected quant: %s", m.View())
	}
}

func TestUnknownCompatibilityRequiresConfirmation(t *testing.T) {
	t.Setenv("ALOCAL_DATA_DIR", t.TempDir())
	e := testEntry("acme/Unknown", "tiny-Q4_K_M.gguf", "Q4_K_M", 0, discovery.CategoryGeneralChat)
	m := New(Options{Entries: []discovery.Entry{e}, Profile: nil, Color: false})
	if cmd := m.requestDownloadCmd(); cmd != nil || !m.confirmDownload || string(m.pendingLevel) != "Unknown" {
		t.Fatalf("unknown download was not gated: cmd=%v status=%s level=%s", cmd, m.status, m.pendingLevel)
	}
	if !strings.Contains(m.View(), "Unknown memory fit") {
		t.Fatalf("confirmation view missing: %s", m.View())
	}
}

func TestIncompatibleDownloadRequiresConfirmation(t *testing.T) {
	t.Setenv("ALOCAL_DATA_DIR", t.TempDir())
	p, err := hardware.LoadProfile("../../tests/fixtures/nogpu4.json")
	if err != nil {
		t.Fatal(err)
	}
	e := testEntry("acme/Huge", "huge-Q4_K_M.gguf", "Q4_K_M", 80_000_000_000, discovery.CategoryGeneralChat)
	m := New(Options{Entries: []discovery.Entry{e}, Profile: p, Color: false})
	if cmd := m.requestDownloadCmd(); cmd != nil || !m.confirmDownload {
		t.Fatalf("incompatible download was not gated: cmd=%v status=%s", cmd, m.status)
	}
	sendKeys(m, "y")
	if !m.downloading {
		t.Fatalf("confirmation did not start download: %s", m.status)
	}
}
