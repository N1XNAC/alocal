package chat

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// A fastfetch/neofetch-style wordmark: a grey block-glyph "A" followed by
// "local" in bright white. No images and no emoji.
const logoLines = `  ▄▀▀▄
 █▀  ▀█
 █▀▀▀▀█
 ▀    ▀`

var (
	logoBlock = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	logoText  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
)

func renderLogo(width int) string {
	lines := strings.Split(logoLines, "\n")
	if len(lines) < 2 {
		return logoLines
	}
	var b strings.Builder
	for i, line := range lines {
		b.WriteString(logoBlock.Render(line))
		if i == 1 {
			b.WriteString("  ")
			b.WriteString(logoText.Render("local"))
		}
		if i < len(lines)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// logoPlain returns the unstyled wordmark for tests and non-TTY output.
func logoPlain() string { return logoLines }
