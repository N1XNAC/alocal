package scoring

import (
	"math"
	"path/filepath"
	"testing"

	"llmfit/internal/hardware"
)

func fixture(t *testing.T, name string) *hardware.Profile {
	t.Helper()
	p, err := hardware.LoadProfile(filepath.Join("..", "..", "tests", "fixtures", name+".json"))
	if err != nil {
		t.Fatalf("load fixture %s: %v", name, err)
	}
	return p
}

func TestInterpolate(t *testing.T) {
	points := []Point{{0, 0}, {10, 100}, {20, 50}}
	cases := []struct{ x, want float64 }{
		{-1, 0}, {0, 0}, {5, 50}, {10, 100}, {15, 75}, {20, 50}, {99, 50},
	}
	for _, c := range cases {
		if got := Interpolate(points, c.x); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("Interpolate(%v) = %v, want %v", c.x, got, c.want)
		}
	}
	if got := Interpolate(nil, 5); got != 0 {
		t.Errorf("empty curve = %v, want 0", got)
	}
}

func TestClampAndTier(t *testing.T) {
	if Clamp(math.NaN(), 1, 2) != 1 {
		t.Error("NaN should clamp to low bound")
	}
	if Clamp(5, 0, 3) != 3 {
		t.Error("high clamp failed")
	}
	cases := map[float64]string{
		0: "Entry", 29.9: "Entry", 30: "Mid", 49.9: "Mid", 50: "High",
		69.9: "High", 70: "Enthusiast", 84.9: "Enthusiast", 85: "Workstation", 100: "Workstation",
	}
	for score, want := range cases {
		if got := TierFor(score); got != want {
			t.Errorf("TierFor(%v) = %q, want %q", score, got, want)
		}
	}
}

func TestEffectiveMemory(t *testing.T) {
	cases := []struct {
		fixture string
		wantGB  float64
		kind    string
	}{
		{"rtx3060", 11.4, "discrete VRAM"},
		{"m2air8", 5.6, "unified memory"},
		{"cpu-only-16", 11.5, "system RAM"},
		{"rtx4090", 22.8, "discrete VRAM"},
		{"nogpu4", 2.8, "system RAM"},
	}
	for _, c := range cases {
		p := fixture(t, c.fixture)
		gb, kind, detail := EffectiveMemory(p)
		if math.Abs(gb-c.wantGB) > 0.01 {
			t.Errorf("%s: effective %.2f GB, want %.2f", c.fixture, gb, c.wantGB)
		}
		if kind != c.kind {
			t.Errorf("%s: kind %q, want %q", c.fixture, kind, c.kind)
		}
		if detail == "" {
			t.Errorf("%s: empty detail", c.fixture)
		}
	}
}

func TestHardwareScores(t *testing.T) {
	cases := []struct {
		fixture string
		want    float64
		tier    string
	}{
		{"rtx3060", 59.6, "High"},
		{"m2air8", 38.7, "Mid"},
		{"cpu-only-16", 42.8, "Mid"},
		{"rtx4090", 80.1, "Enthusiast"},
		{"nogpu4", 20.6, "Entry"},
	}
	for _, c := range cases {
		hw := ScoreHardware(fixture(t, c.fixture))
		if math.Abs(hw.Score-c.want) > 0.5 {
			t.Errorf("%s: score %.1f, want %.1f", c.fixture, hw.Score, c.want)
		}
		if hw.Tier != c.tier {
			t.Errorf("%s: tier %q, want %q", c.fixture, hw.Tier, c.tier)
		}
		var sum float64
		for _, comp := range hw.Components {
			if comp.Weight <= 0 || comp.RawScore < 0 || comp.RawScore > 100 {
				t.Errorf("%s: invalid component %+v", c.fixture, comp)
			}
			sum += comp.Contribution
		}
		if math.Abs(sum-hw.Score) > 0.2 {
			t.Errorf("%s: contributions sum %.1f != score %.1f", c.fixture, sum, hw.Score)
		}
	}
}

