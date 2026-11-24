package scoring

import (
	"fmt"
	"math"
	"strings"

	"llmfit/internal/gpu"
	"llmfit/internal/hardware"
)

// Component is one transparent part of a score.
type Component struct {
	Name         string  `json:"name"`
	Weight       float64 `json:"weight"`
	RawScore     float64 `json:"raw_score"`
	Contribution float64 `json:"contribution"`
	Detail       string  `json:"detail"`
}

// HardwareBreakdown explains a hardware score.
type HardwareBreakdown struct {
	Score             float64     `json:"score"`
	Tier              string      `json:"tier"`
	EffectiveMemoryGB float64     `json:"effective_memory_gb"`
	MemoryKind        string      `json:"memory_kind"`
	BandwidthGBps     float64     `json:"bandwidth_gbps"`
	ComputeBase       float64     `json:"compute_base"`
	Components        []Component `json:"components"`
	Notes             []string    `json:"notes,omitempty"`
}

// EffectiveMemory returns the memory used for model-fit decisions and the
// memory component: usable discrete VRAM, Apple unified RAM at 70%, or
// currently available system RAM for CPU-only machines.
func EffectiveMemory(p *hardware.Profile) (gb float64, kind, detail string) {
	if p == nil {
		return 0, "unknown", "no hardware profile"
	}
	if ds := p.DiscreteGPUs(); len(ds) > 0 {
		var parts []string
		for _, g := range ds {
			usable := g.VRAMTotalGB * DiscreteVRAMUsable
			if g.VRAMFreeGB > 0 && g.VRAMFreeGB < usable {
				usable = g.VRAMFreeGB
			}
			gb += usable
			parts = append(parts, fmt.Sprintf("%s %.1f→%.1f GB", shortGPUName(g.Name), g.VRAMTotalGB, usable))
		}
		detail = strings.Join(parts, " + ")
		if len(ds) > 1 {
			detail += " (summed; tensor split efficiency is not linear)"
		}
		return gb, "discrete VRAM", detail
	}
	if u, ok := p.UnifiedGPU(); ok {
		total := p.RAM.TotalGB
		if total <= 0 {
			total = u.VRAMTotalGB
		}
		gb = total * UnifiedRAMUsable
		return gb, "unified memory", fmt.Sprintf("%.1f GB RAM × %.0f%% = %.1f GB usable", total, UnifiedRAMUsable*100, gb)
	}
	avail := p.RAM.AvailableGB
	note := "available"
	if avail <= 0 && p.RAM.TotalGB > 0 {
		avail = p.RAM.TotalGB * 0.75
		note = "available unknown, estimated from total"
	}
	if avail < 0 {
		avail = 0
	}
	return avail, "system RAM", fmt.Sprintf("%.1f GB %s", avail, note)
}

func shortGPUName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "NVIDIA ")
	name = strings.TrimPrefix(name, "AMD ")
	name = strings.TrimPrefix(name, "Intel ")
	if name == "" {
		return "GPU"
	}
	if len(name) > 34 {
		return name[:31] + "..."
	}
	return name
}

