package recipe

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// =============================================================================
// Helpers
// =============================================================================

// runTestCmd executes the test subcommand against a freshly-built cobra
// command. Returns stdout. Test failures fatalf with the captured
// stderr+stdout for triage.
func runTestCmd(t *testing.T, args []string) string {
	t.Helper()
	out, _ := mustExecCmd(t, newTestCmd(), args)
	return out
}

// runTestCmdErr executes test and returns the (stdout, error) pair.
// Used by negative tests asserting error categories.
func runTestCmdErr(t *testing.T, args []string) (string, error) {
	t.Helper()
	out, _, err := execCmd(t, newTestCmd(), args)
	return out, err
}

// writeRecipeFile writes body to dir/<name>.yaml and returns the absolute
// path. Used in tests so each scenario builds its own recipe in TempDir
// without depending on the (in-flight) Tier-A migration corpus.
func writeRecipeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name+".yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write recipe %s: %v", p, err)
	}
	return p
}

// fixtureServiceRealistic is the path to the canonical service fixture used
// by the v0.1+ test suite. Resolved relative to the package's test cwd
// (cmd/dashgen/recipe), so we walk up three levels to the repo root.
const fixtureServiceRealistic = "../../../testdata/fixtures/service-realistic"

// =============================================================================
// Flag validation
// =============================================================================

func TestTest_RejectsMissingFixture(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeRecipeFile(t, dir, "trivial", trivialServiceRecipe())

	_, err := runTestCmdErr(t, []string{p})
	if err == nil {
		t.Fatal("expected error when --fixture is missing, got nil")
	}
	if !strings.Contains(err.Error(), "--fixture") {
		t.Errorf("error should mention --fixture, got: %v", err)
	}
}

func TestTest_RejectsBadOutput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeRecipeFile(t, dir, "trivial", trivialServiceRecipe())

	_, err := runTestCmdErr(t, []string{p, "--fixture", fixtureServiceRealistic, "--output", "xml"})
	if err == nil {
		t.Fatal("expected error for --output xml, got nil")
	}
	if !strings.Contains(err.Error(), "--output") {
		t.Errorf("error should mention --output, got: %v", err)
	}
}

func TestTest_RejectsBadProfile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeRecipeFile(t, dir, "trivial", trivialServiceRecipe())

	_, err := runTestCmdErr(t, []string{p, "--fixture", fixtureServiceRealistic, "--profile", "bogus"})
	if err == nil {
		t.Fatal("expected error for --profile bogus, got nil")
	}
	if !strings.Contains(err.Error(), "--profile") {
		t.Errorf("error should mention --profile, got: %v", err)
	}
}

func TestTest_RejectsMissingFile(t *testing.T) {
	t.Parallel()
	_, err := runTestCmdErr(t, []string{"/no/such/recipe.yaml", "--fixture", fixtureServiceRealistic})
	if err == nil {
		t.Fatal("expected ErrTestLoadFailure for missing file, got nil")
	}
	if !errors.Is(err, ErrTestLoadFailure) {
		t.Errorf("expected ErrTestLoadFailure, got: %v", err)
	}
}

// =============================================================================
// Fixture errors
// =============================================================================

func TestTest_FixtureNotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeRecipeFile(t, dir, "trivial", trivialServiceRecipe())

	_, err := runTestCmdErr(t, []string{p, "--fixture", "/no/such/fixture/dir"})
	if err == nil {
		t.Fatal("expected ErrTestFixtureError, got nil")
	}
	if !errors.Is(err, ErrTestFixtureError) {
		t.Errorf("expected ErrTestFixtureError, got: %v", err)
	}
}

// =============================================================================
// Recipe matches → panels emitted (DoD)
// =============================================================================

