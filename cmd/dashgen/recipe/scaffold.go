package recipe

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/spf13/cobra"

	"dashgen/internal/recipes"
)

const (
	scaffoldMetricMaxRunes = 128
	scaffoldNameMaxRunes   = 64
)

// adversary: CT3 — Prometheus metric-name constraint applied to --metric,
// --name, and --with-pair halves (rejects shell metacharacters and
// path-traversal sequences at flag parse time).
var scaffoldMetricRe = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)

// validScaffoldSections is the closed #Section enum from schema.cue.
var validScaffoldSections = map[string]bool{
	"overview":   true,
	"traffic":    true,
	"errors":     true,
	"latency":    true,
	"saturation": true,
	"cpu":        true,
	"memory":     true,
	"disk":       true,
	"network":    true,
	"pods":       true,
	"workloads":  true,
	"resources":  true,
}

type scaffoldArgs struct {
	metric     string
	metricType string
	section    string
	profile    string
	name       string
	confidence float64
	output     string
	force      bool
	withPair   string
}

func newScaffoldCmd() *cobra.Command {
	var a scaffoldArgs

	cmd := &cobra.Command{
		Use:   "scaffold",
		Short: "Generate a starter recipe YAML for a given metric",
		Long: `Generate a starter *.yaml recipe file for the given Prometheus metric.

The recipe shape is chosen based on --type:
  counter    → sum-rate-by-labels pattern (Tier-A)
  gauge      → max-by-instance pattern (Tier-A)
  histogram  → histogram_quantile trio p50/p95/p99 (Tier-B)
  summary    → max-by-quantile pattern (Tier-B)

The produced YAML is validated against the recipe schema before being written.
Use --output to write to a file (default: stdout).`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runScaffold(cmd, a)
		},
	}

	cmd.Flags().StringVar(&a.metric, "metric", "",
		"Prometheus metric name; must match ^[a-zA-Z_:][a-zA-Z0-9_:]*$ (required)")
	cmd.Flags().StringVar(&a.metricType, "type", "",
		"metric type: counter, gauge, histogram, or summary (required)")
	cmd.Flags().StringVar(&a.section, "section", "",
		"dashboard section (required): overview traffic errors latency saturation cpu memory disk network pods workloads resources")
	cmd.Flags().StringVar(&a.profile, "profile", "",
		"dashgen profile: service, infra, or k8s (required)")
	cmd.Flags().StringVar(&a.name, "name", "",
		"recipe name override; defaults to --metric with non-[a-z0-9_] mapped to _")
	cmd.Flags().Float64Var(&a.confidence, "confidence", 0.85,
		"match confidence in [0.0, 1.0]")
	cmd.Flags().StringVar(&a.output, "output", "",
		"write to this file path instead of stdout")
	cmd.Flags().BoolVar(&a.force, "force", false,
		"overwrite --output file if it already exists")
	cmd.Flags().StringVar(&a.withPair, "with-pair", "",
		`add a suffix_swap pair_with block (e.g. "_size_bytes↔_avail_bytes")`)

	_ = cmd.MarkFlagRequired("metric")
	_ = cmd.MarkFlagRequired("type")
	_ = cmd.MarkFlagRequired("section")
	_ = cmd.MarkFlagRequired("profile")

	return cmd
}

func runScaffold(cmd *cobra.Command, a scaffoldArgs) error {
	if err := validateScaffoldArgs(a); err != nil {
		return err
	}

	recipeName := a.name
	if recipeName == "" {
		recipeName = metricToRecipeName(a.metric)
	} else {
		recipeName = metricToRecipeName(a.name)
	}

	yaml := buildScaffoldYAML(recipeName, a)

	// Post-scaffold sanity check (CT3 §3.2): produced YAML must pass schema
	// validation. A failure here is a bug in the scaffold templates (exit 64).
	if err := validateScaffoldOutput(yaml); err != nil {
		return fmt.Errorf("internal error: scaffold produced invalid YAML: %w", err)
	}

	// adversary: CT10 — scaffolder as attack vector. Without an explicit
	// --output, scaffold emits to stdout only; no implicit filesystem write.
	if a.output == "" {
		fmt.Fprint(cmd.OutOrStdout(), yaml)
		return nil
	}
	return writeScaffoldFile(cmd, a.output, a.force, yaml)
}

