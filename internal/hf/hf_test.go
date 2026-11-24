package hf

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNormalizeRepo(t *testing.T) {
	cases := map[string]string{
		"https://huggingface.co/bartowski/Qwen2.5-7B-Instruct-GGUF": "bartowski/Qwen2.5-7B-Instruct-GGUF",
		"hf.co/bartowski/Qwen2.5-7B-Instruct-GGUF/":                 "bartowski/Qwen2.5-7B-Instruct-GGUF",
		"hf:bartowski/Qwen2.5-7B-Instruct-GGUF":                     "hf:bartowski/Qwen2.5-7B-Instruct-GGUF",
		"bartowski/Qwen2.5-7B-Instruct-GGUF.git":                    "bartowski/Qwen2.5-7B-Instruct-GGUF",
	}
	for in, want := range cases {
		if got := NormalizeRepo(in); got != want {
			t.Errorf("NormalizeRepo(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPickGGUF(t *testing.T) {
	files := []string{"model-Q3_K_M.gguf", "model-Q4_K_M.gguf", "model-Q8_0.gguf"}
	if got := PickGGUF(files, "Q8_0"); got != "model-Q8_0.gguf" {
		t.Fatalf("PickGGUF(Q8_0) = %q", got)
	}
	if got := PickGGUF(files, ""); got != "model-Q4_K_M.gguf" {
		t.Fatalf("default pick = %q", got)
	}
	if got := PickGGUF(nil, "Q4_K_M"); got != "" {
		t.Fatalf("empty pick = %q", got)
	}
}

func TestVerifyGGUF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model.gguf")
	if err := os.WriteFile(path, []byte("GGUF-ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyGGUF(path); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(t.TempDir(), "bad.gguf")
	if err := os.WriteFile(bad, []byte("oops"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyGGUF(bad); err == nil {
		t.Fatal("bad magic accepted")
	}
}

func TestResolveURLEscapesPath(t *testing.T) {
	c := New()
	url := c.ResolveURL("owner/repo", "sub dir/model-Q4_K_M.gguf")
	if !strings.Contains(url, "/owner/repo/resolve/main/sub%20dir/model-Q4_K_M.gguf") {
		t.Fatalf("url = %s", url)
	}
}

func TestAPIPathsKeepRepositorySlash(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.EscapedPath())
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/tree/main"):
			fmt.Fprint(w, `[]`)
		case strings.HasPrefix(r.URL.Path, "/api/models/"):
			fmt.Fprint(w, `{"id":"owner/repo","siblings":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	if _, err := c.Model(context.Background(), "owner/repo"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Tree(context.Background(), "owner/repo"); err != nil {
		t.Fatal(err)
	}
	want := []string{"/api/models/owner/repo", "/api/models/owner/repo/tree/main"}
	if strings.Join(paths, "|") != strings.Join(want, "|") {
		t.Fatalf("request paths = %v, want %v", paths, want)
	}
}

func TestSearchParsesPublishedMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/models" || r.URL.Query().Get("filter") != "gguf" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[{"id":"acme/Tiny-GGUF","author":"acme","downloads":42,"likes":3,"pipeline_tag":"text-generation","tags":["gguf"],"siblings":[{"rfilename":"tiny-Q4_K_M.gguf"}]}]`)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	results, err := c.Search(context.Background(), "tiny", 10)
	if err != nil || len(results) != 1 {
		t.Fatalf("search = %+v, err = %v", results, err)
	}
	if results[0].ID != "acme/Tiny-GGUF" || results[0].Downloads != 42 || len(results[0].Siblings) != 1 {
		t.Fatalf("bad search result: %+v", results[0])
	}
}

func TestDownloadResumesPartialFileAndVerifies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") == "bytes=4-" {
			w.Header().Set("Content-Length", "3")
			w.Header().Set("Content-Range", "bytes 4-6/7")
			w.WriteHeader(http.StatusPartialContent)
			fmt.Fprint(w, "-ok")
			return
		}
		fmt.Fprint(w, "GGUF-original")
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	dest := filepath.Join(t.TempDir(), "model.gguf")
	if err := os.WriteFile(dest+".part", []byte("GGUF"), 0o644); err != nil {
		t.Fatal(err)
	}
	var lastDone, lastTotal int64
	if err := c.Download(context.Background(), "owner/repo", "model.gguf", dest, func(done, total int64) {
		lastDone, lastTotal = done, total
	}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "GGUF-ok" {
		t.Fatalf("downloaded bytes = %q, want %q", got, "GGUF-ok")
	}
	if lastDone != 7 || lastTotal != 7 {
		t.Fatalf("progress = %d/%d, want 7/7", lastDone, lastTotal)
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatalf("partial file was not consumed: %v", err)
	}
}

func TestCancelledDownloadKeepsPartialFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "64")
		fmt.Fprint(w, "GGUF")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	dest := filepath.Join(t.TempDir(), "model.gguf")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- c.Download(ctx, "owner/repo", "model.gguf", dest, func(done, total int64) {
			if done >= 4 {
				cancel()
			}
		})
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled download reported success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled download did not return")
	}
	part, err := os.ReadFile(dest + ".part")
	if err != nil || string(part) != "GGUF" {
		t.Fatalf("partial data = %q, err = %v; want GGUF", part, err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("cancelled download was published: %v", err)
	}
}

func TestDownloadRejectsIncompleteBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "16")
		io.WriteString(w, "GGUF-short")
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	dest := filepath.Join(t.TempDir(), "model.gguf")
	if err := c.Download(context.Background(), "owner/repo", "model.gguf", dest, nil); err == nil {
		t.Fatal("incomplete body accepted")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("incomplete download was published: %v", err)
	}
}