// trivialServiceRecipe targets api_http_requests_total — a counter present
// in service-realistic. It uses name_equals so the match is unambiguous and
// produces exactly one panel.
func trivialServiceRecipe() string {
	return `apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: t_recipe_match
  section: traffic
  profile: service
  confidence: 0.75
  tier: v0.3
  description: "Test recipe matching api_http_requests_total."
match:
  type: counter
  name_equals: api_http_requests_total
panels:
  - title_template: 'rate: {{ .Metric }}'
    kind: timeseries
    unit: ops/sec
    query_template: 'sum by ({{ groupBy . }}) (rate({{ .Metric }}[{{ .Window }}]))'
    legend_template: '{{ legendFor . }}'
`
}

func TestTest_RecipeMatches_PanelsEmitted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeRecipeFile(t, dir, "match", trivialServiceRecipe())

	out := runTestCmd(t, []string{p, "--fixture", fixtureServiceRealistic})

	// Output must mention the matched metric and at least one panel.
	if !strings.Contains(out, "api_http_requests_total (counter)") {
		t.Errorf("expected match line for api_http_requests_total, got:\n%s", out)
	}
	if !strings.Contains(out, "Recipe: t_recipe_match (profile=service)") {
		t.Errorf("expected recipe header, got:\n%s", out)
	}
	if !strings.Contains(out, "Matched metrics: 1") {
		t.Errorf("expected 'Matched metrics: 1', got:\n%s", out)
	}
	if !strings.Contains(out, "Panels: 1") {
		t.Errorf("expected 'Panels: 1', got:\n%s", out)
	}
	// Verdict line must be present, even if it's a refusal — what matters
	// for DoD is that BuildPanels emitted a panel and validate ran.
	if !strings.Contains(out, "[Verdict:") {
		t.Errorf("expected verdict line, got:\n%s", out)
	}
}

func TestTest_RecipeMatches_JSONOutput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeRecipeFile(t, dir, "match", trivialServiceRecipe())

	out := runTestCmd(t, []string{p, "--fixture", fixtureServiceRealistic, "--output", "json"})

	var results []testRecipeResult
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatalf("json unmarshal: %v\noutput:\n%s", err, out)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Recipe != "t_recipe_match" {
		t.Errorf("Recipe = %q, want t_recipe_match", r.Recipe)
	}
	if r.Profile != "service" {
		t.Errorf("Profile = %q, want service", r.Profile)
	}
	if len(r.Matches) != 1 || r.Matches[0].Metric != "api_http_requests_total" {
		t.Errorf("expected single match for api_http_requests_total, got %+v", r.Matches)
	}
	if len(r.Panels) != 1 {
		t.Errorf("expected 1 panel, got %d", len(r.Panels))
	}
}

// =============================================================================
// Recipe doesn't match → empty result with non-match reasons (DoD)
// =============================================================================

// nonMatchingServiceRecipe targets api_http_requests_total but constrains
// type to gauge — the metric is a counter, so the predicate fails. Because
// the metric appears in name_equals, the CLI must surface a non-match
// reason for it.
func nonMatchingServiceRecipe() string {
	return `apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: t_recipe_nomatch
  section: traffic
  profile: service
  confidence: 0.55
  tier: v0.3
  description: "Test recipe that should NOT match api_http_requests_total (type mismatch)."
match:
  type: gauge
  name_equals: api_http_requests_total
panels:
  - title_template: 'should not render: {{ .Metric }}'
    kind: timeseries
    unit: short
    query_template: '{{ .Metric }}'
    legend_template: '{{ legendFor . }}'
`
}

func TestTest_RecipeNoMatch_EmitsNonMatchReason(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeRecipeFile(t, dir, "nomatch", nonMatchingServiceRecipe())

	out := runTestCmd(t, []string{p, "--fixture", fixtureServiceRealistic})

	if !strings.Contains(out, "Matched metrics: 0") {
		t.Errorf("expected zero matches, got:\n%s", out)
	}
	if !strings.Contains(out, "Panels: 0") {
		t.Errorf("expected zero panels, got:\n%s", out)
	}
	// Non-match block must appear and must call out the type mismatch
	// (the simplest top-level explanation our heuristic produces).
	if !strings.Contains(out, "Non-matches") {
		t.Errorf("expected non-match section, got:\n%s", out)
	}
	if !strings.Contains(out, "api_http_requests_total") {
		t.Errorf("non-match should reference api_http_requests_total, got:\n%s", out)
	}
	if !strings.Contains(out, "type mismatch") {
		t.Errorf("non-match reason should mention type mismatch, got:\n%s", out)
	}
}

