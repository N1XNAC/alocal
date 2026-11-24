package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"llmfit/internal/catalog"
	"llmfit/internal/hardware"
)

// Version is the CLI version.
const Version = "0.1.0"

// App carries I/O and global flags.
type App struct {
	Out     io.Writer
	Err     io.Writer
	In      io.Reader
	JSON    bool
	Verbose bool
	Mock    string
	Color   bool
	Root    *cobra.Command
	catalog *catalog.Catalog
}

// CodedError lets commands choose a stable exit code.
type CodedError struct {
	Code   int
	Msg    string
	Silent bool
}

func (e *CodedError) Error() string { return e.Msg }

func coded(code int, format string, args ...any) error {
	return &CodedError{Code: code, Msg: fmt.Sprintf(format, args...)}
}

func silent(code int) error { return &CodedError{Code: code, Silent: true} }

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	out := os.Stdout
	app := &App{
		Out: out, Err: os.Stderr, In: os.Stdin,
		Color: term.IsTerminal(int(out.Fd())) && os.Getenv("NO_COLOR") == "",
	}
	root := NewRoot(app)
	root.SetOut(app.Out)
	root.SetErr(app.Err)
	err := root.Execute()
	if err == nil {
		return 0
	}
	code := 1
	var ce *CodedError
	if errors.As(err, &ce) {
		code = ce.Code
		if ce.Silent {
			return code
		}
	}
	app.printError(err, code)
	return code
}

// NewRoot builds the command tree.
func NewRoot(app *App) *cobra.Command {
	root := &cobra.Command{
		Use:           "llmfit",
		Short:         "Find the best local LLM this machine can run",
		Long:          "llmfit scans your hardware, computes a transparent 0-100 Hardware Score, and recommends the best-fitting local model from an embedded catalog.",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return coded(2, "%v", err)
	})
	pf := root.PersistentFlags()
	pf.BoolVar(&app.JSON, "json", false, "emit machine-readable JSON")
	pf.BoolVar(&app.Verbose, "verbose", false, "show raw detection sources and score details")
	pf.StringVar(&app.Mock, "mock-hardware", "", "load a mock hardware profile JSON file (- for stdin)")
	app.Root = root

	root.AddCommand(
		newScanCmd(app),
		newScoreCmd(app),
		newRecommendCmd(app),
		newModelsCmd(app),
		newExplainCmd(app),
		newPullCmd(app),
		newCatalogCmd(app),
	)
	return root
}

func (a *App) printError(err error, code int) {
	if a.JSON {
		_ = a.writeJSON(map[string]any{"error": err.Error(), "exit_code": code})
		return
	}
	fmt.Fprintf(a.Err, "llmfit: %s\n", err.Error())
}

func (a *App) writeJSON(v any) error {
	enc := json.NewEncoder(a.Out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func (a *App) loadProfile(ctx context.Context) (*hardware.Profile, error) {
	var d hardware.Detector
	if a.Mock != "" {
		d = hardware.MockDetector{Path: a.Mock}
	} else {
		d = hardware.SystemDetector{Options: hardware.Options{Verbose: a.Verbose}}
	}
	p, err := d.Detect(ctx)
	if err != nil {
		return nil, coded(1, "%v", err)
	}
	return p, nil
}

func (a *App) loadCatalog() (*catalog.Catalog, error) {
	if a.catalog != nil {
		return a.catalog, nil
	}
	c, err := catalog.Load()
	if err != nil {
		return nil, coded(1, "load embedded catalog: %v", err)
	}
	if errs := c.Validate(); len(errs) > 0 {
		return nil, coded(1, "embedded catalog is invalid: %v (run `llmfit catalog validate`)", errs[0])
	}
	a.catalog = c
	return c, nil
}

func short(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}
