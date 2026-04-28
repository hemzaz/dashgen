// explain_test.go — tests for `dashgen recipe explain` (T6B.1).
//
// Coverage:
//   - flag validation (negative tests for each --flag)
//   - recipe-not-found / metric-not-found → ErrExplainNotFound (exit 2)
//   - text (tree) format renders all_of / any_of / not / primitive shapes
//   - json format encodes the evaluation tree
//   - CT5 leak test: assert label NAMES leak through but label VALUES never do,
//     even when the fixture's series.json carries sensitive-looking values.
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

// runExplainCmd executes the explain subcommand against a freshly-built cobra
// command (no shared state between tests). Returns stdout. Failure fatalfs
// with the captured stderr+stdout for triage.
func runExplainCmd(t *testing.T, args []string) string {
	t.Helper()
	out, _ := mustExecCmd(t, newExplainCmd(), args)
	return out
}

// runExplainCmdErr executes explain and returns the (stdout, error) pair.
// Used by negative tests asserting error categories.
func runExplainCmdErr(t *testing.T, args []string) (string, error) {
	t.Helper()
	out, _, err := execCmd(t, newExplainCmd(), args)
	return out, err
}

// writeFixture writes a minimal fixture directory under root with the given
// metadata.json and series.json bytes. Returns the absolute fixture path.
func writeFixture(t *testing.T, root, metadataJSON, seriesJSON string) string {
	t.Helper()
	dir := filepath.Join(root, "fixture")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "metadata.json"), []byte(metadataJSON), 0o644); err != nil {
		t.Fatalf("write metadata.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "series.json"), []byte(seriesJSON), 0o644); err != nil {
		t.Fatalf("write series.json: %v", err)
	}
	return dir
}

// writeExplainRecipe writes a user recipe YAML to dir/<name>.yaml. The body
// is parameterised so tests can craft predicates that exercise specific
// AST shapes (any_of / all_of / not / primitive). Returns the recipe dir.
func writeExplainRecipe(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir recipe dir: %v", err)
	}
	p := filepath.Join(dir, name+".yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write user recipe %s: %v", p, err)
	}
	return dir
}

// recipeYAML returns a counter-rate Tier-A YAML recipe whose match block
// is provided by the caller. The panels block is fixed (it is irrelevant
// to explain — explain only walks Spec.Match).
func recipeYAML(name, profile, match string) string {
	return `apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: ` + name + `
  section: traffic
  profile: ` + profile + `
  confidence: 0.85
  tier: v0.3
  description: "explain test recipe"
match:
` + indentBlock(match, "  ") + `
panels:
  - title_template: 'rate: {{ .Metric }}'
    kind: timeseries
    unit: ops/sec
    query_template: 'sum by ({{ groupBy . }}) (rate({{ .Metric }}[{{ .Window }}]))'
    legend_template: '{{ legendFor . }}'
`
}

// indentBlock prefixes each non-empty line of s with prefix.
func indentBlock(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, ln := range lines {
		if ln == "" {
			continue
		}
		lines[i] = prefix + ln
	}
	return strings.Join(lines, "\n")
}

// =============================================================================
// Flag validation
// =============================================================================

func TestExplain_RejectsMissingName(t *testing.T) {
	t.Parallel()
	_, err := runExplainCmdErr(t, []string{"--metric", "foo", "--fixture", "/tmp/x"})
	if err == nil {
		t.Fatal("expected error for missing --name, got nil")
	}
	if !strings.Contains(err.Error(), "--name") {
		t.Errorf("error should mention --name, got: %v", err)
	}
}

func TestExplain_RejectsMissingMetric(t *testing.T) {
	t.Parallel()
	_, err := runExplainCmdErr(t, []string{"--name", "foo", "--fixture", "/tmp/x"})
	if err == nil {
		t.Fatal("expected error for missing --metric, got nil")
	}
	if !strings.Contains(err.Error(), "--metric") {
		t.Errorf("error should mention --metric, got: %v", err)
	}
}

