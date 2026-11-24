// Package discovery holds the interactive model catalogue. Unlike the
// scoring catalog, one entry is one Hugging Face repository: quantizations are
// files within an entry, never separate base models.
package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"alocal/data"
	"alocal/internal/hf"
	"alocal/internal/local"
	"alocal/internal/scoring"
)

// Category identifiers used by the interactive catalogue.
const (
	CategoryGeneralChat  = "general-chat"
	CategoryCoding       = "coding"
	CategoryReasoning    = "reasoning"
	CategoryMathematics  = "mathematics"
	CategoryMultilingual = "multilingual"
	CategoryVision       = "vision"
	CategoryEmbeddings   = "embeddings"
	CategoryAudio        = "audio"
	CategoryLightweight  = "lightweight"
	CategoryDownloaded   = "downloaded"
)

// Category is a user-facing catalogue section.
type Category struct {
	ID    string
	Label string
}

// Categories is the fixed section order shown in the catalogue.
var Categories = []Category{
	{CategoryGeneralChat, "General Chat"},
	{CategoryCoding, "Coding"},
	{CategoryReasoning, "Reasoning"},
	{CategoryMathematics, "Mathematics"},
	{CategoryMultilingual, "Multilingual"},
	{CategoryVision, "Vision"},
	{CategoryEmbeddings, "Embeddings"},
	{CategoryAudio, "Audio"},
	{CategoryLightweight, "Lightweight Models"},
	{CategoryDownloaded, "Downloaded Models"},
}

