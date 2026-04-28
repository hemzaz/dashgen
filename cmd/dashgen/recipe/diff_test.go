// diff_test.go — tests for `dashgen recipe diff` (T6B.2 + T6B.3).
//
// Coverage targets:
//
//   T6B.2 functional DoD (RECIPES-CLI.md §3.8):
//     - flag validation (each flag rejected on bad value)
//     - identical recipes → exit 0
//     - recipes differing in group_by → exit 1, diff names the changed panel
//       and the field whose value moved (query)
//     - text + json + unified output formats render
//     - --against-builtin: user override vs built-in YAML produces named diff
//
//   T6B.3 edge cases (RECIPES-CLI.md §3.8 + V0.3-PLAN Phase 6B):
//     - cross-profile diff: A=service vs B=infra → both load, command runs
//       to completion under -race (no panic, no hang); result is well-formed
//     - missing recipe: fileA path absent → ErrDiffLoadFailure; --against-builtin
//       on a name with no built-in → ErrDiffNotFound
//     - missing metric: recipe targets a metric absent from the fixture →
//       both sides emit zero panels → diff is empty → exit 0
//
// All tests are -race-safe (no shared state) and t.Parallel() friendly.
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

// runDiffCmdErr executes diff and returns (stdout, error) — used by tests
// that assert on the returned sentinel error.
func runDiffCmdErr(t *testing.T, args []string) (string, error) {
	t.Helper()
	out, _, err := execCmd(t, newDiffCmd(), args)
	return out, err
}

// writeDiffRecipe writes body to dir/<name>.yaml and returns the absolute
// path. Mirrors the pattern in test_test.go.
func writeDiffRecipe(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name+".yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write recipe %s: %v", p, err)
	}
	return p
}

// diffServiceRecipe returns a service-profile recipe targeting
// api_http_requests_total (a counter present in service-realistic). The
// extraGroupBy argument is dropped into the rendered query template via
// groupByWith so two recipes with different group_by tails produce
// different `sum by (...)` clauses — the canonical "differs in group_by"
// scenario from §3.8's example output.
func diffServiceRecipe(name string, extraGroupBy string) string {
	groupBy := `groupBy .`
	if extraGroupBy != "" {
		groupBy = `groupByWith . "` + extraGroupBy + `"`
	}
	return `apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: ` + name + `
  section: traffic
  profile: service
  confidence: 0.80
  tier: v0.3
  description: "diff test recipe"
match:
  type: counter
  name_equals: api_http_requests_total
panels:
  - title_template: 'rate: {{ .Metric }}'
    kind: timeseries
    unit: ops/sec
    query_template: 'sum by ({{ ` + groupBy + ` }}) (rate({{ .Metric }}[{{ .Window }}]))'
    legend_template: '{{ legendFor . }}'
`
}

// diffInfraRecipe returns an infra-profile recipe. Used by the
// cross-profile edge case. Targets a counter that exists in
// service-realistic so BuildPanels under profileService would emit, but
// declaring profile=infra makes BuildPanels emit zero panels (per
// internal/recipes profile gating, demonstrated by
// TestServiceHTTPRate_BuildPanels with ProfileInfra).
func diffInfraRecipe(name string) string {
	return `apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: ` + name + `
  section: saturation
  profile: infra
  confidence: 0.70
  tier: v0.3
  description: "infra-profile recipe for cross-profile diff edge case"
match:
  type: counter
  name_equals: api_http_requests_total
panels:
  - title_template: 'infra rate: {{ .Metric }}'
    kind: timeseries
    unit: ops/sec
    query_template: 'sum by ({{ groupBy . }}) (rate({{ .Metric }}[{{ .Window }}]))'
    legend_template: '{{ legendFor . }}'
`
}

// diffMissingMetricRecipe returns a recipe that targets a metric name
// that intentionally does not exist in any fixture — so BuildPanels
// emits zero panels. Used by the "missing metric" edge case to verify
// that an empty-vs-empty diff exits 0.
func diffMissingMetricRecipe(name string) string {
	return `apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: ` + name + `
  section: traffic
  profile: service
  confidence: 0.55
  tier: v0.3
  description: "diff test recipe targeting a non-existent metric"
match:
  type: counter
  name_equals: definitely_not_in_any_fixture_total
panels:
  - title_template: 'rate: {{ .Metric }}'
    kind: timeseries
    unit: short
    query_template: 'rate({{ .Metric }}[{{ .Window }}])'
    legend_template: '{{ legendFor . }}'
`
}

