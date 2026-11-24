package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"alocal/internal/local"
)

func isolateLocalState(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("ALOCAL_DATA_DIR", filepath.Join(dir, "data"))
	t.Setenv("ALOCAL_CONFIG_DIR", filepath.Join(dir, "config"))
	return dir
}

func TestChatMissingLocalModel(t *testing.T) {
	isolateLocalState(t)
	_, _, err := run(t, "", "chat", "not-downloaded.gguf")
	var ce *CodedError
	if !errors.As(err, &ce) || ce.Code != 2 || !strings.Contains(ce.Msg, "is not downloaded") {
		t.Fatalf("expected missing-GGUF error, got %v", err)
	}
	if !strings.Contains(ce.Msg, "alocal pull not-downloaded.gguf") {
		t.Fatalf("missing pull hint: %s", ce.Msg)
	}
}

func TestChatWithoutModelStillStarts(t *testing.T) {
	isolateLocalState(t)
	_, _, err := run(t, "", "chat")
	var ce *CodedError
	if !errors.As(err, &ce) || ce.Code != 1 || !strings.Contains(ce.Msg, "requires an interactive terminal") {
		t.Fatalf("expected TTY guard (not a missing-model error), got %v", err)
	}
}

func TestChatAutoRecommendsAndGuidesDownload(t *testing.T) {
	isolateLocalState(t)
	_, errOut, err := run(t, "", "chat", "--auto", "--mock-hardware", fixturePath("rtx3060"))
	if err == nil {
		t.Fatal("expected TTY error in non-interactive tests")
	}
	if !strings.Contains(errOut, "Auto-selected:") {
		t.Fatalf("missing auto-selection: %s", errOut)
	}
	if !strings.Contains(errOut, "not downloaded") || !strings.Contains(errOut, "alocal pull") {
		t.Fatalf("missing download guidance: %s", errOut)
	}
}

func TestChatAutoUsesDownloadedRecommendation(t *testing.T) {
	isolateLocalState(t)
	store := &local.ModelStore{Version: 1}
	path := filepath.Join(t.TempDir(), "yi-Q5_K_M.gguf")
	if err := os.WriteFile(path, []byte("GGUFfake"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(local.Model{
		ID:   "bartowski/Yi-1.5-9B-Chat-GGUF/Yi-1.5-9B-Chat-Q5_K_M.gguf",
		Repo: "bartowski/Yi-1.5-9B-Chat-GGUF", File: "Yi-1.5-9B-Chat-Q5_K_M.gguf",
		Path: path, Quant: "Q5_K_M", SizeBytes: 8,
	}); err != nil {
		t.Fatal(err)
	}

	_, errOut, err := run(t, "", "chat", "--auto", "--mock-hardware", fixturePath("rtx3060"))
	if err == nil {
		t.Fatal("expected TTY error in non-interactive tests")
	}
	if strings.Contains(errOut, "not downloaded") {
		t.Fatalf("should use the downloaded recommendation: %s", errOut)
	}
	if !strings.Contains(errOut, "Auto-selected:") {
		t.Fatalf("missing auto-selection: %s", errOut)
	}
}