// File is one real GGUF file in a repository.
type File struct {
	Name      string `json:"name"`
	Quant     string `json:"quant,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
}

// Entry is one base model repository. All metadata originates from the
// Hugging Face Hub or from Alocal's local registry.
type Entry struct {
	ID            string   `json:"id"`
	Repo          string   `json:"repo"`
	Publisher     string   `json:"publisher"`
	DisplayName   string   `json:"display_name"`
	BaseModel     string   `json:"base_model,omitempty"`
	Architecture  string   `json:"architecture,omitempty"`
	ParamsB       float64  `json:"params_b,omitempty"`
	ActiveParamsB float64  `json:"active_params_b,omitempty"`
	File          File     `json:"file"`
	Quantizations []string `json:"quantizations,omitempty"`
	Files         []File   `json:"files,omitempty"`
	MaxContext    int      `json:"max_context,omitempty"`
	Pipeline      string   `json:"pipeline,omitempty"`
	Tasks         []string `json:"tasks,omitempty"`
	Categories    []string `json:"categories,omitempty"`
	License       string   `json:"license,omitempty"`
	Languages     []string `json:"languages,omitempty"`
	Downloads     int64    `json:"downloads,omitempty"`
	Likes         int64    `json:"likes,omitempty"`
	CreatedAt     string   `json:"created_at,omitempty"`
	URL           string   `json:"url,omitempty"`
	Downloaded    bool     `json:"-"`
}

// Catalog is the embedded repository snapshot.
type Catalog struct {
	Version     int     `json:"version"`
	Source      string  `json:"source"`
	GeneratedAt string  `json:"generated_at"`
	Count       int     `json:"count"`
	Models      []Entry `json:"models"`
}

// Load parses and validates the embedded catalogue.
func Load() (*Catalog, error) {
	var c Catalog
	if err := json.Unmarshal(data.DiscoveryJSON, &c); err != nil {
		return nil, fmt.Errorf("parse embedded discovery catalog: %w", err)
	}
	if len(c.Models) < 300 {
		return nil, fmt.Errorf("embedded discovery catalog has %d entries, want at least 300", len(c.Models))
	}
	seen := map[string]bool{}
	for i := range c.Models {
		normalize(&c.Models[i])
		key := strings.ToLower(c.Models[i].Repo)
		if key == "" || seen[key] {
			return nil, fmt.Errorf("duplicate or empty repository entry %q", c.Models[i].Repo)
		}
		seen[key] = true
	}
	return &c, nil
}

// Count returns the number of distinct base models in a set of entries.
func Count(entries []Entry) int {
	seen := map[string]bool{}
	for _, e := range entries {
		key := strings.ToLower(e.Repo)
		if key != "" {
			seen[key] = true
		}
	}
	return len(seen)
}

// Merge combines catalogue sources, deduplicating by repository. Entries with
// real file sizes, richer quant lists or a local download win.
func Merge(groups ...[]Entry) []Entry {
	byRepo := map[string]Entry{}
	var order []string
	for _, group := range groups {
		for _, e := range group {
			key := strings.ToLower(e.Repo)
			if key == "" {
				continue
			}
			old, ok := byRepo[key]
			if !ok {
				order = append(order, key)
			}
			if !ok || better(e, old) {
				if ok {
					e.Downloaded = e.Downloaded || old.Downloaded
				}
				byRepo[key] = e
			} else if e.Downloaded {
				merged := byRepo[key]
				merged.Downloaded = true
				byRepo[key] = merged
			}
		}
	}
	out := make([]Entry, 0, len(order))
	for _, key := range order {
		out = append(out, byRepo[key])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Downloaded != out[j].Downloaded {
			return out[i].Downloaded
		}
		if out[i].Downloads != out[j].Downloads {
			return out[i].Downloads > out[j].Downloads
		}
		return strings.ToLower(out[i].Repo) < strings.ToLower(out[j].Repo)
	})
	return out
}

func better(a, b Entry) bool {
	if a.Downloaded != b.Downloaded {
		return a.Downloaded
	}
	if (a.File.SizeBytes > 0) != (b.File.SizeBytes > 0) {
		return a.File.SizeBytes > 0
	}
	if len(a.Files) != len(b.Files) {
		return len(a.Files) > len(b.Files)
	}
	return len(a.Quantizations) > len(b.Quantizations)
}

// HasCategory reports membership in a catalogue section.
func (e Entry) HasCategory(id string) bool {
	for _, c := range e.Categories {
		if strings.EqualFold(c, id) {
			return true
		}
	}
	return false
}

// Matches applies a free-text search across all published metadata.
func (e Entry) Matches(query string) bool {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(query)))
	if len(fields) == 0 {
		return true
	}
	parts := []string{e.Repo, e.Publisher, e.DisplayName, e.BaseModel, e.Architecture, e.Pipeline, e.License, e.URL}
	parts = append(parts, e.Tasks...)
	parts = append(parts, e.Categories...)
	parts = append(parts, e.Languages...)
	for _, q := range e.Quantizations {
		parts = append(parts, q)
	}
	parts = append(parts, e.File.Name)
	haystack := strings.ToLower(strings.Join(parts, " "))
	for _, term := range fields {
		if !strings.Contains(haystack, term) {
			return false
		}
	}
	return true
}

// SizeGB returns the real representative file size in GB.
func (e Entry) SizeGB() float64 {
	return float64(e.File.SizeBytes) / 1e9
}

// RAMEstimateGB is the runtime memory estimate for the selected quant and
// context length. It is derived from real file size when available.
func (e Entry) RAMEstimateGB(contextSize int) float64 {
	need := scoring.ComputeModelNeed(e.Spec(), contextSize)
	return need.RequiredGB
}

// Spec converts a catalogue entry into the scoring engine's model input.
func (e Entry) Spec() scoring.ModelSpec {
	return scoring.ModelSpec{
		ID:            e.Repo,
		ParamsB:       e.ParamsB,
		ActiveParamsB: e.ActiveParamsB,
		Quant:         e.File.Quant,
		FileSizeGB:    e.SizeGB(),
		MaxContext:    e.MaxContext,
	}
}

// FromLocal converts a downloaded GGUF into a catalogue entry.
func FromLocal(m local.Model) Entry {
	repo := m.Repo
	if repo == "" {
		repo = strings.TrimSuffix(m.ID, "/"+m.File)
	}
	name := m.File
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	display := strings.TrimSuffix(name, ".gguf")
	e := Entry{
		ID:          m.ID,
		Repo:        repo,
		Publisher:   repoPublisher(repo),
		DisplayName: display,
		File:        File{Name: m.File, Quant: m.Quant, SizeBytes: m.SizeBytes},
		Downloaded:  true,
		Categories:  []string{CategoryDownloaded},
		URL:         "https://huggingface.co/" + repo,
	}
	if m.Quant != "" {
		e.Quantizations = []string{m.Quant}
	}
	return e
}

// LocalEntries converts a local registry into catalogue entries.
func LocalEntries(store *local.ModelStore) []Entry {
	if store == nil {
		return nil
	}
	out := make([]Entry, 0, len(store.Models))
	for _, m := range store.Models {
		out = append(out, FromLocal(m))
	}
	return out
}

// Fetch reads fresh GGUF repositories from the Hub. Network failures are
// returned to the caller so the UI can fall back to the embedded snapshot.
func Fetch(ctx context.Context, client *hf.Client, limit int) ([]Entry, error) {
	if client == nil {
		client = hf.New()
	}
	results, err := client.Search(ctx, "", limit)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(results))
	for _, r := range results {
		if r.ID == "" || !strings.Contains(r.ID, "/") {
			continue
		}
		out = append(out, FromSearchResult(r))
	}
	return out, nil
}

// FromSearchResult converts Hub metadata into a catalogue entry. File sizes
// are intentionally left unknown until Enrich is called.
func FromSearchResult(r hf.SearchResult) Entry {
	tags := append([]string(nil), r.Tags...)
	text := r.ID + " " + strings.Join(tags, " ")
	params, active := parseParams(text)
	publisher := r.Author
	if publisher == "" && strings.Contains(r.ID, "/") {
		publisher = repoPublisher(r.ID)
	}
	var files []File
	var quants []string
	seen := map[string]bool{}
	for _, s := range r.Siblings {
		name := s.Rfilename
		if !strings.HasSuffix(strings.ToLower(name), ".gguf") {
			continue
		}
		q := quantFromName(name)
		files = append(files, File{Name: name, Quant: q})
		if q != "" && !seen[q] {
			seen[q] = true
			quants = append(quants, q)
		}
	}
	primary := pickFile(files, "")
	e := Entry{
		ID:            r.ID,
		Repo:          r.ID,
		Publisher:     publisher,
		DisplayName:   displayName(r.ID),
		ParamsB:       params,
		ActiveParamsB: active,
		File:          primary,
		Quantizations: quants,
		Files:         files,
		MaxContext:    parseContext(text),
		Pipeline:      r.PipelineTag,
		Tasks:         cleanTags(tags),
		License:       licenseFromTags(tags),
		Downloads:     r.Downloads,
		Likes:         r.Likes,
		CreatedAt:     r.CreatedAt,
		URL:           "https://huggingface.co/" + r.ID,
	}
	e.Categories = deriveCategories(e.Pipeline, tags, text, params, nil)
	return e
}

// Enrich fills in real file sizes and quantizations from the Hub tree API.
func Enrich(ctx context.Context, client *hf.Client, e *Entry) error {
	if e == nil || e.Repo == "" {
		return fmt.Errorf("empty repository")
	}
	if client == nil {
		client = hf.New()
	}
	tree, err := client.Tree(ctx, e.Repo)
	if err != nil {
		return err
	}
	files := make([]File, 0, len(tree))
	seen := map[string]bool{}
	for _, node := range tree {
		if node.Type != "file" || !strings.HasSuffix(strings.ToLower(node.Path), ".gguf") || node.Size <= 0 {
			continue
		}
		q := quantFromName(node.Path)
		files = append(files, File{Name: node.Path, Quant: q, SizeBytes: node.Size})
		if q != "" && !seen[q] {
			seen[q] = true
			e.Quantizations = append(e.Quantizations, q)
		}
	}
	if len(files) == 0 {
		return fmt.Errorf("no GGUF files with published sizes in %s", e.Repo)
	}
	sort.SliceStable(files, func(i, j int) bool { return files[i].SizeBytes > files[j].SizeBytes })
	e.Files = files
	e.File = pickFile(files, e.File.Quant)
	return nil
}

// SelectFile returns the file matching a quantization label.
func (e Entry) SelectFile(quant string) (File, bool) {
	for _, f := range e.Files {
		if strings.EqualFold(f.Quant, quant) {
			return f, true
		}
	}
	if e.File.Quant != "" && strings.EqualFold(e.File.Quant, quant) {
		return e.File, true
	}
	return File{}, false
}

func normalize(e *Entry) {
	e.Repo = strings.TrimSpace(e.Repo)
	if e.Repo == "" {
		e.Repo = strings.TrimSpace(e.ID)
	}
	if e.ID == "" {
		e.ID = e.Repo
	}
	if e.Publisher == "" {
		e.Publisher = repoPublisher(e.Repo)
	}
	if e.DisplayName == "" {
		e.DisplayName = displayName(e.Repo)
	}
	if e.URL == "" {
		e.URL = "https://huggingface.co/" + e.Repo
	}
	if len(e.Quantizations) == 0 {
		seen := map[string]bool{}
		for _, f := range e.Files {
			if f.Quant != "" && !seen[f.Quant] {
				seen[f.Quant] = true
				e.Quantizations = append(e.Quantizations, f.Quant)
			}
		}
	}
	if len(e.Categories) == 0 {
		text := e.Repo + " " + e.BaseModel + " " + strings.Join(e.Tasks, " ")
		e.Categories = deriveCategories(e.Pipeline, e.Tasks, text, e.ParamsB, e.Languages)
	}
	if e.Downloaded && !e.HasCategory(CategoryDownloaded) {
		e.Categories = append(e.Categories, CategoryDownloaded)
	}
}

func pickFile(files []File, want string) File {
	if len(files) == 0 {
		return File{}
	}
	if want != "" {
		for _, f := range files {
			if strings.EqualFold(f.Quant, want) {
				return f
			}
		}
	}
	for _, preferred := range []string{"Q4_K_M", "Q5_K_M", "Q8_0", "Q6_K"} {
		for _, f := range files {
			if strings.EqualFold(f.Quant, preferred) {
				return f
			}
		}
	}
	return files[0]
}

func repoPublisher(repo string) string {
	if i := strings.Index(repo, "/"); i > 0 {
		return repo[:i]
	}
	return repo
}

func displayName(repo string) string {
	name := repo
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = regexp.MustCompile(`(?i)-?gguf$`).ReplaceAllString(name, "")
	name = strings.NewReplacer("_", " ", "-", " ").Replace(name)
	return strings.TrimSpace(name)
}

var (
	activeParamRE = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*B\s*[-_]?\s*A\s*(\d+(?:\.\d+)?)\s*B`)
	paramRE       = regexp.MustCompile(`(?i)(?:^|[^0-9a-z.])(\d+(?:\.\d+)?)\s*B(?:$|[^a-z])`)
	contextRE     = regexp.MustCompile(`(?i)(?:^|[^0-9a-z.])(\d+(?:\.\d+)?)\s*([km])(?:$|[^a-z0-9])`)
	quantRE       = regexp.MustCompile(`(?i)(?:^|[-_.])(IQ[2-5]_[A-Z0-9]+|Q[0-9]+(?:_[A-Z0-9]+)*|F16|BF16|F32|FP16|FP32)(?:$|[-_.])`)
)

