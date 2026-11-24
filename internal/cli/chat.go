package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"alocal/internal/catalog"
	"alocal/internal/chat"
	"alocal/internal/hf"
	"alocal/internal/local"
	"alocal/internal/match"
)

func newChatCmd(app *App) *cobra.Command {
	var (
		modelArg   string
		system     string
		autoSelect bool
	)

	cmd := &cobra.Command{
		Use:   "chat [model]",
		Short: "Start an interactive chat with a local GGUF model",
		Long: `Start a terminal chat interface powered by Alocal's bundled llama.cpp runtime.

Models are GGUF files downloaded from Hugging Face. No Ollama installation or
background server is needed: Alocal starts a private local llama-server for you.
Press Ctrl+X inside the chat for model switching, downloads, MCP and settings.

Examples:
  alocal                                  # open chat, auto-select a downloaded model
  alocal pull bartowski/Qwen2.5-7B-Instruct-GGUF
  alocal chat --auto                      # best model for this machine
  alocal chat --auto --mock-hardware tests/fixtures/rtx4090.json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			settings, err := local.LoadSettings()
			if err != nil {
				return coded(1, "load settings: %v", err)
			}
			cfg := chat.Config{
				SystemPrompt:  system,
				MockHardware:  app.Mock,
				RuntimeBinary: settings.RuntimePath,
				Settings:      settings,
				NoColor:       !app.Color,
			}
			if app.Color {
				cfg.Status = app.startupStatus(cmd.Context())
			}
			if cfg.SystemPrompt == "" {
				cfg.SystemPrompt = settings.SystemPrompt
			}

			store, err := local.LoadModels()
			if err != nil {
				return coded(1, "load local models: %v", err)
			}

			requested := modelArg
			if len(args) > 0 {
				requested = args[0]
			}

			switch {
			case requested != "":
				lm, ok := store.Find(requested)
				if !ok {
					return coded(2, "GGUF %q is not downloaded%s\nDownload it first: alocal pull %s",
						requested, localSuggestion(store, requested), requested)
				}
				cfg.ModelPath, cfg.ModelLabel = lm.Path, lm.ID

			case autoSelect:
				best, bestErr := autoRecommend(app, cmd, settings.Context)
				if bestErr != nil {
					// A recommendation is a convenience, never a hard requirement.
					fmt.Fprintf(app.Err, "Auto-select unavailable: %v\n", bestErr)
				}
				if lm, ok := findLocalForCatalog(store, best); ok {
					cfg.ModelPath, cfg.ModelLabel = lm.Path, lm.ID
				} else if best != nil {
					repo := hf.NormalizeRepo(best.RuntimeTag("llamacpp"))
					if repo != "" {
						fmt.Fprintf(app.Err, "Recommended %s is not downloaded. Get it with: alocal pull %s (quant %s)\n",
							best.DisplayName, repo, best.Quant)
					}
					if lm, ok := lastLocalModel(store); ok {
						cfg.ModelPath, cfg.ModelLabel = lm.Path, lm.ID
						fmt.Fprintf(app.Err, "Using %s for now. Switch models with Ctrl+X.\n", lm.ID)
					}
				} else if lm, ok := lastLocalModel(store); ok {
					cfg.ModelPath, cfg.ModelLabel = lm.Path, lm.ID
				}

			default:
				if lm, ok := lastLocalModel(store); ok {
					cfg.ModelPath, cfg.ModelLabel = lm.Path, lm.ID
				}
			}

			if err := chat.Run(cfg); err != nil {
				return coded(1, "chat error: %v", err)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&modelArg, "model", "m", "", "ID of a downloaded local GGUF (see `alocal models --local` or Ctrl+X)")
	cmd.Flags().StringVarP(&system, "system", "s", "", "system prompt for the conversation")
	cmd.Flags().BoolVar(&autoSelect, "auto", false, "auto-select the best model for your hardware")

	return cmd
}

// autoRecommend recommends a catalog model using the existing hardware/match
// engine. No network access is required.
func autoRecommend(app *App, cmd *cobra.Command, contextSize int) (*catalog.Model, error) {
	cat, err := app.loadCatalog()
	if err != nil {
		return nil, err
	}
	p, err := app.loadProfile(cmd.Context())
	if err != nil {
		return nil, err
	}
	if contextSize < 512 {
		contextSize = 8192
	}
	res := match.Recommend(p, cat, match.Options{Top: 1, Context: contextSize, AllowContextCap: true})
	var best *catalog.Model
	if len(res.Recommendations) > 0 {
		best = &res.Recommendations[0].Model
	} else if len(res.Stretch) > 0 {
		best = &res.Stretch[0].Model
	} else if len(res.Safer) > 0 {
		best = &res.Safer[0].Model
	}
	if best == nil {
		return nil, fmt.Errorf("no suitable model found for this hardware")
	}
	if best.RuntimeTag("llamacpp") == "" {
		return nil, fmt.Errorf("model %s has no Hugging Face GGUF reference", best.ID)
	}
	fmt.Fprintf(app.Err, "Auto-selected: %s (%s, %s)\n", best.DisplayName,
		hf.NormalizeRepo(best.RuntimeTag("llamacpp")), best.Quant)
	return best, nil
}

// findLocalForCatalog prefers a downloaded GGUF from the recommended repo with
// the recommended quant; otherwise it accepts any GGUF from that repo.
func findLocalForCatalog(store *local.ModelStore, best *catalog.Model) (local.Model, bool) {
	if best == nil {
		return local.Model{}, false
	}
	repo := hf.NormalizeRepo(best.RuntimeTag("llamacpp"))
	if repo == "" {
		return local.Model{}, false
	}
	var fallback local.Model
	for _, m := range store.Models {
		if hf.NormalizeRepo(m.Repo) != repo {
			continue
		}
		if strings.EqualFold(m.Quant, best.Quant) {
			return m, true
		}
		if fallback.ID == "" {
			fallback = m
		}
	}
	return fallback, fallback.ID != ""
}

func lastLocalModel(store *local.ModelStore) (local.Model, bool) {
	if len(store.Models) == 0 {
		return local.Model{}, false
	}
	settings, err := local.LoadSettings()
	if err == nil && settings.LastModelID != "" {
		for _, m := range store.Models {
			if m.ID == settings.LastModelID {
				return m, true
			}
		}
	}
	return store.Models[len(store.Models)-1], true
}

func localSuggestion(store *local.ModelStore, id string) string {
	if len(store.Models) == 0 {
		return ""
	}
	var names []string
	for _, m := range store.Models {
		names = append(names, m.ID)
	}
	return "\nDownloaded GGUFs: " + strings.Join(names, ", ")
}
