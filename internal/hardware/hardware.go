// Package hardware detects the machine's CPU, memory, GPU, and disk, or loads
// a mock profile from JSON. Detection is strictly best-effort: missing tools,
// permission errors, and malformed output become warnings, not failures.
package hardware

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"alocal/internal/gpu"
)

// Source records one raw detection source for --verbose.
type Source struct {
	Name    string `json:"name"`
	Command string `json:"command,omitempty"`
	Output  string `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
}

// OSInfo describes the operating system and execution environment.
type OSInfo struct {
	Name      string `json:"name"`
	Version   string `json:"version,omitempty"`
	Kernel    string `json:"kernel,omitempty"`
	Arch      string `json:"arch"`
	Container bool   `json:"container"`
	WSL       bool   `json:"wsl"`
}

// CPUInfo describes the processor and SIMD capabilities.
type CPUInfo struct {
	Model          string   `json:"model"`
	PhysicalCores  int      `json:"physical_cores"`
	LogicalThreads int      `json:"logical_threads"`
	BaseMHz        float64  `json:"base_mhz,omitempty"`
	MaxMHz         float64  `json:"max_mhz,omitempty"`
	Arch           string   `json:"arch"`
	SIMD           []string `json:"simd"`
	SIMDNote       string   `json:"simd_note,omitempty"`
	Vendor         string   `json:"vendor,omitempty"`
}

// RAMInfo describes installed and currently available memory.
type RAMInfo struct {
	TotalGB       float64 `json:"total_gb"`
	AvailableGB   float64 `json:"available_gb"`
	Type          string  `json:"type,omitempty"`
	SpeedMTs      int     `json:"speed_mts,omitempty"`
	Channels      int     `json:"channels,omitempty"`
	BandwidthGBps float64 `json:"bandwidth_gbps,omitempty"`
}

// GPUInfo describes one adapter. Kind is "discrete", "unified", "integrated",
// or "unknown". Unified memory adapters report total system RAM as VRAM; the
// scoring package applies the 70% usable fraction.
type GPUInfo struct {
	Vendor        string   `json:"vendor"`
	Name          string   `json:"name"`
	Kind          string   `json:"kind"`
	VRAMTotalGB   float64  `json:"vram_total_gb"`
	VRAMFreeGB    float64  `json:"vram_free_gb,omitempty"`
	Driver        string   `json:"driver,omitempty"`
	BandwidthGBps float64  `json:"bandwidth_gbps,omitempty"`
	ComputeTier   float64  `json:"compute_tier,omitempty"`
	Source        string   `json:"source,omitempty"`
	Notes         []string `json:"notes,omitempty"`
}

// DiskInfo describes the filesystem holding the working directory.
type DiskInfo struct {
	Mount   string  `json:"mount,omitempty"`
	Device  string  `json:"device,omitempty"`
	Kind    string  `json:"kind"` // nvme, ssd, hdd, unknown
	TotalGB float64 `json:"total_gb"`
	FreeGB  float64 `json:"free_gb"`
}

// Profile is the complete hardware snapshot. It is also the mock-profile schema.
type Profile struct {
	DetectedAt time.Time `json:"detected_at"`
	Mock       bool      `json:"mock,omitempty"`
	OS         OSInfo    `json:"os"`
	CPU        CPUInfo   `json:"cpu"`
	RAM        RAMInfo   `json:"ram"`
	GPUs       []GPUInfo `json:"gpus"`
	Disk       DiskInfo  `json:"disk"`
	Sources    []Source  `json:"sources,omitempty"`
	Warnings   []string  `json:"warnings,omitempty"`
}

// Detector is the seam that allows real and mock hardware.
type Detector interface {
	Detect(ctx context.Context) (*Profile, error)
}

// Options controls real detection.
type Options struct {
	Verbose        bool
	CommandTimeout time.Duration
}

// SystemDetector probes the live machine.
type SystemDetector struct {
	Options
}

// MockDetector loads a JSON profile.
type MockDetector struct {
	Path string
}

func (m MockDetector) Detect(_ context.Context) (*Profile, error) {
	return LoadProfile(m.Path)
}

// LoadProfile reads and normalizes a mock hardware profile. "-" reads stdin.
func LoadProfile(path string) (*Profile, error) {
	var r io.Reader
	if path == "-" {
		r = os.Stdin
	} else {
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open mock hardware profile %q: %w", path, err)
		}
		defer f.Close()
		r = f
	}
	var p Profile
	if err := json.NewDecoder(r).Decode(&p); err != nil {
		return nil, fmt.Errorf("parse mock hardware profile %q: %w", path, err)
	}
	p.Mock = true
	if p.DetectedAt.IsZero() {
		p.DetectedAt = time.Now().UTC()
	}
	if err := p.Normalize(); err != nil {
		return nil, fmt.Errorf("invalid mock hardware profile %q: %w", path, err)
	}
	return &p, nil
}

// Normalize fills reference values, canonicalizes names, and validates that the
// profile has enough information to score.
func (p *Profile) Normalize() error {
	if p.OS.Arch == "" {
		p.OS.Arch = runtime.GOARCH
	}
	if p.CPU.Arch == "" {
		p.CPU.Arch = p.OS.Arch
	}
	if p.CPU.LogicalThreads == 0 {
		p.CPU.LogicalThreads = p.CPU.PhysicalCores
	}
	if p.CPU.PhysicalCores == 0 {
		p.CPU.PhysicalCores = p.CPU.LogicalThreads
	}
	if p.CPU.PhysicalCores < 0 || p.CPU.LogicalThreads < 0 {
		return fmt.Errorf("cpu core counts cannot be negative")
	}
	if p.RAM.TotalGB < 0 || p.RAM.AvailableGB < 0 {
		return fmt.Errorf("ram sizes cannot be negative")
	}
	if p.RAM.AvailableGB > p.RAM.TotalGB && p.RAM.TotalGB > 0 {
		p.RAM.AvailableGB = p.RAM.TotalGB
	}
	if p.RAM.BandwidthGBps <= 0 {
		p.RAM.BandwidthGBps = gpu.EstimateRAMBandwidth(p.RAM.Type, p.RAM.SpeedMTs)
	}
	p.CPU.SIMD = normalizeSIMD(p.CPU.SIMD)

	table, _ := gpu.Load()
	for i := range p.GPUs {
		g := &p.GPUs[i]
		g.Vendor = gpu.CanonicalVendor(g.Vendor)
		var entry gpu.Entry
		var matched bool
		if table != nil {
			entry, matched = table.Lookup(g.Name, g.Vendor)
			if !matched {
				entry, matched = table.LookupOrFallback(g.Name, g.Vendor)
			}
		}
		if g.Kind == "" {
			switch {
			case matched && entry.Kind != "":
				g.Kind = entry.Kind
			case g.VRAMTotalGB > 0:
				g.Kind = "discrete"
			default:
				g.Kind = "unknown"
			}
		}
		g.Kind = strings.ToLower(strings.TrimSpace(g.Kind))
		if g.Kind == "unified" && g.VRAMTotalGB <= 0 {
			g.VRAMTotalGB = p.RAM.TotalGB
		}
		if g.Kind == "discrete" && g.VRAMTotalGB <= 0 && matched && entry.VRAMGB > 0 {
			g.VRAMTotalGB = entry.VRAMGB
			g.Notes = append(g.Notes, "VRAM estimated from GPU lookup table")
		}
		if g.BandwidthGBps <= 0 && entry.BandwidthGBps > 0 {
			g.BandwidthGBps = entry.BandwidthGBps
		}
		if g.ComputeTier <= 0 && entry.ComputeTier > 0 {
			g.ComputeTier = entry.ComputeTier
		}
		if g.Kind == "integrated" && g.ComputeTier <= 0 {
			g.ComputeTier = 20
		}
		if g.VRAMFreeGB < 0 {
			g.VRAMFreeGB = 0
		}
		if g.VRAMFreeGB > g.VRAMTotalGB {
			g.VRAMFreeGB = g.VRAMTotalGB
		}
	}
	if p.Disk.Kind == "" {
		p.Disk.Kind = "unknown"
	}
	p.Disk.Kind = strings.ToLower(p.Disk.Kind)
	if p.Disk.FreeGB < 0 {
		p.Disk.FreeGB = 0
	}
	if p.DetectedAt.IsZero() {
		p.DetectedAt = time.Now().UTC()
	}
	if p.RAM.TotalGB <= 0 && !hasPositiveVRAM(p.GPUs) {
		return fmt.Errorf("profile has neither ram.total_gb nor a GPU with vram_total_gb")
	}
	return nil
}

func hasPositiveVRAM(gpus []GPUInfo) bool {
	for _, g := range gpus {
		if g.VRAMTotalGB > 0 {
			return true
		}
	}
	return false
}

func normalizeSIMD(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.ToUpper(strings.TrimSpace(s))
		switch s {
		case "ASIMD", "NEON":
			s = "NEON"
		case "AVX512F", "AVX512":
			s = "AVX512"
		case "AVX2":
			s = "AVX2"
		case "SSE4_2", "SSE4.2":
			s = "SSE4.2"
		case "F16C":
			s = "F16C"
		}
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	order := map[string]int{"NEON": 1, "AVX512": 2, "AVX2": 3, "F16C": 4, "SSE4.2": 5}
	sort.SliceStable(out, func(i, j int) bool {
		oi, ok := order[out[i]]
		if !ok {
			oi = 99
		}
		oj, ok := order[out[j]]
		if !ok {
			oj = 99
		}
		return oi < oj
	})
	return out
}

// HasDiscrete reports whether a discrete GPU is present.
func (p *Profile) HasDiscrete() bool {
	for _, g := range p.GPUs {
		if g.Kind == "discrete" && g.VRAMTotalGB > 0 {
			return true
		}
	}
	return false
}

// UnifiedGPU returns the Apple-style unified GPU, if present.
func (p *Profile) UnifiedGPU() (GPUInfo, bool) {
	for _, g := range p.GPUs {
		if g.Kind == "unified" && g.VRAMTotalGB > 0 {
			return g, true
		}
	}
	return GPUInfo{}, false
}

// DiscreteGPUs returns discrete GPUs with positive VRAM.
func (p *Profile) DiscreteGPUs() []GPUInfo {
	var out []GPUInfo
	for _, g := range p.GPUs {
		if g.Kind == "discrete" && g.VRAMTotalGB > 0 {
			out = append(out, g)
		}
	}
	return out
}

// IntegratedGPUs returns integrated adapters.
func (p *Profile) IntegratedGPUs() []GPUInfo {
	var out []GPUInfo
	for _, g := range p.GPUs {
		if g.Kind == "integrated" {
			out = append(out, g)
		}
	}
	return out
}

// CPUInfoSummary is a compact one-line description.
func (p *Profile) CPUInfoSummary() string {
	c := p.CPU
	parts := []string{}
	if c.Model != "" {
		parts = append(parts, c.Model)
	}
	if c.PhysicalCores > 0 {
		parts = append(parts, fmt.Sprintf("%dC/%dT", c.PhysicalCores, c.LogicalThreads))
	}
	if c.MaxMHz > 0 {
		parts = append(parts, fmt.Sprintf("%.1f GHz max", c.MaxMHz/1000))
	}
	return strings.Join(parts, ", ")
}
