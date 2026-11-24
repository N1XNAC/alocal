package runtime

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChooseAsset(t *testing.T) {
	assets := []releaseAsset{
		{Name: "llama-b1-bin-ubuntu-cuda-12.8-x64.tar.gz"},
		{Name: "llama-b1-bin-ubuntu-x64.tar.gz"},
		{Name: "llama-b1-bin-ubuntu-arm64.tar.gz"},
		{Name: "llama-b1-bin-macos-x64.tar.gz"},
		{Name: "llama-b1-bin-macos-arm64.tar.gz"},
		{Name: "llama-b1-bin-win-cpu-x64.zip"},
		{Name: "llama-b1-bin-win-cpu-arm64.zip"},
	}
	cases := map[string]string{
		"linux/amd64":   "llama-b1-bin-ubuntu-x64.tar.gz",
		"linux/arm64":   "llama-b1-bin-ubuntu-arm64.tar.gz",
		"darwin/amd64":  "llama-b1-bin-macos-x64.tar.gz",
		"darwin/arm64":  "llama-b1-bin-macos-arm64.tar.gz",
		"windows/amd64": "llama-b1-bin-win-cpu-x64.zip",
		"windows/arm64": "llama-b1-bin-win-cpu-arm64.zip",
	}
	for platform, want := range cases {
		parts := strings.Split(platform, "/")
		got, err := chooseAsset(assets, parts[0], parts[1])
		if err != nil {
			t.Errorf("%s: %v", platform, err)
			continue
		}
		if got.Name != want {
			t.Errorf("%s chose %s, want %s", platform, got.Name, want)
		}
	}
	if _, err := chooseAsset(assets, "plan9", "amd64"); err == nil {
		t.Fatal("unsupported platform accepted")
	}
}

func TestExtractZipFindsServer(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "llama.zip")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("llama-b1/bin/llama-server.exe")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("binary"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	target := filepath.Join(dir, "extracted", "llama-server.exe")
	if err := extractZip(archive, target); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "binary" {
		t.Fatalf("extracted binary = %q, %v", data, err)
	}
}

func TestExtractTarGzFindsServer(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "llama.tar.gz")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	body := []byte("binary")
	if err := tw.WriteHeader(&tar.Header{Name: "llama-b1/bin/llama-server", Mode: 0o755, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	_ = f.Close()

	target := filepath.Join(dir, "extracted", "llama-server")
	if err := extractTarGz(archive, target); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "binary" {
		t.Fatalf("extracted binary = %q, %v", data, err)
	}
}

func TestChatStreamSSE(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`data: {"choices":[{"delta":{"content":"Hello"}}]}`,
			`data: {"choices":[{"delta":{"content":" world"}}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"completion_tokens":2}}`,
			`data: [DONE]`,
		}
		for _, c := range chunks {
			_, _ = fmt.Fprintln(w, c)
		}
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	if err := client.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	var doneTokens int
	done := 0
	err := client.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, GenOptions{Temperature: 0.1}, func(token string, finished bool, tokens int) {
		if finished {
			done++
			doneTokens = tokens
			return
		}
		text.WriteString(token)
	})
	if err != nil {
		t.Fatal(err)
	}
	if text.String() != "Hello world" {
		t.Fatalf("streamed text = %q", text.String())
	}
	if done != 1 || doneTokens != 2 {
		t.Fatalf("done=%d tokens=%d", done, doneTokens)
	}
}
