package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"llmfit/internal/catalog"
	"llmfit/internal/match"
)

func run(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	app := &App{Out: &out, Err: &errOut, In: strings.NewReader(stdin), Color: false}
	root := NewRoot(app)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errOut.String(), err
}

func fixturePath(name string) string {
	return filepath.Join("..", "..", "tests", "fixtures", name+".json")
}

func TestScoreCommand(t *testing.T) {
	out, _, err := run(t, "", "score", "--mock-hardware", fixturePath("rtx3060"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "59.6\tHigh" {
		t.Errorf("score output = %q", out)
	}
}

func TestScanJSON(t *testing.T) {
	out, _, err := run(t, "", "scan", "--json", "--mock-hardware", fixturePath("m2air8"))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Profile struct {
			RAM struct {
				TotalGB float64 `json:"total_gb"`
			} `json:"ram"`
			GPUs []struct {
				Kind string `json:"kind"`
			} `json:"gpus"`
			Sources []any `json:"sources"`
		} `json:"profile"`
		Hardware struct {
			Score float64 `json:"score"`
			Tier  string  `json:"tier"`
		} `json:"hardware"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if got.Profile.RAM.TotalGB != 8 || len(got.Profile.GPUs) != 1 || got.Profile.GPUs[0].Kind != "unified" {
		t.Errorf("unexpected profile: %+v", got.Profile)
	}
	if got.Hardware.Score <= 0 || got.Hardware.Tier == "" {
		t.Errorf("missing hardware score: %+v", got.Hardware)
	}
	if len(got.Profile.Sources) != 0 {
		t.Errorf("sources should be hidden without --verbose")
	}
}

func TestRecommendJSONAndExitCode(t *testing.T) {
	out, _, err := run(t, "", "recommend", "--json", "--mock-hardware", fixturePath("rtx3060"), "--top", "3")
	if err != nil {
		t.Fatal(err)
	}
	var res match.Result
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res.Status != "ok" || len(res.Recommendations) == 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if res.Recommendations[0].Model.ID != "yi-1.5-9b-chat-q5_k_m" {
		t.Errorf("top = %s", res.Recommendations[0].Model.ID)
	}

	_, _, err = run(t, "", "recommend", "--mock-hardware", fixturePath("nogpu4"), "--context", "131072")
	var ce *CodedError
	if !errors.As(err, &ce) || ce.Code != 3 {
		t.Fatalf("expected exit code 3 for no fit, got %v", err)
	}
}

func TestExplainJSON(t *testing.T) {
	out, _, err := run(t, "", "explain", "qwen2.5-7b-instruct-q4_k_m", "--json", "--mock-hardware", fixturePath("rtx3060"))
	if err != nil {
		t.Fatal(err)
	}
	var got explainJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Model.ID == "" || got.Need.RequiredGB <= 0 || got.Throughput.TokensPerSec <= 0 {
		t.Errorf("incomplete explain output: %+v", got)
	}
	if !got.Fit {
		t.Errorf("7B Q4 should fit a 3060: %+v", got)
	}
}

func TestModelsCommand(t *testing.T) {
	out, _, err := run(t, "", "models", "--json", "--task", "coding")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Count  int             `json:"count"`
		Models []catalog.Model `json:"models"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Count == 0 || got.Count != len(got.Models) {
		t.Errorf("bad count: %d", got.Count)
	}
	for _, m := range got.Models {
		if !m.HasTask("coding") {
			t.Errorf("non-coding model %s", m.ID)
		}
	}
}

func TestCatalogValidateCommand(t *testing.T) {
	out, _, err := run(t, "", "catalog", "validate")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "OK") {
		t.Errorf("unexpected output: %s", out)
	}

	_, _, err = run(t, "", "catalog", "validate", "--file", "/no/such/catalog.json")
	var ce *CodedError
	if !errors.As(err, &ce) || ce.Code != 1 {
		t.Errorf("expected exit 1 for missing file, got %v", err)
	}
}

func TestPullDryRun(t *testing.T) {
	out, _, err := run(t, "", "pull", "qwen2.5-7b-instruct-q4_k_m", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "ollama pull qwen2.5:7b-instruct-q4_K_M" {
		t.Errorf("pull dry-run = %q", out)
	}
}

func TestUsageErrors(t *testing.T) {
	_, _, err := run(t, "", "explain", "not-a-real-model")
	var ce *CodedError
	if !errors.As(err, &ce) || ce.Code != 2 {
		t.Fatalf("unknown model: %v", err)
	}
	_, _, err = run(t, "", "scan", "--not-a-flag")
	if !errors.As(err, &ce) || ce.Code != 2 {
		t.Fatalf("unknown flag: %v", err)
	}
	_, _, err = run(t, "", "recommend", "--task", "bogus", "--mock-hardware", fixturePath("rtx3060"))
	if !errors.As(err, &ce) || ce.Code != 2 {
		t.Fatalf("bad task: %v", err)
	}
}

func TestCatalogValidateBadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	bad := `{"version":1,"score_context":8192,"models":[{"id":"x","params_b":1}]}`
	if err := os.WriteFile(path, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, "", "catalog", "validate", "--file", path)
	var ce *CodedError
	if !errors.As(err, &ce) || ce.Code != 2 {
		t.Fatalf("expected exit 2, got %v", err)
	}
	if !strings.Contains(out, "validation error") {
		t.Errorf("missing validation output: %s", out)
	}
}