func parseParams(text string) (float64, float64) {
	if m := activeParamRE.FindStringSubmatch(text); len(m) == 3 {
		total, _ := strconv.ParseFloat(m[1], 64)
		active, _ := strconv.ParseFloat(m[2], 64)
		return total, active
	}
	best := 0.0
	for _, m := range paramRE.FindAllStringSubmatch(text, -1) {
		v, err := strconv.ParseFloat(m[1], 64)
		if err == nil && v > best && v >= 0.1 && v <= 1000 {
			best = v
		}
	}
	return best, 0
}

func parseContext(text string) int {
	best := 0
	for _, m := range contextRE.FindAllStringSubmatch(text, -1) {
		v, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			continue
		}
		if strings.EqualFold(m[2], "M") {
			v *= 1024
		}
		n := int(v * 1024)
		if n > best && n >= 1024 && n <= 2*1024*1024 {
			best = n
		}
	}
	return best
}

func quantFromName(name string) string {
	if m := quantRE.FindStringSubmatch(name); len(m) == 2 {
		return strings.ToUpper(m[1])
	}
	return ""
}

func cleanTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if strings.HasPrefix(t, "base_model:") || strings.HasPrefix(t, "license:") || strings.HasPrefix(t, "region:") || strings.HasPrefix(t, "arxiv:") {
			continue
		}
		out = append(out, t)
	}
	return out
}

