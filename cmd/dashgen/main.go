// Command dashgen is the thin CLI entrypoint. It parses flags, loads config,
// and dispatches to internal/app packages.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"dashgen/cmd/dashgen/recipe"
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
	case errors.Is(err, recipe.ErrDirExists):
		return 2 // RECIPES-CLI.md §6: existing directory without --force
	// recipe lint exit codes (RECIPES-CLI.md §6).
	case errors.Is(err, recipe.ErrLintResourceLimit):
		return 5 // resource limit: file too large, too many files, deadline
	case errors.Is(err, recipe.ErrLintInputError):
		return 2 // file not found or unreadable
	case errors.Is(err, recipe.ErrLintFailure):
		return 1 // one or more recipe files failed validation
	// recipe show exit codes (RECIPES-CLI.md §3.5).
	case errors.Is(err, recipe.ErrShowAmbiguous):
		return 2 // ambiguous name across profiles
	case errors.Is(err, recipe.ErrShowNotFound):
		return 1 // recipe not found
	// recipe test exit codes (RECIPES-CLI.md §3.6).
	case errors.Is(err, recipe.ErrTestFixtureError):
		return 2 // fixture not found or malformed
	case errors.Is(err, recipe.ErrTestLoadFailure):
		return 1 // recipe load failed
	// recipe explain exit codes (RECIPES-CLI.md §3.7).
	case errors.Is(err, recipe.ErrExplainNotFound):
		return 2 // recipe-not-found or metric-not-found
	// recipe diff exit codes (RECIPES-CLI.md §3.8).
	case errors.Is(err, recipe.ErrDiffNotFound):
		return 2 // --against-builtin recipe not found / ambiguous
	case errors.Is(err, recipe.ErrDiffFixtureError):
		return 2 // fixture missing or malformed
	case errors.Is(err, recipe.ErrDiffLoadFailure):
		return 1 // recipe load failed
	case errors.Is(err, recipe.ErrDiffMismatch):
		return 1 // panels differ (CI gate)
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
	root.AddCommand(recipe.NewCmd())
	return root
}
