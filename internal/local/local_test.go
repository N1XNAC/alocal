package local

import (
	"os"
	"path/filepath"
	"testing"
)

func isolate(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("ALOCAL_DATA_DIR", filepath.Join(dir, "data"))
	t.Setenv("ALOCAL_CONFIG_DIR", filepath.Join(dir, "config"))
}

func TestModelStoreAddFindRemove(t *testing.T) {
	isolate(t)
	store, err := LoadModels()
	if err != nil {
		t.Fatal(err)
	}
	if len(store.Models) != 0 {
		t.Fatal("expected empty store")
	}
	m := Model{ID: "owner/repo/model-Q4_K_M.gguf", Repo: "owner/repo", File: "model-Q4_K_M.gguf", Path: "/tmp/model.gguf", Quant: "Q4_K_M"}
	if err := store.Add(m); err != nil {
		t.Fatal(err)
	}
	if got, ok := store.Find("model-Q4_K_M.gguf"); !ok || got.ID != m.ID {
		t.Fatalf("Find by suffix = %+v, %v", got, ok)
	}
	if err := store.Add(m); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadModels()
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Models) != 1 {
		t.Fatalf("duplicate add produced %d entries", len(reloaded.Models))
	}
	if removed, err := reloaded.Remove(m.ID); err != nil || !removed {
		t.Fatalf("Remove = %v, %v", removed, err)
	}
	if _, ok := reloaded.Find(m.ID); ok {
		t.Fatal("model still found after remove")
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	isolate(t)
	defaults := DefaultSettings()
	if defaults.Context != 8192 || defaults.GPULayers != -1 {
		t.Fatalf("bad defaults: %+v", defaults)
	}
	s := defaults
	s.Context = 16384
	s.Temperature = 0.2
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.Context != 16384 || got.Temperature != 0.2 {
		t.Fatalf("round trip = %+v", got)
	}
}

func TestMCPRoundTrip(t *testing.T) {
	isolate(t)
	cfg, err := LoadMCP()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.AddServer(MCPServer{Name: "filesystem", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-filesystem"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadMCP()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Servers) != 1 || got.Servers[0].Name != "filesystem" {
		t.Fatalf("mcp config = %+v", got)
	}
	if removed, err := got.RemoveServer("filesystem"); err != nil || !removed {
		t.Fatalf("RemoveServer = %v, %v", removed, err)
	}
	if _, err := os.Stat(MCPFile()); err != nil {
		t.Fatalf("mcp file missing: %v", err)
	}
}
