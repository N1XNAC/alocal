package catalogui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"alocal/internal/compat"
	"alocal/internal/discovery"
	"alocal/internal/hf"
	"alocal/internal/local"
)

type fetchedMsg struct {
	entries []discovery.Entry
	err     error
}

type enrichedMsg struct {
	repo  string
	entry discovery.Entry
	err   error
}

type progressMsg struct {
	done  int64
	total int64
	file  string
}

type downloadDoneMsg struct {
	model local.Model
	err   error
}

type statusMsg struct {
	text string
	err  error
}

func (m *Model) fetchCmd() tea.Cmd {
	client := m.client
	if client == nil {
		client = hf.New()
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		entries, err := discovery.Fetch(ctx, client, 120)
		return fetchedMsg{entries: entries, err: err}
	}
}

func (m *Model) enrichCmd() tea.Cmd {
	e := m.selected()
	if e.Repo == "" {
		return nil
	}
	client := m.client
	if client == nil {
		client = hf.New()
	}
	repo := e.Repo
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		copyEntry := e
		err := discovery.Enrich(ctx, client, &copyEntry)
		return enrichedMsg{repo: repo, entry: copyEntry, err: err}
	}
}

func (m *Model) onFetched(v fetchedMsg) {
	if v.err != nil {
		m.status = fmt.Sprintf("Live Hub unavailable; using %d embedded repositories.", discovery.Count(m.entries))
		return
	}
	before := discovery.Count(m.entries)
	m.entries = discovery.Merge(m.entries, v.entries)
	after := discovery.Count(m.entries)
	m.err = nil
	m.applyFilter()
	m.status = fmt.Sprintf("Live Hub added %d repositories (%d total).", after-before, after)
}

func (m *Model) onEnriched(v enrichedMsg) {
	if v.err != nil {
		m.err = v.err
		m.status = "Could not fetch file sizes: " + shortError(v.err)
		return
	}
	m.err = nil
	for i := range m.entries {
		if strings.EqualFold(m.entries[i].Repo, v.repo) {
			v.entry.Downloaded = m.entries[i].Downloaded || v.entry.Downloaded
			m.entries[i] = v.entry
			break
		}
	}
	m.applyFilter()
	m.status = fmt.Sprintf("Loaded %d real file sizes for %s.", len(v.entry.Files), v.repo)
}

func (m *Model) requestDownloadCmd() tea.Cmd {
	e := m.selected()
	file, ok := m.selectedFile()
	if e.Repo == "" || !ok || file.Name == "" {
		m.status = "No GGUF file is known for this repository. Press e to fetch file metadata."
		return nil
	}
	store, err := local.LoadModels()
	if err != nil {
		m.err = err
		m.status = "Could not read local model registry: " + shortError(err)
		return nil
	}
	id := e.Repo + "/" + file.Name
	if _, found := store.Find(id); found {
		m.status = "Already downloaded. Use `alocal chat " + id + "` to run it."
		return nil
	}
	dest := local.ModelPath(e.Repo, file.Name)
	if _, err := os.Stat(dest); err == nil {
		return m.registerDownloadCmd(e, file, dest)
	}
	level := m.compatResult(e).Level
	if level == compat.Incompatible || level == compat.Unknown {
		m.confirmDownload = true
		m.pendingLevel = level
		m.pendingEntry, m.pendingFile, m.pendingDest = e, file, dest
		if level == compat.Incompatible {
			m.status = "Incompatible with this machine. Press y to confirm or n to cancel."
		} else {
			m.status = "Memory fit is unknown. Press y to confirm or n to cancel."
		}
		return nil
	}
	return m.beginDownload(e, file, dest)
}

func (m *Model) beginDownload(e discovery.Entry, file discovery.File, dest string) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.cancelRequested = false
	m.downloading = true
	m.err = nil
	m.progressFile = file.Name
	m.progressDone, m.progressTotal = 0, file.SizeBytes
	m.status = "Downloading " + file.Name + "…"
	client := m.client
	if client == nil {
		client = hf.New()
	}
	program := m.program
	return func() tea.Msg {
		err := client.Download(ctx, e.Repo, file.Name, dest, func(done, total int64) {
			if program != nil {
				program.Send(progressMsg{done: done, total: total, file: file.Name})
			}
		})
		if err != nil {
			return downloadDoneMsg{err: err}
		}
		return m.registerDownloadCmd(e, file, dest)()
	}
}

func (m *Model) registerDownloadCmd(e discovery.Entry, file discovery.File, dest string) tea.Cmd {
	return func() tea.Msg {
		if err := hf.VerifyGGUF(dest); err != nil {
			return downloadDoneMsg{err: fmt.Errorf("verify %s: %w", file.Name, err)}
		}
		info, err := os.Stat(dest)
		if err != nil {
			return downloadDoneMsg{err: err}
		}
		model := local.Model{
			ID:        e.Repo + "/" + file.Name,
			Repo:      e.Repo,
			File:      file.Name,
			Path:      dest,
			SizeBytes: info.Size(),
			Quant:     quantFromName(file.Name),
			Family:    or(e.BaseModel, e.DisplayName),
		}
		store, err := local.LoadModels()
		if err != nil {
			return downloadDoneMsg{err: err}
		}
		if err := store.Add(model); err != nil {
			return downloadDoneMsg{err: err}
		}
		return downloadDoneMsg{model: model}
	}
}

func (m *Model) onDownloadDone(v downloadDoneMsg) {
	m.downloading = false
	m.cancel = nil
	if v.err != nil {
		if m.cancelRequested {
			m.cancelRequested = false
			m.status = "Download cancelled; partial data was kept for resume."
			return
		}
		m.err = v.err
		m.status = "Download failed: " + shortError(v.err) + " (partial data was kept for resume)"
		return
	}
	m.cancelRequested = false
	store, err := local.LoadModels()
	if err != nil {
		m.err = err
		m.status = "Downloaded, but the local registry could not be reloaded: " + shortError(err)
		return
	}
	m.err = nil
	localEntries := discovery.LocalEntries(store)
	m.entries = discovery.Merge(m.entries, localEntries)
	m.rebuildCounts()
	m.setCategoryByID(discovery.CategoryDownloaded)
	for i, e := range m.filtered {
		if e.Repo == v.model.Repo && e.File.Name == v.model.File {
			m.cursor = i
			m.page = i / m.pageSize()
			break
		}
	}
	m.detail = true
	m.showRequirements = true
	m.status = "Downloaded " + v.model.ID + ". Run it with: alocal chat " + v.model.ID
}

func (m *Model) setCategoryByID(id string) {
	for i, cat := range catalogueSections {
		if cat.ID == id {
			m.setCategory(i)
			return
		}
	}
}

func quantFromName(name string) string {
	upper := strings.ToUpper(name)
	for _, q := range []string{"IQ4_XS", "Q4_K_M", "Q5_K_M", "Q6_K", "Q8_0", "Q3_K_M", "BF16", "F16", "FP16"} {
		if strings.Contains(upper, q) {
			return q
		}
	}
	return ""
}

func shortError(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if len(s) > 110 {
		return s[:107] + "..."
	}
	return s
}
