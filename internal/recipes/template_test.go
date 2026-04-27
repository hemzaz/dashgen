package recipes

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// --- fixture helpers -------------------------------------------------------

// minimalRecipe is just enough YAML structure to extract template strings
// from testdata/valid/*.yaml without pulling in the full YAML loader (T1A.1).
type minimalRecipe struct {
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Panels []struct {
		QueryTemplate  string `yaml:"query_template"`
		LegendTemplate string `yaml:"legend_template"`
		TitleTemplate  string `yaml:"title_template"`
	} `yaml:"panels"`
}

// syntheticHTTPRateCtx returns a RenderContext suitable for rendering
// service_http_rate's query_template.
func syntheticHTTPRateCtx() RenderContext {
	return RenderContext{
		Metric:      "http_requests_total",
		Type:        "counter",
		ScopeFilter: `job="$job"`,
		Window:      "5m",
		GroupBy:     []string{"job", "route"},
		Labels: map[string]string{
			"job":   "",
			"route": "",
		},
		LabelList: []string{"job", "route"},
	}
}

// --- TestParse_HappyPath ---------------------------------------------------

// TestParse_HappyPath parses all template strings from the 7
// testdata/valid/*.yaml fixtures. Skips if the directory is absent
// (pre-T1A.1 environments without the fixtures in place).
func TestParse_HappyPath(t *testing.T) {
	dir := filepath.Join("testdata", "valid")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("testdata/valid not available: %v", err)
	}

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", entry.Name(), err)
		}

		var rec minimalRecipe
		if err := yaml.Unmarshal(data, &rec); err != nil {
			t.Fatalf("yaml.Unmarshal %s: %v", entry.Name(), err)
		}

		base := strings.TrimSuffix(entry.Name(), ".yaml")
		for i, panel := range rec.Panels {
			for kind, src := range map[string]string{
				"query_template":  panel.QueryTemplate,
				"legend_template": panel.LegendTemplate,
				"title_template":  panel.TitleTemplate,
			} {
				if src == "" {
					continue
				}
				name := strings.Join([]string{base, kind, string(rune('0'+i))}, "/")
				if _, err := Parse(name, src); err != nil {
					t.Errorf("Parse(%s): %v", name, err)
				}
			}
		}
	}
}

// --- forbidden-directive tests --------------------------------------------

func TestParse_RejectsDefine(t *testing.T) {
	src := `{{ define "x" }}hello{{ end }}`
	_, err := Parse("reject_define", src)
	if err == nil {
		t.Fatal("expected error for {{ define }}, got nil")
	}
	if !errors.Is(err, ErrForbiddenDirective) {
		t.Errorf("expected ErrForbiddenDirective, got: %v", err)
	}
}

func TestParse_RejectsTemplate(t *testing.T) {
	src := `{{ template "x" . }}`
	_, err := Parse("reject_template", src)
	if err == nil {
		t.Fatal("expected error for {{ template }}, got nil")
	}
	if !errors.Is(err, ErrForbiddenDirective) {
		t.Errorf("expected ErrForbiddenDirective, got: %v", err)
	}
}

func TestParse_RejectsBlock(t *testing.T) {
	src := `{{ block "x" . }}fallback{{ end }}`
	_, err := Parse("reject_block", src)
	if err == nil {
		t.Fatal("expected error for {{ block }}, got nil")
	}
	if !errors.Is(err, ErrForbiddenDirective) {
		t.Errorf("expected ErrForbiddenDirective, got: %v", err)
	}
}

// TestParse_RejectsUndefinedHelper verifies that referencing an unknown
// function in a template is caught at parse time (not render time), because
// text/template validates all function names during parsing.
func TestParse_RejectsUndefinedHelper(t *testing.T) {
	src := `{{ doesNotExist . }}`
	_, err := Parse("reject_unknown_helper", src)
	if err == nil {
		t.Fatal("expected error for unknown helper, got nil")
	}
}

// --- render tests ----------------------------------------------------------