func TestMultiGPUAndUnknownGPU(t *testing.T) {
	p := &hardware.Profile{
		OS:  hardware.OSInfo{Arch: "x86_64"},
		CPU: hardware.CPUInfo{Model: "Test CPU", PhysicalCores: 8, LogicalThreads: 16, BaseMHz: 3600, Arch: "x86_64", SIMD: []string{"AVX2"}},
		RAM: hardware.RAMInfo{TotalGB: 32, AvailableGB: 24, Type: "DDR4", SpeedMTs: 3200},
		GPUs: []hardware.GPUInfo{
			{Vendor: "NVIDIA", Name: "Mystery Accelerator 9000", Kind: "discrete", VRAMTotalGB: 16},
			{Vendor: "NVIDIA", Name: "Second Mystery", Kind: "discrete", VRAMTotalGB: 16},
		},
		Disk: hardware.DiskInfo{Kind: "nvme", FreeGB: 500, TotalGB: 1000},
	}
	if err := p.Normalize(); err != nil {
		t.Fatal(err)
	}
	gb, kind, _ := EffectiveMemory(p)
	if math.Abs(gb-30.4) > 0.01 || kind != "discrete VRAM" {
		t.Fatalf("effective = %.2f %q, want 30.4 discrete VRAM", gb, kind)
	}
	hw := ScoreHardware(p)
	if hw.BandwidthGBps <= 0 || hw.ComputeBase <= 0 || hw.Score <= 0 {
		t.Fatalf("fallback GPU not scored: %+v", hw)
	}
	if hw.Score > 100 || hw.Score < 0 {
		t.Fatalf("score out of range: %v", hw.Score)
	}
}

func TestModelNeed(t *testing.T) {
	spec := ModelSpec{
		ParamsB: 7.62, ActiveParamsB: 7.62, Quant: "Q4_K_M", FileSizeGB: 4.62,
		MaxContext: 32768, Layers: 28, KVHeads: 4, HeadDim: 128,
	}
	need := ComputeModelNeed(spec, 8192)
	if need.RequiredGB < 5.5 || need.RequiredGB > 7 {
		t.Errorf("required = %.2f GB", need.RequiredGB)
	}
	if need.Score < 30 || need.Score > 40 {
		t.Errorf("score = %.1f", need.Score)
	}
	larger := ComputeModelNeed(spec, 32768)
	if larger.KVGB <= need.KVGB || larger.RequiredGB <= need.RequiredGB || larger.Score <= need.Score {
		t.Errorf("larger context should increase KV/requirement/score: %+v vs %+v", larger, need)
	}
	half := spec
	half.Quant = "Q8_0"
	half.FileSizeGB = 8.1
	if hq := ComputeModelNeed(half, 8192); hq.Score <= need.Score {
		t.Errorf("Q8 score %.1f should exceed Q4 %.1f", hq.Score, need.Score)
	}
	moe := spec
	moe.ParamsB, moe.ActiveParamsB = 30.5, 3.3
	moe.FileSizeGB = 18.5
	mo := ComputeModelNeed(moe, 8192)
	if mo.DemandScore >= ComputeModelNeed(spec, 8192).DemandScore {
		t.Errorf("MoE active-parameter demand should be lower")
	}
}

func TestThroughputEstimate(t *testing.T) {
	spec := ModelSpec{
		ParamsB: 8.03, ActiveParamsB: 8.03, Quant: "Q4_K_M", FileSizeGB: 4.87,
		MaxContext: 131072, Layers: 32, KVHeads: 8, HeadDim: 128,
	}
	gpuTPS := EstimateThroughput(fixture(t, "rtx3060"), spec)
	cpuTPS := EstimateThroughput(fixture(t, "cpu-only-16"), spec)
	if gpuTPS.TokensPerSec <= cpuTPS.TokensPerSec {
		t.Errorf("GPU %.1f should beat CPU %.1f", gpuTPS.TokensPerSec, cpuTPS.TokensPerSec)
	}
	if gpuTPS.Low >= gpuTPS.TokensPerSec || gpuTPS.High <= gpuTPS.TokensPerSec {
		t.Errorf("range invalid: %+v", gpuTPS)
	}
	if gpuTPS.Formula == "" || !gpuTPS.IsEstimate {
		t.Errorf("missing formula/estimate flag: %+v", gpuTPS)
	}
}