func TestExplain_RejectsMissingFixture(t *testing.T) {
	t.Parallel()
	_, err := runExplainCmdErr(t, []string{"--name", "foo", "--metric", "bar"})
	if err == nil {
		t.Fatal("expected error for missing --fixture, got nil")
	}
	if !strings.Contains(err.Error(), "--fixture") {
		t.Errorf("error should mention --fixture, got: %v", err)
	}
}

func TestExplain_RejectsBadOutput(t *testing.T) {
	t.Parallel()
	_, err := runExplainCmdErr(t, []string{
		"--name", "foo", "--metric", "bar", "--fixture", "/tmp/x", "--output", "xml",
	})
	if err == nil {
		t.Fatal("expected error for --output xml, got nil")
	}
	if !strings.Contains(err.Error(), "--output") {
		t.Errorf("error should mention --output, got: %v", err)
	}
}

func TestExplain_RejectsBadProfile(t *testing.T) {
	t.Parallel()
	_, err := runExplainCmdErr(t, []string{
		"--name", "foo", "--metric", "bar", "--fixture", "/tmp/x", "--profile", "bogus",
	})
	if err == nil {
		t.Fatal("expected error for --profile bogus, got nil")
	}
	if !strings.Contains(err.Error(), "--profile") {
		t.Errorf("error should mention --profile, got: %v", err)
	}
}

// =============================================================================
// Not-found semantics — exit code 2 via ErrExplainNotFound
// =============================================================================

func TestExplain_RecipeNotFoundReturnsSentinel(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	fixtureDir := writeFixture(t, root,
		`{"http_requests_total":[{"type":"counter","help":"x","unit":""}]}`,
		`[{"__name__":"http_requests_total","job":"x","instance":"i1"}]`,
	)
	_, err := runExplainCmdErr(t, []string{
		"--name", "zzz_no_such_recipe",
		"--metric", "http_requests_total",
		"--fixture", fixtureDir,
		"--no-user-recipes",
	})
	if err == nil {
		t.Fatal("expected ErrExplainNotFound, got nil")
	}
	if !errors.Is(err, ErrExplainNotFound) {
		t.Errorf("expected errors.Is(err, ErrExplainNotFound), got: %v", err)
	}
}

func TestExplain_MetricNotFoundReturnsSentinel(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	recipeDir := writeExplainRecipe(t, filepath.Join(root, "recipes"),
		"explain_simple",
		recipeYAML("explain_simple", "service", "type: counter\nname_equals: foo_total\n"),
	)
	fixtureDir := writeFixture(t, root,
		`{"foo_total":[{"type":"counter","help":"x","unit":""}]}`,
		`[{"__name__":"foo_total","job":"x"}]`,
	)
	_, err := runExplainCmdErr(t, []string{
		"--name", "explain_simple",
		"--metric", "no_such_metric",
		"--fixture", fixtureDir,
		"--recipes-dir", recipeDir,
	})
	if err == nil {
		t.Fatal("expected ErrExplainNotFound for missing metric, got nil")
	}
	if !errors.Is(err, ErrExplainNotFound) {
		t.Errorf("expected errors.Is(err, ErrExplainNotFound), got: %v", err)
	}
	if !strings.Contains(err.Error(), "no_such_metric") {
		t.Errorf("error should mention the missing metric name, got: %v", err)
	}
}

// =============================================================================
// Text (tree) output
// =============================================================================

