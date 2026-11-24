// Package match implements the hard memory filter and nearest-without-
// exceeding recommendation rule.
package match

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"alocal/internal/catalog"
	"alocal/internal/hardware"
	"alocal/internal/scoring"
)

// Options controls recommendation behavior.
type Options struct {
	Task         string
	Context      int
	Runtime      string
	Top          int
	SafetyMargin float64
	// AllowContextCap permits models whose max context is below the requested
	// context; KV estimates are then capped and flagged. Normal recommendations
	// require the requested context to be supported.
	AllowContextCap bool
}

// Recommendation is one model with its reasoning and estimates.
type Recommendation struct {
	Model      catalog.Model      `json:"model"`
	Need       scoring.ModelNeed  `json:"need"`
	Throughput scoring.Throughput `json:"throughput"`
	Fit        bool               `json:"fit"`
	ScoreDelta float64            `json:"score_headroom"`
	Reason     string             `json:"reason"`
}

// Result is the full matching output.
type Result struct {
	Status            string                    `json:"status"`
	Message           string                    `json:"message"`
	Hardware          scoring.HardwareBreakdown `json:"hardware"`
	EffectiveMemoryGB float64                   `json:"effective_memory_gb"`
	MemoryLimitGB     float64                   `json:"memory_limit_gb"`
	SafetyMargin      float64                   `json:"safety_margin"`
	Recommendations   []Recommendation          `json:"recommendations"`
	Safer             []Recommendation          `json:"safer,omitempty"`
	Stretch           []Recommendation          `json:"stretch,omitempty"`
	Suggestions       []string                  `json:"suggestions,omitempty"`
}

type candidate struct {
	model catalog.Model
	need  scoring.ModelNeed
	fit   bool
}

