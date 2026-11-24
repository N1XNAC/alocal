package runtime

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Options configures a llama-server instance.
type Options struct {
	Context   int
	GPULayers int
}

// Server owns a running llama-server process.
type Server struct {
	cmd      *exec.Cmd
	baseURL  string
	stderr   bytes.Buffer
	mu       sync.Mutex
	onOutput func(string)
}

// Start launches llama-server on a free local port and waits until it is healthy.
func Start(ctx context.Context, binary, modelPath string, opts Options) (*Server, error) {
	return StartWithProgress(ctx, binary, modelPath, opts, nil)
}

// StartWithProgress is Start with an optional status callback. Lines from
// llama-server that indicate model-loading progress are forwarded to progress.
func StartWithProgress(ctx context.Context, binary, modelPath string, opts Options, progress func(string)) (*Server, error) {
	report := func(s string) {
		if progress != nil {
			progress(s)
		}
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	args := []string{
		"-m", modelPath,
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(port),
		"-c", strconv.Itoa(opts.Context),
		"-ngl", strconv.Itoa(opts.GPULayers),
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	srv := &Server{cmd: cmd, baseURL: "http://127.0.0.1:" + strconv.Itoa(port), onOutput: report}
	cmd.Stdout = &writer{srv: srv}
	cmd.Stderr = &writer{srv: srv}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	timeout := 300 * time.Second
	if raw := os.Getenv("ALOCAL_LOAD_TIMEOUT"); raw != "" {
		if secs, err := strconv.Atoi(raw); err == nil && secs > 0 {
			timeout = time.Duration(secs) * time.Second
		}
	}
	report("llama-server started; loading model into memory (first load can take a while)...")
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := NewClient(srv.baseURL).Health(ctx); err == nil {
			return srv, nil
		}
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return nil, fmt.Errorf("llama-server exited: %s", strings.TrimSpace(srv.logs()))
		}
		select {
		case <-ctx.Done():
			_ = srv.Close()
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	_ = srv.Close()
	return nil, fmt.Errorf("llama-server did not become ready within %s: %s", timeout, strings.TrimSpace(srv.logs()))
}

// writer captures llama-server output and forwards meaningful lines to the
// progress callback so the TUI can show real loading stages.
type writer struct{ srv *Server }

func (w *writer) Write(p []byte) (int, error) {
	w.srv.mu.Lock()
	w.srv.stderr.Write(p)
	cb := w.srv.onOutput
	w.srv.mu.Unlock()
	if cb != nil {
		for _, line := range strings.Split(string(p), "\n") {
			line = strings.TrimSpace(line)
			if interestingLine(line) {
				cb(line)
			}
		}
	}
	return len(p), nil
}

func interestingLine(line string) bool {
	if line == "" {
		return false
	}
	lower := strings.ToLower(line)
	for _, needle := range []string{
		"loading model", "load_tensors", "llama_model_load", "kv cache",
		"offloaded", "compute buffer", "model buffer", "ready", "listening",
		"error", "failed", "out of memory", "gguf",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func (s *Server) logs() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stderr.String()
}

// BaseURL is the HTTP endpoint of the running server.
func (s *Server) BaseURL() string { return s.baseURL }

// Close stops the server process.
func (s *Server) Close() error {
	if s.cmd == nil || s.cmd.Process == nil {
		return nil
	}
	_ = s.cmd.Process.Kill()
	done := make(chan error, 1)
	go func() { done <- s.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
	return nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
