// Package config loads and resolves user configuration for a single run.
//
// Config sources are merged in this order (later wins):
//  1. built-in defaults
//  2. YAML config file, if provided
//  3. fields explicitly supplied by the CLI flags on the RunConfig passed in
//
// Config validation is deliberately thin in this package. Validation beyond
// presence-and-shape belongs to the packages that own the values.
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// RunConfig is the resolved, immutable configuration for a single CLI run.
type RunConfig struct {
	PromURL     string
	FixtureDir  string
	Profile     string
	OutDir      string
	ConfigPath  string
	DryRun      bool
	Strict      bool
	Job         string
	Namespace   string
	MetricMatch string
	HTTPTimeout time.Duration
	RunBudget   time.Duration
	// MaxPanels overrides the profile's default panel cap. Zero means "use
	// the profile default" (profiles.PanelCap).
	MaxPanels    int
	FileOverride *FileConfig
	// Exprs holds PromQL expressions supplied via repeated --expr CLI
	// flags. Consumed by the `validate` subcommand only.
	Exprs []string
	// ExprFile points to a file containing one PromQL expression per
	// non-empty, non-`#`-prefixed line. Consumed by `validate` only.
	ExprFile string
	// Provider selects the v0.2 enrichment provider. Zero-value (or "off")
	// uses internal/enrich.NoopEnricher and produces output byte-identical
	// to v0.1. Phase 3+ adds "anthropic" and "openai". Unknown values are
	// rejected by app/generate as ErrBackend.
	Provider string
	// Model overrides the provider's default model id. Empty means "use the
	// provider default". Ignored when Provider is "" or "off".
	Model string
	// EnrichModes is a subset of {titles, rationale, classify,
	// unknown-grouping, all, none}. Empty (nil) means "none" — no
	// enrichment is invoked even if a provider is configured. Consumed by
	// app/generate's applyEnrichment glue once it gates per-mode calls.
	EnrichModes []string
	// CacheDir overrides the on-disk cache directory used by
	// internal/enrich.Cache. Empty means use the default
	// ~/.cache/dashgen/enrich.
	CacheDir string
	// NoEnrichCache forces re-fetch instead of returning a cache hit. Used
	// for authoring/debugging. Default false (cache enabled).
	NoEnrichCache bool
	// InPlace enables idempotent regeneration: when true, output files
	// are only rewritten when their bytes differ from what's on disk.
	// Default false preserves v0.1 behavior (always rewrite). Cross-
	// section preservation across inventory changes is OUT of scope —
	// see .omc/plans/v0.2-remainder.md §7.2.
	InPlace bool
}

// FileConfig is the on-disk YAML schema. Fields are optional; unset fields
// fall back to the defaults in this package.
type FileConfig struct {
	IgnoredMetrics    []string          `yaml:"ignored_metrics,omitempty"`
	GroupingOverrides map[string]string `yaml:"grouping_overrides,omitempty"`
	UnitOverrides     map[string]string `yaml:"unit_overrides,omitempty"`
	LabelAllowList    []string          `yaml:"label_allow_list,omitempty"`
	LabelDenyList     []string          `yaml:"label_deny_list,omitempty"`
}

// Defaults returns the built-in defaults applied before any file or flag
// merge. It is safe to mutate the returned value.
func Defaults() *RunConfig {
	return &RunConfig{
		Profile:     "service",
		OutDir:      "./out",
		HTTPTimeout: 5 * time.Second,
		RunBudget:   60 * time.Second,
	}
}

// Load reads a YAML config file from path and returns a RunConfig with the
// file overrides applied on top of Defaults. A path of "" returns Defaults.
//
// Flag merging is the caller's responsibility; the CLI layer overwrites
// fields on the returned RunConfig after Load.
func Load(path string) (*RunConfig, error) {
	cfg := Defaults()
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var fc FileConfig
	if err := yaml.Unmarshal(data, &fc); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	cfg.ConfigPath = path
	cfg.FileOverride = &fc
	return cfg, nil
}
