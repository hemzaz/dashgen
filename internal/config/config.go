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
	PromURL      string
	FixtureDir   string
	Profile      string
	OutDir       string
	ConfigPath   string
	DryRun       bool
	Strict       bool
	Job          string
	Namespace    string
	MetricMatch  string
	HTTPTimeout  time.Duration
	RunBudget    time.Duration
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
