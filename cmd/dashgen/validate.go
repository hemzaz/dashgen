package main

import (
	"context"

	"github.com/spf13/cobra"

	appvalidate "dashgen/internal/app/validate"
	"dashgen/internal/config"
)

func newValidateCmd() *cobra.Command {
	var (
		promURL    string
		fixtureDir string
		exprs      []string
		fromFile   string
		strict     bool
		configPath string
	)

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate one or more PromQL expressions against a backend",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			// CLI flags override file config and defaults. The app
			// layer re-checks mutual exclusivity as defense in depth.
			if promURL != "" {
				cfg.PromURL = promURL
			}
			if fixtureDir != "" {
				cfg.FixtureDir = fixtureDir
			}
			cfg.Exprs = exprs
			cfg.ExprFile = fromFile
			cfg.Strict = strict
			return appvalidate.Run(cmd.Context(), cfg)
		},
	}

	cmd.Flags().StringVar(&promURL, "prom-url", "", "Prometheus-compatible HTTP API base URL")
	cmd.Flags().StringVar(&fixtureDir, "fixture-dir", "", "offline fixture directory (mutually exclusive with --prom-url)")
	cmd.Flags().StringArrayVar(&exprs, "expr", nil, "PromQL expression to validate (repeatable)")
	cmd.Flags().StringVar(&fromFile, "from", "", "file containing PromQL expressions, one per line")
	cmd.Flags().BoolVar(&strict, "strict", false, "treat warnings as failure")
	cmd.Flags().StringVar(&configPath, "config", "", "config file path")

	cmd.SetContext(context.Background())
	return cmd
}
