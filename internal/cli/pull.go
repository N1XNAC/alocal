package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"alocal/internal/hf"
	"alocal/internal/local"
)

// newDownloadCmd is the discoverable spelling of pull.
func newDownloadCmd(app *App) *cobra.Command {
	cmd := newPullCmd(app)
	cmd.Use = "download [model-id|owner/repo]"
	cmd.Short = "Download a GGUF from Hugging Face (alias of pull)"
	cmd.Aliases = []string{"get"}
	cmd.Long = strings.ReplaceAll(cmd.Long, "alocal pull", "alocal download")
	return cmd
}

func newPullCmd(app *App) *cobra.Command {
	var (
		yes     bool
		dryRun  bool
		repoArg string
		fileArg string
		quant   string
	)

	cmd := &cobra.Command{
		Use:   "pull [model-id|owner/repo]",
		Short: "Download a GGUF from Hugging Face",
		Long: `Download a GGUF model from Hugging Face into Alocal's local model store.

Accepts an embedded catalog id, a Hugging Face repository, or hf:owner/repo.
Use --quant to choose a quantization (default: Q4_K_M for direct repos, or the
catalog entry's quant for catalog ids). Files are verified as GGUF before they
are registered, and a SHA-256-sized .part file is used during download.

Examples:
  alocal pull qwen2.5-7b-instruct-q4_k_m
  alocal pull bartowski/Qwen2.5-7B-Instruct-GGUF --quant Q5_K_M
  alocal pull hf:bartowski/Qwen2.5-7B-Instruct-GGUF --file Qwen2.5-7B-Instruct-Q4_K_M.gguf
  alocal pull bartowski/Qwen2.5-7B-Instruct-GGUF --dry-run`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			arg := ""
			if len(args) > 0 {
				arg = args[0]
			}
			if arg != "" && repoArg != "" {
				return coded(2, "pass either a positional model/repo or --repo, not both")
			}
			if repoArg != "" {
				arg = repoArg
			}

			repo := ""
			wantQuant := quant
			catalogFamily := ""
			if arg != "" {
				if strings.HasPrefix(arg, "hf:") {
					repo = hf.NormalizeRepo(strings.TrimPrefix(arg, "hf:"))
				} else if cat, err := app.loadCatalog(); err == nil {
					if m, ok := cat.Find(arg); ok {
						repo = hf.NormalizeRepo(m.RuntimeTag("llamacpp"))
						catalogFamily = m.Family
						if wantQuant == "" {
							wantQuant = m.Quant
						}
					}
				}
				if repo == "" {
					repo = hf.NormalizeRepo(arg)
				}
			}
			if repo == "" || !strings.Contains(repo, "/") {
				return coded(2, "provide a catalog id or Hugging Face repo (owner/name)\nExample: alocal pull bartowski/Qwen2.5-7B-Instruct-GGUF")
			}

			client := hf.New()
			file := fileArg
			if file == "" {
				files, err := client.GGUFFiles(cmd.Context(), repo)
				if err != nil {
					return coded(1, "list GGUF files in %s: %v", repo, err)
				}
				if len(files) == 0 {
					return coded(2, "no GGUF files found in %s", repo)
				}
				file = hf.PickGGUF(files, wantQuant)
			}

			dest := filepath.Join(local.ModelsDir(), strings.ReplaceAll(repo, "/", "_"), filepath.Base(file))
			verb := cmd.Name()
			if called := cmd.CalledAs(); called != "" {
				verb = called
			}
			command := fmt.Sprintf("alocal %s %s --quant %s --file %s", verb, repo, orDash(wantQuant), file)
			if dryRun {
				if app.JSON {
					return app.writeJSON(map[string]any{"runtime": "llamacpp", "repo": repo, "file": file, "path": dest, "command": command, "ran": false})
				}
				fmt.Fprintln(app.Out, command)
				return nil
			}

			if _, err := os.Stat(dest); err == nil && !yes {
				fmt.Fprintf(app.Out, "Already downloaded: %s\n", dest)
				return registerLocalModel(repo, file, dest, catalogFamily, app.JSON, app)
			}

			if app.JSON && !yes {
				return coded(2, "refusing to prompt while --json is set; pass --yes")
			}
			if !yes {
				fmt.Fprintf(app.Out, "Download from Hugging Face: %s\n  file: %s\n  to:   %s\nContinue? [y/N] ", repo, file, dest)
				answer, _ := bufio.NewReader(app.In).ReadString('\n')
				answer = strings.ToLower(strings.TrimSpace(answer))
				if answer != "y" && answer != "yes" {
					fmt.Fprintln(app.Out, "Cancelled.")
					return nil
				}
			}

			lastPct := -1
			err := client.Download(cmd.Context(), repo, file, dest, func(done, total int64) {
				if app.JSON || total <= 0 {
					return
				}
				pct := int(float64(done) / float64(total) * 100)
				if pct/10 != lastPct {
					lastPct = pct / 10
					fmt.Fprintf(app.Err, "  downloading %s: %d%% (%.1f / %.1f GB)\n", file, pct,
						float64(done)/1e9, float64(total)/1e9)
				}
			})
			if err != nil {
				return coded(1, "download failed: %v", err)
			}
			return registerLocalModel(repo, file, dest, catalogFamily, app.JSON, app)
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the resolved download without downloading")
	cmd.Flags().StringVar(&repoArg, "repo", "", "Hugging Face repository (owner/name)")
	cmd.Flags().StringVar(&fileArg, "file", "", "exact GGUF filename in the repository")
	cmd.Flags().StringVar(&quant, "quant", "", "preferred quantization, e.g. Q4_K_M or Q5_K_M")
	return cmd
}

func quantFromFilename(file string) string {
	upper := strings.ToUpper(file)
	for _, q := range []string{"Q3_K_M", "Q4_K_M", "Q5_K_M", "Q6_K", "Q8_0", "FP16", "F16", "BF16"} {
		if strings.Contains(upper, q) {
			return q
		}
	}
	return ""
}

func registerLocalModel(repo, file, dest, family string, asJSON bool, app *App) error {
	info, err := os.Stat(dest)
	if err != nil {
		return coded(1, "inspect downloaded model: %v", err)
	}
	model := local.Model{
		ID:        repo + "/" + file,
		Repo:      repo,
		File:      file,
		Path:      dest,
		SizeBytes: info.Size(),
		Quant:     quantFromFilename(file),
		Family:    family,
	}
	store, err := local.LoadModels()
	if err != nil {
		return coded(1, "load local model registry: %v", err)
	}
	if err := store.Add(model); err != nil {
		return coded(1, "save local model registry: %v", err)
	}
	if asJSON {
		return app.writeJSON(map[string]any{
			"runtime": "llamacpp", "id": model.ID, "repo": repo, "file": file,
			"path": dest, "size_bytes": model.SizeBytes, "quant": model.Quant, "ran": true,
		})
	}
	fmt.Fprintf(app.Out, "\nDownloaded %s (%.1f GB).\n  Path: %s\n  Start: alocal chat %s\n",
		model.ID, float64(model.SizeBytes)/1e9, dest, model.ID)
	return nil
}