// validateScaffoldArgs validates all flag values per CT3.
func validateScaffoldArgs(a scaffoldArgs) error {
	// --metric: Prometheus metric-name constraint (CT3).
	if runes := []rune(a.metric); len(runes) > scaffoldMetricMaxRunes {
		return fmt.Errorf("flag --metric: %q exceeds max length %d runes", a.metric, scaffoldMetricMaxRunes)
	}
	if !scaffoldMetricRe.MatchString(a.metric) {
		return fmt.Errorf("flag --metric: %q must match ^[a-zA-Z_:][a-zA-Z0-9_:]*$", a.metric)
	}

	// --type: closed enum.
	switch a.metricType {
	case "counter", "gauge", "histogram", "summary":
	default:
		return fmt.Errorf("flag --type: %q must be one of [counter gauge histogram summary]", a.metricType)
	}

	// --section: closed enum from schema.cue #Section.
	if !validScaffoldSections[a.section] {
		return fmt.Errorf("flag --section: %q must be one of [overview traffic errors latency saturation cpu memory disk network pods workloads resources]", a.section)
	}

	// --profile: closed enum.
	switch a.profile {
	case "service", "infra", "k8s":
	default:
		return fmt.Errorf("flag --profile: %q must be one of [service infra k8s]", a.profile)
	}

	// --confidence: numeric range.
	if a.confidence < 0.0 || a.confidence > 1.0 {
		return fmt.Errorf("flag --confidence: %g must be in [0.0, 1.0]", a.confidence)
	}

	// --name: same constraint as --metric (CT3).
	if a.name != "" {
		if runes := []rune(a.name); len(runes) > scaffoldNameMaxRunes {
			return fmt.Errorf("flag --name: %q exceeds max length %d runes", a.name, scaffoldNameMaxRunes)
		}
		if !scaffoldMetricRe.MatchString(a.name) {
			return fmt.Errorf("flag --name: %q must match ^[a-zA-Z_:][a-zA-Z0-9_:]*$", a.name)
		}
	}

	// --with-pair: parse and validate <from>↔<to> halves (CT3).
	if a.withPair != "" {
		if err := validateScaffoldPair(a.withPair); err != nil {
			return err
		}
	}

	return nil
}

// validateScaffoldPair parses and validates "--with-pair <from>↔<to>" (CT3).
func validateScaffoldPair(spec string) error {
	parts := strings.SplitN(spec, "↔", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("flag --with-pair: %q must be <from>↔<to> (e.g. '_size_bytes↔_avail_bytes')", spec)
	}
	if !scaffoldMetricRe.MatchString(parts[0]) {
		return fmt.Errorf("flag --with-pair: from-suffix %q must match ^[a-zA-Z_:][a-zA-Z0-9_:]*$", parts[0])
	}
	if !scaffoldMetricRe.MatchString(parts[1]) {
		return fmt.Errorf("flag --with-pair: to-suffix %q must match ^[a-zA-Z_:][a-zA-Z0-9_:]*$", parts[1])
	}
	return nil
}