// TestExplain_TextRendersPrimitive_AllPass covers a primitive predicate
// where every check passes; the RESULT line must read true.
func TestExplain_TextRendersPrimitive_AllPass(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	recipeDir := writeExplainRecipe(t, filepath.Join(root, "recipes"),
		"explain_simple",
		recipeYAML("explain_simple", "service", "type: counter\nname_equals: foo_total\n"),
	)
	fixtureDir := writeFixture(t, root,
		`{"foo_total":[{"type":"counter","help":"x","unit":""}]}`,
		`[{"__name__":"foo_total","job":"x"}]`,
	)
	out := runExplainCmd(t, []string{
		"--name", "explain_simple",
		"--metric", "foo_total",
		"--fixture", fixtureDir,
		"--recipes-dir", recipeDir,
	})

	wants := []string{
		"Recipe: explain_simple",
		"Metric: foo_total (counter)",
		"type: counter",
		"name_equals: foo_total",
		"RESULT: true",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("text output missing %q\nfull output:\n%s", w, out)
		}
	}
}

// TestExplain_TextRendersPrimitive_OneFails covers a primitive predicate
// where one check fails; the RESULT line must read false.
func TestExplain_TextRendersPrimitive_OneFails(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	recipeDir := writeExplainRecipe(t, filepath.Join(root, "recipes"),
		"explain_mismatch",
		recipeYAML("explain_mismatch", "service", "type: gauge\nname_equals: foo_total\n"),
	)
	fixtureDir := writeFixture(t, root,
		`{"foo_total":[{"type":"counter","help":"x","unit":""}]}`,
		`[{"__name__":"foo_total","job":"x"}]`,
	)
	out := runExplainCmd(t, []string{
		"--name", "explain_mismatch",
		"--metric", "foo_total",
		"--fixture", fixtureDir,
		"--recipes-dir", recipeDir,
	})

	if !strings.Contains(out, "RESULT: false") {
		t.Errorf("expected RESULT: false (gauge != counter), got:\n%s", out)
	}
	// The failing check should carry the ✗ marker.
	if !strings.Contains(out, "✗") {
		t.Errorf("expected at least one ✗ marker, got:\n%s", out)
	}
}

// TestExplain_TextRendersAnyOf covers a top-level any_of node with
// multiple primitive children. The tree must include "(any_of)" and
// per-child labels.
func TestExplain_TextRendersAnyOf(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	body := `any_of:
  - name_equals: foo_total
  - name_equals: bar_total
`
	recipeDir := writeExplainRecipe(t, filepath.Join(root, "recipes"),
		"explain_anyof",
		recipeYAML("explain_anyof", "service", body),
	)
	fixtureDir := writeFixture(t, root,
		`{"bar_total":[{"type":"counter","help":"x","unit":""}]}`,
		`[{"__name__":"bar_total","job":"x"}]`,
	)
	out := runExplainCmd(t, []string{
		"--name", "explain_anyof",
		"--metric", "bar_total",
		"--fixture", fixtureDir,
		"--recipes-dir", recipeDir,
	})
	if !strings.Contains(out, "(any_of)") {
		t.Errorf("expected (any_of) in tree, got:\n%s", out)
	}
	if !strings.Contains(out, "[0]") || !strings.Contains(out, "[1]") {
		t.Errorf("expected ordinal child labels [0] and [1], got:\n%s", out)
	}
	if !strings.Contains(out, "RESULT: true") {
		t.Errorf("expected RESULT: true (matches second branch), got:\n%s", out)
	}
}

// TestExplain_TextRendersAllOfAndNot covers a nested all_of+not
// composition. The tree must include both kind annotations.
func TestExplain_TextRendersAllOfAndNot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	body := `all_of:
  - type: counter
  - not:
      name_has_prefix: zz_
`
	recipeDir := writeExplainRecipe(t, filepath.Join(root, "recipes"),
		"explain_allof_not",
		recipeYAML("explain_allof_not", "service", body),
	)
	fixtureDir := writeFixture(t, root,
		`{"foo_total":[{"type":"counter","help":"x","unit":""}]}`,
		`[{"__name__":"foo_total","job":"x"}]`,
	)
	out := runExplainCmd(t, []string{
		"--name", "explain_allof_not",
		"--metric", "foo_total",
		"--fixture", fixtureDir,
		"--recipes-dir", recipeDir,
	})
	if !strings.Contains(out, "(all_of)") {
		t.Errorf("expected (all_of) in tree, got:\n%s", out)
	}
	if !strings.Contains(out, "(not)") {
		t.Errorf("expected (not) in tree, got:\n%s", out)
	}
	if !strings.Contains(out, "RESULT: true") {
		t.Errorf("expected RESULT: true, got:\n%s", out)
	}
}

