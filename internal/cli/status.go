package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"alocal/internal/hardware"
	"alocal/internal/runtime"
)

// startupStatus builds the concise lines printed below the wordmark. Every
// value comes from the detector or from the local filesystem; nothing is
// guessed for display. The budget keeps startup responsive on slow machines.
func (a *App) startupStatus(ctx context.Context) []string {
	lines := []string{fmt.Sprintf("Version   %s", Version)}
	if p, err := a.loadProfileFast(ctx); err == nil && p != nil {
		lines = append(lines, hardwareStatusLine(p))
	} else if err != nil {
		lines = append(lines, "Hardware  detection unavailable: "+short(err.Error(), 72))
	}
	lines = append(lines, runtime.StatusLine())
	return lines
}

func (a *App) loadProfileFast(ctx context.Context) (*hardware.Profile, error) {
	if a.Mock != "" {
		return (hardware.MockDetector{Path: a.Mock}).Detect(ctx)
	}
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	d := hardware.SystemDetector{Options: hardware.Options{CommandTimeout: 600 * time.Millisecond}}
	return d.Detect(ctx)
}

func hardwareStatusLine(p *hardware.Profile) string {
	var parts []string
	if name := strings.TrimSpace(p.CPU.Model); name != "" {
		parts = append(parts, shortenCPU(name))
	}
	if p.RAM.TotalGB > 0 {
		parts = append(parts, fmt.Sprintf("%.1f GB RAM", p.RAM.TotalGB))
	}
	if len(p.CPU.SIMD) > 0 {
		n := len(p.CPU.SIMD)
		if n > 2 {
			n = 2
		}
		parts = append(parts, strings.Join(p.CPU.SIMD[:n], "/"))
	}
	if len(p.GPUs) > 0 {
		g := p.GPUs[0]
		name := strings.TrimSpace(g.Name)
		if name == "" {
			name = g.Vendor
		}
		if g.VRAMTotalGB > 0 {
			name += fmt.Sprintf(" %.0f GB", g.VRAMTotalGB)
		}
		if name != "" {
			parts = append(parts, name)
		}
	}
	if len(parts) == 0 {
		return "Hardware  detected, no summary fields published"
	}
	return "Hardware  " + strings.Join(parts, " · ")
}

func shortenCPU(name string) string {
	for _, marker := range []string{" with ", " @ ", " CPU"} {
		if i := strings.Index(name, marker); i > 0 {
			name = name[:i]
		}
	}
	if len(name) > 44 {
		name = strings.TrimSpace(name[:41]) + "..."
	}
	return name
}
