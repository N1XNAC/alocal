package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"alocal/internal/local"
	"alocal/internal/update"
)

func newUpdateCmd(app *App) *cobra.Command {
	var (
		repoFlag  string
		baseFlag  string
		tagFlag   string
		checkOnly bool
		dryRun    bool
		yes       bool
	)

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update Alocal to the latest release",
		Long: `Download and install the latest Alocal release binary.

The update source is remembered by install.sh / install.ps1, so most users can
simply run:

  alocal update
  alocal update --check
  alocal update --tag v0.2.0 --yes

The binary is verified against checksums.txt before it replaces the running
executable. On Windows the old binary is kept as alocal.exe.old.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := local.LoadUpdateSource()
			if err != nil {
				return coded(1, "load update source: %v", err)
			}
			repo := firstNonEmpty(repoFlag, os.Getenv("ALOCAL_REPO"), src.Repo)
			baseURL := firstNonEmpty(baseFlag, os.Getenv("ALOCAL_BASE_URL"), src.BaseURL)
			if repo == "" && baseURL == "" {
				return coded(2, "no update source configured\nSet ALOCAL_REPO=owner/repo, run `alocal update --repo owner/repo`, or reinstall with the one-line installer")
			}

			exe, err := os.Executable()
			if err != nil {
				return coded(1, "locate current executable: %v", err)
			}
			cfg := update.Config{
				Repo:           repo,
				BaseURL:        baseURL,
				Version:        tagFlag,
				CurrentVersion: Version,
				Executable:     exe,
				APIURL:         os.Getenv("GITHUB_API_URL"),
				ReleaseBase:    os.Getenv("ALOCAL_RELEASE_URL"),
			}
			rel, err := cfg.Resolve(cmd.Context())
			if err != nil {
				return coded(1, "resolve update: %v", err)
			}
			asset := cfg.AssetName()
			current := update.IsCurrent(Version, rel.Tag)

			if checkOnly {
				if app.JSON {
					return app.writeJSON(map[string]any{
						"current": Version, "latest": rel.Tag, "up_to_date": current, "asset": asset,
					})
				}
				fmt.Fprintf(app.Out, "Current: %s\nLatest:  %s\nAsset:   %s\n", Version, rel.Tag, asset)
				if current {
					fmt.Fprintln(app.Out, "Already up to date.")
				} else {
					fmt.Fprintln(app.Out, "Run `alocal update` to install the new version.")
				}
				return nil
			}

			if current && !cmd.Flags().Changed("tag") && !dryRun {
				if app.JSON {
					return app.writeJSON(map[string]any{"current": Version, "latest": rel.Tag, "up_to_date": true, "updated": false})
				}
				fmt.Fprintf(app.Out, "Already up to date (%s).\n", Version)
				return nil
			}

			downloadURL := strings.TrimRight(rel.BaseURL, "/") + "/" + asset
			if dryRun {
				if app.JSON {
					return app.writeJSON(map[string]any{"from": Version, "to": rel.Tag, "asset": asset, "url": downloadURL, "path": exe, "ran": false})
				}
				fmt.Fprintf(app.Out, "Would download %s\nand replace %s\n", downloadURL, exe)
				return nil
			}

			if app.JSON && !yes {
				return coded(2, "refusing to prompt while --json is set; pass --yes")
			}
			if !yes {
				fmt.Fprintf(app.Out, "Update Alocal %s → %s\n  asset: %s\n  path:  %s\nContinue? [y/N] ", Version, rel.Tag, asset, exe)
				answer, _ := bufio.NewReader(app.In).ReadString('\n')
				answer = strings.ToLower(strings.TrimSpace(answer))
				if answer != "y" && answer != "yes" {
					fmt.Fprintln(app.Out, "Cancelled.")
					return nil
				}
			}

			res, err := cfg.Apply(cmd.Context(), rel)
			if err != nil {
				return coded(1, "update failed: %v", err)
			}
			if app.JSON {
				return app.writeJSON(res)
			}
			fmt.Fprintf(app.Out, "Updated Alocal %s → %s\n", res.From, res.To)
			fmt.Fprintf(app.Out, "  Installed: %s\n", res.Path)
			if res.BackupPath != "" {
				fmt.Fprintf(app.Out, "  Previous binary kept at %s (delete it after restarting).\n", res.BackupPath)
			}
			fmt.Fprintln(app.Out, "Restart your terminal and run `alocal --version` to confirm.")
			return nil
		},
	}

	cmd.Flags().StringVar(&repoFlag, "repo", "", "GitHub repository owner/name for releases")
	cmd.Flags().StringVar(&baseFlag, "base-url", "", "release download base URL (mirror)")
	cmd.Flags().StringVar(&tagFlag, "tag", "", "specific release tag (default: latest)")
	cmd.Flags().BoolVar(&checkOnly, "check", false, "only report the available version")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what would be downloaded")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}
