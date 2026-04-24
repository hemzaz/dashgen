package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"dashgen/internal/app/inspect"
	"dashgen/internal/config"
)

func newInspectCmd() *cobra.Command {
	var (
		promURL     string
		fixtureDir  string
		profile     string
		configPath  string
		job         string
		namespace   string
		metricMatch string
		maxPanels   int
	)

	cmd := &cobra.Command{
		Use:   "inspect",
		Short: "Inspect discovered inventory, classification, and recipe matches",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			if promURL != "" && fixtureDir != "" {
				return fmt.Errorf("--prom-url and --fixture-dir are mutually exclusive")
			}
			if promURL == "" && fixtureDir == "" {
				return fmt.Errorf("one of --prom-url or --fixture-dir is required")
			}
			if promURL != "" {
				cfg.PromURL = promURL
			}
			if fixtureDir != "" {
				cfg.FixtureDir = fixtureDir
			}
			if profile != "" {
				cfg.Profile = profile
			}
			cfg.Job = job
			cfg.Namespace = namespace
			cfg.MetricMatch = metricMatch
			if maxPanels > 0 {
				cfg.MaxPanels = maxPanels
			}
			return inspect.Run(cmd.Context(), cfg)
		},
	}

	cmd.Flags().StringVar(&promURL, "prom-url", "", "Prometheus-compatible HTTP API base URL")
	cmd.Flags().StringVar(&fixtureDir, "fixture-dir", "", "offline fixture directory (mutually exclusive with --prom-url)")
	cmd.Flags().StringVar(&profile, "profile", "", "dashboard profile (service|infra|k8s)")
	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	cmd.Flags().StringVar(&job, "job", "", "restrict discovery to job label")
	cmd.Flags().StringVar(&namespace, "namespace", "", "restrict discovery to namespace label")
	cmd.Flags().StringVar(&metricMatch, "metric-match", "", "metric-name substring filter")
	cmd.Flags().IntVar(&maxPanels, "max-panels", 0, "override the profile's panel cap (0 = profile default)")

	cmd.SetContext(context.Background())
	return cmd
}
