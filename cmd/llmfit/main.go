package main

import (
	"fmt"
	"os"

	"llmfit/internal/cli"
)

func main() {
	os.Exit(run())
}

// run keeps the process exit path panic-safe: an internal bug prints a short
// diagnostic instead of a stack trace, and still returns a non-zero code.
func run() (code int) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "llmfit: internal error: %v\n", r)
			code = 1
		}
	}()
	return cli.Execute()
}
