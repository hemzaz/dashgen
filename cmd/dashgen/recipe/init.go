package recipe

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// ErrDirExists is returned when the target directory already contains one or
// more of the scaffolded files and --force was not passed.
// main.exitCodeFor maps this sentinel to exit code 2 (RECIPES-CLI.md §6).
var ErrDirExists = errors.New("recipe directory already initialized; use --force to overwrite")

// scaffoldedFiles are the three files written by "dashgen recipe init".
var scaffoldedFiles = [3]string{"README.md", "example.yaml", ".gitignore"}

func newInitCmd() *cobra.Command {
	var (
		configDir string
		force     bool
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Bootstrap the user recipes directory with a README, example recipe, and .gitignore",
		Long: `Create the user recipes directory at $XDG_CONFIG_HOME/dashgen/recipes/
(fallback: $HOME/.config/dashgen/recipes/) and write three starter files:
README.md, example.yaml, and .gitignore.

Pass --force to overwrite existing files.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInit(cmd, configDir, force)
		},
	}
	cmd.Flags().StringVar(&configDir, "config-dir", "",
		"override default recipes directory (default: $XDG_CONFIG_HOME/dashgen/recipes/)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing scaffolded files")
	return cmd
}

func runInit(cmd *cobra.Command, configDir string, force bool) error {
	dir, err := resolveRecipesDir(configDir)
	if err != nil {
		return fmt.Errorf("resolve recipes directory: %w", err)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create recipes directory %s: %w", dir, err)
	}

	// Without --force, refuse if any of the three scaffolded files already exist.
	if !force {
		for _, f := range scaffoldedFiles {
			p := filepath.Join(dir, f)
			if _, statErr := os.Stat(p); statErr == nil {
				return fmt.Errorf("%w: %s", ErrDirExists, p)
			}
		}
	}

	writes := [3]struct {
		name    string
		content string
	}{
		{"README.md", recipeReadme},
		{"example.yaml", recipeExampleYAML},
		{".gitignore", recipeGitignore},
	}
	for _, w := range writes {
		p := filepath.Join(dir, w.name)
		if err := os.WriteFile(p, []byte(w.content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", p, err)
		}
	}

	fmt.Fprintln(cmd.OutOrStdout(), dir)
	fmt.Fprintln(cmd.OutOrStdout(), "Recipes directory ready. Drop *.yaml files here, then run 'dashgen generate' to use them.")
	return nil
}

// resolveRecipesDir returns the absolute path of the user recipes directory.
// When override is non-empty it is resolved to an absolute path and returned.
// Otherwise $XDG_CONFIG_HOME/dashgen/recipes/ is used, falling back to
// $HOME/.config/dashgen/recipes/ when XDG_CONFIG_HOME is unset or empty.
func resolveRecipesDir(override string) (string, error) {
	if override != "" {
		abs, err := filepath.Abs(override)
		if err != nil {
			return "", fmt.Errorf("resolve path %q: %w", override, err)
		}
		return abs, nil
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "dashgen", "recipes"), nil
}

// =============================================================================
// Scaffolded file contents
// =============================================================================

const recipeReadme = `# DashGen User Recipes

Drop your *.yaml recipe files here. They are loaded automatically when you
run "dashgen generate".

## Documentation

  - Recipe DSL specification: docs/RECIPES-DSL.md
  - User guide: docs/RECIPES-USER-GUIDE.md (coming soon)
  - Schema reference: internal/recipes/schema.cue

## Common Pitfalls

  1. Missing apiVersion: every recipe must begin with "apiVersion: dashgen.io/v1".
  2. Invalid section: "section" must be one of:
     overview, traffic, errors, latency, saturation, cpu, memory,
     disk, network, pods, workloads, resources.
  3. Name format: "metadata.name" must match ^[a-z][a-z0-9_]*$ (max 64 chars).
  4. At least one panel: "panels" must contain at least one entry.
  5. Known units: prefer canonical values -- ops/sec, seconds, bytes, count, etc.
     Non-canonical units are accepted but emit a lint warning.

Validate your recipes with:

    dashgen recipe lint <file>.yaml
`

const recipeGitignore = `*.bak
*.tmp
.DS_Store
`

// recipeExampleYAML is a fully-commented Tier-A counter recipe modelled on the
// built-in service_grpc_rate shape. It must remain valid against schema.cue.
const recipeExampleYAML = `# DashGen User Recipe -- Tier-A Counter Pattern
#
# This is the canonical starting point for a custom metric recipe.
# It follows the gRPC call-rate pattern from the built-in recipe catalog.
#
# Steps:
#   1. Copy this file: cp example.yaml my_metric.yaml
#   2. Edit the fields below to match your metric.
#   3. Validate:  dashgen recipe lint my_metric.yaml

# Required. Must be exactly "dashgen.io/v1".
apiVersion: dashgen.io/v1

# Required. Must be exactly "Recipe".
kind: Recipe

metadata:
  # Unique recipe identifier. Must match ^[a-z][a-z0-9_]*$ (max 64 chars).
  # Convention: mirror the Prometheus metric name you are targeting.
  name: example_rpc_rate

  # Dashboard section this recipe populates.
  # Allowed: overview traffic errors latency saturation
  #          cpu memory disk network pods workloads resources
  section: traffic

  # Which dashgen profile loads this recipe.
  # Allowed: service infra k8s
  profile: service

  # Match confidence in [0.0, 1.0]. Higher values win when two recipes match
  # the same metric. 0.85 is a good default for purpose-built custom recipes.
  confidence: 0.85

  # Recipe schema version. Use "v0.3" for all new recipes.
  tier: v0.3

  # Optional: one-sentence description (max 280 chars).
  description: "Example gRPC call rate recipe. Replace with your metric description."

  # Optional: tags for documentation and filtering via "dashgen recipe list".
  tags: [example, counter, grpc]

# match: predicate evaluated against every discovered Prometheus metric.
# This recipe fires on metrics that satisfy all conditions below.
match:
  # type: restrict to one Prometheus metric type.
  # Allowed: counter gauge histogram summary
  type: counter

  # any_trait: fires if the metric has ANY of these classifier-assigned traits.
  # Known traits: service_http  service_grpc  latency_histogram
  # Remove this field to match by name or type alone.
  any_trait: [service_grpc]

# panels: list of Grafana panels to emit when the recipe fires.
# Minimum 1, maximum 16. Each entry becomes one panel in the dashboard.
panels:
  - # title_template: Go text/template for the panel heading.
    # Available: .Metric (metric name), .Window (rate window, e.g. "5m").
    title_template: 'gRPC call rate: {{ .Metric }}'

    # kind: Grafana visualization type.
    # Allowed: timeseries stat gauge barchart   (default: timeseries)
    kind: timeseries

    # unit: Y-axis display unit. Canonical values:
    #   ops/sec  errors/sec  seconds  bytes  bytes/sec
    #   ratio  percent  short  iops  count  days
    # Non-canonical strings are accepted with a lint warning.
    unit: reqps

    # preferred_labels: label names to include in group-by expressions.
    # "job" and "instance" are always included when present.
    preferred_labels: [grpc_service, grpc_method]

    # query_template: Go text/template producing a PromQL expression.
    # groupBy .  => safe label set (preferred_labels + job + instance)
    # .Window    => configured rate window string
    query_template: 'sum by ({{ groupBy . }}) (rate({{ .Metric }}[{{ .Window }}]))'

    # legend_template: Go text/template for the Grafana series legend.
    # legendFor . => joins grouped label values with "/"
    legend_template: '{{ legendFor . }}'

    # rationale_template: optional. Written to rationale.md alongside the dashboard.
    rationale_template: 'Counter "{{ .Metric }}" carries gRPC-shape labels; rate over {{ .Window }} grouped by {{ groupBy . }}.'
`
