package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"alocal/internal/scoring"
)

func newScoreCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "score",
		Short: "Print only the Hardware Score and tier",
		Long:  "Print a script-friendly hardware score. Use --verbose for the full component breakdown, or --json for structured output.",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := app.loadProfile(cmd.Context())
			if err != nil {
				return err
			}
			hw := scoring.ScoreHardware(p)
			if app.JSON {
				return app.writeJSON(hw)
			}
			u := app.ui()
			if !app.Verbose {
				if u.Color {
					fmt.Fprintf(u.Out, "%s\n", u.Score(hw.Score, hw.Tier))
				} else {
					fmt.Fprintf(u.Out, "%.1f\t%s\n", hw.Score, hw.Tier)
				}
				return nil
			}
			u.Title("Alocal hardware score")
			fmt.Fprintf(u.Out, "  %s\n", u.Score(hw.Score, hw.Tier))
			fmt.Fprintf(u.Out, "  Effective model memory: %s (%s)\n", fmtGB(hw.EffectiveMemoryGB), hw.MemoryKind)
			fmt.Fprintf(u.Out, "  Bandwidth: %.0f GB/s · compute base: %.0f/100\n\n", hw.BandwidthGBps, hw.ComputeBase)
			u.Table([]string{"COMPONENT", "WEIGHT", "RAW /100", "CONTRIBUTED"}, componentRows(hw))
			for _, c := range hw.Components {
				fmt.Fprintf(u.Out, "  %s %s\n", u.Label(c.Name+":"), u.Dim(c.Detail))
			}
			fmt.Fprintf(u.Out, "\n  %s\n", u.Dim("Score = 0.50·memory + 0.25·bandwidth + 0.20·compute + 0.05·disk"))
			for _, n := range hw.Notes {
				fmt.Fprintf(u.Out, "  %s %s\n", u.Warn("note:"), u.Dim(n))
			}
			return nil
		},
	}
}

func componentRows(hw scoring.HardwareBreakdown) [][]string {
	rows := make([][]string, 0, len(hw.Components))
	for _, c := range hw.Components {
		rows = append(rows, []string{
			c.Name, fmtPercent(c.Weight), fmtScore(c.RawScore), fmtScore(c.Contribution),
		})
	}
	return rows
}