// =============================================================================
// Flag validation
// =============================================================================

func TestDiff_RejectsMissingFixture(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := writeDiffRecipe(t, dir, "a", diffServiceRecipe("diff_a", ""))
	b := writeDiffRecipe(t, dir, "b", diffServiceRecipe("diff_b", ""))

	_, err := runDiffCmdErr(t, []string{a, b})
	if err == nil {
		t.Fatal("expected error when --fixture is missing, got nil")
	}
	if !strings.Contains(err.Error(), "--fixture") {
		t.Errorf("error should mention --fixture, got: %v", err)
	}
}

func TestDiff_RejectsBadOutput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := writeDiffRecipe(t, dir, "a", diffServiceRecipe("diff_a", ""))
	b := writeDiffRecipe(t, dir, "b", diffServiceRecipe("diff_b", ""))

	_, err := runDiffCmdErr(t, []string{
		a, b,
		"--fixture", fixtureServiceRealistic,
		"--output", "xml",
	})
	if err == nil {
		t.Fatal("expected error for --output xml, got nil")
	}
	if !strings.Contains(err.Error(), "--output") {
		t.Errorf("error should mention --output, got: %v", err)
	}
}

func TestDiff_RejectsBadProfile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := writeDiffRecipe(t, dir, "a", diffServiceRecipe("diff_a", ""))

	_, err := runDiffCmdErr(t, []string{
		a, "--against-builtin",
		"--fixture", fixtureServiceRealistic,
		"--profile", "bogus",
	})
	if err == nil {
		t.Fatal("expected error for --profile bogus, got nil")
	}
	if !strings.Contains(err.Error(), "--profile") {
		t.Errorf("error should mention --profile, got: %v", err)
	}
}

func TestDiff_RejectsTwoArgsWithAgainstBuiltin(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := writeDiffRecipe(t, dir, "a", diffServiceRecipe("diff_a", ""))
	b := writeDiffRecipe(t, dir, "b", diffServiceRecipe("diff_b", ""))

	_, err := runDiffCmdErr(t, []string{
		a, b,
		"--against-builtin",
		"--fixture", fixtureServiceRealistic,
	})
	if err == nil {
		t.Fatal("expected error when --against-builtin given two positional args, got nil")
	}
	if !strings.Contains(err.Error(), "--against-builtin") {
		t.Errorf("error should mention --against-builtin, got: %v", err)
	}
}

func TestDiff_RejectsSingleArgWithoutAgainstBuiltin(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := writeDiffRecipe(t, dir, "a", diffServiceRecipe("diff_a", ""))

	_, err := runDiffCmdErr(t, []string{a, "--fixture", fixtureServiceRealistic})
	if err == nil {
		t.Fatal("expected error when given one positional arg without --against-builtin, got nil")
	}
	if !strings.Contains(err.Error(), "two positional") {
		t.Errorf("error should mention two-positional requirement, got: %v", err)
	}
}

// =============================================================================
// Identical recipes → exit 0 (DoD)
// =============================================================================

func TestDiff_IdenticalRecipes_NoDiff(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := diffServiceRecipe("diff_id", "")
	a := writeDiffRecipe(t, dir, "a", body)
	b := writeDiffRecipe(t, dir, "b", body)

	out, err := runDiffCmdErr(t, []string{a, b, "--fixture", fixtureServiceRealistic})
	if err != nil {
		t.Fatalf("identical recipes must exit 0, got err: %v\nstdout:\n%s", err, out)
	}
	// Summary counts must all be zero — body of "no diff" branch.
	if !strings.Contains(out, "Panels added:    0") {
		t.Errorf("expected 'Panels added:    0', got:\n%s", out)
	}
	if !strings.Contains(out, "Panels removed:  0") {
		t.Errorf("expected 'Panels removed:  0', got:\n%s", out)
	}
	if !strings.Contains(out, "Panels changed:  0") {
		t.Errorf("expected 'Panels changed:  0', got:\n%s", out)
	}
}

// =============================================================================
// Differing recipes → exit 1 + named diff (DoD)
// =============================================================================

