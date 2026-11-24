package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"llmfit/internal/catalog"
	"llmfit/internal/match"
	"llmfit/internal/scoring"
)

type explainJSON struct {
	Model             catalog.Model             `json:"model"`
	Hardware          scoring.HardwareBreakdown `json:"hardware"`
	EffectiveMemoryGB float64                   `json:"effective_memory_gb"`
	MemoryLimitGB     float64                   `json:"memory_limit_gb"`
	Fit               bool                      `json:"fit"`
	Status            string                    `json:"status"`
	Need              scoring.ModelNeed         `json:"need"`
	Throughput        scoring.Throughput        `json:"throughput"`
	Reason            string                    `json:"reason"`
	Suggestions       []string                  `json:"suggestions,omitempty"`
}

func newExplainCmd(app *App) *cobra.Command {
	var context int
	var safety float64
	cmd := &cobra.Command{
		Use:   "explain <model-id>",
		Short: "Explain a model's fit and score on this machine",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return coded(2, "explain requires exactly one model id (try `llmfit models`)")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if context < 512 {
				return coded(2, "--context must be at least 512 tokens")
			}
			cat, err := app.loadCatalog()
			if err != nil {
				return err
			}
			m, ok := cat.Find(args[0])
			if !ok {
				return coded(2, "unknown model %q%s", args[0], suggestionSuffix(cat, args[0]))
			}
			p, err := app.loadProfile(cmd.Context())
			if err != nil {
				return err
			}
			one := &catalog.Catalog{Version: cat.Version, ScoreContext: cat.ScoreContext, Models: []catalog.Model{m}}
			res := match.Recommend(p, one, match.Options{Context: context, SafetyMargin: safety, Top: 1, AllowContextCap: true})
			var rec match.Recommendation
			switch {
			case len(res.Recommendations) > 0:
				rec = res.Recommendations[0]
			case len(res.Stretch) > 0:
				rec = res.Stretch[0]
			default:
				return coded(1, "could not evaluate %s", m.ID)
			}
			if app.JSON {
				return app.writeJSON(explainJSON{
					Model: m, Hardware: res.Hardware, EffectiveMemoryGB: res.EffectiveMemoryGB,
					MemoryLimitGB: res.MemoryLimitGB, Fit: rec.Fit, Status: res.Status,
					Need: rec.Need, Throughput: rec.Throughput, Reason: rec.Reason,
					Suggestions: res.Suggestions,
				})
			}
			printExplain(app.ui(), m, res, rec)
			return nil
		},
	}
	cmd.Flags().IntVar(&context, "context", 8192, "context length used for KV-cache estimate")
	cmd.Flags().Float64Var(&safety, "safety-margin", 0.15, "fraction of effective memory reserved as safety margin")
	return cmd
}

func suggestionSuffix(cat *catalog.Catalog, id string) string {
	lower := strings.ToLower(id)
	var matches []string
	for _, m := range cat.Models {
		if strings.Contains(strings.ToLower(m.ID), lower) || strings.Contains(strings.ToLower(m.Family), lower) {
			matches = append(matches, m.ID)
			if len(matches) == 5 {
				break
			}
		}
	}
	if len(matches) == 0 {
		return " (run `llmfit models` to list ids)"
	}
	return ". Did you mean: " + strings.Join(matches, ", ")
}

func printExplain(u *UI, m catalog.Model, res match.Result, rec match.Recommendation) {
	u.Title("llmfit explain")
	fmt.Fprintf(u.Out, "  %s  %s\n", u.Label(m.DisplayName), u.Dim(m.ID))
	fmt.Fprintf(u.Out, "  %s · %s · release %d\n\n", m.Family, m.License, m.ReleaseYear)

	u.Section("Model")
	u.KV("Parameters", fmtParams(m.ParamsB, m.ActiveParamsB))
	u.KV("Quantization", fmt.Sprintf("%s · file %.1f GB", m.Quant, m.FileSizeGB))
	u.KV("Max context", fmt.Sprintf("%d tokens", m.MaxContext))
	u.KV("Tasks", m.TaskSummary())
	u.KV("Ollama", orDash(m.RuntimeTag("ollama")))
	u.KV("llama.cpp", orDash(m.RuntimeTag("llamacpp")))
	u.KV("LM Studio", orDash(m.RuntimeTag("lmstudio")))

	fmt.Fprintln(u.Out)
	u.Section("Memory at requested context")
	need := rec.Need
	if need.ContextCapped {
		fmt.Fprintf(u.Out, "  %s\n", u.Warn(fmt.Sprintf("Context capped from %d to model max %d tokens.", need.RequestedContext, need.Context)))
	}
	u.KV("Context", fmt.Sprintf("%d tokens", need.Context))
	u.KV("Weights", fmtGB(m.FileSizeGB))
	if need.KVUnknown {
		u.KV("KV cache", u.Warn("unknown (arch fields missing)"))
	} else {
		u.KV("KV cache", fmtGB(need.KVGB))
	}
	overhead := need.RequiredGB - (m.FileSizeGB + need.KVGB)
	u.KV("Runtime overhead", fmt.Sprintf("%.1f GB (12%% + 0.35 GB)", overhead))
	u.KV("Required", fmtGB(need.RequiredGB))
	u.KV("Safe limit", fmt.Sprintf("%s (effective %s, %.0f%% reserve)", fmtGB(res.MemoryLimitGB), fmtGB(res.EffectiveMemoryGB), res.SafetyMargin*100))

	fmt.Fprintln(u.Out)
	u.Section("Scores")
	fmt.Fprintf(u.Out, "  Model score: %s  ·  Hardware score: %s\n", fmtScore(need.Score), u.Score(res.Hardware.Score, res.Hardware.Tier))
	fmt.Fprintf(u.Out, "  %s\n", u.Dim(fmt.Sprintf("Model = 0.70 × memory-need %.1f + 0.30 × demand %.1f = %.1f", need.MemoryScore, need.DemandScore, need.Score)))
	if rec.Fit {
		if res.Status == "ok" {
			fmt.Fprintf(u.Out, "  %s\n", u.Verdict(true, "Fits memory and scores at or below the hardware score."))
		} else {
			fmt.Fprintf(u.Out, "  %s\n", u.Verdict(false, "Fits memory but exceeds the hardware score; expect slower inference."))
		}
	} else {
		fmt.Fprintf(u.Out, "  %s\n", u.Verdict(false, fmt.Sprintf("Does not fit: short by %.1f GB.", need.RequiredGB-res.MemoryLimitGB)))
	}

	fmt.Fprintln(u.Out)
	u.Section("Estimated speed")
	u.KV("Tokens/sec", fmtTok(rec.Throughput)+" (estimate)")
	fmt.Fprintf(u.Out, "  %s\n", u.Dim(rec.Throughput.Formula))
	fmt.Fprintf(u.Out, "  %s\n\n", u.Dim("Not a benchmark. ±35% typical variance; context length and backend change results."))

	u.Section("Why")
	fmt.Fprintf(u.Out, "  %s\n", rec.Reason)
	if len(res.Suggestions) > 0 {
		fmt.Fprintln(u.Out)
		u.Section("What would help")
		for _, s := range res.Suggestions {
			u.Bullet(s)
		}
	}
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}