// =============================================================================
// JSON output
// =============================================================================

func TestExplain_JSONOutput(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	recipeDir := writeExplainRecipe(t, filepath.Join(root, "recipes"),
		"explain_json",
		recipeYAML("explain_json", "service", "type: counter\nname_equals: foo_total\n"),
	)
	fixtureDir := writeFixture(t, root,
		`{"foo_total":[{"type":"counter","help":"x","unit":""}]}`,
		`[{"__name__":"foo_total","job":"x"}]`,
	)
	out := runExplainCmd(t, []string{
		"--name", "explain_json",
		"--metric", "foo_total",
		"--fixture", fixtureDir,
		"--recipes-dir", recipeDir,
		"--output", "json",
	})

	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("json unmarshal failed: %v\nOutput:\n%s", err, out)
	}
	if obj["recipe"] != "explain_json" {
		t.Errorf("recipe = %v, want explain_json", obj["recipe"])
	}
	if obj["metric"] != "foo_total" {
		t.Errorf("metric = %v, want foo_total", obj["metric"])
	}
	if obj["metric_type"] != "counter" {
		t.Errorf("metric_type = %v, want counter", obj["metric_type"])
	}
	if obj["result"] != true {
		t.Errorf("result = %v, want true", obj["result"])
	}
	tree, ok := obj["tree"].(map[string]any)
	if !ok {
		t.Fatalf("tree is not a map: %T", obj["tree"])
	}
	if tree["kind"] != "primitive" {
		t.Errorf("tree.kind = %v, want primitive", tree["kind"])
	}
	checks, ok := tree["checks"].([]any)
	if !ok {
		t.Fatalf("tree.checks is not an array: %T", tree["checks"])
	}
	if len(checks) != 2 {
		t.Errorf("tree.checks length = %d, want 2 (type + name_equals)", len(checks))
	}
}

// TestExplain_JSONOutput_AllOf covers the JSON encoding of a logical node
// — children must be present and recursively structured.
func TestExplain_JSONOutput_AllOf(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	body := `all_of:
  - type: counter
  - name_equals: foo_total
`
	recipeDir := writeExplainRecipe(t, filepath.Join(root, "recipes"),
		"explain_allof_json",
		recipeYAML("explain_allof_json", "service", body),
	)
	fixtureDir := writeFixture(t, root,
		`{"foo_total":[{"type":"counter","help":"x","unit":""}]}`,
		`[{"__name__":"foo_total","job":"x"}]`,
	)
	out := runExplainCmd(t, []string{
		"--name", "explain_allof_json",
		"--metric", "foo_total",
		"--fixture", fixtureDir,
		"--recipes-dir", recipeDir,
		"--output", "json",
	})

	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("json unmarshal failed: %v\nOutput:\n%s", err, out)
	}
	tree := obj["tree"].(map[string]any)
	if tree["kind"] != "all_of" {
		t.Errorf("tree.kind = %v, want all_of", tree["kind"])
	}
	children, ok := tree["children"].([]any)
	if !ok {
		t.Fatalf("tree.children is not an array: %T", tree["children"])
	}
	if len(children) != 2 {
		t.Errorf("tree.children length = %d, want 2", len(children))
	}
}

// =============================================================================
// CT5 — label-NAMES-only invariant (RECIPES-CLI.md §9.2)
// =============================================================================

