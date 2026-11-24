package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"alocal/internal/catalog"
	"alocal/internal/chat"
	"alocal/internal/hardware"
	"alocal/internal/match"
	"alocal/internal/ollama"
)

func newChatCmd(app *App) *cobra.Command {
	var (
		model      string
		ollamaURL  string
		system     string
		autoSelect bool
	)

	cmd := &cobra.Command{
		Use:   "chat [model]",
		Short: "Start an interactive chat with a local LLM",
		Long: `Start a terminal chat interface powered by Ollama.

If no model is specified and --auto is set, Alocal recommends the best model
for your hardware. Otherwise, provide the Ollama model name directly.

Examples:
  alocal chat llama3.2:3b
  alocal chat --auto
  alocal chat qwen2.5:7b --system "You are a helpful coding assistant"
  alocal chat --ollama-url http://remote-host:11434 llama3.2:3b`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			selectedModel := model
			selectedID := ""
			if len(args) > 0 {
				selectedModel = args[0]
			}

			if selectedModel == "" && autoSelect {
				tag, id, err := autoRecommendModel(app, cmd)
				if err != nil {
					return err
				}
				selectedModel = tag
				selectedID = id
			}

			if selectedModel == "" {
				// Try to pick the first available Ollama model
				client := ollama.New(ollamaURL)
				ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
				defer cancel()
				models, err := client.ListModels(ctx)
				if err != nil {
					return coded(1, "No model specified and cannot reach Ollama at %s.\n\nUsage:\n  alocal chat <model-name>\n  alocal chat --auto\n\nInstall Ollama: https://ollama.com", client.BaseURL)
				}
				if len(models) == 0 {
					return coded(1, "No models found in Ollama. Pull one first:\n  ollama pull llama3.2:3b\nThen:\n  alocal chat llama3.2:3b")
				}
				selectedModel = models[0].Name
				fmt.Fprintf(app.Err, "No model specified, using: %s\n", selectedModel)
			}

			// Verify Ollama is reachable
			client := ollama.New(ollamaURL)
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			if err := client.Ping(ctx); err != nil {
				return coded(1, "Cannot reach Ollama at %s.\nMake sure Ollama is running: ollama serve\nInstall: https://ollama.com", client.BaseURL)
			}

			// A recommended tag is only useful if it has actually been pulled.
			listCtx, listCancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer listCancel()
			if installed, listErr := client.ListModels(listCtx); listErr == nil && !hasOllamaModel(installed, selectedModel) {
				if selectedID != "" && len(installed) > 0 {
					// Bare `alocal` / `--auto` should work immediately: use a pulled model
					// and tell the user how to get the actual recommendation.
					fmt.Fprintf(app.Err, "Recommended %s is not pulled; using %s instead.\nTo use the recommendation: alocal pull %s\n",
						selectedModel, installed[0].Name, selectedID)
					selectedModel = installed[0].Name
				} else {
					pull := "ollama pull " + selectedModel
					if selectedID != "" {
						pull = "alocal pull " + selectedID + "  # or: " + pull
					}
					return coded(1, "Model %q is not installed in Ollama yet.\nPull it first:\n  %s\nThen rerun: alocal chat %s", selectedModel, pull, selectedModel)
				}
			}

			cfg := chat.Config{
				ModelName:    selectedModel,
				OllamaURL:    ollamaURL,
				SystemPrompt: system,
			}
			if err := chat.Run(cfg); err != nil {
				return coded(1, "chat error: %v", err)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&model, "model", "m", "", "Ollama model name (e.g. llama3.2:3b)")
	cmd.Flags().StringVar(&ollamaURL, "ollama-url", "", "Ollama API URL (default: $OLLAMA_HOST or http://localhost:11434)")
	cmd.Flags().StringVarP(&system, "system", "s", "", "system prompt for the conversation")
	cmd.Flags().BoolVar(&autoSelect, "auto", false, "auto-select the best model for your hardware")

	return cmd
}

func autoRecommendModel(app *App, cmd *cobra.Command) (string, string, error) {
	cat, err := app.loadCatalog()
	if err != nil {
		return "", "", err
	}

	var d hardware.Detector
	if app.Mock != "" {
		d = hardware.MockDetector{Path: app.Mock}
	} else {
		d = hardware.SystemDetector{Options: hardware.Options{Verbose: false}}
	}
	p, err := d.Detect(cmd.Context())
	if err != nil {
		return "", "", coded(1, "hardware scan failed: %v", err)
	}

	res := match.Recommend(p, cat, match.Options{Top: 1, AllowContextCap: true})
	var best *catalog.Model
	if len(res.Recommendations) > 0 {
		best = &res.Recommendations[0].Model
	} else if len(res.Stretch) > 0 {
		best = &res.Stretch[0].Model
	} else if len(res.Safer) > 0 {
		best = &res.Safer[0].Model
	}

	if best == nil {
		return "", "", coded(1, "No suitable model found for your hardware. Try specifying one:\n  alocal chat llama3.2:1b")
	}

	tag := best.RuntimeTag("ollama")
	if tag == "" {
		return "", "", coded(1, "Best model %s has no Ollama tag", best.ID)
	}

	fmt.Fprintf(app.Err, "Auto-selected: %s (%s)\n", best.DisplayName, tag)
	return tag, best.ID, nil
}

func hasOllamaModel(models []ollama.ModelInfo, tag string) bool {
	for _, m := range models {
		if strings.EqualFold(m.Name, tag) || strings.EqualFold(m.Model, tag) {
			return true
		}
		if strings.EqualFold(strings.TrimSuffix(m.Name, ":latest"), tag) ||
			strings.EqualFold(m.Name, strings.TrimSuffix(tag, ":latest")) {
			return true
		}
	}
	return false
}
