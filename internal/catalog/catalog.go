// Package catalog loads, validates, and scores the embedded model catalog.
package catalog

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"alocal/data"
	"alocal/internal/scoring"
)

// Arch holds the fields needed for a KV-cache estimate.
type Arch struct {
	Layers  int `json:"layers"`
	KVHeads int `json:"kv_heads"`
	HeadDim int `json:"head_dim"`
}

// Runtimes holds per-runtime identifiers.
type Runtimes struct {
	Ollama   string `json:"ollama,omitempty"`
	LlamaCpp string `json:"llamacpp,omitempty"`
	LMStudio string `json:"lmstudio,omitempty"`
}

// Model is one model + quantization entry.
type Model struct {
	ID            string   `json:"id"`
	Family        string   `json:"family"`
	DisplayName   string   `json:"display_name"`
	ParamsB       float64  `json:"params_b"`
	ActiveParamsB float64  `json:"active_params_b"`
	Quant         string   `json:"quant"`
	FileSizeGB    float64  `json:"file_size_gb"`
	MaxContext    int      `json:"max_context"`
	Tasks         []string `json:"tasks"`
	License       string   `json:"license"`
	ReleaseYear   int      `json:"release_year"`
	Arch          Arch     `json:"arch"`
	Runtimes      Runtimes `json:"runtimes"`
	ModelScore    float64  `json:"model_score"`
	ScoreContext  int      `json:"score_context,omitempty"`
}

// Catalog is the JSON document shape of data/models.json.
type Catalog struct {
	Version      int     `json:"version"`
	ScoreContext int     `json:"score_context"`
	Models       []Model `json:"models"`
}

// Load parses the embedded catalog.
func Load() (*Catalog, error) {
	return Parse(data.ModelsJSON)
}

// Parse parses a catalog document.
func Parse(b []byte) (*Catalog, error) {
	var c Catalog
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse model catalog: %w", err)
	}
	if c.ScoreContext <= 0 {
		c.ScoreContext = 8192
	}
	return &c, nil
}

// Spec converts a model to the scoring input.
func (m Model) Spec() scoring.ModelSpec {
	return scoring.ModelSpec{
		ID: m.ID, ParamsB: m.ParamsB, ActiveParamsB: m.ActiveParamsB,
		Quant: m.Quant, FileSizeGB: m.FileSizeGB, MaxContext: m.MaxContext,
		Layers: m.Arch.Layers, KVHeads: m.Arch.KVHeads, HeadDim: m.Arch.HeadDim,
	}
}

// Need computes memory and score at a requested context.
func (m Model) Need(context int) scoring.ModelNeed {
	return scoring.ComputeModelNeed(m.Spec(), context)
}

// ScoreContextFor returns the per-entry score context or the catalog default.
func (c *Catalog) ScoreContextFor(m Model) int {
	if m.ScoreContext > 0 {
		return m.ScoreContext
	}
	if c.ScoreContext > 0 {
		return c.ScoreContext
	}
	return 8192
}

// RecomputeScores recalculates every model_score at the catalog context.
func (c *Catalog) RecomputeScores() {
	for i := range c.Models {
		ctx := c.ScoreContextFor(c.Models[i])
		c.Models[i].ScoreContext = ctx
		c.Models[i].ModelScore = c.Models[i].Need(ctx).Score
	}
}