// TestExplain_CT5_NoLabelValueLeak is the load-bearing privacy regression
// guard for `dashgen recipe explain`. It crafts a fixture whose series
// carry sensitive-looking label VALUES (e.g. instance="secret-prod-host-1"),
// runs explain with a recipe that exercises every label/trait/name
// predicate shape, and asserts:
//
//  1. The label NAME `instance` DOES appear in the output (proves the
//     `has_label` / `has_label_any` / `has_label_all` paths render names).
//  2. The label VALUE `secret-prod-host-1` (and other sensitive synthetic
//     values) NEVER appear — neither in the text/tree output nor in the
//     JSON output.
//
// This guards against future refactors that might inadvertently surface
// label values via `m.Descriptor.Labels`, the `actual` field, or any
// other path. If this test ever fails, the CT5 invariant is broken and
// the change must be reverted.
func TestExplain_CT5_NoLabelValueLeak(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// Recipe that exercises every NAME-level predicate shape, including
	// has_label / has_label_any / has_label_all (the highest-risk paths).
	body := `type: counter
name_equals: secret_metric_total
has_label: instance
has_label_any: [job, instance]
has_label_all: [job, instance, secret_label]
any_trait: [service_http]
`
	recipeDir := writeExplainRecipe(t, filepath.Join(root, "recipes"),
		"explain_ct5",
		recipeYAML("explain_ct5", "service", body),
	)

	// Fixture whose series.json embeds sensitive-looking label VALUES.
	// These exact strings are the negative-assertion canaries.
	fixtureDir := writeFixture(t, root,
		`{"secret_metric_total":[{"type":"counter","help":"x","unit":""}]}`,
		`[
			{
				"__name__": "secret_metric_total",
				"job": "checkout-prod",
				"instance": "secret-prod-host-1",
				"secret_label": "AKIAIOSFODNN7EXAMPLE-test-canary",
				"customer_id": "user-pii-canary-42"
			}
		]`,
	)

	// Sensitive value canaries — must NOT appear in any output.
	sensitiveValues := []string{
		"secret-prod-host-1",
		"AKIAIOSFODNN7EXAMPLE-test-canary",
		"user-pii-canary-42",
		"checkout-prod", // also a label value, must not leak
	}

	// --- text/tree output ----------------------------------------------------
	textOut := runExplainCmd(t, []string{
		"--name", "explain_ct5",
		"--metric", "secret_metric_total",
		"--fixture", fixtureDir,
		"--recipes-dir", recipeDir,
	})

	// Positive: the label NAME `instance` MUST be present (proves
	// has_label rendering wires through to actual output).
	if !strings.Contains(textOut, "instance") {
		t.Errorf("text output missing label NAME 'instance'; CT5 negative-assert "+
			"would be vacuous. Output:\n%s", textOut)
	}
	// Negative: sensitive values MUST NOT appear.
	for _, v := range sensitiveValues {
		if strings.Contains(textOut, v) {
			t.Errorf("CT5 LEAK: text output contains sensitive label VALUE %q\nFull output:\n%s", v, textOut)
		}
	}

	// --- json output ---------------------------------------------------------
	jsonOut := runExplainCmd(t, []string{
		"--name", "explain_ct5",
		"--metric", "secret_metric_total",
		"--fixture", fixtureDir,
		"--recipes-dir", recipeDir,
		"--output", "json",
	})

	// Positive: label NAME `instance` must appear in JSON too.
	if !strings.Contains(jsonOut, `"instance"`) {
		t.Errorf("json output missing label NAME 'instance'; CT5 negative-assert "+
			"would be vacuous. Output:\n%s", jsonOut)
	}
	// Negative: sensitive values MUST NOT appear in JSON either.
	for _, v := range sensitiveValues {
		if strings.Contains(jsonOut, v) {
			t.Errorf("CT5 LEAK: json output contains sensitive label VALUE %q\nFull output:\n%s", v, jsonOut)
		}
	}

	// Belt-and-suspenders: the JSON must round-trip cleanly so we know we
	// asserted on the actual encoded text and not on truncated noise.
	var obj map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &obj); err != nil {
		t.Fatalf("json unmarshal failed (CT5 verification dropped): %v\nOutput:\n%s", err, jsonOut)
	}
}
