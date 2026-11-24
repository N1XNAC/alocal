package runtime

import (
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"

	"alocal/internal/local"
)

// StatusLine reports the locally visible llama.cpp runtime state. It never
// downloads or starts anything, so it is safe to call during startup.
func StatusLine() string {
	if override := os.Getenv("ALOCAL_LLAMA_SERVER"); override != "" {
		if info, err := os.Stat(override); err == nil && !info.IsDir() {
			return "Runtime   llama.cpp · ready (" + shortenPath(override) + ")"
		}
		return "Runtime   llama.cpp · ALOCAL_LLAMA_SERVER is missing: " + shortenPath(override)
	}
	if _, err := exec.LookPath("llama-server"); err == nil {
		return "Runtime   llama.cpp · llama-server on PATH"
	}
	name := "llama-server"
	if goruntime.GOOS == "windows" {
		name += ".exe"
	}
	cached := filepath.Join(local.RuntimeDir(), name)
	if info, err := os.Stat(cached); err == nil && info.Size() > 0 {
		return "Runtime   llama.cpp · cached binary ready"
	}
	return "Runtime   llama.cpp · fetched on first model load"
}

func shortenPath(p string) string {
	p = strings.TrimSpace(p)
	if len(p) <= 48 {
		return p
	}
	return "..." + p[len(p)-45:]
}

// StatusDetail is useful for tests and for the catalogue's runtime details.
func StatusDetail() string {
	return "llama.cpp binary name: " + serverBinaryName()
}

func serverBinaryName() string {
	if goruntime.GOOS == "windows" {
		return "llama-server.exe"
	}
	return "llama-server"
}
