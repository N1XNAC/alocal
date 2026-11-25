// Package main is the Alocal GUI entry point. It starts an embedded HTTP
// server that serves a dark-themed desktop application and opens the system
// browser to the local UI.
//
// The GUI wraps Alocal's existing Go libraries: model discovery, hardware
// detection, llama.cpp runtime, Hugging Face downloads, and the local model
// registry. No Ollama or external inference backend is required.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"alocal/internal/gui"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:0", "bind address for the GUI server")
	flag.Parse()

	srv, err := gui.NewServer()
	if err != nil {
		log.Fatalf("failed to create GUI server: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", srv.Handler()))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "web/index.html")
	})

	l, err := srv.Listen(*addr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	url := "http://" + l.Addr().String()
	log.Printf("Alocal GUI listening on %s", url)

	go func() {
		time.Sleep(500 * time.Millisecond)
		_ = openBrowser(url)
	}()

	if err := srv.Serve(l); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}

func openBrowser(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "linux":
		cmd = "xdg-open"
	case "darwin":
		cmd = "open"
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	}

	if cmd == "" {
		return nil
	}

	c := exec.Command(cmd, append(args, url)...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}
