package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"dashgen/internal/app/generate"
	"dashgen/internal/config"
)

func newGenerateCmd() *cobra.Command {
	var (
		promURL     string
		fixtureDir  string
		profile     string
		outDir      string
		configPath  string
		dryRun      bool
		strict      bool
		job         string
		namespace   string
		metricMatch string
	)

	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate a dashboard bundle for one backend",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			// Enforce mutual exclusivity of backend sources at the CLI layer.
			if promURL != "" && fixtureDir != "" {
				return fmt.Errorf("--prom-url and --fixture-dir are mutually exclusive")
			}
			if promURL == "" && fixtureDir == "" {
				return fmt.Errorf("one of --prom-url or --fixture-dir is required")
			}
			// CLI flags override file config and defaults.
			if promURL != "" {
				cfg.PromURL = promURL
			}
			if fixtureDir != "" {
				cfg.FixtureDir = fixtureDir
			}
			if profile != "" {
				cfg.Profile = profile
			}
			if outDir != "" {
				cfg.OutDir = outDir
			}
			cfg.DryRun = dryRun
			cfg.Strict = strict
			cfg.Job = job
			cfg.Namespace = namespace
			cfg.MetricMatch = metricMatch
			return generate.Run(cmd.Context(), cfg)
		},
	}

	cmd.Flags().StringVar(&promURL, "prom-url", "", "Prometheus-compatible HTTP API base URL")
	cmd.Flags().StringVar(&fixtureDir, "fixture-dir", "", "offline fixture directory (mutually exclusive with --prom-url)")
	cmd.Flags().StringVar(&profile, "profile", "", "dashboard profile (service|infra|k8s)")
	cmd.Flags().StringVar(&outDir, "out", "", "output directory")
	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "do not write output files")
	cmd.Flags().BoolVar(&strict, "strict", false, "treat warnings as failure")
	cmd.Flags().StringVar(&job, "job", "", "restrict discovery to job label")
	cmd.Flags().StringVar(&namespace, "namespace", "", "restrict discovery to namespace label")
	cmd.Flags().StringVar(&metricMatch, "metric-match", "", "metric-name substring filter")

	cmd.SetContext(context.Background())
	return cmd
}
