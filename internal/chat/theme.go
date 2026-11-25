package chat

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"alocal/internal/brand"
)

// The chat surface uses one palette and one set of type roles. Nothing sets a
// background: the terminal's own surface stays visible.
var (
	titleStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(brand.White))
	textStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color(brand.White))
	mutedStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color(brand.Muted))
	faintStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color(brand.Faint))
	accentStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(brand.Purple))
	lavenderStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(brand.Lavender))
	successStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color(brand.Success))
	warnStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color(brand.Warning))
	dangerStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(brand.Danger))
	keyStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color(brand.Lavender))
	ruleStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color(brand.Rule))
	userStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(brand.Lavender))
	assistantStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(brand.Purple))
	dimStyle       = mutedStyle
	helpStyle      = faintStyle
	errorStyle     = dangerStyle
	statusStyle    = mutedStyle
)

// st renders a style unless colour has been explicitly disabled.
func (m *Model) st(style lipgloss.Style, text string) string {
	if m.noColor {
		return text
	}
	return style.Render(text)
}

// hint renders a keyboard shortcut and its action: the key is lavender, the
// action stays quiet.
func (m *Model) hint(key, action string) string {
	return m.st(keyStyle, key) + m.st(faintStyle, " "+action)
}

func (m *Model) hintLine(pairs ...[2]string) string {
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		parts = append(parts, m.hint(p[0], p[1]))
	}
	return strings.Join(parts, m.st(faintStyle, "  ·  "))
}

func (m *Model) rule() string {
	w := m.width - 2
	if w < 24 {
		return ""
	}
	if w > 120 {
		w = 120
	}
	return m.st(ruleStyle, strings.Repeat("─", w))
}
