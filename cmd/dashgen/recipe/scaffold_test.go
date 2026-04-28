package recipe

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dashgen/internal/recipes"
)

// testScaffoldTypeValid is a shared helper: runs scaffold for the given type
// and validates the produced YAML through the recipe loader (schema + template
// validation).
func testScaffoldTypeValid(t *testing.T, metricType, metric, section string) {
	t.Helper()
	t.Parallel()

	var buf bytes.Buffer
	cmd := newScaffoldCmd()
	cmd.SetOut(&buf)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{
		"--metric", metric,
		"--type", metricType,
		"--section", section,
		"--profile", "service",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute(%s): %v", metricType, err)
	}

	got := buf.String()
	if got == "" {
		t.Fatalf("expected non-empty YAML output for type %s", metricType)
	}

	// Write to a temp file and validate through the loader (mirrors the
	// TestInit_ExampleYAMLIsValid pattern from init_test.go).
	dir := t.TempDir()
	p := filepath.Join(dir, "scaffold.yaml")
	if err := os.WriteFile(p, []byte(got), 0o644); err != nil {
		t.Fatalf("write temp yaml: %v", err)
	}
	if _, err := recipes.LoadFile(context.Background(), recipes.LoaderConfig{}, p, recipes.SourceUser); err != nil {
		t.Errorf("schema validation failed for --type %s: %v\nYAML:\n%s", metricType, err, got)
	}
}

// TestScaffold_CounterValid — counter produces schema-valid YAML.
func TestScaffold_CounterValid(t *testing.T) {
	testScaffoldTypeValid(t, "counter", "mycorp_requests_total", "traffic")
}

// TestScaffold_GaugeValid — gauge produces schema-valid YAML.
func TestScaffold_GaugeValid(t *testing.T) {
	testScaffoldTypeValid(t, "gauge", "mycorp_queue_depth", "saturation")
}

// TestScaffold_HistogramValid — histogram produces schema-valid YAML with 3 panels.
func TestScaffold_HistogramValid(t *testing.T) {
	testScaffoldTypeValid(t, "histogram", "mycorp_request_duration_seconds", "latency")
}

// TestScaffold_SummaryValid — summary produces schema-valid YAML.
func TestScaffold_SummaryValid(t *testing.T) {
	testScaffoldTypeValid(t, "summary", "mycorp_process_latency_seconds", "latency")
}

// TestScaffold_CT3_BadMetricRejected verifies that --metric values containing
// shell metacharacters or path-traversal sequences are rejected at flag
// validation with a non-zero exit (RECIPES-CLI.md §9 CT3).
func TestScaffold_CT3_BadMetricRejected(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		metric string
	}{
		{"path_traversal", "../etc/passwd"},
		{"semicolon", "metric;rm"},
		{"double_ampersand", "metric&&evil"},
		{"pipe", "metric|pipe"},
		{"backtick", "metric`cmd`"},
		{"dollar_expansion", "metric$var"},
		{"space", "metric with spaces"},
		{"newline", "metric\ntab"},
		{"empty", ""},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cmd := newScaffoldCmd()
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			cmd.SetArgs([]string{
				"--metric", tc.metric,
				"--type", "counter",
				"--section", "traffic",
				"--profile", "service",
			})
			err := cmd.Execute()
			if err == nil {
				t.Errorf("expected error for metric %q, got nil", tc.metric)
			}
		})
	}
}

// TestScaffold_BadTypeRejected verifies that an invalid --type produces an error.
func TestScaffold_BadTypeRejected(t *testing.T) {
	t.Parallel()
	cmd := newScaffoldCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{
		"--metric", "my_metric",
		"--type", "timeseries",
		"--section", "traffic",
		"--profile", "service",
	})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error for invalid --type, got nil")
	}
}

// TestScaffold_BadSectionRejected verifies that an invalid --section produces an error.
func TestScaffold_BadSectionRejected(t *testing.T) {
	t.Parallel()
	cmd := newScaffoldCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{
		"--metric", "my_metric",
		"--type", "counter",
		"--section", "satturation",
		"--profile", "service",
	})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error for invalid --section, got nil")
	}
}

// TestScaffold_BadProfileRejected verifies that an invalid --profile produces an error.
func TestScaffold_BadProfileRejected(t *testing.T) {
	t.Parallel()
	cmd := newScaffoldCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{
		"--metric", "my_metric",
		"--type", "counter",
		"--section", "traffic",
		"--profile", "lambda",
	})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error for invalid --profile, got nil")
	}
}

