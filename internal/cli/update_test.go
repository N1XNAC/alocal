package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"alocal/internal/local"
)

func TestUpdateRequiresSource(t *testing.T) {
	isolateLocalState(t)
	t.Setenv("ALOCAL_REPO", "")
	t.Setenv("ALOCAL_BASE_URL", "")
	_, _, err := run(t, "", "update", "--check")
	var ce *CodedError
	if !errors.As(err, &ce) || ce.Code != 2 || !strings.Contains(ce.Msg, "no update source") {
		t.Fatalf("expected missing-source error, got %v", err)
	}
}

func TestUpdateCheckAndDryRun(t *testing.T) {
	isolateLocalState(t)
	base := "http://example.invalid/download"

	out, _, err := run(t, "", "update", "--base-url", base, "--tag", "v2.0.0", "--check", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var check struct {
		Current  string `json:"current"`
		Latest   string `json:"latest"`
		UpToDate bool   `json:"up_to_date"`
		Asset    string `json:"asset"`
	}
	if err := json.Unmarshal([]byte(out), &check); err != nil {
		t.Fatal(err)
	}
	if check.Current != Version || check.Latest != "v2.0.0" || check.UpToDate || check.Asset == "" {
		t.Fatalf("check = %+v", check)
	}

	_, _, err = run(t, "", "update", "--base-url", base, "--tag", "v0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	out, _, err = run(t, "", "update", "--base-url", base, "--tag", "v0.1.0", "--check")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Already up to date") {
		t.Fatalf("check output = %s", out)
	}

	out, _, err = run(t, "", "update", "--base-url", base, "--tag", "v2.0.0", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, base+"/alocal-") || !strings.Contains(out, "Would download") {
		t.Fatalf("dry-run output = %s", out)
	}
}

func TestUpdateUsesPersistedSource(t *testing.T) {
	dir := isolateLocalState(t)
	cfgDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := local.UpdateSource{BaseURL: "http://example.invalid/releases"}
	if err := src.Save(); err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, "", "update", "--tag", "v3.1.4", "--check", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"latest": "v3.1.4"`) {
		t.Fatalf("persisted source ignored: %s", out)
	}
}
