package hardware

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFixtures(t *testing.T) {
	cases := []struct {
		name      string
		ram       float64
		gpuKind   string
		gpuVRAM   float64
		bandwidth float64
	}{
		{"rtx3060", 32, "discrete", 12, 360},
		{"m2air8", 8, "unified", 8, 100},
		{"cpu-only-16", 16, "", 0, 51.2},
		{"rtx4090", 64, "discrete", 24, 1008},
		{"nogpu4", 4, "", 0, 38.4},
	}
	for _, c := range cases {
		p, err := LoadProfile(filepath.Join("..", "..", "tests", "fixtures", c.name+".json"))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !p.Mock {
			t.Errorf("%s: mock flag not set", c.name)
		}
		if math.Abs(p.RAM.TotalGB-c.ram) > 0.01 {
			t.Errorf("%s: RAM %.1f", c.name, p.RAM.TotalGB)
		}
		if c.gpuKind == "" {
			if len(p.GPUs) != 0 {
				t.Errorf("%s: unexpected GPU %+v", c.name, p.GPUs)
			}
			continue
		}
		if len(p.GPUs) != 1 {
			t.Fatalf("%s: GPUs %+v", c.name, p.GPUs)
		}
		g := p.GPUs[0]
		if g.Kind != c.gpuKind || math.Abs(g.VRAMTotalGB-c.gpuVRAM) > 0.01 {
			t.Errorf("%s: GPU kind=%s vram=%.1f", c.name, g.Kind, g.VRAMTotalGB)
		}
		if math.Abs(g.BandwidthGBps-c.bandwidth) > 0.01 {
			t.Errorf("%s: GPU bandwidth %.1f, want %.1f", c.name, g.BandwidthGBps, c.bandwidth)
		}
	}
}

func TestLoadProfileErrors(t *testing.T) {
	if _, err := LoadProfile("/definitely/not/here.json"); err == nil {
		t.Error("expected error for missing file")
	}
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProfile(bad); err == nil {
		t.Error("expected parse error")
	}
	empty := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(empty, []byte(`{"cpu":{"model":"x"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProfile(empty); err == nil {
		t.Error("expected incomplete-profile error")
	}
}

func TestMockDetector(t *testing.T) {
	d := MockDetector{Path: filepath.Join("..", "..", "tests", "fixtures", "m2air8.json")}
	p, err := d.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !p.Mock || p.CPU.Model != "Apple M2" {
		t.Errorf("unexpected profile: %+v", p)
	}
}

func TestNormalizeAppleUnified(t *testing.T) {
	p := &Profile{
		OS:  OSInfo{Arch: "aarch64"},
		CPU: CPUInfo{Model: "Apple M4 Pro", PhysicalCores: 12, LogicalThreads: 12, Arch: "aarch64", SIMD: []string{"NEON"}},
		RAM: RAMInfo{TotalGB: 24, AvailableGB: 18},
		GPUs: []GPUInfo{
			{Vendor: "Apple", Name: "Apple M4 Pro", Kind: "unified"},
		},
		Disk: DiskInfo{Kind: "nvme", FreeGB: 100, TotalGB: 500},
	}
	if err := p.Normalize(); err != nil {
		t.Fatal(err)
	}
	g := p.GPUs[0]
	if g.VRAMTotalGB != 24 {
		t.Errorf("unified VRAM = %.1f, want 24", g.VRAMTotalGB)
	}
	if g.BandwidthGBps != 273 || g.ComputeTier != 58 {
		t.Errorf("Apple M4 Pro lookup = bw %.0f tier %.0f", g.BandwidthGBps, g.ComputeTier)
	}
}

func TestNormalizeUnknownGPUFallback(t *testing.T) {
	p := &Profile{
		OS:  OSInfo{Arch: "x86_64"},
		CPU: CPUInfo{PhysicalCores: 4, LogicalThreads: 8, Arch: "x86_64"},
		RAM: RAMInfo{TotalGB: 16, AvailableGB: 12},
		GPUs: []GPUInfo{
			{Vendor: "NVIDIA", Name: "Totally Unknown Accelerator", Kind: "discrete", VRAMTotalGB: 8},
		},
	}
	if err := p.Normalize(); err != nil {
		t.Fatal(err)
	}
	if p.GPUs[0].BandwidthGBps != 350 || p.GPUs[0].ComputeTier != 50 {
		t.Errorf("fallback not applied: %+v", p.GPUs[0])
	}
}