// TestScaffold_WithPairProducesSuffixSwap verifies that --with-pair produces a
// suffix_swap pair_with block and the result passes schema validation.
func TestScaffold_WithPairProducesSuffixSwap(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	cmd := newScaffoldCmd()
	cmd.SetOut(&buf)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{
		"--metric", "node_filesystem_size_bytes",
		"--type", "gauge",
		"--section", "disk",
		"--profile", "infra",
		"--with-pair", "_size_bytes↔_avail_bytes",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got := buf.String()

	// Structural checks on the pair_with block.
	for _, want := range []string{
		"suffix_swap:",
		"from_suffix: _size_bytes",
		"to_suffix: _avail_bytes",
		"on_missing: omit",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\nYAML:\n%s", want, got)
		}
	}

	// Validate through the loader — pair_with block must pass schema.
	dir := t.TempDir()
	p := filepath.Join(dir, "pair.yaml")
	if err := os.WriteFile(p, []byte(got), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := recipes.LoadFile(context.Background(), recipes.LoaderConfig{}, p, recipes.SourceUser); err != nil {
		t.Errorf("schema validation failed for --with-pair output: %v\nYAML:\n%s", err, got)
	}
}

// TestScaffold_WithPairBadSpec verifies that malformed --with-pair values are rejected.
func TestScaffold_WithPairBadSpec(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		spec string
	}{
		{"no_arrow", "no_arrow"},
		{"arrow_only", "↔"},
		{"from_only", "from_only↔"},
		{"to_only", "↔to_only"},
		{"bad_from_char", "from&bad↔to"},
		{"bad_to_char", "from↔to&bad"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cmd := newScaffoldCmd()
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			cmd.SetArgs([]string{
				"--metric", "my_metric",
				"--type", "gauge",
				"--section", "saturation",
				"--profile", "service",
				"--with-pair", tc.spec,
			})
			if err := cmd.Execute(); err == nil {
				t.Errorf("expected error for --with-pair %q, got nil", tc.spec)
			}
		})
	}
}

// TestScaffold_OutputToFile verifies that --output writes YAML to disk and
// stdout contains "Wrote:".
func TestScaffold_OutputToFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	out := filepath.Join(dir, "scaffold.yaml")

	var buf bytes.Buffer
	cmd := newScaffoldCmd()
	cmd.SetOut(&buf)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{
		"--metric", "mycorp_queue_depth",
		"--type", "gauge",
		"--section", "saturation",
		"--profile", "service",
		"--output", out,
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if len(data) == 0 {
		t.Error("output file is empty")
	}
	if !strings.Contains(buf.String(), "Wrote:") {
		t.Errorf("stdout %q does not contain 'Wrote:'", buf.String())
	}
}

// TestScaffold_RefusesOverwrite verifies that --output refuses to overwrite an
// existing file without --force.
func TestScaffold_RefusesOverwrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	out := filepath.Join(dir, "existing.yaml")
	if err := os.WriteFile(out, []byte("pre-existing"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cmd := newScaffoldCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{
		"--metric", "my_metric",
		"--type", "counter",
		"--section", "traffic",
		"--profile", "service",
		"--output", out,
	})

	if err := cmd.Execute(); err == nil {
		t.Error("expected error on existing file without --force, got nil")
	}

	// File must still contain the original content.
	data, _ := os.ReadFile(out)
	if string(data) != "pre-existing" {
		t.Error("file was overwritten without --force")
	}
}

// TestScaffold_ForceOverwrite verifies that --force allows overwriting.
func TestScaffold_ForceOverwrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	out := filepath.Join(dir, "existing.yaml")
	if err := os.WriteFile(out, []byte("old content"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cmd := newScaffoldCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{
		"--metric", "my_metric",
		"--type", "counter",
		"--section", "traffic",
		"--profile", "service",
		"--output", out,
		"--force",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute with --force: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(data), "old content") {
		t.Error("file still contains old content after --force overwrite")
	}
	if !strings.Contains(string(data), "apiVersion: dashgen.io/v1") {
		t.Error("output file does not contain expected apiVersion after --force")
	}
}

// TestScaffold_MetricToRecipeName verifies the metric-name sanitization helper.
func TestScaffold_MetricToRecipeName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"my_metric", "my_metric"},
		{"myHTTPRequests", "myhttprequests"},
		{"MY_METRIC", "my_metric"},
		{"metric:colon", "metric_colon"},
		{"_leading_underscore", "m__leading_underscore"},
		{"123numeric", "m_123numeric"},
	}
	for _, tc := range cases {
		got := metricToRecipeName(tc.in)
		if got != tc.want {
			t.Errorf("metricToRecipeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestScaffold_HistogramHasThreePanels verifies the histogram scaffold emits
// exactly the p50/p95/p99 trio.
func TestScaffold_HistogramHasThreePanels(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	cmd := newScaffoldCmd()
	cmd.SetOut(&buf)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{
		"--metric", "http_request_duration_seconds",
		"--type", "histogram",
		"--section", "latency",
		"--profile", "service",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got := buf.String()
	for _, want := range []string{"p50", "p95", "p99"} {
		if !strings.Contains(got, want) {
			t.Errorf("histogram output missing %s panel\nYAML:\n%s", want, got)
		}
	}
}
