package runtime

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Options configures a llama-server instance.
type Options struct {
	Context   int
	GPULayers int
}

// Server owns a running llama-server process.
type Server struct {
	cmd     *exec.Cmd
	baseURL string
	stderr  bytes.Buffer
}

// Start launches llama-server on a free local port and waits until it is healthy.
func Start(ctx context.Context, binary, modelPath string, opts Options) (*Server, error) {
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
	srv := &Server{cmd: cmd, baseURL: "http://127.0.0.1:" + strconv.Itoa(port)}
	cmd.Stdout = &srv.stderr
	cmd.Stderr = &srv.stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	client := NewClient(srv.baseURL)
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		if err := client.Health(ctx); err == nil {
			return srv, nil
		}
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return nil, fmt.Errorf("llama-server exited: %s", strings.TrimSpace(srv.stderr.String()))
		}
		select {
		case <-ctx.Done():
			_ = srv.Close()
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	_ = srv.Close()
	return nil, fmt.Errorf("llama-server did not become ready: %s", strings.TrimSpace(srv.stderr.String()))
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
