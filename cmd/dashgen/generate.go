package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"dashgen/internal/app/generate"
	"dashgen/internal/config"
	"dashgen/internal/enrich"
)

func newGenerateCmd() *cobra.Command {
	return newGenerateCmdWithRunner(generate.Run)
}

// newGenerateCmdWithRunner builds the generate sub-command. runFn is the
// function called with the resolved RunConfig; production code passes
// generate.Run; tests pass a capturing stub.
func newGenerateCmdWithRunner(runFn func(context.Context, *config.RunConfig) error) *cobra.Command {
	var (
		promURL      string
		fixtureDir   string
		profile      string
		outDir       string
		configPath   string
		dryRun       bool
		strict       bool
		inPlace      bool
		job          string
		namespace    string
		metricMatch  string
		maxPanels    int
		// v0.2 enrichment flags
		provider              string
		providerModel         string
		enrichModes           string
		noEnrichCache         bool
		cacheDir              string
		logEnrichmentPayloads bool
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
			cfg.InPlace = inPlace
			cfg.Job = job
			cfg.Namespace = namespace
			cfg.MetricMatch = metricMatch
			if maxPanels > 0 {
				cfg.MaxPanels = maxPanels
			}
			// v0.2 enrichment flags
			if provider != "" {
				cfg.Provider = provider
			}
			if providerModel != "" {
				cfg.Model = providerModel
			}
			if enrichModes != "" {
				parts := strings.Split(enrichModes, ",")
				modes := make([]string, 0, len(parts))
				for _, p := range parts {
					if t := strings.TrimSpace(p); t != "" {
						modes = append(modes, t)
					}
				}
				cfg.EnrichModes = modes
			}
			if noEnrichCache {
				cfg.NoEnrichCache = true
			}
			if cacheDir != "" {
				cfg.CacheDir = cacheDir
			}
			cfg.LogEnrichmentPayloads = logEnrichmentPayloads
			return runFn(cmd.Context(), cfg)
		},
	}

	cmd.Flags().StringVar(&promURL, "prom-url", "", "Prometheus-compatible HTTP API base URL")
	cmd.Flags().StringVar(&fixtureDir, "fixture-dir", "", "offline fixture directory (mutually exclusive with --prom-url)")
	cmd.Flags().StringVar(&profile, "profile", "", "dashboard profile (service|infra|k8s)")
	cmd.Flags().StringVar(&outDir, "out", "", "output directory")
	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "do not write output files")
	cmd.Flags().BoolVar(&strict, "strict", false, "treat warnings as failure")
	cmd.Flags().BoolVar(&inPlace, "in-place", false, "skip rewriting unchanged output files (idempotent re-runs)")
	cmd.Flags().StringVar(&job, "job", "", "restrict discovery to job label")
	cmd.Flags().StringVar(&namespace, "namespace", "", "restrict discovery to namespace label")
	cmd.Flags().StringVar(&metricMatch, "metric-match", "", "metric-name substring filter")
	cmd.Flags().IntVar(&maxPanels, "max-panels", 0, "override the profile's panel cap (0 = profile default)")
	// v0.2 enrichment flags
	cmd.Flags().StringVar(&provider, "provider", "", "enrichment provider: off|"+strings.Join(enrich.Providers(), "|")+" (default: off/noop)")
	cmd.Flags().StringVar(&providerModel, "provider-model", "", "override the provider's default model id")
	cmd.Flags().StringVar(&enrichModes, "enrich", "", "comma-separated enrichment modes: titles,rationale,classify,all,none")
	cmd.Flags().BoolVar(&noEnrichCache, "no-enrich-cache", false, "bypass the enrichment disk cache (force fresh request)")
	cmd.Flags().StringVar(&cacheDir, "cache-dir", "", "override enrichment cache directory (default: ~/.cache/dashgen/enrich)")
	// --log-enrichment-payloads is DEBUG-ONLY: when set with a non-noop
	// provider, the generate pipeline emits a one-line summary (function
	// name, byte count, redacted preview) per outbound enrichment HTTP
	// call to stderr. The flag is hidden from --help unless DASHGEN_DEBUG=1
	// so it cannot drift into production CI flows (ADVERSARY §6 "debug
	// paths become product paths"). The preview is computed from wire
	// bytes only — never from pre-redaction caller input — so anything
	// ValidateBriefs would reject cannot reach the log.
	cmd.Flags().BoolVar(&logEnrichmentPayloads, "log-enrichment-payloads", false, "DEBUG: log a one-line summary (function, byte count, redacted preview) per outbound enrichment HTTP call to stderr")
	if os.Getenv("DASHGEN_DEBUG") != "1" {
		_ = cmd.Flags().MarkHidden("log-enrichment-payloads")
	}

	cmd.SetContext(context.Background())
	return cmd
}
