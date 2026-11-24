package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"alocal/internal/catalog"
	"alocal/internal/local"
)

func newModelsCmd(app *App) *cobra.Command {
	var f catalog.Filter
	var listLocal bool
	cmd := &cobra.Command{
		Use:   "models",
		Short: "List the embedded model catalog",
		RunE: func(cmd *cobra.Command, args []string) error {
			if listLocal {
				store, err := local.LoadModels()
				if err != nil {
					return coded(1, "load local models: %v", err)
				}
				if app.JSON {
					return app.writeJSON(map[string]any{"count": len(store.Models), "models": store.Models})
				}
				u := app.ui()
				u.Title("Downloaded GGUF models")
				if len(store.Models) == 0 {
					fmt.Fprintln(u.Out, u.Warn("  No GGUFs downloaded. Try: alocal pull bartowski/Qwen2.5-7B-Instruct-GGUF"))
					return nil
				}
				rows := make([][]string, 0, len(store.Models))
				for _, m := range store.Models {
					rows = append(rows, []string{short(m.ID, 52), orDash(m.Quant), fmtGB(float64(m.SizeBytes) / 1e9), m.Path})
				}
				u.Table([]string{"ID", "QUANT", "SIZE", "PATH"}, rows)
				return nil
			}
			cat, err := app.loadCatalog()
			if err != nil {
				return err
			}
			if f.Runtime != "" {
				norm := catalog.NormalizeRuntime(f.Runtime)
				if norm != "ollama" && norm != "llamacpp" && norm != "lmstudio" {
					return coded(2, "unknown runtime %q", f.Runtime)
				}
				f.Runtime = norm
			}
			models := cat.Filter(f)
			if app.JSON {
				return app.writeJSON(map[string]any{
					"count": len(models), "score_context": cat.ScoreContext, "models": models,
				})
			}
			u := app.ui()
			u.Title("Alocal model catalog")
			fmt.Fprintf(u.Out, "  %s\n\n", u.Dim(fmt.Sprintf("%d of %d entries · scores at %d-token context unless capped", len(models), len(cat.Models), cat.ScoreContext)))
			if len(models) == 0 {
				fmt.Fprintln(u.Out, u.Warn("  No models matched those filters."))
				return nil
			}
			rows := make([][]string, 0, len(models))
			for _, m := range models {
				rows = append(rows, []string{
					short(m.ID, 42),
					fmtParams(m.ParamsB, m.ActiveParamsB),
					m.Quant,
					fmtGB(m.FileSizeGB),
					fmt.Sprintf("%dK", m.MaxContext/1024),
					m.TaskSummary(),
					fmtScore(m.ModelScore),
				})
			}
			u.Table([]string{"ID", "PARAMS", "QUANT", "SIZE", "CTX", "TASKS", "SCORE"}, rows)
			return nil
		},
	}
	cmd.Flags().BoolVar(&listLocal, "local", false, "list downloaded GGUF files instead of the embedded catalog")
	cmd.Flags().StringVar(&f.Task, "task", "", "filter by task: coding, chat, reasoning, general")
	cmd.Flags().StringVar(&f.Family, "family", "", "filter by family or display-name substring")
	cmd.Flags().StringVar(&f.Quant, "quant", "", "filter by quantization, e.g. Q4_K_M")
	cmd.Flags().StringVar(&f.Runtime, "runtime", "", "filter to models with an ollama, llamacpp, or lmstudio reference")
	cmd.Flags().IntVar(&f.MinContext, "min-context", 0, "minimum max context length")
	cmd.Flags().Float64Var(&f.MaxFileGB, "max-size", 0, "maximum model file size in GB")
	return cmd
}