// Find returns a model by exact ID.
func (c *Catalog) Find(id string) (Model, bool) {
	for _, m := range c.Models {
		if m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}

// Sorted returns models ordered by score descending, then parameters descending.
func (c *Catalog) Sorted() []Model {
	out := append([]Model(nil), c.Models...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ModelScore != out[j].ModelScore {
			return out[i].ModelScore > out[j].ModelScore
		}
		if out[i].ParamsB != out[j].ParamsB {
			return out[i].ParamsB > out[j].ParamsB
		}
		return out[i].ID < out[j].ID
	})
	return out
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

var allowedTasks = map[string]bool{"chat": true, "coding": true, "reasoning": true, "general": true}

// Validate checks schema, references, and recomputes scores. It returns all
// problems rather than stopping at the first one.
func (c *Catalog) Validate() []error {
	var errs []error
	if c.Version <= 0 {
		errs = append(errs, fmt.Errorf("catalog.version must be positive"))
	}
	if c.ScoreContext <= 0 {
		errs = append(errs, fmt.Errorf("catalog.score_context must be positive"))
	}
	if len(c.Models) == 0 {
		errs = append(errs, fmt.Errorf("catalog contains no models"))
	}
	seen := map[string]bool{}
	for i, m := range c.Models {
		label := m.ID
		if label == "" {
			label = fmt.Sprintf("models[%d]", i)
		}
		add := func(format string, args ...any) {
			errs = append(errs, fmt.Errorf("%s: %s", label, fmt.Sprintf(format, args...)))
		}
		if m.ID == "" {
			add("id is required")
		} else if !idPattern.MatchString(m.ID) {
			add("id must be a lowercase slug")
		}
		if seen[m.ID] {
			add("duplicate id")
		}
		seen[m.ID] = true
		if strings.TrimSpace(m.Family) == "" {
			add("family is required")
		}
		if strings.TrimSpace(m.DisplayName) == "" {
			add("display_name is required")
		}
		if m.ParamsB <= 0 {
			add("params_b must be positive")
		}
		if m.ActiveParamsB <= 0 {
			add("active_params_b must be positive")
		}
		if m.ActiveParamsB > m.ParamsB*1.01 {
			add("active_params_b cannot exceed params_b")
		}
		bpw, ok := scoring.BitsPerWeight[m.Quant]
		if !ok {
			add("unknown quant %q (allowed: %s)", m.Quant, strings.Join(scoring.Quantizations, ", "))
		}
		if m.FileSizeGB <= 0 {
			add("file_size_gb must be positive")
		} else if ok {
			expected := m.ParamsB * bpw / 8
			if expected > 0 && (m.FileSizeGB < expected*0.45 || m.FileSizeGB > expected*2.0+0.2) {
				add("file_size_gb %.2f is inconsistent with %.2fB at %s (expected ~%.2f)", m.FileSizeGB, m.ParamsB, m.Quant, expected)
			}
		}
		if m.MaxContext <= 0 {
			add("max_context must be positive")
		}
		if len(m.Tasks) == 0 {
			add("at least one task tag is required")
		}
		taskSeen := map[string]bool{}
		for _, t := range m.Tasks {
			if !allowedTasks[t] {
				add("unknown task tag %q", t)
			}
			if taskSeen[t] {
				add("duplicate task tag %q", t)
			}
			taskSeen[t] = true
		}
		if strings.TrimSpace(m.License) == "" {
			add("license is required")
		}
		if m.ReleaseYear < 2000 || m.ReleaseYear > 2035 {
			add("release_year %d is out of range", m.ReleaseYear)
		}
		if m.Arch.Layers <= 0 || m.Arch.KVHeads <= 0 || m.Arch.HeadDim <= 0 {
			add("arch.layers, arch.kv_heads, and arch.head_dim must be positive")
		}
		if m.Runtimes.Ollama == "" {
			add("runtimes.ollama tag is required")
		}
		need := m.Need(c.ScoreContextFor(m))
		if need.Score < 0 || need.Score > 100 {
			add("computed model_score %.1f is outside 0-100", need.Score)
		}
		if m.ModelScore <= 0 {
			add("model_score is missing; run the catalog generator")
		} else if diff := m.ModelScore - need.Score; diff > 0.16 || diff < -0.16 {
			add("model_score %.1f does not match recomputed %.1f at %d context", m.ModelScore, need.Score, need.Context)
		}
	}
	return errs
}

// Filter narrows the catalog for the models command.
type Filter struct {
	Task       string
	Family     string
	Quant      string
	Runtime    string
	MinContext int
	MaxFileGB  float64
}

// Filter returns matching models in stable catalog order.
func (c *Catalog) Filter(f Filter) []Model {
	var out []Model
	for _, m := range c.Models {
		if f.Task != "" && !m.HasTask(f.Task) {
			continue
		}
		if f.Family != "" && !strings.Contains(strings.ToLower(m.Family+" "+m.DisplayName), strings.ToLower(f.Family)) {
			continue
		}
		if f.Quant != "" && !strings.EqualFold(m.Quant, f.Quant) {
			continue
		}
		if f.Runtime != "" && m.RuntimeTag(f.Runtime) == "" {
			continue
		}
		if f.MinContext > 0 && m.MaxContext < f.MinContext {
			continue
		}
		if f.MaxFileGB > 0 && m.FileSizeGB > f.MaxFileGB {
			continue
		}
		out = append(out, m)
	}
	return out
}

// HasTask reports whether the model has a task tag.
func (m Model) HasTask(task string) bool {
	for _, t := range m.Tasks {
		if strings.EqualFold(t, task) {
			return true
		}
	}
	return false
}

// NormalizeRuntime canonicalizes runtime names.
func NormalizeRuntime(r string) string {
	r = strings.ToLower(strings.TrimSpace(r))
	switch r {
	case "llama.cpp", "llama_cpp", "llamacpp", "gguf":
		return "llamacpp"
	case "lm-studio", "lm_studio", "lmstudio":
		return "lmstudio"
	case "ollama":
		return "ollama"
	}
	return r
}

// RuntimeTag returns the reference for a runtime, or "" if absent.
func (m Model) RuntimeTag(runtime string) string {
	switch NormalizeRuntime(runtime) {
	case "ollama":
		return m.Runtimes.Ollama
	case "llamacpp":
		if m.Runtimes.LlamaCpp == "" {
			return ""
		}
		return "hf.co/" + strings.TrimPrefix(m.Runtimes.LlamaCpp, "hf.co/")
	case "lmstudio":
		return m.Runtimes.LMStudio
	default:
		return ""
	}
}

// TaskSummary is a compact tag list.
func (m Model) TaskSummary() string { return strings.Join(m.Tasks, ", ") }
