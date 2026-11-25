package brand

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestLogoIsGeometricHexagonWithA(t *testing.T) {
	plain := Logo(false, 0)
	lines := strings.Split(plain, "\n")
	if len(lines) != 7 {
		t.Fatalf("logo rows = %d, want 7: %q", len(lines), plain)
	}
	if !strings.Contains(plain, "╱▔▔▔▔▔╲") || !strings.Contains(plain, "╲▁▁▁▁▁▁╱") {
		t.Fatalf("hexagon outline is missing: %q", plain)
	}
	if !strings.Contains(plain, "╱────╲") {
		t.Fatalf("A crossbar is missing: %q", plain)
	}
	if strings.Contains(plain, "╲╱") || strings.Contains(plain, "╱╲╱") {
		t.Fatalf("hexagon corners must not collapse into V shapes: %q", plain)
	}
	if strings.Count(plain, Dash) != 1 {
		t.Fatalf("dash count = %d, want 1: %q", strings.Count(plain, Dash), plain)
	}
	if !strings.Contains(plain, "Alocal "+Dash) {
		t.Fatalf("wordmark is missing: %q", plain)
	}
	if strings.ContainsAny(plain, "🤖🐙🚀") {
		t.Fatalf("logo must not use emoji: %q", plain)
	}
}

func TestLogoColorHasNoBackground(t *testing.T) {
	colored := Logo(true, 0)
	for _, code := range []string{Purple, Lavender, White} {
		if !strings.Contains(colored, "\x1b[38;5;"+code+"m") {
			t.Fatalf("colour %s missing: %q", code, colored)
		}
	}
	if strings.Contains(colored, "\x1b[48;") || strings.Contains(colored, "\x1b[4m") {
		t.Fatalf("logo must leave the terminal background transparent: %q", colored)
	}
}

func TestCompactRespectsWidth(t *testing.T) {
	for _, width := range []int{4, 12, 40} {
		got := Compact(false, width)
		if lipgloss.Width(got) > width {
			t.Fatalf("Compact(%d) width = %d: %q", width, lipgloss.Width(got), got)
		}
	}
	got := Compact(false, 0)
	if !strings.Contains(got, "⬡") || !strings.Contains(got, "Alocal") {
		t.Fatalf("compact lockup missing the mark or wordmark: %q", got)
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
