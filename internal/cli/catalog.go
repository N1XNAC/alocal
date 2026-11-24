package cli

import (
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"alocal/data"
	"alocal/internal/catalog"
)

func newCatalogCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "catalog",
		Short: "Inspect or validate the model catalog",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newCatalogValidateCmd(app))
	return cmd
}

func newCatalogValidateCmd(app *App) *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate catalog schema and recompute model scores",
		RunE: func(cmd *cobra.Command, args []string) error {
			var raw []byte
			var err error
			source := "embedded data/models.json"
			if file != "" {
				source = file
				raw, err = os.ReadFile(file)
				if err != nil {
					return coded(1, "read catalog %q: %v", file, err)
				}
			} else {
				raw = data.ModelsJSON
			}
			cat, err := catalog.Parse(raw)
			if err != nil {
				return coded(2, "%v", err)
			}
			errs := cat.Validate()
			families := map[string]bool{}
			var scores []float64
			for _, m := range cat.Models {
				families[m.Family] = true
				scores = append(scores, m.Need(cat.ScoreContextFor(m)).Score)
			}
			sort.Float64s(scores)
			minScore, maxScore := 0.0, 0.0
			if len(scores) > 0 {
				minScore, maxScore = scores[0], scores[len(scores)-1]
			}
			if app.JSON {
				msgs := make([]string, 0, len(errs))
				for _, e := range errs {
					msgs = append(msgs, e.Error())
				}
				if err := app.writeJSON(map[string]any{
					"ok": len(errs) == 0, "source": source, "models": len(cat.Models),
					"families": len(families), "score_context": cat.ScoreContext,
					"min_score": minScore, "max_score": maxScore, "errors": msgs,
				}); err != nil {
					return err
				}
			} else {
				u := app.ui()
				u.Title("Alocal catalog validate")
				fmt.Fprintf(u.Out, "  Source: %s\n", source)
				fmt.Fprintf(u.Out, "  Models: %d in %d families · score context %d\n", len(cat.Models), len(families), cat.ScoreContext)
				if len(cat.Models) > 0 {
					fmt.Fprintf(u.Out, "  Recomputed scores: min %.1f, max %.1f\n", minScore, maxScore)
				}
				fmt.Fprintln(u.Out)
				if len(errs) == 0 {
					fmt.Fprintf(u.Out, "  %s\n", u.Good("OK — schema valid and every stored model_score matches the formula."))
					if len(cat.Models) < 40 {
						fmt.Fprintf(u.Out, "  %s\n", u.Warn(fmt.Sprintf("Warning: only %d models; the shipped catalog targets at least 40.", len(cat.Models))))
					}
					return nil
				}
				fmt.Fprintf(u.Out, "  %s\n", u.Bad(fmt.Sprintf("%d validation error(s):", len(errs))))
				for _, e := range errs {
					u.Bullet(e.Error())
				}
			}
			if len(errs) > 0 {
				return silent(2)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "validate a catalog JSON file instead of the embedded catalog")
	return cmd
}