func TestDiff_DifferingGroupBy_ExitMismatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Both recipes target the same metric but render different group_by
	// — the canonical example from RECIPES-CLI.md §3.8.
	a := writeDiffRecipe(t, dir, "a", diffServiceRecipe("diff_gb", ""))
	b := writeDiffRecipe(t, dir, "b", diffServiceRecipe("diff_gb", "region"))

	out, err := runDiffCmdErr(t, []string{a, b, "--fixture", fixtureServiceRealistic})
	if !errors.Is(err, ErrDiffMismatch) {
		t.Fatalf("expected ErrDiffMismatch (exit 1), got: %v\nstdout:\n%s", err, out)
	}
	// Header carries the recipe name.
	if !strings.Contains(out, "Recipe: diff_gb") {
		t.Errorf("expected 'Recipe: diff_gb' header, got:\n%s", out)
	}
	// Changed section identifies the panel by title and shows the query
	// field as the differing field.
	if !strings.Contains(out, "Panels changed:") {
		t.Errorf("expected 'Panels changed:' section, got:\n%s", out)
	}
	if !strings.Contains(out, "[rate: api_http_requests_total]") {
		t.Errorf("expected named panel '[rate: api_http_requests_total]', got:\n%s", out)
	}
	if !strings.Contains(out, "query:") {
		t.Errorf("expected 'query:' diff field, got:\n%s", out)
	}
	// The B-side query must include the extra `region` label introduced
	// by groupByWith. The A-side must not.
	if !strings.Contains(out, "region") {
		t.Errorf("B-side query should include the extra 'region' group-by label, got:\n%s", out)
	}
	if !strings.Contains(out, "Panels changed:  1") {
		t.Errorf("expected summary 'Panels changed:  1', got:\n%s", out)
	}
}

// =============================================================================
// JSON format
// =============================================================================

func TestDiff_JSONOutput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := writeDiffRecipe(t, dir, "a", diffServiceRecipe("diff_json", ""))
	b := writeDiffRecipe(t, dir, "b", diffServiceRecipe("diff_json", "region"))

	out, err := runDiffCmdErr(t, []string{
		a, b,
		"--fixture", fixtureServiceRealistic,
		"--output", "json",
	})
	if !errors.Is(err, ErrDiffMismatch) {
		t.Fatalf("expected ErrDiffMismatch (exit 1), got: %v\nstdout:\n%s", err, out)
	}

	var r diffResult
	if jerr := json.Unmarshal([]byte(out), &r); jerr != nil {
		t.Fatalf("json unmarshal: %v\noutput:\n%s", jerr, out)
	}
	if r.A.Recipe != "diff_json" || r.B.Recipe != "diff_json" {
		t.Errorf("recipe names: a=%q b=%q, want diff_json/diff_json", r.A.Recipe, r.B.Recipe)
	}
	if len(r.Changed) != 1 {
		t.Fatalf("expected 1 changed panel, got %d (full result: %+v)", len(r.Changed), r)
	}
	if r.Changed[0].Title != "rate: api_http_requests_total" {
		t.Errorf("changed title = %q, want 'rate: api_http_requests_total'", r.Changed[0].Title)
	}
	// At least the query field must differ; legend may also differ.
	gotQuery := false
	for _, fd := range r.Changed[0].Fields {
		if fd.Field == "query" {
			gotQuery = true
			if !strings.Contains(fd.B, "region") {
				t.Errorf("B-side query should contain 'region', got: %s", fd.B)
			}
		}
	}
	if !gotQuery {
		t.Errorf("expected 'query' in differing fields, got: %+v", r.Changed[0].Fields)
	}
}

// =============================================================================
// Unified format
// =============================================================================

func TestDiff_UnifiedOutput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := writeDiffRecipe(t, dir, "a", diffServiceRecipe("diff_uni", ""))
	b := writeDiffRecipe(t, dir, "b", diffServiceRecipe("diff_uni", "region"))

	out, err := runDiffCmdErr(t, []string{
		a, b,
		"--fixture", fixtureServiceRealistic,
		"--output", "unified",
	})
	if !errors.Is(err, ErrDiffMismatch) {
		t.Fatalf("expected ErrDiffMismatch (exit 1), got: %v\nstdout:\n%s", err, out)
	}
	// --- / +++ header pair (RECIPES-CLI.md §5.4).
	if !strings.HasPrefix(out, "--- ") {
		t.Errorf("expected unified output to start with '--- ', got:\n%s", out)
	}
	if !strings.Contains(out, "+++ ") {
		t.Errorf("expected unified output to contain '+++ ', got:\n%s", out)
	}
	// Hunk header keyed by panel title.
	if !strings.Contains(out, "@@ rate: api_http_requests_total @@") {
		t.Errorf("expected hunk '@@ rate: api_http_requests_total @@', got:\n%s", out)
	}
	// Standard +/- lines for the differing query.
	if !strings.Contains(out, "-query:") || !strings.Contains(out, "+query:") {
		t.Errorf("expected -/+ query lines in unified output, got:\n%s", out)
	}
}

