// Package brand renders the Alocal terminal wordmark. It uses only ANSI
// foreground colors, so the terminal's own background remains transparent.
package brand

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const (
	purple   = "141"
	white    = "15"
	lavender = "225"
)

// HexagonA is a pixel-style hexagon containing a geometric capital A. There
// is deliberately no dash or cursor inside the A; the dash belongs after the
// wordmark (see Dash).
const HexagonA = `   ▄▄███▄▄
  ██     ██
 █   ▄▄   █
 █  █  █  █
 █  █▀▀█  █
 █ █    █ █
  ██    ██
   ▀▀███▀▀`

// Dash is the small purple horizontal dash rendered after the wordmark.
const Dash = "─"

// wordHeight is the number of bitmap rows used by the pixel wordmark. It is
// vertically centered against the taller hexagon.
const wordHeight = 5

// pixelGlyphs maps a character to a 5-row bitmap used for the wordmark.
var pixelGlyphs = map[rune][]string{
	'A': {"  █  ", " █ █ ", " ███ ", " █ █ ", " █ █ "},
	'l': {"██   ", " █   ", " █   ", " █   ", " █   "},
	'o': {" ██  ", "█  █ ", "█  █ ", "█  █ ", " ██  "},
	'c': {" ██  ", "█    ", "█    ", "█    ", " ██  "},
	'a': {"     ", " ██  ", "█  █ ", "█ ██ ", " ███ "},
}

// Logo renders the hexagon and the pixel wordmark side by side. Color is
// disabled automatically when color is false; a background is never set.
func Logo(color bool, width int) string {
	hex := strings.Split(HexagonA, "\n")
	word := pixelWord("Alocal")
	hexWidth := lipgloss.Width(HexagonA)
	wordWidth := lipgloss.Width(strings.Join(word, "\n"))

	lines := make([]string, 0, len(hex))
	for i, hexLine := range hex {
		wordIndex := i - (len(hex)-wordHeight+1)/2
		var wordLine string
		if wordIndex >= 0 && wordIndex < len(word) {
			wordLine = word[wordIndex]
		}
		line := padRight(hexLine, hexWidth) + "  " + padRight(wordLine, wordWidth)
		if wordIndex == wordHeight/2 {
			line += " " + Dash
		}
		lines = append(lines, colorizeLogoLine(line, hexWidth+2, wordWidth, color))
	}
	out := strings.Join(lines, "\n")
	if width > 0 && lipgloss.Width(out) > width {
		return Compact(color, width)
	}
	return out
}

// Compact is a one-line fallback for narrow terminals.
func Compact(color bool, width int) string {
	text := "⬢ Alocal " + Dash
	if width > 0 && lipgloss.Width(text) > width {
		text = truncateRunes(text, width)
	}
	if !color {
		return text
	}
	i := strings.Index(text, "Alocal")
	if i < 0 {
		return paint(text, purple)
	}
	dashAt := strings.LastIndex(text, Dash)
	out := paint(text[:i], purple) + paint(text[i:i+len("Alocal")], white)
	if dashAt >= i+len("Alocal") {
		out += paint(text[i+len("Alocal"):dashAt], lavender) + paint(text[dashAt:], purple)
	} else {
		out += paint(text[i+len("Alocal"):], lavender)
	}
	return out
}

// Banner renders the logo followed by a version label and status lines.
// Callers supply truthful status text; brand never invents values.
func Banner(version string, status []string, color bool, width int) string {
	var b strings.Builder
	b.WriteString(Logo(color, width))
	b.WriteString("\n")
	label := "Alocal"
	if version != "" {
		label += " " + version
	}
	if color {
		label = paint(label, lavender)
	}
	b.WriteString(label)
	for _, line := range status {
		b.WriteString("\n  ")
		b.WriteString(line)
	}
	return b.String()
}

func colorizeLogoLine(line string, split, wordWidth int, color bool) string {
	if !color {
		return line
	}
	runes := []rune(line)
	if len(runes) <= split {
		return paint(string(runes), purple)
	}
	hexPart := string(runes[:split])
	rest := string(runes[split:])
	if idx := strings.Index(rest, Dash); idx >= 0 {
		wordPart := rest[:idx]
		dashPart := rest[idx:]
		out := paint(hexPart, purple)
		if strings.TrimSpace(wordPart) != "" {
			out += paint(wordPart, lavender)
		} else {
			out += wordPart
		}
		return out + paint(dashPart, purple)
	}
	if strings.TrimSpace(rest) == "" {
		return paint(hexPart, purple) + rest
	}
	return paint(hexPart, purple) + paint(rest, white)
}

// paint emits SGR foreground codes directly. lipgloss strips color when it
// thinks output is not a TTY, but the wordmark must remain ANSI-compatible
// even when composed into a Bubble Tea frame buffer first.
func paint(s, color string) string {
	if s == "" {
		return ""
	}
	return "\x1b[38;5;" + color + "m" + s + "\x1b[0m"
}

func padRight(s string, width int) string {
	n := lipgloss.Width(s)
	if n >= width {
		return s
	}
	return s + strings.Repeat(" ", width-n)
}

func pixelWord(s string) []string {
	rows := make([]string, wordHeight)
	for _, r := range s {
		glyph, ok := pixelGlyphs[r]
		if !ok {
			glyph = make([]string, wordHeight)
			for i := range glyph {
				glyph[i] = strings.Repeat(" ", 5)
			}
		}
		for i := range rows {
			if rows[i] != "" {
				rows[i] += " "
			}
			rows[i] += glyph[i]
		}
	}
	for i := range rows {
		rows[i] = strings.TrimRight(rows[i], " ")
	}
	return rows
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
