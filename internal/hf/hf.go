// Package hf downloads GGUF files from Hugging Face.
package hf

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Client talks to the Hugging Face Hub.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// New builds a client using HF_ENDPOINT and HF_TOKEN when set.
func New() *Client {
	base := os.Getenv("HF_ENDPOINT")
	if base == "" {
		base = "https://huggingface.co"
	}
	token := os.Getenv("HF_TOKEN")
	if token == "" {
		token = os.Getenv("HUGGING_FACE_HUB_TOKEN")
	}
	return &Client{BaseURL: strings.TrimRight(base, "/"), Token: token, HTTP: &http.Client{}}
}

// NormalizeRepo accepts hf.co/owner/repo, URLs, and bare owner/repo.
func NormalizeRepo(repo string) string {
	repo = strings.TrimSpace(repo)
	repo = strings.TrimSuffix(repo, ".git")
	for _, prefix := range []string{"https://huggingface.co/", "http://huggingface.co/", "https://hf.co/", "http://hf.co/", "hf.co/", "huggingface.co/"} {
		if strings.HasPrefix(repo, prefix) {
			repo = strings.TrimPrefix(repo, prefix)
			break
		}
	}
	repo = strings.Trim(repo, "/")
	if parts := strings.Split(repo, "/"); len(parts) > 2 && parts[0] == "tree" {
		repo = strings.Join(parts[1:3], "/")
	}
	return repo
}

// ModelInfo is the subset of the Hugging Face model API we use.
type ModelInfo struct {
	ID       string `json:"id"`
	Siblings []struct {
		Rfilename string `json:"rfilename"`
	} `json:"siblings"`
}

func (c *Client) request(ctx context.Context, url string) (*http.Response, error) {
	return c.requestWithHeaders(ctx, url, nil)
}

func (c *Client) requestWithHeaders(ctx context.Context, url string, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("hugging face returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return resp, nil
}

// repoAPIPath escapes each path segment but keeps the owner/name separator.
// Hugging Face rejects a fully escaped repository (owner%2Fname) on its API.
func repoAPIPath(repo string) string {
	parts := strings.Split(NormalizeRepo(repo), "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

// Model fetches repository metadata.
func (c *Client) Model(ctx context.Context, repo string) (*ModelInfo, error) {
	repo = NormalizeRepo(repo)
	if repo == "" {
		return nil, fmt.Errorf("empty Hugging Face repository")
	}
	resp, err := c.request(ctx, c.BaseURL+"/api/models/"+repoAPIPath(repo))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var info ModelInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("parse model metadata: %w", err)
	}
	if info.ID == "" {
		info.ID = repo
	}
	return &info, nil
}

// GGUFFiles lists .gguf files in a repository.
func (c *Client) GGUFFiles(ctx context.Context, repo string) ([]string, error) {
	info, err := c.Model(ctx, repo)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, sibling := range info.Siblings {
		name := sibling.Rfilename
		if strings.HasSuffix(strings.ToLower(name), ".gguf") && !strings.Contains(name, "/") {
			files = append(files, name)
		}
	}
	return files, nil
}

// SearchResult is one repository returned by the Hub search API. It contains
// only metadata published by Hugging Face and the repository owner.
type SearchResult struct {
	ID          string   `json:"id"`
	Author      string   `json:"author"`
	PipelineTag string   `json:"pipeline_tag"`
	LibraryName string   `json:"library_name"`
	Downloads   int64    `json:"downloads"`
	Likes       int64    `json:"likes"`
	CreatedAt   string   `json:"createdAt"`
	Tags        []string `json:"tags"`
	Siblings    []struct {
		Rfilename string `json:"rfilename"`
	} `json:"siblings"`
}

// Search lists GGUF repositories, newest downloads first. An empty query
// returns the most downloaded GGUF repositories.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 300 {
		limit = 300
	}
	q := url.Values{}
	q.Set("filter", "gguf")
	q.Set("sort", "downloads")
	q.Set("direction", "-1")
	q.Set("full", "true")
	q.Set("limit", strconv.Itoa(limit))
	if strings.TrimSpace(query) != "" {
		q.Set("search", strings.TrimSpace(query))
	}
	resp, err := c.request(ctx, c.BaseURL+"/api/models?"+q.Encode())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var results []SearchResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("parse model search: %w", err)
	}
	return results, nil
}

