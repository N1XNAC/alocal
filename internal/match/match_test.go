package match

import (
	"math"
	"path/filepath"
	"testing"

	"llmfit/internal/catalog"
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

func TestFixtureRecommendations(t *testing.T) {
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		fixture string
		top     string
	}{
		{"rtx3060", "yi-1.5-9b-chat-q5_k_m"},
		{"m2air8", "qwen2.5-3b-instruct-q8_0"},
		{"cpu-only-16", "granite-3.1-8b-instruct-q5_k_m"},
		{"rtx4090", "mistral-small-3.1-24b-instruct-q4_k_m"},
		{"nogpu4", "llama-3.2-1b-instruct-q8_0"},
	}
	for _, c := range cases {
		p := fixture(t, c.fixture)
		res := Recommend(p, cat, Options{Context: 8192, Top: 3})
		if res.Status != "ok" {
			t.Errorf("%s: status %s: %s", c.fixture, res.Status, res.Message)
			continue
		}
		if len(res.Recommendations) == 0 {
			t.Errorf("%s: no recommendations", c.fixture)
			continue
		}
		got := res.Recommendations[0]
		if got.Model.ID != c.top {
			t.Errorf("%s: top %s, want %s", c.fixture, got.Model.ID, c.top)
		}
		if !got.Fit {
			t.Errorf("%s: top pick does not fit", c.fixture)
		}
		if got.Need.Score > res.Hardware.Score+0.05 {
			t.Errorf("%s: top score %.1f exceeds hardware %.1f", c.fixture, got.Need.Score, res.Hardware.Score)
		}
		if got.Need.RequiredGB > res.MemoryLimitGB+0.01 {
			t.Errorf("%s: required %.1f > limit %.1f", c.fixture, got.Need.RequiredGB, res.MemoryLimitGB)
		}
		if len(res.Recommendations) > 1 && res.Recommendations[1].Need.Score > got.Need.Score+0.05 {
			t.Errorf("%s: recommendations not ordered by score", c.fixture)
		}
	}
}

func TestHardFilterBeatsScore(t *testing.T) {
	p := fixture(t, "rtx3060")
	cat := &catalog.Catalog{Version: 1, ScoreContext: 8192, Models: []catalog.Model{
		{
			ID: "small", Family: "Test", DisplayName: "Small", ParamsB: 3, ActiveParamsB: 3,
			Quant: "Q4_K_M", FileSizeGB: 1.9, MaxContext: 32768,
			Tasks: []string{"chat"}, License: "test", ReleaseYear: 2024,
			Arch:     catalog.Arch{Layers: 36, KVHeads: 2, HeadDim: 128},
			Runtimes: catalog.Runtimes{Ollama: "small"},
		},
		{
			ID: "big", Family: "Test", DisplayName: "Big", ParamsB: 14, ActiveParamsB: 14,
			Quant: "Q4_K_M", FileSizeGB: 9.0, MaxContext: 32768,
			Tasks: []string{"chat"}, License: "test", ReleaseYear: 2024,
			Arch:     catalog.Arch{Layers: 48, KVHeads: 8, HeadDim: 128},
			Runtimes: catalog.Runtimes{Ollama: "big"},
		},
	}}
	res := Recommend(p, cat, Options{Context: 8192, Top: 5})
	if res.Status != "ok" {
		t.Fatalf("status %s: %s", res.Status, res.Message)
	}
	if res.Recommendations[0].Model.ID != "small" {
		t.Fatalf("chose %s; big model should have failed the hard memory filter", res.Recommendations[0].Model.ID)
	}
	for _, r := range res.Recommendations {
		if r.Model.ID == "big" {
			t.Fatalf("big model passed the memory filter: %.1f GB vs %.1f", r.Need.RequiredGB, res.MemoryLimitGB)
		}
	}
}

