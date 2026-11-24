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
	"path"
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("hugging face returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return resp, nil
}

// Model fetches repository metadata.
func (c *Client) Model(ctx context.Context, repo string) (*ModelInfo, error) {
	repo = NormalizeRepo(repo)
	if repo == "" {
		return nil, fmt.Errorf("empty Hugging Face repository")
	}
	resp, err := c.request(ctx, c.BaseURL+"/api/models/"+url.PathEscape(repo))
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
	return fmt.Sprintf("%s/%s/resolve/main/%s?download=true", c.BaseURL, NormalizeRepo(repo), strings.Join(parts, "/"))
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

// Download streams a GGUF to dest, reporting progress, then verifies the header.
func (c *Client) Download(ctx context.Context, repo, file, dest string, progress func(done, total int64)) error {
	resp, err := c.request(ctx, c.ResolveURL(repo, file))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if err := os.MkdirAll(path.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp := dest + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	pr := &progressReader{total: resp.ContentLength, report: progress, reader: resp.Body}
	_, copyErr := io.Copy(out, pr)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
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