// =============================================================================
// Pair-missing case (DoD)
// =============================================================================

// pairMissingRecipe matches api_http_requests_total (a counter that exists
// in service-realistic) but declares a pair via suffix_swap pointing to a
// metric name that absolutely does not exist. With on_missing=omit, the
// recipe must Match the metric (Match is predicate-only) but BuildPanels
// must emit zero panels because pair resolution drops the metric.
func pairMissingRecipe() string {
	return `apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: t_recipe_pair_missing
  section: traffic
  profile: service
  confidence: 0.55
  tier: v0.3
  description: "Test recipe whose pair is intentionally absent."
pair_with:
  suffix_swap:
    from_suffix: _total
    to_suffix: _zzz_definitely_does_not_exist_total
  on_missing: omit
match:
  type: counter
  name_equals: api_http_requests_total
panels:
  - title_template: 'paired: {{ .Metric }}'
    kind: timeseries
    unit: short
    requires_pair: true
    query_template: 'rate({{ .Metric }}[{{ .Window }}]) / rate({{ .Pair.Name }}[{{ .Window }}])'
    legend_template: '{{ legendFor . }}'
`
}

func TestTest_PairMissing_NoPanels(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeRecipeFile(t, dir, "pair_missing", pairMissingRecipe())

	out := runTestCmd(t, []string{p, "--fixture", fixtureServiceRealistic})

	// Pair-missing under on_missing=omit collapses the metric out of
	// BuildPanels, so 0 panels must be emitted regardless of whether the
	// match step recorded the metric. The diagnostic line "matched metrics
	// produced no panels" must fire when matches > 0 panels == 0.
	if !strings.Contains(out, "Panels: 0") {
		t.Errorf("expected zero panels for pair-missing case, got:\n%s", out)
	}
}

// =============================================================================
// --metric filter
// =============================================================================

func TestTest_MetricFilterNarrowsInventory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeRecipeFile(t, dir, "match", trivialServiceRecipe())

	// With --metric set to the exact name, the fixture's discover layer
	// substring-filters down to (at most) that metric. The recipe must
	// still match it.
	out := runTestCmd(t, []string{
		p,
		"--fixture", fixtureServiceRealistic,
		"--metric", "api_http_requests_total",
	})
	if !strings.Contains(out, "Matched metrics: 1") {
		t.Errorf("expected single match under --metric filter, got:\n%s", out)
	}
	if !strings.Contains(out, "api_http_requests_total") {
		t.Errorf("filtered match must mention api_http_requests_total, got:\n%s", out)
	}
}

// =============================================================================
// --verbose
// =============================================================================

func TestTest_VerboseShowsFullQuery(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeRecipeFile(t, dir, "match", trivialServiceRecipe())

	out := runTestCmd(t, []string{
		p,
		"--fixture", fixtureServiceRealistic,
		"--verbose",
	})
	// The query template renders to: sum by (...) (rate(api_http_requests_total[5m]))
	if !strings.Contains(out, "rate(api_http_requests_total") {
		t.Errorf("verbose output should preserve full PromQL, got:\n%s", out)
	}
}

// =============================================================================
// Determinism
// =============================================================================

func TestTest_DeterministicOutput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeRecipeFile(t, dir, "match", trivialServiceRecipe())

	for _, output := range []string{"text", "json"} {
		out1 := runTestCmd(t, []string{p, "--fixture", fixtureServiceRealistic, "--output", output})
		out2 := runTestCmd(t, []string{p, "--fixture", fixtureServiceRealistic, "--output", output})
		if out1 != out2 {
			t.Errorf("--output %s is not deterministic across runs", output)
		}
	}
}