// ScoreHardware computes the 0-100 score and its breakdown.
func ScoreHardware(p *hardware.Profile) HardwareBreakdown {
	var out HardwareBreakdown
	if p == nil {
		out.Tier = TierFor(0)
		out.Components = zeroComponents("no hardware profile")
		return out
	}
	memGB, memKind, memDetail := EffectiveMemory(p)
	out.EffectiveMemoryGB = Round1(memGB)
	out.MemoryKind = memKind
	memScore := Clamp(Interpolate(MemoryNeedCurve, memGB), 0, 100)

	table, _ := gpu.Load()
	bw, bwDetail, bwNotes := bandwidthFor(p, table)
	out.BandwidthGBps = Round1(bw)
	bwScore := Clamp(Interpolate(BandwidthCurve, bw), 0, 100)

	compute, computeDetail := computeFor(p, table)
	out.ComputeBase = Round1(compute)
	computeScore := Clamp(compute, 0, 100)
	if computeScore > 0 && computeScore < 5 {
		computeScore = 5
	}

	diskScore, diskDetail := diskFor(p)

	out.Components = []Component{
		{Name: "Memory", Weight: WeightMemory, RawScore: Round1(memScore), Contribution: Round1(memScore * WeightMemory), Detail: fmt.Sprintf("%s: %s", memKind, memDetail)},
		{Name: "Bandwidth", Weight: WeightBandwidth, RawScore: Round1(bwScore), Contribution: Round1(bwScore * WeightBandwidth), Detail: bwDetail},
		{Name: "Compute", Weight: WeightCompute, RawScore: Round1(computeScore), Contribution: Round1(computeScore * WeightCompute), Detail: computeDetail},
		{Name: "Disk", Weight: WeightDisk, RawScore: Round1(diskScore), Contribution: Round1(diskScore * WeightDisk), Detail: diskDetail},
	}
	var total float64
	for _, c := range out.Components {
		total += c.Contribution
	}
	out.Score = Round1(Clamp(total, 0, 100))
	out.Tier = TierFor(out.Score)
	out.Notes = append(out.Notes, bwNotes...)
	for _, w := range p.Warnings {
		if strings.Contains(strings.ToLower(w), "mock") {
			continue
		}
		out.Notes = append(out.Notes, w)
	}
	return out
}

func zeroComponents(detail string) []Component {
	return []Component{
		{Name: "Memory", Weight: WeightMemory, Detail: detail},
		{Name: "Bandwidth", Weight: WeightBandwidth, Detail: detail},
		{Name: "Compute", Weight: WeightCompute, Detail: detail},
		{Name: "Disk", Weight: WeightDisk, Detail: detail},
	}
}

func bandwidthFor(p *hardware.Profile, table *gpu.Table) (float64, string, []string) {
	var notes []string
	if ds := p.DiscreteGPUs(); len(ds) > 0 {
		var values []float64
		var names []string
		for _, g := range ds {
			bw := g.BandwidthGBps
			if bw <= 0 && table != nil {
				e, matched := table.LookupOrFallback(g.Name, g.Vendor)
				bw = e.BandwidthGBps
				if !matched {
					notes = append(notes, fmt.Sprintf("%s not found in GPU table; vendor fallback bandwidth used", shortGPUName(g.Name)))
				}
			}
			if bw <= 0 {
				bw = 150
				notes = append(notes, fmt.Sprintf("%s bandwidth unknown; 150 GB/s assumed", shortGPUName(g.Name)))
			}
			values = append(values, bw)
			names = append(names, fmt.Sprintf("%s %.0f GB/s", shortGPUName(g.Name), bw))
		}
		best := 0.0
		bestIdx := 0
		for i, v := range values {
			if v > best {
				best = v
				bestIdx = i
			}
		}
		total := best
		for i, v := range values {
			if i != bestIdx {
				total += v * 0.10
			}
		}
		detail := strings.Join(names, " + ") + " (max + 10% of additional GPUs)"
		if len(values) > 1 {
			notes = append(notes, "multi-GPU bandwidth is estimated; real tensor-split scaling is lower")
		}
		return total, detail, notes
	}
	if u, ok := p.UnifiedGPU(); ok {
		bw := u.BandwidthGBps
		if bw <= 0 && table != nil {
			e, _ := table.LookupOrFallback(u.Name, "Apple")
			bw = e.BandwidthGBps
		}
		if bw <= 0 {
			bw = 100
		}
		return bw, fmt.Sprintf("Apple unified memory: %.0f GB/s", bw), notes
	}
	bw := p.RAM.BandwidthGBps
	if bw <= 0 {
		bw = gpu.EstimateRAMBandwidth(p.RAM.Type, p.RAM.SpeedMTs)
	}
	detail := fmt.Sprintf("system RAM estimate %.0f GB/s", bw)
	if p.RAM.Type != "" {
		detail = fmt.Sprintf("%s-%d estimate %.0f GB/s", p.RAM.Type, p.RAM.SpeedMTs, bw)
	}
	return bw, detail, notes
}

