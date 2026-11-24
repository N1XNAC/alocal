package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"alocal/internal/match"
)

func newRecommendCmd(app *App) *cobra.Command {
	var (
		task    string
		context int
		runtime string
		top     int
		safety  float64
	)
	cmd := &cobra.Command{
		Use:   "recommend",
		Short: "Recommend the best-fitting local model",
		Long:  "Apply a hard memory-fit filter, then choose the highest Model Score that does not exceed the Hardware Score.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if context < 512 {
				return coded(2, "--context must be at least 512 tokens")
			}
			if top < 1 {
				return coded(2, "--top must be at least 1")
			}
			if safety < 0 || safety > 0.5 {
				return coded(2, "--safety-margin must be between 0 and 0.5")
			}
			p, err := app.loadProfile(cmd.Context())
			if err != nil {
				return err
			}
			cat, err := app.loadCatalog()
			if err != nil {
				return err
			}
			res := match.Recommend(p, cat, match.Options{
				Task: task, Context: context, Runtime: runtime,
				Top: top, SafetyMargin: safety,
			})
			if app.JSON {
				if err := app.writeJSON(res); err != nil {
					return err
				}
			} else {
				printRecommendations(app.ui(), res, task, context, runtime)
			}
			switch res.Status {
			case "ok":
				return nil
			case "invalid":
				return coded(2, "%s", res.Message)
			case "no_models":
				return coded(2, "%s", res.Message)
			default:
				return silent(3)
			}
		},
	}
	cmd.Flags().StringVar(&task, "task", "", "prefer a task: coding, chat, reasoning, general")
	cmd.Flags().IntVar(&context, "context", 8192, "target context length in tokens")
	cmd.Flags().StringVar(&runtime, "runtime", "", "filter to a runtime: ollama, llamacpp, lmstudio")
	cmd.Flags().IntVar(&top, "top", 5, "number of recommendations to show")
	cmd.Flags().Float64Var(&safety, "safety-margin", 0.15, "fraction of effective memory reserved as safety margin (0 uses the 15% default)")
	return cmd
}

func printRecommendations(u *UI, res match.Result, task string, context int, runtime string) {
	u.Title("Alocal recommendation")
	hw := res.Hardware
	fmt.Fprintf(u.Out, "  Hardware: %s  ·  %s usable %s\n", u.Score(hw.Score, hw.Tier), fmtGB(res.EffectiveMemoryGB), hw.MemoryKind)
	fmt.Fprintf(u.Out, "  Safe memory limit: %s (%.0f%% reserve)\n", fmtGB(res.MemoryLimitGB), res.SafetyMargin*100)
	filters := []string{fmt.Sprintf("context %d", context)}
	if task != "" {
		filters = append(filters, "task "+task)
	}
	if runtime != "" {
		filters = append(filters, "runtime "+runtime)
	}
	fmt.Fprintf(u.Out, "  Filters: %s\n\n", strings.Join(filters, " · "))

	if res.Status != "ok" {
		fmt.Fprintf(u.Out, "  %s\n\n", u.Warn(res.Message))
	} else {
		fmt.Fprintf(u.Out, "  %s\n\n", u.Good(res.Message))
	}

	if len(res.Recommendations) > 0 {
		u.Section("Recommended")
		printRecTable(u, res.Recommendations)
		for _, r := range res.Recommendations {
			fmt.Fprintf(u.Out, "  %s %s\n", u.Label(r.Model.DisplayName+":"), u.Dim(r.Reason))
		}
		fmt.Fprintln(u.Out)
		top := res.Recommendations[0]
		fmt.Fprintf(u.Out, "  %s\n", u.Dim("Tok/s estimate: "+top.Throughput.Formula))
		fmt.Fprintf(u.Out, "  %s\n", u.Dim("Tokens/sec is a memory-bandwidth roofline estimate (±35%), not a benchmark. Real speed depends on backend, batch size, context, and offload."))
	}

	if len(res.Safer) > 0 {
		fmt.Fprintln(u.Out)
		u.Section("Safer alternatives (smaller)")
		printRecTable(u, res.Safer)
	}
	if len(res.Stretch) > 0 {
		fmt.Fprintln(u.Out)
		u.Section("Stretch alternatives (larger; may be slow or not fit)")
		printRecTable(u, res.Stretch)
		for _, r := range res.Stretch {
			fmt.Fprintf(u.Out, "  %s %s\n", u.Label(r.Model.DisplayName+":"), u.Dim(r.Reason))
		}
	}
	if len(res.Suggestions) > 0 {
		fmt.Fprintln(u.Out)
		u.Section("What would help")
		for _, s := range res.Suggestions {
			u.Bullet(s)
		}
	}
	if res.Status != "ok" {
		fmt.Fprintln(u.Out)
		fmt.Fprintf(u.Out, "  %s\n", u.Dim("No ideal match (exit code 3). Use --json for scripts."))
	}
}

func printRecTable(u *UI, recs []match.Recommendation) {
	rows := make([][]string, 0, len(recs))
	for _, r := range recs {
		fit := "yes"
		if !r.Fit {
			fit = "no"
		}
		scoreVsHW := fmt.Sprintf("%s / %.1f", fmtScore(r.Need.Score), r.Need.Score+r.ScoreDelta)
		rows = append(rows, []string{
			short(r.Model.DisplayName, 30),
			r.Model.Quant,
			fmtParams(r.Model.ParamsB, r.Model.ActiveParamsB),
			scoreVsHW,
			fmtGB(r.Need.RequiredGB),
			fit,
			fmtTok(r.Throughput),
		})
	}
	u.Table([]string{"MODEL", "QUANT", "PARAMS", "MODEL/HW", "MEMORY", "FITS", "TOK/S EST"}, rows)
}
