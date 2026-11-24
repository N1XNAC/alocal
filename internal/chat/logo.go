package chat

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// A neofetch/fastfetch-style wordmark drawn with terminal block glyphs.
// No images and no emoji.
const logoLines = `  ▄▀▀ █▀▀ █▀▀ ▀█▀ █▀█ █▀▀
  █▀  ██▀ █▀▀  █  █ █ ██▀
   ▀▀▀ ▀▀▀ ▀▀▀  ▀  ▀▀▀ ▀▀▀`

var (
	logoA = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	logoB = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("141"))
	logoC = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("75"))
)

func renderLogo(width int) string {
	lines := strings.Split(logoLines, "\n")
	if len(lines) != 3 {
		return logoLines
	}
	var b strings.Builder
	b.WriteString(logoA.Render(lines[0]))
	b.WriteString("\n")
	b.WriteString(logoB.Render(lines[1]))
	b.WriteString("\n")
	b.WriteString(logoC.Render(lines[2]))
	return b.String()
}