func licenseFromTags(tags []string) string {
	for _, t := range tags {
		if strings.HasPrefix(t, "license:") {
			return strings.TrimPrefix(t, "license:")
		}
	}
	return ""
}

func deriveCategories(pipeline string, tags []string, text string, params float64, languages []string) []string {
	lower := make([]string, 0, len(tags)+1)
	lower = append(lower, strings.ToLower(pipeline))
	for _, t := range tags {
		lower = append(lower, strings.ToLower(t))
	}
	has := func(terms ...string) bool {
		for _, term := range terms {
			for _, t := range lower {
				if t == term || strings.Contains(t, term) {
					return true
				}
			}
		}
		return false
	}
	mentions := func(terms ...string) bool {
		s := strings.ToLower(text)
		for _, term := range terms {
			if strings.Contains(s, term) {
				return true
			}
		}
		return false
	}

	var out []string
	add := func(id string) {
		for _, c := range out {
			if c == id {
				return
			}
		}
		out = append(out, id)
	}
	if has("text-to-speech", "automatic-speech-recognition", "audio-to-audio", "text-to-audio", "whisper", "tts", "speech", "audio") {
		add(CategoryAudio)
	}
	if has("sentence-similarity", "feature-extraction", "embedding") {
		add(CategoryEmbeddings)
	}
	if has("image-text-to-text", "image-classification", "object-detection", "vision", "multimodal", "llava") {
		add(CategoryVision)
	}
	if has("code", "coding") || mentions("coder", "starcoder", "devstral") {
		add(CategoryCoding)
	}
	if has("reasoning", "r1") || mentions("reasoning", "deepseek-r1", "think") {
		add(CategoryReasoning)
	}
	if has("math", "mathematics") || mentions("math", "gsm8k") {
		add(CategoryMathematics)
	}
	if has("multilingual", "polyglot") || len(languages) > 1 {
		add(CategoryMultilingual)
	}
	if params > 0 && params <= 4 {
		add(CategoryLightweight)
	}
	if has("text-generation", "conversational", "instruct", "chat") || (len(out) == 0 && params > 0) {
		add(CategoryGeneralChat)
	}
	return out
}