// TestRender_HappyPath renders service_http_rate's query_template and
// checks the resulting PromQL string against the expected golden value.
func TestRender_HappyPath(t *testing.T) {
	src := `sum by ({{ groupBy . }}) (
  rate({{ .Metric }}{ {{ .ScopeFilter }} }[{{ .Window }}])
)`
	tpl, err := Parse("service_http_rate/query", src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	ctx := syntheticHTTPRateCtx()
	got, err := tpl.Render(ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	want := `sum by (job, route) (
  rate(http_requests_total{ job="$job" }[5m])
)`
	if got != want {
		t.Errorf("render mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

// TestRender_DeterministicMapIteration verifies that rendering the same
// template twice with identical input produces byte-equal output.
func TestRender_DeterministicMapIteration(t *testing.T) {
	src := `{{ .Metric }} {{ groupBy . }} {{ legendFor . }}`
	tpl, err := Parse("determinism_check", src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	ctx := syntheticHTTPRateCtx()
	first, err := tpl.Render(ctx)
	if err != nil {
		t.Fatalf("first Render: %v", err)
	}
	second, err := tpl.Render(ctx)
	if err != nil {
		t.Fatalf("second Render: %v", err)
	}
	if first != second {
		t.Errorf("non-deterministic output:\nfirst:  %q\nsecond: %q", first, second)
	}
}

// TestRender_OutputCap verifies that rendering a template whose output
// exceeds 16 KB returns ErrTemplateOutputTooLarge.
func TestRender_OutputCap(t *testing.T) {
	// Template that emits the ScopeFilter field verbatim.
	// A ScopeFilter > 16 KB drives the output over the cap.
	src := `{{ .ScopeFilter }}`
	tpl, err := Parse("output_cap", src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	ctx := RenderContext{
		ScopeFilter: strings.Repeat("x", maxTemplateOutputBytes+1),
	}
	_, err = tpl.Render(ctx)
	if err == nil {
		t.Fatal("expected ErrTemplateOutputTooLarge, got nil")
	}
	if !errors.Is(err, ErrTemplateOutputTooLarge) {
		t.Errorf("expected ErrTemplateOutputTooLarge, got: %v", err)
	}
}

// --- helper unit tests -----------------------------------------------------

func TestHelper_GroupBy(t *testing.T) {
	ctx := RenderContext{GroupBy: []string{"instance", "job"}}
	tpl := MustParse("groupby", `{{ groupBy . }}`)
	got, err := tpl.Render(ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "instance, job" {
		t.Errorf("groupBy: got %q, want %q", got, "instance, job")
	}
}

func TestHelper_GroupBy_Empty(t *testing.T) {
	ctx := RenderContext{GroupBy: nil}
	tpl := MustParse("groupby_empty", `{{ groupBy . }}`)
	got, err := tpl.Render(ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "" {
		t.Errorf("groupBy empty: got %q, want %q", got, "")
	}
}

func TestHelper_GroupByWith(t *testing.T) {
	ctx := RenderContext{GroupBy: []string{"instance", "job"}}
	tpl := MustParse("groupbywith", `{{ groupByWith . "le" }}`)
	got, err := tpl.Render(ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "instance, job, le" {
		t.Errorf("groupByWith: got %q, want %q", got, "instance, job, le")
	}
}

func TestHelper_GroupByWith_DropsExisting(t *testing.T) {
	ctx := RenderContext{GroupBy: []string{"job", "route"}}
	tpl := MustParse("groupbywith_dedup", `{{ groupByWith . "job" "le" }}`)
	got, err := tpl.Render(ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// "job" is already in GroupBy — must not appear twice.
	if got != "job, route, le" {
		t.Errorf("groupByWith dedup: got %q, want %q", got, "job, route, le")
	}
}

func TestHelper_GroupByWith_DropsBanned(t *testing.T) {
	ctx := RenderContext{GroupBy: []string{"job"}}
	// "user_id" is in bannedLabels; must be silently dropped.
	tpl := MustParse("groupbywith_banned", `{{ groupByWith . "user_id" "le" }}`)
	got, err := tpl.Render(ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "job, le" {
		t.Errorf("groupByWith banned: got %q, want %q", got, "job, le")
	}
}

func TestHelper_LegendFor(t *testing.T) {
	ctx := RenderContext{GroupBy: []string{"job", "route"}}
	tpl := MustParse("legendfor", `{{ legendFor . }}`)
	got, err := tpl.Render(ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// legendFor wraps each label in Grafana's {{ }} syntax.
	if got != "{{job}} {{route}}" {
		t.Errorf("legendFor: got %q, want %q", got, "{{job}} {{route}}")
	}
}

func TestHelper_LegendFor_Empty(t *testing.T) {
	ctx := RenderContext{GroupBy: nil}
	tpl := MustParse("legendfor_empty", `{{ legendFor . }}`)
	got, err := tpl.Render(ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "" {
		t.Errorf("legendFor empty: got %q, want %q", got, "")
	}
}

func TestHelper_BucketName_AppendsSuffix(t *testing.T) {
	ctx := RenderContext{Metric: "http_request_duration_seconds"}
	tpl := MustParse("bucketname_append", `{{ bucketName .Metric }}`)
	got, err := tpl.Render(ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "http_request_duration_seconds_bucket" {
		t.Errorf("bucketName append: got %q, want %q", got, "http_request_duration_seconds_bucket")
	}
}

func TestHelper_BucketName_Idempotent(t *testing.T) {
	ctx := RenderContext{Metric: "http_request_duration_seconds_bucket"}
	tpl := MustParse("bucketname_idempotent", `{{ bucketName .Metric }}`)
	got, err := tpl.Render(ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "http_request_duration_seconds_bucket" {
		t.Errorf("bucketName idempotent: got %q, want %q", got, "http_request_duration_seconds_bucket")
	}
}

func TestHelper_StripSuffix_Removes(t *testing.T) {
	ctx := RenderContext{Metric: "http_request_size_bytes_bucket"}
	tpl := MustParse("stripsuffix_removes", `{{ stripSuffix .Metric "_bucket" }}`)
	got, err := tpl.Render(ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "http_request_size_bytes" {
		t.Errorf("stripSuffix removes: got %q, want %q", got, "http_request_size_bytes")
	}
}

func TestHelper_StripSuffix_NoSuffix(t *testing.T) {
	ctx := RenderContext{Metric: "http_request_size_bytes"}
	tpl := MustParse("stripsuffix_noop", `{{ stripSuffix .Metric "_bucket" }}`)
	got, err := tpl.Render(ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "http_request_size_bytes" {
		t.Errorf("stripSuffix noop: got %q, want %q", got, "http_request_size_bytes")
	}
}

// TestRender_PairContext verifies that .Pair.Name is accessible when Pair
// is populated — as used by infra_filesystem_usage's query_template.
func TestRender_PairContext(t *testing.T) {
	src := `{{ .Metric }} / {{ .Pair.Name }}`
	tpl, err := Parse("pair_context", src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ctx := RenderContext{
		Metric: "node_filesystem_size_bytes",
		Pair: &PairContext{
			Name: "node_filesystem_avail_bytes",
			Type: "gauge",
		},
	}
	got, err := tpl.Render(ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "node_filesystem_size_bytes / node_filesystem_avail_bytes"
	if got != want {
		t.Errorf("pair render: got %q, want %q", got, want)
	}
}

// TestParse_ASTNodeBudgetExceeded verifies that a template exceeding
// maxASTNodes returns ErrASTNodeBudgetExceeded. Each {{ .Metric }} action
// contributes ~4 AST nodes; 80 repetitions comfortably exceeds 256.
func TestParse_ASTNodeBudgetExceeded(t *testing.T) {
	src := strings.Repeat(`{{ .Metric }}`, 80)
	_, err := Parse("budget_exceeded", src)
	if err == nil {
		t.Fatal("expected ErrASTNodeBudgetExceeded, got nil")
	}
	if !errors.Is(err, ErrASTNodeBudgetExceeded) {
		t.Errorf("expected ErrASTNodeBudgetExceeded, got: %v", err)
	}
}