// Recommend runs the matching engine.
func Recommend(p *hardware.Profile, cat *catalog.Catalog, opts Options) Result {
	if opts.Top <= 0 {
		opts.Top = 5
	}
	if opts.SafetyMargin <= 0 {
		opts.SafetyMargin = 0.15
	}
	opts.SafetyMargin = scoring.Clamp(opts.SafetyMargin, 0, 0.5)
	if opts.Context <= 0 {
		opts.Context = 8192
	}
	opts.Task = strings.ToLower(strings.TrimSpace(opts.Task))
	opts.Runtime = catalog.NormalizeRuntime(opts.Runtime)

	res := Result{SafetyMargin: opts.SafetyMargin}
	res.Hardware = scoring.ScoreHardware(p)
	effective, kind, _ := scoring.EffectiveMemory(p)
	limit := effective * (1 - opts.SafetyMargin)
	res.EffectiveMemoryGB = scoring.Round1(effective)
	res.MemoryLimitGB = scoring.Round1(limit)

	if cat == nil || len(cat.Models) == 0 {
		res.Status = "no_models"
		res.Message = "The model catalog is empty. Run `alocal catalog validate` to diagnose the data file."
		return res
	}
	if opts.Task != "" && !validTask(opts.Task) {
		res.Status = "invalid"
		res.Message = fmt.Sprintf("Unknown task %q. Use coding, chat, reasoning, or general.", opts.Task)
		return res
	}
	if opts.Runtime != "" && opts.Runtime != "ollama" && opts.Runtime != "llamacpp" && opts.Runtime != "lmstudio" {
		res.Status = "invalid"
		res.Message = fmt.Sprintf("Unknown runtime %q. Use ollama, llamacpp, or lmstudio.", opts.Runtime)
		return res
	}

	var candidates []candidate
	excludedByTask := false
	excludedByContext := false
	for _, m := range cat.Models {
		if opts.Task != "" && !m.HasTask(opts.Task) {
			excludedByTask = true
			continue
		}
		if opts.Runtime != "" && m.RuntimeTag(opts.Runtime) == "" {
			continue
		}
		if !opts.AllowContextCap && opts.Context > m.MaxContext {
			excludedByContext = true
			continue
		}
		need := m.Need(opts.Context)
		candidates = append(candidates, candidate{model: m, need: need, fit: need.RequiredGB <= limit})
	}
	if len(candidates) == 0 {
		res.Status = "no_models"
		switch {
		case excludedByTask && excludedByContext:
			res.Message = fmt.Sprintf("No models tagged %q support a %d-token context.", opts.Task, opts.Context)
		case excludedByTask:
			res.Message = fmt.Sprintf("No models in the catalog are tagged %q.", opts.Task)
		case excludedByContext:
			res.Message = fmt.Sprintf("No models support a %d-token context. Try a smaller --context.", opts.Context)
		default:
			res.Message = "No models match the requested runtime/context filters."
		}
		return res
	}

	var ideal, overBudget, noFit []candidate
	for _, c := range candidates {
		switch {
		case !c.fit:
			noFit = append(noFit, c)
		case c.need.Score <= res.Hardware.Score+0.05:
			ideal = append(ideal, c)
		default:
			overBudget = append(overBudget, c)
		}
	}
	sortCandidates(ideal, opts.Task)
	sort.SliceStable(overBudget, func(i, j int) bool {
		if overBudget[i].need.Score != overBudget[j].need.Score {
			return overBudget[i].need.Score < overBudget[j].need.Score
		}
		return overBudget[i].need.RequiredGB < overBudget[j].need.RequiredGB
	})
	sort.SliceStable(noFit, func(i, j int) bool {
		if noFit[i].need.RequiredGB != noFit[j].need.RequiredGB {
			return noFit[i].need.RequiredGB < noFit[j].need.RequiredGB
		}
		return noFit[i].need.Score < noFit[j].need.Score
	})

	toRec := func(c candidate, fit bool) Recommendation {
		return Recommendation{
			Model: c.model, Need: c.need,
			Throughput: scoring.EstimateThroughput(p, c.model.Spec()),
			Fit:        fit,
			ScoreDelta: scoring.Round1(res.Hardware.Score - c.need.Score),
			Reason:     reasonFor(c, fit, res.Hardware.Score, limit, kind, opts),
		}
	}

	switch {
	case len(ideal) > 0:
		res.Status = "ok"
		n := opts.Top
		if n > len(ideal) {
			n = len(ideal)
		}
		for _, c := range ideal[:n] {
			res.Recommendations = append(res.Recommendations, toRec(c, true))
		}
		best := ideal[0]
		shown := map[string]bool{}
		for _, r := range res.Recommendations {
			shown[r.Model.ID] = true
		}
		for _, c := range ideal {
			// Safer means meaningfully smaller: either it needs less memory
			// than the top pick or it scores clearly lower while fitting.
			if shown[c.model.ID] {
				continue
			}
			if c.need.RequiredGB >= best.need.RequiredGB && c.need.Score > best.need.Score-1.0 {
				continue
			}
			res.Safer = append(res.Safer, toRec(c, true))
			if len(res.Safer) == 3 {
				break
			}
		}
		if len(overBudget) > 0 {
			n := 2
			if n > len(overBudget) {
				n = len(overBudget)
			}
			for _, c := range overBudget[:n] {
				res.Stretch = append(res.Stretch, toRec(c, true))
			}
		} else if len(noFit) > 0 {
			n := 1
			if n > len(noFit) {
				n = len(noFit)
			}
			for _, c := range noFit[:n] {
				res.Stretch = append(res.Stretch, toRec(c, false))
			}
		}
		res.Message = fmt.Sprintf("Best fit: %s (%s). It needs %.1f GB; your safe limit is %.1f GB.",
			best.model.DisplayName, best.model.Quant, best.need.RequiredGB, limit)

	case len(overBudget) > 0:
		res.Status = "over_budget"
		n := 2
		if n > len(overBudget) {
			n = len(overBudget)
		}
		for _, c := range overBudget[:n] {
			res.Stretch = append(res.Stretch, toRec(c, true))
		}
		res.Message = fmt.Sprintf("No catalog model both fits memory and scores at or below your hardware score (%.1f). The smallest that fits is %s, but expect it to be slow.",
			res.Hardware.Score, overBudget[0].model.DisplayName)
		res.Suggestions = append(res.Suggestions,
			fmt.Sprintf("Choose a smaller quantization or a lighter model; the smallest runnable option needs %.1f GB.", overBudget[0].need.RequiredGB),
			"Close other GPU/CPU-heavy applications to free memory before running it.")

	default:
		res.Status = "no_fit"
		smallest := noFit[0]
		rec := toRec(smallest, false)
		res.Stretch = append(res.Stretch, rec)
		res.Message = fmt.Sprintf("Nothing fits: the smallest catalog model needs %.1f GB, but your safe memory limit is %.1f GB.",
			smallest.need.RequiredGB, limit)
		res.Suggestions = append(res.Suggestions, hardwareSuggestions(smallest, effective, kind, opts)...)
	}

	return res
}

