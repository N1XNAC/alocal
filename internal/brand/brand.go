// Package brand renders Alocal's terminal identity. The mark is a geometric
// hexagon whose inner strokes form a capital A. It is drawn on a verified
// character grid so the outline stays connected at every terminal size, and it
// uses only ANSI foreground colours so the terminal background stays visible.
package brand

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Palette tokens. One colour means one thing across the CLI and TUI.
// Values are xterm-256 indices chosen to stay legible on dark terminals.
const (
	Purple   = "141" // primary accent: actions, selection, the mark
	Lavender = "183" // secondary accent: headings, active labels
	Pale     = "225" // highlight: the top facet of the mark
	White    = "15"  // primary text on the terminal's own background
	Muted    = "245" // secondary text
	Faint    = "242" // tertiary text, metadata
	Rule     = "238" // hairlines and quiet separators
	Success  = "114" // ready, verified, complete
	Warning  = "179" // caution, partial fit
	Danger   = "203" // errors, incompatible, destructive
)

// HexagonA is the primary mark: a flat-topped hexagon with a capital A
// inscribed in the same stroke weight. Every diagonal connects on the cell
// grid, so the outline never breaks into stray corner glyphs.
const HexagonA = `   ╱▔▔▔▔▔╲
  ╱  ╱╲   ╲
 ╱  ╱  ╲   ╲
│  ╱────╲   │
╲ ╱      ╲ ╱
 ╲        ╱
  ╲▁▁▁▁▁▁╱`

// Dash is the cursor rendered after the wordmark. It is intentionally one
// cell wide so it reads as a caret, not a block.
const Dash = "▏"

const (
	markWidth = 13
	markGap   = 3
)

// markColors is a quiet top-to-bottom gradient: pale at the crown, lavender
// through the shoulders, violet at the base.
var markColors = []string{Pale, Pale, Lavender, Lavender, Purple, Purple, "99"}

// Logo renders the hexagon-A and the wordmark side by side, vertically
// centred. It falls back to a compact lockup when the terminal is narrow.
func Logo(color bool, width int) string {
	mark := strings.Split(HexagonA, "\n")
	word := wordmarkLines(color)

	if width > 0 && width < markWidth+markGap+lipgloss.Width("Alocal "+Dash) {
		return Compact(color, width)
	}

	lines := make([]string, 0, len(mark))
	for i, row := range mark {
		left := padRight(row, markWidth)
		right := ""
		if i < len(word) {
			right = word[i]
		}
		if color && strings.TrimSpace(left) != "" {
			left = paint(left, markColors[i])
		}
		lines = append(lines, left+strings.Repeat(" ", markGap)+right)
	}
	return strings.Join(lines, "\n")
}

// wordmarkLines is the right-hand lockup: a bold white wordmark, a violet
// cursor and one quiet descriptor. Empty rows keep it optically centred.
func wordmarkLines(color bool) []string {
	rows := make([]string, 7)
	if color {
		rows[2] = paintBold("Alocal", White) + " " + paint(Dash, Purple)
		rows[4] = paint("local inference", Muted)
		return rows
	}
	rows[2] = "Alocal " + Dash
	rows[4] = "local inference"
	return rows
}

// Compact is the one-line lockup used by narrow terminals and list screens.
func Compact(color bool, width int) string {
	text := "⬡ Alocal " + Dash
	if width > 0 && lipgloss.Width(text) > width {
		text = "Alocal " + Dash
	}
	if width > 0 && lipgloss.Width(text) > width {
		text = truncateRunes(text, width)
	}
	if !color {
		return text
	}
	switch {
	case strings.HasPrefix(text, "⬡"):
		return paint("⬡", Purple) + " " +
			paint("Alocal", White) + " " + paint(Dash, Purple)
	case strings.HasPrefix(text, "Alocal"):
		rest := strings.TrimPrefix(text, "Alocal")
		if before, _, ok := strings.Cut(rest, Dash); ok {
			return paint("Alocal", White) + before + paint(Dash, Purple)
		}
		return paint("Alocal", White) + rest
	default:
		return paint(text, Purple)
	}
}

// Banner renders the full lockup followed by a version label and truthful
// status lines supplied by the caller.
func Banner(version string, status []string, color bool, width int) string {
	var b strings.Builder
	b.WriteString(Logo(color, width))
	b.WriteString("\n")
	label := "Alocal"
	if version != "" {
		label += " " + version
	}
	if color {
		label = paint(label, Lavender)
	}
	b.WriteString(label)
	for _, line := range status {
		b.WriteString("\n  ")
		b.WriteString(line)
	}
	return b.String()
}

// paint emits SGR foreground codes directly. lipgloss strips colour when it
// thinks output is not a TTY, but the mark must survive being composed into a
// Bubble Tea frame buffer first.
func paint(s, color string) string {
	if s == "" {
		return ""
	}
	return "\x1b[38;5;" + color + "m" + s + "\x1b[0m"
}

// paintBold keeps the exact foreground sequence used by paint, with bold
// enabled first. The single reset at the end returns every attribute.
func paintBold(s, color string) string {
	if s == "" {
		return ""
	}
	return "\x1b[1m" + paint(s, color)
}

func padRight(s string, width int) string {
	n := lipgloss.Width(s)
	if n >= width {
		return s
	}
	return s + strings.Repeat(" ", width-n)
}

func truncateRunes(s string, width int) string {
	if width <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}
