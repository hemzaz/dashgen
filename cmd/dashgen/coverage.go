package main

import (
	"github.com/spf13/cobra"

	appcov "dashgen/internal/app/coverage"
)

// newCoverageCmd wires the `dashgen coverage` subcommand. It reads a
// fixture-dir inventory and (optionally) a dashboard.json bundle, then
// emits a deterministic JSON coverage report.
func newCoverageCmd() *cobra.Command {
	var (
		fixtureDir string
		in         string
		out        string
	)
	cmd := &cobra.Command{
		Use:   "coverage",
		Short: "Report metric coverage for an inventory and (optionally) a dashboard bundle",
		RunE: func(_ *cobra.Command, _ []string) error {
			return appcov.Run(&appcov.Config{
				FixtureDir: fixtureDir,
				In:         in,
				Out:        out,
			})
		},
	}
	cmd.Flags().StringVar(&fixtureDir, "fixture-dir", "", "directory containing metadata.json (required)")
	cmd.Flags().StringVar(&in, "in", "", "directory containing dashboard.json to compute coverage against (optional)")
	cmd.Flags().StringVar(&out, "out", "", "write JSON report to file; empty means stdout")
	_ = cmd.MarkFlagRequired("fixture-dir")
	return cmd
}
