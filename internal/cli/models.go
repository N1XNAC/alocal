package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"alocal/internal/catalog"
	"alocal/internal/catalogui"
	"alocal/internal/discovery"
	"alocal/internal/hf"
	"alocal/internal/local"
)

func newModelsCmd(app *App) *cobra.Command {
	var f catalog.Filter
	var listLocal bool
	cmd := &cobra.Command{
		Use:   "models",
		Short: "Browse the interactive model catalogue",
		Long: `Browse real GGUF repositories from Hugging Face.

In an interactive terminal, alocal models opens a searchable catalogue with
categories, pagination, detailed hardware compatibility and a download
workflow. Piped output, --json and explicit filters keep the original table
output so scripts are unaffected.

Categories include General Chat, Coding, Reasoning, Mathematics, Multilingual,
Vision, Embeddings, Audio, Lightweight Models and Downloaded Models.

Set ALOCAL_OFFLINE=1 to skip the optional live Hugging Face refresh.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if openInteractiveCatalog(cmd, app, listLocal) {
				return runInteractiveCatalog(cmd, app)
			}
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

// openInteractiveCatalog keeps scripts and explicit filters on the original
// non-interactive path. Only a bare `alocal models` in a TTY opens the UI.
func openInteractiveCatalog(cmd *cobra.Command, app *App, listLocal bool) bool {
	if app.JSON || listLocal || !catalogui.IsTerminal() {
		return false
	}
	for _, name := range []string{"task", "family", "quant", "runtime", "min-context", "max-size"} {
		if cmd.Flags().Changed(name) {
			return false
		}
	}
	return true
}

func runInteractiveCatalog(cmd *cobra.Command, app *App) error {
	profile, err := app.loadProfileFast(cmd.Context())
	if err != nil {
		fmt.Fprintf(app.Err, "Hardware detection unavailable: %v\n", err)
		profile = nil
	}
	embedded, err := discovery.Load()
	if err != nil {
		return coded(1, "load interactive catalogue: %v", err)
	}
	store, err := local.LoadModels()
	if err != nil {
		return coded(1, "load local models: %v", err)
	}
	entries := discovery.Merge(embedded.Models, discovery.LocalEntries(store))
	err = catalogui.Run(catalogui.Options{
		Entries:      entries,
		Profile:      profile,
		Client:       hf.New(),
		Version:      Version,
		Color:        app.Color,
		FetchDynamic: !strings.EqualFold(os.Getenv("ALOCAL_OFFLINE"), "1"),
	})
	if err != nil {
		return coded(1, "model catalogue: %v", err)
	}
	return nil
}
