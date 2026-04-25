package main

import (
	"github.com/spf13/cobra"

	applint "dashgen/internal/app/lint"
)

// newLintCmd wires the `dashgen lint` subcommand. It runs every
// registered check in `internal/lint` against an existing
// dashboard.json bundle and emits a deterministic JSON report.
//
// Exit codes are mapped by main.exitCodeFor: ErrInput, ErrRender, and
// ErrLintFailure each get their own non-zero code so CI can distinguish
// "you pointed me at the wrong directory" from "the dashboard ships
// content lint refuses."
func newLintCmd() *cobra.Command {
	var (
		in  string
		out string
	)
	cmd := &cobra.Command{
		Use:   "lint",
		Short: "Lint an existing dashboard bundle for quality and safety regressions",
		RunE: func(_ *cobra.Command, _ []string) error {
			return applint.Run(&applint.Config{In: in, Out: out})
		},
	}
	cmd.Flags().StringVar(&in, "in", "", "directory containing dashboard.json (required)")
	cmd.Flags().StringVar(&out, "out", "", "write JSON report to file; empty means stdout")
	_ = cmd.MarkFlagRequired("in")
	return cmd
}