func computeFor(p *hardware.Profile, table *gpu.Table) (float64, string) {
	discrete := p.DiscreteGPUs()
	unified, hasUnified := p.UnifiedGPU()
	if len(discrete) > 0 {
		var values []float64
		var names []string
		for _, g := range discrete {
			tier := g.ComputeTier
			if tier <= 0 && table != nil {
				e, _ := table.LookupOrFallback(g.Name, g.Vendor)
				tier = e.ComputeTier
			}
			if tier <= 0 {
				tier = 35
			}
			values = append(values, tier)
			names = append(names, fmt.Sprintf("%s tier %.0f", shortGPUName(g.Name), tier))
		}
		best := 0.0
		bestIdx := 0
		for i, v := range values {
			if v > best {
				best = v
				bestIdx = i
			}
		}
		total := best
		for i, v := range values {
			if i != bestIdx {
				total += v * 0.05
			}
		}
		return Clamp(total, 0, 100), strings.Join(names, " + ") + " (GPU compute tier)"
	}
	if hasUnified {
		tier := unified.ComputeTier
		if tier <= 0 && table != nil {
			e, _ := table.LookupOrFallback(unified.Name, "Apple")
			tier = e.ComputeTier
		}
		if tier <= 0 {
			tier = 40
		}
		return Clamp(tier, 0, 100), fmt.Sprintf("Apple GPU compute tier %.0f (Metal)", tier)
	}
	cores := p.CPU.PhysicalCores
	if cores <= 0 {
		cores = p.CPU.LogicalThreads
	}
	if cores <= 0 {
		return 5, "no CPU core count available; minimum compute score assumed"
	}
	ghz := p.CPU.BaseMHz / 1000
	if ghz <= 0 {
		ghz = p.CPU.MaxMHz / 1000
	}
	if ghz <= 0 {
		ghz = 2.5
	}
	factor, level := simdFactor(p.CPU.SIMD)
	effort := float64(cores) * ghz * factor
	score := Clamp(65*math.Sqrt(effort/64), 5, 65)
	detail := fmt.Sprintf("%d cores × %.2f GHz × %.2f SIMD (%s) = %.1f effort → %.0f/100 CPU cap",
		cores, ghz, factor, level, effort, score)
	return score, detail
}

func simdFactor(simd []string) (float64, string) {
	best := 0.90
	level := "unknown"
	for _, s := range simd {
		switch strings.ToUpper(s) {
		case "AVX512":
			return 1.50, "AVX-512"
		case "AVX2":
			if best < 1.25 {
				best, level = 1.25, "AVX2"
			}
		case "NEON":
			if best < 1.20 {
				best, level = 1.20, "NEON"
			}
		case "SSE4.2":
			if best < 1.00 {
				best, level = 1.00, "SSE4.2"
			}
		}
	}
	return best, level
}

func diskFor(p *hardware.Profile) (float64, string) {
	var score float64
	switch strings.ToLower(p.Disk.Kind) {
	case "nvme":
		score = 100
	case "ssd":
		score = 80
	case "hdd":
		score = 25
	default:
		score = 50
	}
	detail := fmt.Sprintf("%s disk", p.Disk.Kind)
	if p.Disk.FreeGB > 0 {
		detail += fmt.Sprintf(" (%.0f GB free)", p.Disk.FreeGB)
		if p.Disk.FreeGB < 10 {
			score = math.Min(score, 10)
			detail += "; critically low free space"
		} else if p.Disk.FreeGB < 25 {
			score = math.Min(score, 35)
			detail += "; low free space"
		}
	}
	return score, detail
}
