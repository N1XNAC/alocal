package catalog

import (
	"strings"
	"testing"
)

func TestEmbeddedCatalogValid(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Models) < 40 {
		t.Fatalf("catalog has %d models, want at least 40", len(c.Models))
	}
	if errs := c.Validate(); len(errs) > 0 {
		for _, e := range errs {
			t.Error(e)
		}
	}
	seen := map[string]bool{}
	families := map[string]bool{}
	for _, m := range c.Models {
		if seen[m.ID] {
			t.Errorf("duplicate id %s", m.ID)
		}
		seen[m.ID] = true
		families[m.Family] = true
		if m.ModelScore <= 0 {
			t.Errorf("%s has no score", m.ID)
		}
		if m.ScoreContext != 8192 {
			t.Errorf("%s score_context = %d", m.ID, m.ScoreContext)
		}
	}
	if len(families) < 10 {
		t.Errorf("only %d families", len(families))
	}
}

func TestFindAndFilter(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	m, ok := c.Find("qwen2.5-coder-7b-instruct-q4_k_m")
	if !ok {
		t.Fatal("expected coder model")
	}
	if !m.HasTask("coding") || m.RuntimeTag("ollama") == "" {
		t.Errorf("unexpected model metadata: %+v", m)
	}
	models := c.Filter(Filter{Task: "coding", Quant: "Q4_K_M", Runtime: "ollama", MinContext: 32768})
	if len(models) == 0 {
		t.Fatal("no coding models after filter")
	}
	for _, m := range models {
		if !m.HasTask("coding") || m.Quant != "Q4_K_M" || m.MaxContext < 32768 {
			t.Errorf("filter leak: %+v", m)
		}
	}
}

func TestContextCap(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range c.Models {
		if m.MaxContext == 4096 {
			need := m.Need(8192)
			if !need.ContextCapped || need.Context != 4096 {
				t.Fatalf("%s should cap context: %+v", m.ID, need)
			}
			return
		}
	}
	t.Fatal("no 4K-context model found")
}

func TestRecomputeScoresStable(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	cp := *c
	cp.Models = append([]Model(nil), c.Models...)
	for i := range cp.Models {
		cp.Models[i].ModelScore = 0
	}
	cp.RecomputeScores()
	for i := range c.Models {
		if c.Models[i].ModelScore != cp.Models[i].ModelScore {
			t.Errorf("%s: stored %.1f recomputed %.1f", c.Models[i].ID, c.Models[i].ModelScore, cp.Models[i].ModelScore)
		}
	}
}

func TestValidationCatchesProblems(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	bad := *c
	bad.Models = append([]Model(nil), c.Models[:2]...)
	bad.Models[0].ID = "Bad ID"
	bad.Models[0].Quant = "Q2"
	bad.Models[0].Tasks = []string{"nonsense"}
	bad.Models[0].Arch = Arch{}
	bad.Models[0].Runtimes.Ollama = ""
	bad.Models[0].ModelScore = 0
	bad.Models[1].ID = bad.Models[0].ID
	errs := bad.Validate()
	joined := ""
	for _, e := range errs {
		joined += e.Error() + "\n"
	}
	for _, want := range []string{"lowercase slug", "unknown quant", "unknown task", "arch.layers", "runtimes.ollama", "model_score", "duplicate id"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing validation error %q in:\n%s", want, joined)
		}
	}
}
