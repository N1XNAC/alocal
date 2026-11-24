// Package update downloads and installs newer Alocal release binaries.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Config describes an update source and target.
type Config struct {
	Repo           string
	BaseURL        string // release download base, e.g. github.com/owner/repo/releases/latest/download
	Version        string // "latest" (default) or an explicit tag such as v0.2.0
	CurrentVersion string
	Asset          string // defaults to alocal-<goos>-<goarch>
	Executable     string // defaults to os.Executable()
	APIURL         string // defaults to https://api.github.com (or GITHUB_API_URL)
	ReleaseBase    string // advanced override for release asset downloads
	HTTPClient     *http.Client
}

// Release is the resolved target of an update.
type Release struct {
	Tag     string
	BaseURL string
	Assets  []string
}

// Result describes a completed update.
type Result struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Asset      string `json:"asset"`
	Path       string `json:"path"`
	BackupPath string `json:"backup_path,omitempty"`
	Restart    bool   `json:"restart_required"`
}

func (c *Config) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

// AssetName returns the release binary name for this OS/architecture.
func (c *Config) AssetName() string {
	if c.Asset != "" {
		return c.Asset
	}
	name := fmt.Sprintf("alocal-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// Resolve determines the release tag and asset download base.
func (c *Config) Resolve(ctx context.Context) (*Release, error) {
	tag := strings.TrimSpace(c.Version)
	if tag == "" {
		tag = "latest"
	}
	if c.BaseURL != "" {
		return &Release{Tag: tag, BaseURL: strings.TrimRight(c.BaseURL, "/")}, nil
	}
	if strings.TrimSpace(c.Repo) == "" {
		return nil, fmt.Errorf("no update source configured (set ALOCAL_REPO or run `alocal update --repo owner/repo`)")
	}

	api := strings.TrimRight(c.APIURL, "/")
	if api == "" {
		api = os.Getenv("GITHUB_API_URL")
	}
	if api == "" {
		api = "https://api.github.com"
	}

	var endpoint string
	if tag == "latest" {
		endpoint = fmt.Sprintf("%s/repos/%s/releases/latest", api, c.Repo)
	} else {
		endpoint = fmt.Sprintf("%s/repos/%s/releases/tags/%s", api, c.Repo, url.PathEscape(tag))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if token := firstEnv("GITHUB_TOKEN", "GH_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("query GitHub release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("GitHub returned %d for %s: %s", resp.StatusCode, c.Repo, strings.TrimSpace(string(body)))
	}
	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("parse GitHub release: %w", err)
	}
	if release.TagName == "" {
		return nil, fmt.Errorf("release has no tag name")
	}
	base := c.ReleaseBase
	if base == "" {
		base = fmt.Sprintf("https://github.com/%s/releases/download/%s", c.Repo, release.TagName)
	}
	assets := make([]string, 0, len(release.Assets))
	for _, a := range release.Assets {
		assets = append(assets, a.Name)
	}
	return &Release{Tag: release.TagName, BaseURL: strings.TrimRight(base, "/"), Assets: assets}, nil
}

// HasAsset reports whether the release publishes the expected binary asset.
func (r *Release) HasAsset(asset string) bool {
	if len(r.Assets) == 0 {
		return true // mirror or API response without asset metadata
	}
	for _, a := range r.Assets {
		if filepath.Base(a) == asset {
			return true
		}
	}
	return false
}

// IsCurrent reports whether current is the same release as target.
func IsCurrent(current, target string) bool {
	return NormalizeVersion(current) != "" && NormalizeVersion(current) == NormalizeVersion(target)
}

// NormalizeVersion strips a leading v and surrounding whitespace.
func NormalizeVersion(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

// Apply downloads, verifies, and installs the release binary.
func (c *Config) Apply(ctx context.Context, rel *Release) (Result, error) {
	asset := c.AssetName()
	if !rel.HasAsset(asset) {
		return Result{}, fmt.Errorf("release %s has no asset %s", rel.Tag, asset)
	}

	assetPath, sum, err := c.download(ctx, rel.BaseURL+"/"+asset)
	if err != nil {
		return Result{}, fmt.Errorf("download %s: %w", asset, err)
	}
	defer os.Remove(assetPath)
	sumsPath, _, err := c.download(ctx, rel.BaseURL+"/checksums.txt")
	if err != nil {
		return Result{}, fmt.Errorf("download checksums.txt: %w", err)
	}
	defer os.Remove(sumsPath)
	checksums, err := os.ReadFile(sumsPath)
	if err != nil {
		return Result{}, err
	}
	expected, err := parseChecksum(checksums, asset)
	if err != nil {
		return Result{}, err
	}
	if !strings.EqualFold(expected, sum) {
		return Result{}, fmt.Errorf("checksum mismatch: expected %s, got %s", expected, sum)
	}

	target := c.Executable
	if target == "" {
		target, err = os.Executable()
		if err != nil {
			return Result{}, fmt.Errorf("locate current executable: %w", err)
		}
	}
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	}
	mode := os.FileMode(0o755)
	if info, err := os.Stat(target); err == nil {
		mode = info.Mode().Perm()
	}

	staged, err := os.CreateTemp(filepath.Dir(target), ".alocal-update-*")
	if err != nil {
		return Result{}, fmt.Errorf("stage update: %w", err)
	}
	stagedPath := staged.Name()
	defer os.Remove(stagedPath)
	src, err := os.Open(assetPath)
	if err != nil {
		staged.Close()
		return Result{}, err
	}
	_, copyErr := io.Copy(staged, src)
	closeErr := staged.Close()
	src.Close()
	if copyErr != nil || closeErr != nil {
		if copyErr != nil {
			return Result{}, copyErr
		}
		return Result{}, closeErr
	}
	if err := os.Chmod(stagedPath, mode); err != nil {
		return Result{}, err
	}

	res := Result{From: c.CurrentVersion, To: rel.Tag, Asset: asset, Path: target, Restart: true}
	if runtime.GOOS == "windows" {
		backup := target + ".old"
		_ = os.Remove(backup)
		if err := os.Rename(target, backup); err != nil {
			return Result{}, fmt.Errorf("rename current executable: %w", err)
		}
		if err := os.Rename(stagedPath, target); err != nil {
			_ = os.Rename(backup, target)
			return Result{}, fmt.Errorf("install update: %w", err)
		}
		res.BackupPath = backup
		return res, nil
	}
	if err := os.Rename(stagedPath, target); err != nil {
		return Result{}, fmt.Errorf("install update: %w", err)
	}
	return res, nil
}

// download streams a URL to a temporary file and returns its path and SHA-256.
func (c *Config) download(ctx context.Context, rawURL string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	tmp, err := os.CreateTemp("", "alocal-download-*")
	if err != nil {
		return "", "", err
	}
	hasher := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(tmp, hasher), resp.Body)
	closeErr := tmp.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(tmp.Name())
		if copyErr != nil {
			return "", "", copyErr
		}
		return "", "", closeErr
	}
	return tmp.Name(), hex.EncodeToString(hasher.Sum(nil)), nil
}

func parseChecksum(data []byte, asset string) (string, error) {
	want := filepath.Base(asset)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := filepath.Base(strings.TrimPrefix(fields[1], "*"))
		if name == want {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("no SHA-256 checksum for %s in checksums.txt", asset)
}

func firstEnv(names ...string) string {
	for _, name := range names {
		if v := os.Getenv(name); v != "" {
			return v
		}
	}
	return ""
}
