package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/charmbracelet/lipgloss"

	"alocal/internal/scoring"
)

// UI renders either styled terminal output or plain TTY-friendly text.
type UI struct {
	Out   io.Writer
	Color bool
}

func (a *App) ui() *UI { return &UI{Out: a.Out, Color: a.Color && !a.JSON} }

var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	sectionStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("111"))
	labelStyle   = lipgloss.NewStyle().Bold(true)
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	goodStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	warnStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	badStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("203"))
)

func (u *UI) style(s lipgloss.Style, text string) string {
	if !u.Color {
		return text
	}
	return s.Render(text)
}

func (u *UI) Title(s string)        { fmt.Fprintln(u.Out, u.style(titleStyle, s)) }
func (u *UI) Section(s string)      { fmt.Fprintln(u.Out, u.style(sectionStyle, s)) }
func (u *UI) Label(s string) string { return u.style(labelStyle, s) }
func (u *UI) Dim(s string) string   { return u.style(dimStyle, s) }
func (u *UI) Good(s string) string  { return u.style(goodStyle, s) }
func (u *UI) Warn(s string) string  { return u.style(warnStyle, s) }
func (u *UI) Bad(s string) string   { return u.style(badStyle, s) }

func (u *UI) KV(key, value string) {
	fmt.Fprintf(u.Out, "  %s: %s\n", u.Label(key), value)
}

func (u *UI) Bullet(s string) {
	fmt.Fprintf(u.Out, "  • %s\n", s)
}

func (u *UI) Table(headers []string, rows [][]string) {
	tw := tabwriter.NewWriter(u.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	for _, row := range rows {
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	tw.Flush()
}

func (u *UI) Score(score float64, tier string) string {
	label := fmt.Sprintf("%.1f / 100  %s", score, tier)
	switch {
	case !u.Color:
		return label
	case score >= 70:
		return goodStyle.Render(label)
	case score >= 50:
		return warnStyle.Render(label)
	default:
		return badStyle.Render(label)
	}
}

func (u *UI) Verdict(ok bool, text string) string {
	if ok {
		return u.Good(text)
	}
	return u.Bad(text)
}

func fmtGB(v float64) string { return fmt.Sprintf("%.1f GB", v) }

func fmtScore(v float64) string { return fmt.Sprintf("%.1f", v) }

func fmtParams(params, active float64) string {
	if active > 0 && active < params*0.8 {
		return fmt.Sprintf("%.1fB (%.1fB active)", params, active)
	}
	return fmt.Sprintf("%.1fB", params)
}

func fmtTok(t scoring.Throughput) string {
	if t.TokensPerSec <= 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.1f (%.1f–%.1f)", t.TokensPerSec, t.Low, t.High)
}

func fmtPercent(w float64) string { return fmt.Sprintf("%.0f%%", w*100) }

func tierScoreLine(hw scoring.HardwareBreakdown) string {
	return fmt.Sprintf("%.1f / 100  %s", hw.Score, hw.Tier)
}
