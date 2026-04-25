// Command dashgen is the thin CLI entrypoint. It parses flags, loads config,
// and dispatches to internal/app packages.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	appcov "dashgen/internal/app/coverage"
	"dashgen/internal/app/generate"
	applint "dashgen/internal/app/lint"
)

// Exit codes. Mirrors the error categories exported by internal/app/generate,
// internal/app/lint, and internal/app/coverage.
const (
	exitOK                  = 0
	exitGenericError        = 1
	exitBackendError        = 2
	exitRenderError         = 3
	exitStrictViolation     = 4
	exitLintInputError      = 5
	exitLintRenderError     = 6
	exitLintFailure         = 7
	exitCoverageInputError  = 8
	exitCoverageRenderError = 9
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(exitCodeFor(err))
	}
}

func exitCodeFor(err error) int {
	switch {
	case errors.Is(err, generate.ErrBackend):
		return exitBackendError
	case errors.Is(err, generate.ErrRender):
		return exitRenderError
	case errors.Is(err, generate.ErrStrictViolation):
		return exitStrictViolation
	case errors.Is(err, applint.ErrLintFailure):
		return exitLintFailure
	case errors.Is(err, applint.ErrInput):
		return exitLintInputError
	case errors.Is(err, applint.ErrRender):
		return exitLintRenderError
	case errors.Is(err, appcov.ErrInput):
		return exitCoverageInputError
	case errors.Is(err, appcov.ErrRender):
		return exitCoverageRenderError
	default:
		return exitGenericError
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "dashgen",
		Short: "Generate reviewable Grafana dashboards from a Prometheus backend",
		// Suppress cobra's default usage+error noise for RunE failures — main
		// prints a single "error:" line and picks the exit code. Usage is
		// still shown by --help and on flag-parse errors.
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newGenerateCmd())
	root.AddCommand(newValidateCmd())
	root.AddCommand(newInspectCmd())
	root.AddCommand(newLintCmd())
	root.AddCommand(newCoverageCmd())
	return root
}