// =============================================================================
// --against-builtin (DoD)
// =============================================================================

// overrideOfServiceCPU is a user override of the built-in service_cpu
// recipe that introduces an extra group_by label, producing a different
// rendered query than the built-in. Built-in YAML at:
//
//	internal/recipes/data/service/service_cpu.yaml
//
// The built-in name 'service_cpu' is in the Tier-A migrated corpus and
// is registered by NewProfileRegistries() unconditionally — independent
// of worker-1's in-flight Tier-B migration.
func overrideOfServiceCPU() string {
	return `apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: service_cpu
  section: saturation
  profile: service
  confidence: 0.85
  tier: v0.3
  description: "user override of service_cpu introducing extra group-by label"
  tags: [cpu, process, counter]

match:
  type: counter
  name_equals_any: [process_cpu_seconds_total, container_cpu_usage_seconds_total]

panels:
  - title_template: 'CPU (cores used): {{ .Metric }}'
    kind: timeseries
    unit: short
    query_template: 'sum by ({{ groupByWith . "region" }}) (rate({{ .Metric }}[{{ .Window }}]))'
    legend_template: '{{ legendFor . }}'
    rationale_template: 'CPU-seconds counter "{{ .Metric }}"; rate over {{ .Window }} yields cores consumed.'
`
}

func TestDiff_AgainstBuiltin_ProducesNamedDiff(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	override := writeDiffRecipe(t, dir, "service_cpu_override", overrideOfServiceCPU())

	out, err := runDiffCmdErr(t, []string{
		override,
		"--against-builtin",
		"--fixture", fixtureServiceRealistic,
	})
	if !errors.Is(err, ErrDiffMismatch) {
		t.Fatalf("expected ErrDiffMismatch (override differs from built-in), got: %v\nstdout:\n%s", err, out)
	}
	// Header must carry the recipe name and the synthetic <built-in> label
	// for side B (so users can tell which side the override is on).
	if !strings.Contains(out, "Recipe: service_cpu") {
		t.Errorf("expected 'Recipe: service_cpu' header, got:\n%s", out)
	}
	if !strings.Contains(out, "<built-in>") {
		t.Errorf("expected '<built-in>' label for side B, got:\n%s", out)
	}
	if !strings.Contains(out, "Panels changed:") {
		t.Errorf("expected 'Panels changed:' section, got:\n%s", out)
	}
}

// =============================================================================
// Edge case T6B.3: cross-profile diff
// =============================================================================

// TestDiff_CrossProfile_NoCrash verifies that when fileA declares
// profile=service and fileB declares profile=infra (and the fixture is
// a service fixture), the diff command still completes correctly under
// -race: it loads both, runs both BuildPanels, and produces a valid
// diff result. The infra recipe BuildPanels with profile=infra emits 0
// panels per recipes-package profile gating, so all of A's panels become
// "removed in B" — but the command must not panic or hang.
func TestDiff_CrossProfile_NoCrash(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := writeDiffRecipe(t, dir, "a", diffServiceRecipe("diff_xp_a", ""))
	b := writeDiffRecipe(t, dir, "b", diffInfraRecipe("diff_xp_b"))

	out, _, execErr := execCmd(t, newDiffCmd(), []string{
		a, b,
		"--fixture", fixtureServiceRealistic,
		"--output", "json",
	})
	// Either exit 0 or ErrDiffMismatch — both are valid for cross-profile
	// where one side may yield zero panels. What's important is no other
	// error class (no fixture/load/notfound errors).
	if execErr != nil && !errors.Is(execErr, ErrDiffMismatch) {
		t.Fatalf("cross-profile diff returned unexpected error: %v\nstdout:\n%s", execErr, out)
	}
	var r diffResult
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("json unmarshal failed for cross-profile diff: %v\noutput:\n%s", err, out)
	}
	if r.A.Profile != "service" {
		t.Errorf("A.Profile = %q, want service", r.A.Profile)
	}
	if r.B.Profile != "infra" {
		t.Errorf("B.Profile = %q, want infra", r.B.Profile)
	}
}

