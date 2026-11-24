package cli

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"alocal/internal/local"
)

func fakeHFServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/models/"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"id":"bartowski/Test-GGUF","siblings":[{"rfilename":"model-Q4_K_M.gguf"}]}`)
		case strings.Contains(r.URL.Path, "/resolve/main/"):
			_, _ = w.Write([]byte(body))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPullDownloadsAndRegistersGGUF(t *testing.T) {
	dir := isolateLocalState(t)
	srv := fakeHFServer(t, "GGUF"+strings.Repeat("x", 128))
	t.Setenv("HF_ENDPOINT", srv.URL)

	out, _, err := run(t, "", "pull", "hf:bartowski/Test-GGUF", "--quant", "Q4_K_M", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Downloaded bartowski/Test-GGUF/model-Q4_K_M.gguf") {
		t.Fatalf("unexpected output: %s", out)
	}

	store, err := local.LoadModels()
	if err != nil {
		t.Fatal(err)
	}
	if len(store.Models) != 1 || store.Models[0].Quant != "Q4_K_M" {
		t.Fatalf("registry = %+v", store.Models)
	}
	data, err := os.ReadFile(store.Models[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data[:4]) != "GGUF" {
		t.Fatalf("downloaded file has bad magic: %q", data[:4])
	}
	if !strings.HasPrefix(store.Models[0].Path, filepath.Join(dir, "data", "models")) {
		t.Fatalf("unexpected path: %s", store.Models[0].Path)
	}
}

func TestPullRejectsInvalidGGUF(t *testing.T) {
	isolateLocalState(t)
	srv := fakeHFServer(t, "not-a-gguf")
	t.Setenv("HF_ENDPOINT", srv.URL)

	_, _, err := run(t, "", "pull", "hf:bartowski/Test-GGUF", "--file", "model-Q4_K_M.gguf", "--yes")
	var ce *CodedError
	if !errors.As(err, &ce) || ce.Code != 1 || !strings.Contains(ce.Msg, "not a valid GGUF") {
		t.Fatalf("expected invalid GGUF error, got %v", err)
	}
	store, _ := local.LoadModels()
	if len(store.Models) != 0 {
		t.Fatalf("invalid model was registered: %+v", store.Models)
	}
}
