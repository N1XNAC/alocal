package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"alocal/internal/hardware"
	"alocal/internal/scoring"
)

type scanJSON struct {
	Profile  *hardware.Profile         `json:"profile"`
	Hardware scoring.HardwareBreakdown `json:"hardware"`
}

func newScanCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "scan",
		Short: "Detect CPU, RAM, GPUs, and disk",
		Long:  "Detect hardware with gopsutil plus platform tools. Missing tools and permission errors become warnings; detection never aborts.",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := app.loadProfile(cmd.Context())
			if err != nil {
				return err
			}
			hw := scoring.ScoreHardware(p)
			if app.JSON {
				profile := *p
				if !app.Verbose {
					profile.Sources = nil
				}
				return app.writeJSON(scanJSON{Profile: &profile, Hardware: hw})
			}
			printScan(app.ui(), p, hw, app.Verbose)
			return nil
		},
	}
}

func printScan(u *UI, p *hardware.Profile, hw scoring.HardwareBreakdown, verbose bool) {
	u.Title("Alocal hardware scan")
	if p.Mock {
		fmt.Fprintln(u.Out, u.Warn("  MOCK PROFILE — values came from a JSON file, not this machine"))
	}
	fmt.Fprintln(u.Out)

	u.Section("System")
	env := ""
	if p.OS.Container {
		env = " · container"
	}
	if p.OS.WSL {
		env += " · WSL"
	}
	u.KV("OS", fmt.Sprintf("%s %s (%s)%s", p.OS.Name, p.OS.Version, p.OS.Arch, env))
	if p.OS.Kernel != "" {
		u.KV("Kernel", p.OS.Kernel)
	}

	fmt.Fprintln(u.Out)
	u.Section("CPU")
	u.KV("Model", p.CPU.Model)
	u.KV("Cores", fmt.Sprintf("%d physical / %d logical", p.CPU.PhysicalCores, p.CPU.LogicalThreads))
	base, max := "unknown", "unknown"
	if p.CPU.BaseMHz > 0 {
		base = fmt.Sprintf("%.2f GHz", p.CPU.BaseMHz/1000)
	}
	if p.CPU.MaxMHz > 0 {
		max = fmt.Sprintf("%.2f GHz", p.CPU.MaxMHz/1000)
	}
	u.KV("Clock", fmt.Sprintf("base %s / max %s", base, max))
	u.KV("Architecture", p.CPU.Arch)
	simd := "unknown"
	if len(p.CPU.SIMD) > 0 {
		simd = strings.Join(p.CPU.SIMD, ", ")
	}
	u.KV("SIMD", simd)
	if p.CPU.SIMDNote != "" {
		fmt.Fprintf(u.Out, "    %s\n", u.Dim(p.CPU.SIMDNote))
	}

	fmt.Fprintln(u.Out)
	u.Section("Memory")
	u.KV("Total", fmtGB(p.RAM.TotalGB))
	u.KV("Available", fmtGB(p.RAM.AvailableGB))
	memType := p.RAM.Type
	if memType == "" {
		memType = "unknown"
	}
	speed := "unknown"
	if p.RAM.SpeedMTs > 0 {
		speed = fmt.Sprintf("%d MT/s", p.RAM.SpeedMTs)
	}
	u.KV("Type / speed", fmt.Sprintf("%s / %s", memType, speed))
	if p.RAM.BandwidthGBps > 0 {
		u.KV("Est. bandwidth", fmt.Sprintf("%.1f GB/s (theoretical dual-channel estimate)", p.RAM.BandwidthGBps))
	}

	fmt.Fprintln(u.Out)
	u.Section("GPU")
	if len(p.GPUs) == 0 {
		fmt.Fprintf(u.Out, "  %s\n", u.Dim("None detected — CPU-only inference"))
	}
	for i, g := range p.GPUs {
		kind := g.Kind
		if kind == "" {
			kind = "unknown"
		}
		line := fmt.Sprintf("%s (%s, %s)", g.Name, g.Vendor, kind)
		if g.Kind == "integrated" {
			line += " " + u.Warn("[low capability]")
		}
		if i == 0 {
			fmt.Fprintf(u.Out, "  %s\n", u.Label(line))
		} else {
			fmt.Fprintf(u.Out, "  %s\n", line)
		}
		if g.Kind == "unified" {
			fmt.Fprintf(u.Out, "    VRAM: %.1f GB total, unified (≈%.1f GB usable at 70%%)\n", g.VRAMTotalGB, g.VRAMTotalGB*scoring.UnifiedRAMUsable)
		} else if g.Kind == "integrated" {
			fmt.Fprintf(u.Out, "    VRAM: shared system memory\n")
		} else {
			free := ""
			if g.VRAMFreeGB > 0 {
				free = fmt.Sprintf(", %.1f GB free", g.VRAMFreeGB)
			}
			fmt.Fprintf(u.Out, "    VRAM: %.1f GB total%s\n", g.VRAMTotalGB, free)
		}
		if g.BandwidthGBps > 0 {
			fmt.Fprintf(u.Out, "    Bandwidth: %.0f GB/s · compute tier %.0f/100\n", g.BandwidthGBps, g.ComputeTier)
		}
		if g.Driver != "" {
			fmt.Fprintf(u.Out, "    Driver: %s\n", g.Driver)
		}
		if g.Source != "" {
			fmt.Fprintf(u.Out, "    %s\n", u.Dim("source: "+g.Source))
		}
		for _, note := range g.Notes {
			fmt.Fprintf(u.Out, "    %s\n", u.Dim(note))
		}
	}

	fmt.Fprintln(u.Out)
	u.Section("Disk")
	kind := p.Disk.Kind
	if kind == "" {
		kind = "unknown"
	}
	u.KV("Type", strings.ToUpper(kind))
	u.KV("Free", fmt.Sprintf("%.1f GB of %.1f GB", p.Disk.FreeGB, p.Disk.TotalGB))
	if p.Disk.Mount != "" {
		u.KV("Mount", p.Disk.Mount)
	}
	if p.Disk.Device != "" {
		u.KV("Device", p.Disk.Device)
	}

	fmt.Fprintln(u.Out)
	u.Section("Hardware score")
	fmt.Fprintf(u.Out, "  %s  ·  effective memory %s\n", u.Score(hw.Score, hw.Tier), fmtGB(hw.EffectiveMemoryGB))
	if verbose {
		printScoreComponents(u, hw)
	}

	if len(p.Warnings) > 0 {
		fmt.Fprintln(u.Out)
		u.Section("Warnings")
		for _, w := range p.Warnings {
			u.Bullet(u.Warn(w))
		}
	}
	if verbose && len(p.Sources) > 0 {
		fmt.Fprintln(u.Out)
		u.Section("Raw detection sources")
		for _, s := range p.Sources {
			fmt.Fprintf(u.Out, "  %s\n", u.Label("["+s.Name+"]"))
			if s.Command != "" {
				fmt.Fprintf(u.Out, "    %s\n", u.Dim("$ "+s.Command))
			}
			if s.Error != "" {
				fmt.Fprintf(u.Out, "    %s\n", u.Warn("error: "+s.Error))
			}
			if s.Output != "" {
				for _, line := range strings.Split(s.Output, "\n") {
					fmt.Fprintf(u.Out, "    %s\n", u.Dim(line))
				}
			}
		}
	}
	fmt.Fprintln(u.Out)
	fmt.Fprintf(u.Out, "%s\n", u.Dim("Next: alocal recommend"))
}

func printScoreComponents(u *UI, hw scoring.HardwareBreakdown) {
	fmt.Fprintln(u.Out)
	u.Table(
		[]string{"COMPONENT", "WEIGHT", "RAW /100", "CONTRIBUTED"},
		componentRows(hw),
	)
	for _, c := range hw.Components {
		fmt.Fprintf(u.Out, "  %s %s\n", u.Label(c.Name+":"), u.Dim(c.Detail))
	}
	fmt.Fprintln(u.Out)
	fmt.Fprintf(u.Out, "  Score = 0.50·memory + 0.25·bandwidth + 0.20·compute + 0.05·disk = %s\n", u.Good(fmtScore(hw.Score)))
	for _, n := range hw.Notes {
		fmt.Fprintf(u.Out, "  %s %s\n", u.Warn("note:"), u.Dim(n))
	}
}
