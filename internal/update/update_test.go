package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testAsset() string {
	name := fmt.Sprintf("alocal-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

func newFakeRelease(t *testing.T, binary []byte, checksum string) (*httptest.Server, string) {
	t.Helper()
	asset := testAsset()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/repos/owner/repo/releases/"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"tag_name":"v9.9.9","assets":[{"name":%q},{"name":"checksums.txt"}]}`, asset)
		case strings.HasSuffix(r.URL.Path, "/"+asset):
			_, _ = w.Write(binary)
		case strings.HasSuffix(r.URL.Path, "/checksums.txt"):
			_, _ = fmt.Fprintln(w, checksum+"  "+asset)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, asset
}

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestResolveAndApply(t *testing.T) {
	binary := []byte("new alocal binary")
	srv, asset := newFakeRelease(t, binary, hashOf(binary))
	target := filepath.Join(t.TempDir(), "alocal")
	if runtime.GOOS == "windows" {
		target += ".exe"
	}
	if err := os.WriteFile(target, []byte("old alocal binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{
		Repo:           "owner/repo",
		APIURL:         srv.URL,
		ReleaseBase:    srv.URL + "/download",
		CurrentVersion: "0.1.0",
		Executable:     target,
	}
	rel, err := cfg.Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rel.Tag != "v9.9.9" || !rel.HasAsset(asset) {
		t.Fatalf("release = %+v", rel)
	}
	res, err := cfg.Apply(context.Background(), rel)
	if err != nil {
		t.Fatal(err)
	}
	if res.To != "v9.9.9" || res.Asset != asset || !res.Restart {
		t.Fatalf("result = %+v", res)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(binary) {
		t.Fatalf("installed binary = %q", got)
	}
}

func TestApplyRejectsChecksumMismatch(t *testing.T) {
	srv, _ := newFakeRelease(t, []byte("new binary"), strings.Repeat("0", 64))
	target := filepath.Join(t.TempDir(), "alocal")
	if err := os.WriteFile(target, []byte("original"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{BaseURL: srv.URL + "/download", Executable: target, Version: "v9.9.9"}
	rel, err := cfg.Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.Apply(context.Background(), rel); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "original" {
		t.Fatalf("target was modified: %q", got)
	}
}

func TestVersionHelpers(t *testing.T) {
	if !IsCurrent("0.1.0", "v0.1.0") {
		t.Fatal("v-prefixed tags should compare equal")
	}
	if IsCurrent("0.1.0", "0.2.0") {
		t.Fatal("different versions compared equal")
	}
	if NormalizeVersion(" v1.2.3 ") != "1.2.3" {
		t.Fatalf("normalize = %q", NormalizeVersion(" v1.2.3 "))
	}
	if _, err := parseChecksum([]byte("abc  *alocal-linux-amd64\n"), "alocal-linux-amd64"); err != nil {
		t.Fatal(err)
	}
}
