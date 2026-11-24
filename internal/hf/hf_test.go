package hf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