// TreeFile is one file entry from the Hub tree API.
type TreeFile struct {
	Path string `json:"path"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

// Tree lists repository files with real byte sizes (recursive).
func (c *Client) Tree(ctx context.Context, repo string) ([]TreeFile, error) {
	repo = NormalizeRepo(repo)
	if repo == "" {
		return nil, fmt.Errorf("empty Hugging Face repository")
	}
	url := c.BaseURL + "/api/models/" + repoAPIPath(repo) + "/tree/main?recursive=true&expand=false"
	resp, err := c.request(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var files []TreeFile
	if err := json.NewDecoder(resp.Body).Decode(&files); err != nil {
		return nil, fmt.Errorf("parse repository tree: %w", err)
	}
	return files, nil
}

// PickGGUF chooses a quant file; an empty quant prefers Q4_K_M.
func PickGGUF(files []string, quant string) string {
	if len(files) == 0 {
		return ""
	}
	want := strings.ToUpper(strings.TrimSpace(quant))
	if want != "" {
		for _, f := range files {
			if strings.Contains(strings.ToUpper(f), want) {
				return f
			}
		}
	}
	for _, preferred := range []string{"Q4_K_M", "Q5_K_M", "Q8_0", "Q6_K", "F16"} {
		for _, f := range files {
			if strings.Contains(strings.ToUpper(f), preferred) {
				return f
			}
		}
	}
	return files[0]
}

// ResolveURL returns the direct download URL for a file in a repo.
func (c *Client) ResolveURL(repo, file string) string {
	parts := strings.Split(file, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return fmt.Sprintf("%s/%s/resolve/main/%s?download=true", c.BaseURL, repoAPIPath(repo), strings.Join(parts, "/"))
}

type progressReader struct {
	reader io.Reader
	read   int64
	total  int64
	report func(done, total int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.reader.Read(b)
	p.read += int64(n)
	if p.report != nil {
		p.report(p.read, p.total)
	}
	return n, err
}

// Download streams a GGUF to dest, reporting progress, then verifies the
// header. An existing .part file is resumed when the server supports Range
// requests; cancelled downloads therefore keep their partial data.
func (c *Client) Download(ctx context.Context, repo, file, dest string, progress func(done, total int64)) error {
	tmp := dest + ".part"
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	var resume int64
	if info, err := os.Stat(tmp); err == nil && info.Size() > 0 {
		resume = info.Size()
	}
	headers := map[string]string{}
	if resume > 0 {
		headers["Range"] = "bytes=" + strconv.FormatInt(resume, 10) + "-"
	}
	resp, err := c.requestWithHeaders(ctx, c.ResolveURL(repo, file), headers)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	appendMode := resume > 0 && resp.StatusCode == http.StatusPartialContent
	if !appendMode {
		resume = 0
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if appendMode {
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	out, err := os.OpenFile(tmp, flags, 0o644)
	if err != nil {
		return err
	}
	total := resp.ContentLength
	if appendMode && total >= 0 {
		total += resume
	}
	pr := &progressReader{read: resume, total: total, report: progress, reader: resp.Body}
	if progress != nil {
		progress(pr.read, pr.total)
	}
	_, copyErr := io.Copy(out, pr)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr // keep .part so the next attempt can resume
	}
	if closeErr != nil {
		return closeErr
	}
	if total >= 0 {
		if info, err := os.Stat(tmp); err == nil && info.Size() != total {
			return fmt.Errorf("incomplete download: got %d of %d bytes", info.Size(), total)
		}
	}
	if err := VerifyGGUF(tmp); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("%s is not a valid GGUF file: %w", file, err)
	}
	return os.Rename(tmp, dest)
}

// VerifyGGUF checks the GGUF magic bytes.
func VerifyGGUF(filePath string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return fmt.Errorf("file too short: %w", err)
	}
	if string(magic[:]) != "GGUF" {
		return fmt.Errorf("bad magic %q", magic[:])
	}
	return nil
}
