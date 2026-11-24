package gpu

import (
	"math"
	"testing"
)

func TestEmbeddedTable(t *testing.T) {
	table, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(table.Entries) < 60 {
		t.Fatalf("GPU table has %d entries, want at least 60", len(table.Entries))
	}
	cases := []struct {
		name     string
		vendor   string
		wantVRAM float64
		wantBW   float64
		wantTier float64
		kind     string
	}{
		{"NVIDIA GeForce RTX 3060", "NVIDIA", 12, 360, 63, "discrete"},
		{"NVIDIA GeForce RTX 4090", "NVIDIA", 24, 1008, 95, "discrete"},
		{"NVIDIA GeForce RTX 5070 Ti", "NVIDIA", 16, 896, 89, "discrete"},
		{"AMD Radeon RX 7900 XTX", "AMD", 24, 960, 89, "discrete"},
		{"AMD Radeon RX 9070 XT", "AMD", 16, 645, 84, "discrete"},
		{"Intel Arc B580", "Intel", 12, 456, 60, "discrete"},
		{"Apple M2 Max", "Apple", 0, 400, 64, "unified"},
		{"Apple M4", "Apple", 0, 120, 45, "unified"},
	}
	for _, c := range cases {
		e, ok := table.Lookup(c.name, c.vendor)
		if !ok {
			t.Errorf("no lookup for %s", c.name)
			continue
		}
		if e.VRAMGB != c.wantVRAM || e.BandwidthGBps != c.wantBW || e.ComputeTier != c.wantTier || e.Kind != c.kind {
			t.Errorf("%s: got vram=%v bw=%v tier=%v kind=%s", c.name, e.VRAMGB, e.BandwidthGBps, e.ComputeTier, e.Kind)
		}
	}
}

func TestVendorCanonicalization(t *testing.T) {
	cases := map[string]string{
		"NVIDIA Corporation GA104 [GeForce RTX 3060]": "NVIDIA",
		"Advanced Micro Devices, Inc. [AMD/ATI]":      "AMD",
		"Intel Corporation Kaby Lake-U GT2":           "Intel", // must not match "ati" inside Corporation
		"Apple M3 Pro":                                "Apple",
		"Some Unknown Vendor":                         "unknown",
	}
	for in, want := range cases {
		if got := CanonicalVendor(in); got != want {
			t.Errorf("CanonicalVendor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFallback(t *testing.T) {
	table, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	e, matched := table.LookupOrFallback("NVIDIA Mystery 9999", "NVIDIA")
	if matched {
		t.Fatal("unexpected match")
	}
	if e.BandwidthGBps != 350 || e.ComputeTier != 50 {
		t.Errorf("unexpected fallback %+v", e)
	}
}

func TestEstimateRAMBandwidth(t *testing.T) {
	cases := []struct {
		memType string
		speed   int
		want    float64
	}{
		{"DDR4", 3200, 51.2},
		{"DDR5", 6000, 96},
		{"LPDDR5X", 7500, 120},
		{"", 0, 30},
		{"DDR4", 0, 51.2},
	}
	for _, c := range cases {
		if got := EstimateRAMBandwidth(c.memType, c.speed); math.Abs(got-c.want) > 0.01 {
			t.Errorf("EstimateRAMBandwidth(%q,%d) = %v, want %v", c.memType, c.speed, got, c.want)
		}
	}
}
