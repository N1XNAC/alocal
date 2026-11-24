package cli

import (
	"bufio"
	"fmt"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

func newPullCmd(app *App) *cobra.Command {
	var yes bool
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "pull <model-id>",
		Short: "Pull a catalog model with Ollama",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return coded(2, "pull requires exactly one model id (try `alocal models`)")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cat, err := app.loadCatalog()
			if err != nil {
				return err
			}
			m, ok := cat.Find(args[0])
			if !ok {
				return coded(2, "unknown model %q%s", args[0], suggestionSuffix(cat, args[0]))
			}
			tag := m.RuntimeTag("ollama")
			if tag == "" {
				return coded(2, "model %s has no Ollama tag", m.ID)
			}
			command := fmt.Sprintf("ollama pull %s", tag)
			if dryRun {
				if app.JSON {
					return app.writeJSON(map[string]any{"runtime": "ollama", "tag": tag, "command": command, "ran": false})
				}
				fmt.Fprintf(app.Out, "%s\n", command)
				return nil
			}
			ollamaPath, lookErr := exec.LookPath("ollama")
			if lookErr != nil {
				return coded(1, "Ollama is not installed or not on PATH. Install it from https://ollama.com, or use the llama.cpp reference: %s", orDash(m.RuntimeTag("llamacpp")))
			}
			if app.JSON && !yes {
				return coded(2, "refusing to prompt while --json is set; pass --yes")
			}
			if !yes {
				fmt.Fprintf(app.Out, "Ollama found at %s\n", ollamaPath)
				fmt.Fprintf(app.Out, "About to run: %s\nPull this model? [y/N] ", command)
				reader := bufio.NewReader(app.In)
				answer, _ := reader.ReadString('\n')
				answer = strings.ToLower(strings.TrimSpace(answer))
				if answer != "y" && answer != "yes" {
					fmt.Fprintln(app.Out, "Cancelled.")
					return nil
				}
			}
			run := exec.Command(ollamaPath, "pull", tag)
			run.Stdin = app.In
			if app.JSON {
				run.Stdout = app.Err
				run.Stderr = app.Err
			} else {
				run.Stdout = app.Out
				run.Stderr = app.Err
			}
			if err := run.Run(); err != nil {
				return coded(1, "ollama pull failed: %v", err)
			}
			if app.JSON {
				return app.writeJSON(map[string]any{"runtime": "ollama", "tag": tag, "command": command, "ran": true})
			}
			fmt.Fprintf(app.Out, "\nPulled %s (%s).\n", m.DisplayName, tag)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the command without running it")
	return cmd
}
