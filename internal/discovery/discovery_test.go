package discovery

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"alocal/internal/hf"
	"alocal/internal/local"
)

func TestEmbeddedCatalogHasRealDistinctRepositories(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Models) < 300 {
		t.Fatalf("models = %d, want >= 300", len(c.Models))
	}
	if got := Count(c.Models); got != len(c.Models) {
		t.Fatalf("distinct = %d, entries = %d", got, len(c.Models))
	}
	for _, e := range c.Models {
		if !strings.Contains(e.Repo, "/") || e.URL != "https://huggingface.co/"+e.Repo {
			t.Fatalf("bad repository metadata: %+v", e)
		}
	}
}

func TestEveryRequiredCategoryHasEntries(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, cat := range Categories[:len(Categories)-1] {
		n := 0
		for _, e := range c.Models {
			if e.HasCategory(cat.ID) {
				n++
			}
		}
		if n == 0 {
			t.Fatalf("category %s has no entries", cat.ID)
		}
	}
}

func TestSearchAndMerge(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	var found []Entry
	for _, e := range c.Models {
		if e.Matches("coder q4_k_m") {
			found = append(found, e)
		}
	}
	if len(found) == 0 {
		t.Fatal("search returned no models")
	}
	merged := Merge(found, []Entry{{Repo: found[0].Repo, DisplayName: "duplicate", Downloaded: true}})
	if len(merged) != len(found) {
		t.Fatalf("merge count = %d, want %d", len(merged), len(found))
	}
	if !merged[0].Downloaded {
		t.Fatal("local copy should win during merge")
	}
}

func TestFromLocalAndLocalEntries(t *testing.T) {
	store := &local.ModelStore{Version: 1, Models: []local.Model{{
		ID: "owner/repo/model-Q4_K_M.gguf", Repo: "owner/repo", File: "model-Q4_K_M.gguf",
		Quant: "Q4_K_M", SizeBytes: 4_000_000_000,
	}}}
	entries := LocalEntries(store)
	if len(entries) != 1 || !entries[0].Downloaded {
		t.Fatalf("bad local entries: %+v", entries)
	}
	if !entries[0].HasCategory(CategoryDownloaded) {
		t.Fatal("downloaded category missing")
	}
	if entries[0].SizeGB() != 4 {
		t.Fatalf("size = %.2f, want 4", entries[0].SizeGB())
	}
}

func TestFetchAndEnrichUseRealHubMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/models" && r.URL.RawQuery != "":
			fmt.Fprint(w, `[{"id":"acme/Tiny-1B-Instruct-GGUF","author":"acme","downloads":42,"likes":3,"pipeline_tag":"text-generation","tags":["gguf","conversational","license:apache-2.0"],"siblings":[{"rfilename":"Tiny-1B-Instruct-Q4_K_M.gguf"}]}]`)
		case strings.HasSuffix(r.URL.Path, "/tree/main"):
			fmt.Fprint(w, `[{"type":"file","path":"Tiny-1B-Instruct-Q4_K_M.gguf","size":850000000},{"type":"file","path":"README.md","size":12}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	client := &hf.Client{BaseURL: srv.URL, HTTP: srv.Client()}

	entries, err := Fetch(context.Background(), client, 10)
	if err != nil || len(entries) != 1 {
		t.Fatalf("fetch = %+v, err = %v", entries, err)
	}
	e := entries[0]
	if e.Publisher != "acme" || e.ParamsB != 1 || !e.HasCategory(CategoryLightweight) {
		t.Fatalf("bad parsed entry: %+v", e)
	}
	if e.File.SizeBytes != 0 {
		t.Fatal("dynamic entries must not invent sizes before enrichment")
	}
	if err := Enrich(context.Background(), client, &e); err != nil {
		t.Fatal(err)
	}
	if e.File.SizeBytes != 850000000 || e.File.Quant != "Q4_K_M" {
		t.Fatalf("bad enriched file: %+v", e.File)
	}
}