func validTask(t string) bool {
	switch t {
	case "chat", "coding", "reasoning", "general":
		return true
	}
	return false
}

func sortCandidates(cs []candidate, task string) {
	sort.SliceStable(cs, func(i, j int) bool {
		a, b := cs[i], cs[j]
		if math.Abs(a.need.Score-b.need.Score) > 0.05 {
			return a.need.Score > b.need.Score
		}
		if a.model.ParamsB != b.model.ParamsB {
			return a.model.ParamsB > b.model.ParamsB
		}
		if a.model.ReleaseYear != b.model.ReleaseYear {
			return a.model.ReleaseYear > b.model.ReleaseYear
		}
		if task != "" {
			if a.model.HasTask(task) != b.model.HasTask(task) {
				return a.model.HasTask(task)
			}
		}
		return a.model.ID < b.model.ID
	})
}

func reasonFor(c candidate, fit bool, hw, limit float64, memKind string, opts Options) string {
	headroom := hw - c.need.Score
	base := fmt.Sprintf("Requires %.1f GB of %s (safe limit %.1f GB); model score %.1f vs hardware %.1f (%.1f headroom).",
		c.need.RequiredGB, memKind, limit, c.need.Score, hw, headroom)
	if !fit {
		return strings.TrimSpace(base + fmt.Sprintf(" Does not fit: short by %.1f GB.", c.need.RequiredGB-limit))
	}
	if c.need.Score > hw {
		return strings.TrimSpace(base + " Fits memory but exceeds the hardware score; expect slower tokens/sec.")
	}
	if headroom <= 5 {
		return strings.TrimSpace(base + " This is a stretch pick that uses most of the machine.")
	}
	if c.model.ActiveParamsB > 0 && c.model.ActiveParamsB < c.model.ParamsB*0.8 {
		return strings.TrimSpace(base + fmt.Sprintf(" MoE model: %d/%dB active parameters keep compute demand low.", int(math.Round(c.model.ActiveParamsB)), int(math.Round(c.model.ParamsB))))
	}
	if opts.Task != "" {
		return strings.TrimSpace(base + fmt.Sprintf(" Tagged %s and the closest score at or below the hardware score.", opts.Task))
	}
	return strings.TrimSpace(base + " Closest score at or below the hardware score without exceeding the memory limit.")
}

func hardwareSuggestions(c candidate, effective float64, kind string, opts Options) []string {
	var out []string
	required := c.need.RequiredGB
	neededEffective := required / (1 - opts.SafetyMargin)
	short := neededEffective - effective
	out = append(out, fmt.Sprintf("You need about %.1f GB more usable model memory (%.1f GB total after the %.0f%% safety reserve).",
		math.Max(short, 0), neededEffective, opts.SafetyMargin*100))
	switch kind {
	case "discrete VRAM":
		out = append(out, fmt.Sprintf("A GPU with at least %.0f GB of VRAM would fit %s.", math.Ceil(neededEffective), c.model.DisplayName))
		out = append(out, "Multi-GPU sums VRAM in this tool, but tensor-split scaling is not linear.")
	case "unified memory":
		ram := neededEffective / scoring.UnifiedRAMUsable
		out = append(out, fmt.Sprintf("An Apple Silicon machine with at least %.0f GB unified RAM would fit %s (70%% usable).", math.Ceil(ram), c.model.DisplayName))
	default:
		out = append(out, fmt.Sprintf("At least %.0f GB of free system RAM would fit %s.", math.Ceil(neededEffective), c.model.DisplayName))
		out = append(out, "A discrete GPU would improve both fit and tokens/sec far more than adding RAM alone.")
	}
	out = append(out, fmt.Sprintf("Or pick a smaller model/quantization; the closest is %s at %.1f GB.", c.model.ID, required))
	return out
}