// metricToRecipeName converts a Prometheus metric name to a valid recipe name:
//  1. lowercase all runes
//  2. replace non-[a-z0-9_] with _
//  3. truncate to 64 runes
//  4. prepend "m_" if result does not start with [a-z]
func metricToRecipeName(metric string) string {
	var b strings.Builder
	for _, r := range metric {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(unicode.ToLower(r))
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	s := b.String()
	if runes := []rune(s); len(runes) > scaffoldNameMaxRunes {
		s = string(runes[:scaffoldNameMaxRunes])
	}
	if len(s) == 0 || s[0] < 'a' || s[0] > 'z' {
		s = "m_" + s
	}
	return s
}

// buildScaffoldYAML assembles the full recipe YAML string for the given args.
func buildScaffoldYAML(recipeName string, a scaffoldArgs) string {
	tier := "Tier-A"
	if a.metricType == "histogram" || a.metricType == "summary" {
		tier = "Tier-B"
	}

	var sb strings.Builder

	// Header comment + apiVersion + kind + metadata.
	sb.WriteString(fmt.Sprintf(
		"# DashGen Scaffold Recipe — %s (%s)\n"+
			"#\n"+
			"# Generated by: dashgen recipe scaffold --metric %s --type %s --section %s --profile %s\n"+
			"# Validate:     dashgen recipe lint <this-file>\n"+
			"# Test:         dashgen recipe test <this-file> --fixture testdata/fixtures/service-realistic\n"+
			"\napiVersion: dashgen.io/v1\nkind: Recipe\n"+
			"\nmetadata:\n"+
			"  name: %s\n"+
			"  section: %s\n"+
			"  profile: %s\n"+
			"  confidence: %.2f\n"+
			"  tier: v0.3\n"+
			"  description: \"Scaffolded %s recipe for %s.\"\n",
		a.metricType, tier,
		a.metric, a.metricType, a.section, a.profile,
		recipeName, a.section, a.profile, a.confidence,
		a.metricType, a.metric,
	))

	// Optional pair_with block.
	if a.withPair != "" {
		parts := strings.SplitN(a.withPair, "↔", 2)
		sb.WriteString(fmt.Sprintf(
			"\npair_with:\n"+
				"  on_missing: omit\n"+
				"  suffix_swap:\n"+
				"    from_suffix: %s\n"+
				"    to_suffix: %s\n",
			parts[0], parts[1],
		))
	}

	// match block.
	sb.WriteString(fmt.Sprintf(
		"\nmatch:\n"+
			"  type: %s\n"+
			"  name_equals: %s\n",
		a.metricType, a.metric,
	))

	// panels block.
	sb.WriteString("\n")
	switch a.metricType {
	case "counter":
		sb.WriteString(scaffoldCounterPanels())
	case "gauge":
		sb.WriteString(scaffoldGaugePanels())
	case "histogram":
		sb.WriteString(scaffoldHistogramPanels())
	case "summary":
		sb.WriteString(scaffoldSummaryPanels())
	}

	return sb.String()
}

func scaffoldCounterPanels() string {
	return `panels:
  - title_template: 'Rate: {{ .Metric }}'
    kind: timeseries
    unit: ops/sec
    query_template: 'sum by ({{ groupBy . }}) (rate({{ .Metric }}[{{ .Window }}]))'
    legend_template: '{{ legendFor . }}'
    rationale_template: 'Counter "{{ .Metric }}" rate over {{ .Window }}.'
`
}

func scaffoldGaugePanels() string {
	return `panels:
  - title_template: '{{ .Metric }}'
    kind: timeseries
    unit: short
    query_template: 'max by ({{ groupBy . }}) ({{ .Metric }})'
    legend_template: '{{ legendFor . }}'
    rationale_template: 'Gauge "{{ .Metric }}" maximum by instance.'
`
}

func scaffoldHistogramPanels() string {
	return `panels:
  - title_template: 'p50 latency: {{ .Metric }}'
    kind: timeseries
    unit: seconds
    query_template: 'histogram_quantile(0.50, sum by (le, {{ groupBy . }}) (rate({{ .Metric }}_bucket[{{ .Window }}])))'
    legend_template: 'p50 {{ legendFor . }}'
    rationale_template: 'Histogram "{{ .Metric }}" p50 latency over {{ .Window }}.'
  - title_template: 'p95 latency: {{ .Metric }}'
    kind: timeseries
    unit: seconds
    query_template: 'histogram_quantile(0.95, sum by (le, {{ groupBy . }}) (rate({{ .Metric }}_bucket[{{ .Window }}])))'
    legend_template: 'p95 {{ legendFor . }}'
    rationale_template: 'Histogram "{{ .Metric }}" p95 latency over {{ .Window }}.'
  - title_template: 'p99 latency: {{ .Metric }}'
    kind: timeseries
    unit: seconds
    query_template: 'histogram_quantile(0.99, sum by (le, {{ groupBy . }}) (rate({{ .Metric }}_bucket[{{ .Window }}])))'
    legend_template: 'p99 {{ legendFor . }}'
    rationale_template: 'Histogram "{{ .Metric }}" p99 latency over {{ .Window }}.'
`
}

func scaffoldSummaryPanels() string {
	return `panels:
  - title_template: 'Quantiles: {{ .Metric }}'
    kind: timeseries
    unit: seconds
    query_template: 'max by (quantile, {{ groupBy . }}) ({{ .Metric }})'
    legend_template: '{{ legendFor . }}'
    rationale_template: 'Summary "{{ .Metric }}" quantile distribution.'
`
}

// validateScaffoldOutput writes yaml to a temp file and validates it through
// the recipe loader. A failure indicates a bug in the scaffold templates.
func validateScaffoldOutput(yaml string) error {
	f, err := os.CreateTemp("", "dashgen-scaffold-*.yaml")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	name := f.Name()
	defer os.Remove(name) //nolint:errcheck
	if _, err := f.WriteString(yaml); err != nil {
		f.Close() //nolint:errcheck
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	_, err = recipes.LoadFile(context.Background(), recipes.LoaderConfig{}, name, recipes.SourceUser)
	return err
}

// writeScaffoldFile writes the YAML to path, respecting --force.
// adversary: CT1 — refuses to overwrite an existing file unless --force is set.
// adversary: CT4 — uses O_EXCL|O_CREATE so an existing symlink at <path> is
// rejected (the link target is never followed and never modified).
func writeScaffoldFile(cmd *cobra.Command, path string, force bool, yaml string) error {
	var (
		f   *os.File
		err error
	)
	if force {
		f, err = os.Create(path)
	} else {
		f, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	}
	if err != nil {
		if !force && os.IsExist(err) {
			return fmt.Errorf("output file %q already exists; use --force to overwrite", path)
		}
		return fmt.Errorf("open output file %q: %w", path, err)
	}
	defer f.Close() //nolint:errcheck
	if _, err := f.WriteString(yaml); err != nil {
		return fmt.Errorf("write output file %q: %w", path, err)
	}
	abs, _ := filepath.Abs(path)
	fmt.Fprintf(cmd.OutOrStdout(), "Wrote: %s\n", abs)
	fmt.Fprintf(cmd.OutOrStdout(), "Next:  $EDITOR %s\n      dashgen recipe lint %s\n", abs, abs)
	return nil
}