func TestNoFitAndOverBudget(t *testing.T) {
	small := fixture(t, "nogpu4")
	huge := catalog.Model{
		ID: "huge", Family: "Test", DisplayName: "Huge", ParamsB: 70, ActiveParamsB: 70,
		Quant: "Q4_K_M", FileSizeGB: 42, MaxContext: 131072,
		Tasks: []string{"chat"}, License: "test", ReleaseYear: 2024,
		Arch:     catalog.Arch{Layers: 80, KVHeads: 8, HeadDim: 128},
		Runtimes: catalog.Runtimes{Ollama: "huge"},
	}
	res := Recommend(small, &catalog.Catalog{Version: 1, ScoreContext: 8192, Models: []catalog.Model{huge}}, Options{Context: 8192})
	if res.Status != "no_fit" {
		t.Fatalf("expected no_fit for 4GB machine, got %s (%s)", res.Status, res.Message)
	}
	if len(res.Stretch) == 0 || res.Stretch[0].Fit {
		t.Fatalf("expected non-fitting stretch suggestion: %+v", res.Stretch)
	}
	if len(res.Suggestions) == 0 {
		t.Fatal("no hardware-change suggestions")
	}

	// This model fits the 4 GB machine's safe limit but its model score is
	// deliberately above the machine's hardware score.
	fitsButSlow := catalog.Model{
		ID: "fits-but-slow", Family: "Test", DisplayName: "Fits but slow", ParamsB: 8, ActiveParamsB: 8,
		Quant: "Q4_K_M", FileSizeGB: 1.6, MaxContext: 32768,
		Tasks: []string{"chat"}, License: "test", ReleaseYear: 2024,
		Arch:     catalog.Arch{Layers: 8, KVHeads: 1, HeadDim: 64},
		Runtimes: catalog.Runtimes{Ollama: "fits"},
	}
	res = Recommend(small, &catalog.Catalog{Version: 1, ScoreContext: 8192, Models: []catalog.Model{fitsButSlow}}, Options{Context: 8192})
	if res.Status != "over_budget" {
		t.Fatalf("expected over_budget, got %s (%s)", res.Status, res.Message)
	}
	if len(res.Stretch) == 0 || !res.Stretch[0].Fit {
		t.Fatalf("over-budget model should fit memory: %+v", res.Stretch)
	}
	if res.Stretch[0].Need.Score <= res.Hardware.Score {
		t.Fatalf("stretch score %.1f should exceed hardware %.1f", res.Stretch[0].Need.Score, res.Hardware.Score)
	}
}

func TestTaskAndContextFilters(t *testing.T) {
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	p := fixture(t, "m2air8")
	res := Recommend(p, cat, Options{Task: "coding", Context: 8192, Top: 5})
	if res.Status != "ok" {
		t.Fatalf("coding status %s: %s", res.Status, res.Message)
	}
	for _, r := range res.Recommendations {
		if !r.Model.HasTask("coding") {
			t.Errorf("%s is not coding", r.Model.ID)
		}
	}

	nogpu := fixture(t, "nogpu4")
	res = Recommend(nogpu, cat, Options{Context: 32768, Top: 5})
	if res.Status != "ok" {
		t.Fatalf("context status %s: %s", res.Status, res.Message)
	}
	for _, r := range res.Recommendations {
		if r.Model.MaxContext < 32768 {
			t.Errorf("%s does not support 32K context", r.Model.ID)
		}
	}

	res = Recommend(p, cat, Options{Task: "bogus", Context: 8192})
	if res.Status != "invalid" {
		t.Errorf("invalid task status = %s", res.Status)
	}
	res = Recommend(p, cat, Options{Runtime: "bogus", Context: 8192})
	if res.Status != "invalid" {
		t.Errorf("invalid runtime status = %s", res.Status)
	}
}

func TestTieBreakers(t *testing.T) {
	p := fixture(t, "rtx4090")
	mk := func(id string, params float64) catalog.Model {
		return catalog.Model{
			ID: id, Family: "Test", DisplayName: id, ParamsB: params, ActiveParamsB: 8,
			Quant: "Q4_K_M", FileSizeGB: 5, MaxContext: 32768,
			Tasks: []string{"chat"}, License: "test", ReleaseYear: 2024,
			Arch:     catalog.Arch{Layers: 32, KVHeads: 8, HeadDim: 128},
			Runtimes: catalog.Runtimes{Ollama: id},
		}
	}
	cat := &catalog.Catalog{Version: 1, ScoreContext: 8192, Models: []catalog.Model{
		mk("smaller-params", 8.0),
		mk("larger-params", 9.0),
	}}
	res := Recommend(p, cat, Options{Context: 8192})
	if res.Status != "ok" {
		t.Fatalf("status %s", res.Status)
	}
	if res.Recommendations[0].Model.ID != "larger-params" {
		t.Fatalf("tie-breaker chose %s, want larger-params", res.Recommendations[0].Model.ID)
	}
	if math.Abs(res.Recommendations[0].Need.Score-res.Recommendations[1].Need.Score) > 0.05 {
		t.Fatalf("synthetic models were not tied: %.2f vs %.2f",
			res.Recommendations[0].Need.Score, res.Recommendations[1].Need.Score)
	}
}

func TestSafetyMargin(t *testing.T) {
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	p := fixture(t, "rtx3060")
	strict := Recommend(p, cat, Options{Context: 8192, SafetyMargin: 0.30})
	loose := Recommend(p, cat, Options{Context: 8192, SafetyMargin: 0.05})
	if strict.MemoryLimitGB >= loose.MemoryLimitGB {
		t.Errorf("strict limit %.1f should be below loose %.1f", strict.MemoryLimitGB, loose.MemoryLimitGB)
	}
	if strict.Hardware.Score != loose.Hardware.Score {
		t.Errorf("safety margin must not change hardware score")
	}
}