// =============================================================================
// Edge case T6B.3: missing recipe file (fileA / fileB doesn't exist)
// =============================================================================

func TestDiff_MissingRecipeFileA(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	b := writeDiffRecipe(t, dir, "b", diffServiceRecipe("diff_b", ""))

	_, err := runDiffCmdErr(t, []string{
		"/no/such/recipe.yaml", b,
		"--fixture", fixtureServiceRealistic,
	})
	if !errors.Is(err, ErrDiffLoadFailure) {
		t.Errorf("expected ErrDiffLoadFailure for missing fileA, got: %v", err)
	}
}

func TestDiff_MissingRecipeFileB(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := writeDiffRecipe(t, dir, "a", diffServiceRecipe("diff_a", ""))

	_, err := runDiffCmdErr(t, []string{
		a, "/no/such/recipe.yaml",
		"--fixture", fixtureServiceRealistic,
	})
	if !errors.Is(err, ErrDiffLoadFailure) {
		t.Errorf("expected ErrDiffLoadFailure for missing fileB, got: %v", err)
	}
}

// =============================================================================
// Edge case T6B.3: --against-builtin with a recipe name that has no built-in
// =============================================================================

func TestDiff_AgainstBuiltin_NotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Recipe with a name that definitely is not in the built-in catalog.
	a := writeDiffRecipe(t, dir, "no_such_builtin",
		diffServiceRecipe("definitely_no_such_builtin_recipe_xyz", ""))

	_, err := runDiffCmdErr(t, []string{
		a, "--against-builtin",
		"--fixture", fixtureServiceRealistic,
	})
	if !errors.Is(err, ErrDiffNotFound) {
		t.Errorf("expected ErrDiffNotFound when --against-builtin name has no match, got: %v", err)
	}
}

// =============================================================================
// Edge case T6B.3: missing metric (recipe targets metric absent from fixture)
// =============================================================================

func TestDiff_MissingMetricInFixture_NoDiff(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := diffMissingMetricRecipe("diff_missing_metric")
	a := writeDiffRecipe(t, dir, "a", body)
	b := writeDiffRecipe(t, dir, "b", body)

	// Both sides target the same non-existent metric → both BuildPanels
	// outputs are empty → the diff is empty → exit 0.
	out, err := runDiffCmdErr(t, []string{
		a, b,
		"--fixture", fixtureServiceRealistic,
	})
	if err != nil {
		t.Fatalf("missing-metric symmetric case must exit 0, got err: %v\nstdout:\n%s", err, out)
	}
	if !strings.Contains(out, "Panels added:    0") {
		t.Errorf("expected zero added, got:\n%s", out)
	}
	if !strings.Contains(out, "Panels removed:  0") {
		t.Errorf("expected zero removed, got:\n%s", out)
	}
	if !strings.Contains(out, "Panels changed:  0") {
		t.Errorf("expected zero changed, got:\n%s", out)
	}
}

// =============================================================================
// Edge case T6B.3: missing fixture
// =============================================================================

func TestDiff_FixtureNotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := writeDiffRecipe(t, dir, "a", diffServiceRecipe("diff_a", ""))
	b := writeDiffRecipe(t, dir, "b", diffServiceRecipe("diff_b", ""))

	_, err := runDiffCmdErr(t, []string{
		a, b,
		"--fixture", "/no/such/fixture/dir",
	})
	if !errors.Is(err, ErrDiffFixtureError) {
		t.Errorf("expected ErrDiffFixtureError, got: %v", err)
	}
}

// =============================================================================
// Determinism (zero flake under -race)
// =============================================================================

func TestDiff_DeterministicOutput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := writeDiffRecipe(t, dir, "a", diffServiceRecipe("diff_det", ""))
	b := writeDiffRecipe(t, dir, "b", diffServiceRecipe("diff_det", "region"))

	for _, output := range []string{"text", "json", "unified"} {
		out1, _, _ := execCmd(t, newDiffCmd(), []string{a, b, "--fixture", fixtureServiceRealistic, "--output", output})
		out2, _, _ := execCmd(t, newDiffCmd(), []string{a, b, "--fixture", fixtureServiceRealistic, "--output", output})
		if out1 != out2 {
			t.Errorf("--output %s is not deterministic across runs", output)
		}
		// Sanity: output is non-empty when a real diff exists.
		if strings.TrimSpace(out1) == "" {
			t.Errorf("--output %s produced empty output for a non-empty diff", output)
		}
	}
}
