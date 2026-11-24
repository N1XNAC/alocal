package brand

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestLogoHasHexagonPixelWordAndTrailingDash(t *testing.T) {
	plain := Logo(false, 0)
	lines := strings.Split(plain, "\n")
	if len(lines) != 8 {
		t.Fatalf("logo rows = %d, want 8: %q", len(lines), plain)
	}
	if !strings.Contains(plain, "▄▄███▄▄") || !strings.Contains(plain, "▀▀███▀▀") {
		t.Fatalf("hexagon is missing: %q", plain)
	}
	if strings.Count(plain, Dash) != 1 {
		t.Fatalf("dash count = %d, want 1: %q", strings.Count(plain, Dash), plain)
	}
	dashLine := -1
	for i, line := range lines {
		if strings.Contains(line, Dash) {
			dashLine = i
		}
	}
	if dashLine != 4 {
		t.Fatalf("dash row = %d, want 4 (beside the wordmark): %q", dashLine, plain)
	}
	if strings.ContainsAny(plain, "🤖🐙🚀") {
		t.Fatalf("logo must not use emoji: %q", plain)
	}
}

func TestLogoColorHasNoBackground(t *testing.T) {
	colored := Logo(true, 0)
	if !strings.Contains(colored, "\x1b[38;5;141m") {
		t.Fatalf("purple foreground missing: %q", colored)
	}
	if strings.Contains(colored, "\x1b[48;") || strings.Contains(colored, "\x1b[4m") {
		t.Fatalf("logo must leave the terminal background transparent: %q", colored)
	}
	if !strings.Contains(colored, "\x1b[38;5;225m") {
		t.Fatalf("pale lavender foreground missing: %q", colored)
	}
}

func TestCompactRespectsWidth(t *testing.T) {
	for _, width := range []int{4, 12, 40} {
		got := Compact(false, width)
		if lipgloss.Width(got) > width {
			t.Fatalf("Compact(%d) width = %d: %q", width, lipgloss.Width(got), got)
		}
	}
}

func TestBannerIncludesVersionAndStatus(t *testing.T) {
	got := Banner("1.2.3", []string{"Hardware  test machine", "Runtime   ready"}, false, 0)
	for _, want := range []string{"Alocal 1.2.3", "Hardware  test machine", "Runtime   ready"} {
		if !strings.Contains(got, want) {
			t.Fatalf("banner missing %q: %q", want, got)
		}
	}
}
