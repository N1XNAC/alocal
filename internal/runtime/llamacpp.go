package runtime

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"alocal/internal/local"
)

const releasesAPI = "https://api.github.com/repos/ggml-org/llama.cpp/releases?per_page=5"

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// Ensure locates or downloads a llama-server binary.
func Ensure(ctx context.Context) (string, error) {
	return EnsureWithProgress(ctx, nil)
}

// EnsureWithProgress is Ensure with an optional status callback. The callback
// is called on the goroutine doing the work, so callers must send the message
// through a safe channel (for example tea.Program.Send).
func EnsureWithProgress(ctx context.Context, progress func(string)) (string, error) {
	report := func(s string) {
		if progress != nil {
			progress(s)
		}
	}
	if override := os.Getenv("ALOCAL_LLAMA_SERVER"); override != "" {
		report("Using ALOCAL_LLAMA_SERVER override")
		return override, nil
	}
	if path, err := exec.LookPath("llama-server"); err == nil {
		report("Using llama-server from PATH")
		return path, nil
	}

	name := "llama-server"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	target := filepath.Join(local.RuntimeDir(), name)
	if info, err := os.Stat(target); err == nil && info.Size() > 0 {
		report("Using cached llama.cpp runtime")
		return target, nil
	}

	report("llama.cpp runtime not found - downloading it once from GitHub...")
	assets, err := fetchAssets(ctx)
	if err != nil {
		return "", fmt.Errorf("find llama.cpp release: %w", err)
	}
	asset, err := chooseAsset(assets, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", err
	}
	report("Fetching " + asset.Name + " (this is a one-time download)")
	if err := extractAsset(ctx, asset, target); err != nil {
		return "", fmt.Errorf("install llama.cpp runtime: %w", err)
	}
	if err := os.Chmod(target, 0o755); err != nil {
		return "", err
	}
	report("llama.cpp runtime installed")
	return target, nil
}

func fetchAssets(ctx context.Context) ([]releaseAsset, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesAPI, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub returned %d", resp.StatusCode)
	}
	var releases []struct {
		Assets []releaseAsset `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}
	for _, rel := range releases {
		if len(rel.Assets) > 0 {
			return rel.Assets, nil
		}
	}
	return nil, fmt.Errorf("no release assets found")
}

var excluded = []string{"cuda", "rocm", "vulkan", "openvino", "sycl", "s390x", "snapdragon", "cudart"}

func chooseAsset(assets []releaseAsset, goos, goarch string) (releaseAsset, error) {
	var want string
	switch goos + "/" + goarch {
	case "linux/amd64":
		want = "bin-ubuntu-x64.tar.gz"
	case "linux/arm64":
		want = "bin-ubuntu-arm64.tar.gz"
	case "darwin/amd64":
		want = "bin-macos-x64.tar.gz"
	case "darwin/arm64":
		want = "bin-macos-arm64.tar.gz"
	case "windows/amd64":
		want = "bin-win-cpu-x64.zip"
	case "windows/arm64":
		want = "bin-win-cpu-arm64.zip"
	default:
		return releaseAsset{}, fmt.Errorf("unsupported platform %s/%s for llama.cpp", goos, goarch)
	}

	for _, a := range assets {
		name := strings.ToLower(a.Name)
		if strings.Contains(name, want) {
			blocked := false
			for _, bad := range excluded {
				if strings.Contains(name, bad) {
					blocked = true
					break
				}
			}
			if !blocked {
				return a, nil
			}
		}
	}
	return releaseAsset{}, fmt.Errorf("no llama.cpp asset matching %s", want)
}

func extractAsset(ctx context.Context, asset releaseAsset, target string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.BrowserDownloadURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %d", resp.StatusCode)
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), "llama-*.archive")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if strings.HasSuffix(strings.ToLower(asset.Name), ".zip") {
		return extractZip(tmp.Name(), target)
	}
	if strings.HasSuffix(strings.ToLower(asset.Name), ".tar.gz") || strings.HasSuffix(strings.ToLower(asset.Name), ".tgz") {
		return extractTarGz(tmp.Name(), target)
	}
	return fmt.Errorf("unsupported archive %s", asset.Name)
}

func wantedArchiveEntry(base, binary string) bool {
	if base == binary || strings.HasPrefix(base, binary+".") {
		return true
	}
	lower := strings.ToLower(base)
	return strings.HasSuffix(lower, ".dll") || strings.HasSuffix(lower, ".so") ||
		strings.Contains(lower, ".so.") || strings.HasSuffix(lower, ".dylib")
}

func extractZip(archivePath, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer r.Close()
	binary := filepath.Base(target)
	found := false
	for _, f := range r.File {
		base := filepath.Base(f.Name)
		if f.FileInfo().IsDir() || !wantedArchiveEntry(base, binary) {
			continue
		}
		src, err := f.Open()
		if err != nil {
			return err
		}
		dest := filepath.Join(filepath.Dir(target), base)
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			src.Close()
			return err
		}
		_, copyErr := io.Copy(out, src)
		out.Close()
		src.Close()
		if copyErr != nil {
			return copyErr
		}
		if base == binary {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("llama-server binary not found in %s", filepath.Base(archivePath))
	}
	return nil
}

func extractTarGz(archivePath, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	binary := filepath.Base(target)
	found := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		base := filepath.Base(hdr.Name)
		if hdr.Typeflag != tar.TypeReg || !wantedArchiveEntry(base, binary) {
			continue
		}
		dest := filepath.Join(filepath.Dir(target), base)
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, tr)
		out.Close()
		if copyErr != nil {
			return copyErr
		}
		if base == binary {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("llama-server binary not found in %s", filepath.Base(archivePath))
	}
	return nil
}
