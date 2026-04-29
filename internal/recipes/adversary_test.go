// adversary_test.go — DSL adversary corpus (T7.1).
//
// Each TestAdversary_T<n>_* exercises one threat from
// docs/RECIPES-DSL-ADVERSARY.md. Fixtures live at testdata/adversarial/.
//
// Naming follows the spec: short_name comes from the §4 fixture filename
// (without the .yaml). Tests assert the documented behavior verbatim —
// rejection at load (with a position+message that contains the doc's
// stated substring), refusal at render, or contained execution.
package recipes

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

// adversarialDir is the on-disk location of the corpus.
const adversarialDir = "testdata/adversarial"

// loadAdversarial loads one named fixture from testdata/adversarial/
// using the default LoaderConfig. Returns the typed LoadError for fine
// assertions when load fails; (LoadedRecipe{}, err) on any other error.
func loadAdversarial(t *testing.T, name string) (LoadedRecipe, error) {
	t.Helper()
	path, err := filepath.Abs(filepath.Join(adversarialDir, name))
	if err != nil {
		t.Fatalf("abs(%s): %v", name, err)
	}
	return LoadFile(context.Background(), LoaderConfig{}, path, SourceUser)
}

// =============================================================================
// T1 — YAML billion-laughs / anchor-expansion DoS
// =============================================================================

// TestAdversary_T1_yaml_billion_laughs asserts that a YAML document with a
// chained alias forest is rejected by the loader. Yaml.v3's anchor caps fire
// on the alias graph before unification runs.
func TestAdversary_T1_yaml_billion_laughs(t *testing.T) {
	_, err := loadAdversarial(t, "yaml_billion_laughs.yaml")
	if err == nil {
		t.Fatal("expected loader rejection of billion-laughs alias forest, got nil")
	}
	var le *LoadError
	if !errors.As(err, &le) {
		t.Fatalf("expected *LoadError, got %T: %v", err, err)
	}
}

// TestAdversary_T1_yaml_recursive_anchor covers the alias-loop variant.
// A cyclic alias is rejected at parse or unification — load fails either way.
func TestAdversary_T1_yaml_recursive_anchor(t *testing.T) {
	_, err := loadAdversarial(t, "yaml_recursive_anchor.yaml")
	if err == nil {
		t.Fatal("expected loader rejection of cyclic alias, got nil")
	}
	var le *LoadError
	if !errors.As(err, &le) {
		t.Fatalf("expected *LoadError, got %T: %v", err, err)
	}
}

// =============================================================================
// T2 — Recipe count exhaustion
// =============================================================================

// TestAdversary_T2_recipe_count_exhaustion exercises the per-directory file-count
// cap. The threat is mitigated by Discover refusing to enumerate >MaxFilesPerDir
// files. We use a small temp dir + tight cap to drive the path quickly.
func TestAdversary_T2_recipe_count_exhaustion(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 5; i++ {
		writeFile(t, filepath.Join(dir, padIdx("r", i)+".yaml"), validRecipe)
	}
	_, err := Discover(LoaderConfig{UserDirs: []string{dir}, MaxFilesPerDir: 2})
	if err == nil {
		t.Fatal("expected file-count cap rejection, got nil")
	}
	var le *LoadError
	if !errors.As(err, &le) {
		t.Fatalf("expected *LoadError, got %T: %v", err, err)
	}
	if le.Code != ErrCodeFileCount {
		t.Errorf("Code=%q, want %q", le.Code, ErrCodeFileCount)
	}
}

// =============================================================================
// T3 — Catastrophic CUE evaluation (deadline must fire)
// =============================================================================

// TestAdversary_T3_cue_eval_pathological asserts the CUE evaluation
// deadline path is wired. Running with CUEDeadline=1ns guarantees the
// wall-clock guard fires regardless of the fixture's actual eval cost,
// which proves the timeout machinery is plumbed end-to-end.
func TestAdversary_T3_cue_eval_pathological(t *testing.T) {
	path, err := filepath.Abs(filepath.Join(adversarialDir, "cue_eval_pathological.yaml"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	_, err = LoadFile(context.Background(), LoaderConfig{CUEDeadline: 1 * time.Nanosecond}, path, SourceUser)
	if err == nil {
		t.Fatal("expected CUE-deadline rejection, got nil")
	}
	var le *LoadError
	if !errors.As(err, &le) {
		t.Fatalf("expected *LoadError, got %T: %v", err, err)
	}
	if le.Code != ErrCodeDeadline {
		t.Errorf("Code=%q, want %q", le.Code, ErrCodeDeadline)
	}
	if !strings.Contains(le.Message, "deadline") {
		t.Errorf("Message should mention deadline: %q", le.Message)
	}
}

// =============================================================================
// T4 — ReDoS (RE2 makes match linear; loader accepts the recipe)
// =============================================================================

// TestAdversary_T4_redos_pattern verifies that a polynomial-blowup regex
// loads cleanly (RE2 is structurally immune to backtracking-ReDoS) and that
// matching it against a long adversarial input completes in linear time.
// Companion to TestEval_RedosImmunity in matcher_test.go.
func TestAdversary_T4_redos_pattern(t *testing.T) {
	loaded, err := loadAdversarial(t, "redos_pattern.yaml")
	if err != nil {
		t.Fatalf("loader rejected RE2-safe redos pattern (RE2 should accept): %v", err)
	}
	if loaded.Spec.Match.NameMatches != "^(a+)+b$" {
		t.Errorf("decoded NameMatches=%q, want %q", loaded.Spec.Match.NameMatches, "^(a+)+b$")
	}
	yr, err := NewYAMLRecipe(loaded)
	if err != nil {
		t.Fatalf("NewYAMLRecipe: %v", err)
	}
	adversarial := strings.Repeat("a", 5000) + "!"
	m := makeView(adversarial, inventory.MetricTypeCounter, nil, nil)
	start := time.Now()
	got := yr.Match(m)
	elapsed := time.Since(start)
	if got {
		t.Errorf("Match should return false on adversarial non-matching input")
	}
	if elapsed > 50*time.Millisecond {
		t.Errorf("RE2 immunity violation: match took %v on 5000-char adversarial input", elapsed)
	}
}

// =============================================================================
// T5 — Template parse-bomb (forbidden directives + AST budget)
// =============================================================================

// TestAdversary_T5_template_define_directive verifies the {{ define }}
// directive is rejected by the loader (via ErrForbiddenDirective).
func TestAdversary_T5_template_define_directive(t *testing.T) {
	_, err := loadAdversarial(t, "template_define_directive.yaml")
	if err == nil {
		t.Fatal("expected loader rejection of {{ define }}, got nil")
	}
	if !strings.Contains(err.Error(), "forbidden directive") {
		t.Errorf("error should mention forbidden directive: %v", err)
	}
}

// TestAdversary_T5_template_template_directive verifies {{ template }} is
// rejected. text/template parses the call and the AST walk catches it as a
// TemplateNode.
func TestAdversary_T5_template_template_directive(t *testing.T) {
	_, err := loadAdversarial(t, "template_template_directive.yaml")
	if err == nil {
		t.Fatal("expected loader rejection of {{ template }}, got nil")
	}
	if !strings.Contains(err.Error(), "forbidden directive") {
		t.Errorf("error should mention forbidden directive: %v", err)
	}
}

// TestAdversary_T5_template_block_directive verifies {{ block }} is rejected.
// {{ block }} registers an associated sub-template, caught by the
// len(tpl.Templates()) > 1 check.
func TestAdversary_T5_template_block_directive(t *testing.T) {
	_, err := loadAdversarial(t, "template_block_directive.yaml")
	if err == nil {
		t.Fatal("expected loader rejection of {{ block }}, got nil")
	}
	if !strings.Contains(err.Error(), "forbidden directive") {
		t.Errorf("error should mention forbidden directive: %v", err)
	}
}

// TestAdversary_T5_template_nested_if verifies the AST node-count budget.
// The fixture's query_template emits ~320 AST nodes (>256 cap), so loader
// rejects via ErrASTNodeBudgetExceeded.
func TestAdversary_T5_template_nested_if(t *testing.T) {
	_, err := loadAdversarial(t, "template_nested_if.yaml")
	if err == nil {
		t.Fatal("expected loader rejection of oversized template AST, got nil")
	}
	if !errors.Is(err, ErrASTNodeBudgetExceeded) {
		// Fall back to substring match in case wrapping changes — the spec
		// requires the message to mention the AST budget.
		if !strings.Contains(err.Error(), "AST node count") {
			t.Errorf("error should mention AST node count exceeded: %v", err)
		}
	}
}

// =============================================================================
// T6 — Render-time output cap
// =============================================================================

// TestAdversary_T6_render_size_blowup loads the fixture (it is structurally
// valid) and renders against a context whose ScopeFilter exceeds the 16 KB
// per-render output cap. The Template.Render call returns
// ErrTemplateOutputTooLarge.
func TestAdversary_T6_render_size_blowup(t *testing.T) {
	loaded, err := loadAdversarial(t, "render_size_blowup.yaml")
	if err != nil {
		t.Fatalf("loader rejected the fixture (should load cleanly): %v", err)
	}
	yr, err := NewYAMLRecipe(loaded)
	if err != nil {
		t.Fatalf("NewYAMLRecipe: %v", err)
	}
	huge := strings.Repeat("x", maxTemplateOutputBytes+1)
	_, err = yr.queryTmpls[0].Render(RenderContext{ScopeFilter: huge})
	if err == nil {
		t.Fatal("expected ErrTemplateOutputTooLarge, got nil")
	}
	if !errors.Is(err, ErrTemplateOutputTooLarge) {
		t.Errorf("expected ErrTemplateOutputTooLarge, got: %v", err)
	}
}

// =============================================================================
// T7 — Predicate explosion (depth + node-count budgets)
// =============================================================================

// TestAdversary_T7_predicate_deep_nesting asserts the loader rejects a
// predicate whose nesting depth exceeds maxPredicateDepth (8). The fixture
// nests `not` 9 levels deep.
func TestAdversary_T7_predicate_deep_nesting(t *testing.T) {
	_, err := loadAdversarial(t, "predicate_deep_nesting.yaml")
	if err == nil {
		t.Fatal("expected loader rejection of deep predicate, got nil")
	}
	if !errors.Is(err, ErrPredicateTooDeep) {
		if !strings.Contains(err.Error(), "depth") {
			t.Errorf("error should mention predicate depth: %v", err)
		}
	}
}

// TestAdversary_T7_predicate_node_explosion asserts the loader rejects a
// predicate whose total node count exceeds maxPredicateNodes (64).
func TestAdversary_T7_predicate_node_explosion(t *testing.T) {
	_, err := loadAdversarial(t, "predicate_node_explosion.yaml")
	if err == nil {
		t.Fatal("expected loader rejection of large predicate, got nil")
	}
	if !errors.Is(err, ErrPredicateTooMany) {
		if !strings.Contains(err.Error(), "node count") {
			t.Errorf("error should mention predicate node count: %v", err)
		}
	}
}

// =============================================================================
// T8 — Banned-label injection via query template
// =============================================================================

// TestAdversary_T8_banned_label_grouping verifies the loader DOES accept a
// recipe whose template hardcodes a banned high-cardinality label. The
// loader is not the safety boundary — every emitted query runs through the
// 5-stage validate pipeline (validate.safetyStage) which refuses any banned
// label in grouping. This test pins the loader's tolerance + asserts the
// rendered query contains the banned label literally so the validate-side
// safety test (in internal/validate) can pin the refusal verdict.
func TestAdversary_T8_banned_label_grouping(t *testing.T) {
	loaded, err := loadAdversarial(t, "banned_label_grouping.yaml")
	if err != nil {
		t.Fatalf("loader rejected banned-label recipe (should load — validate refuses downstream): %v", err)
	}
	yr, err := NewYAMLRecipe(loaded)
	if err != nil {
		t.Fatalf("NewYAMLRecipe: %v", err)
	}
	hit := makeView("http_requests_total", inventory.MetricTypeCounter, nil, []string{"job"})
	snap := ClassifiedInventorySnapshot{Metrics: []ClassifiedMetricView{hit}}
	panels := yr.BuildPanels(snap, profiles.ProfileService)
	if len(panels) == 0 {
		t.Fatal("expected at least one rendered panel")
	}
	if !strings.Contains(panels[0].Queries[0].Expr, "user_id") {
		t.Errorf("rendered expr should contain 'user_id' literally so validate-stage safety can refuse: %q", panels[0].Queries[0].Expr)
	}
}

// =============================================================================
// T9 — PromQL grammar abuse (loader accepts; validate stage-1 refuses)
// =============================================================================

// TestAdversary_T9_promql_grammar_abuse verifies the loader accepts a recipe
// whose query_template renders to syntactically-invalid PromQL. The loader
// does not parse rendered queries at load time (would require synthetic match
// contexts and is fragile); the validate pipeline's stage-1 parse rejects the
// emitted query with ReasonParseError.
func TestAdversary_T9_promql_grammar_abuse(t *testing.T) {
	loaded, err := loadAdversarial(t, "promql_grammar_abuse.yaml")
	if err != nil {
		t.Fatalf("loader rejected malformed-PromQL recipe (should load — validate refuses downstream): %v", err)
	}
	yr, err := NewYAMLRecipe(loaded)
	if err != nil {
		t.Fatalf("NewYAMLRecipe: %v", err)
	}
	hit := makeView("http_requests_total", inventory.MetricTypeCounter, nil, []string{"job"})
	snap := ClassifiedInventorySnapshot{Metrics: []ClassifiedMetricView{hit}}
	panels := yr.BuildPanels(snap, profiles.ProfileService)
	if len(panels) == 0 {
		t.Fatal("expected at least one rendered panel (validate would refuse it later)")
	}
	expr := panels[0].Queries[0].Expr
	// Mismatched parens — a minimum sanity check on the malformed-PromQL shape.
	if strings.Count(expr, "(") == strings.Count(expr, ")") {
		t.Errorf("rendered expr should be syntactically malformed (parens mismatched), got: %q", expr)
	}
}

// =============================================================================
// T10 — Determinism via map iteration
// =============================================================================

// TestAdversary_T10_template_map_iteration verifies that a template ranging
// over RenderContext.Labels produces byte-identical output across renders.
// text/template sorts map keys before iteration; this test asserts no
// regression in that property.
func TestAdversary_T10_template_map_iteration(t *testing.T) {
	loaded, err := loadAdversarial(t, "template_map_iteration.yaml")
	if err != nil {
		t.Fatalf("loader rejected map-iteration fixture (should load): %v", err)
	}
	yr, err := NewYAMLRecipe(loaded)
	if err != nil {
		t.Fatalf("NewYAMLRecipe: %v", err)
	}
	hit := makeView(
		"http_requests_total",
		inventory.MetricTypeCounter,
		nil,
		[]string{"zeta", "alpha", "mid", "beta"}, // intentionally unsorted
	)
	snap := ClassifiedInventorySnapshot{Metrics: []ClassifiedMetricView{hit}}
	first := yr.BuildPanels(snap, profiles.ProfileService)
	if len(first) == 0 {
		t.Fatal("expected rendered panel")
	}
	for i := 0; i < 10; i++ {
		got := yr.BuildPanels(snap, profiles.ProfileService)
		if len(got) != len(first) {
			t.Fatalf("iter %d: panel count drift %d → %d", i, len(first), len(got))
		}
		if got[0].Queries[0].Expr != first[0].Queries[0].Expr {
			t.Fatalf(
				"iter %d: non-deterministic render:\nfirst: %q\ngot:   %q",
				i, first[0].Queries[0].Expr, got[0].Queries[0].Expr,
			)
		}
	}
}

// =============================================================================
// T11 — Symlink escape via --recipes-dir
// =============================================================================

// TestAdversary_T11_symlink_escape creates a symlink at runtime pointing
// outside the user's recipe dir. The loader must reject the link. Skipped
// on Windows where symlink semantics differ.
func TestAdversary_T11_symlink_escape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink escape test is unix-only")
	}
	inside := t.TempDir()
	outside := t.TempDir()

	target := filepath.Join(outside, "evil.yaml")
	writeFile(t, target, validRecipe)

	link := filepath.Join(inside, "link.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not supported on this filesystem: %v", err)
	}

	_, err := Discover(LoaderConfig{UserDirs: []string{inside}})
	if err == nil {
		t.Fatal("expected symlink-escape rejection, got nil")
	}
	var le *LoadError
	if !errors.As(err, &le) {
		t.Fatalf("expected *LoadError, got %T: %v", err, err)
	}
	if le.Code != ErrCodeSymlinkEscape {
		t.Errorf("Code=%q, want %q", le.Code, ErrCodeSymlinkEscape)
	}
	if !strings.Contains(le.Message, "symlink target outside dir") {
		t.Errorf("Message should mention symlink target outside dir: %q", le.Message)
	}
}

// =============================================================================
// T12 — Built-in recipe override (visibility, not prevention)
// =============================================================================

// TestAdversary_T12_shadow_override loads the user-source shadow fixture and
// confirms the registry emits a WARN naming the overridden recipe and replaces
// the built-in. Override is intended behavior; this test pins the visibility
// contract.
func TestAdversary_T12_shadow_override(t *testing.T) {
	path, err := filepath.Abs(filepath.Join(adversarialDir, "shadow_override.yaml"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	loaded, err := LoadFile(context.Background(), LoaderConfig{}, path, SourceUser)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	pr := NewProfileRegistries()
	cap := &captureLogger{}
	pr.WithLogger(cap)
	if err := pr.RegisterFromLoaded([]LoadedRecipe{loaded}); err != nil {
		t.Fatalf("RegisterFromLoaded: %v", err)
	}

	got := pr.Service.ByName("service_http_rate")
	if got == nil {
		t.Fatal("override did not register")
	}
	if _, ok := got.(*YAMLRecipe); !ok {
		t.Errorf("got %T, want *YAMLRecipe", got)
	}

	combined := strings.Join(cap.all(), "\n")
	if !strings.Contains(combined, "service_http_rate") {
		t.Errorf("override warning should name the recipe: %q", combined)
	}
	if !strings.Contains(combined, "overrides built-in") {
		t.Errorf("override warning should mention built-in override: %q", combined)
	}
}

// =============================================================================
// T13 — Per-recipe panel fan-out cap
// =============================================================================

// TestAdversary_T13_panel_fan_out asserts the schema rejects recipes
// declaring more than 16 panels. CUE's list.MaxItems(16) on #Recipe.panels
// fires at unification time.
func TestAdversary_T13_panel_fan_out(t *testing.T) {
	_, err := loadAdversarial(t, "panel_fan_out.yaml")
	if err == nil {
		t.Fatal("expected schema rejection of 17-panel recipe, got nil")
	}
	if !strings.Contains(err.Error(), "panels") {
		t.Errorf("error should reference panels list: %v", err)
	}
}

// =============================================================================
// T16 — apiVersion downgrade / missing
// =============================================================================

// TestAdversary_T16_missing_apiversion asserts the loader rejects YAML
// without a top-level apiVersion field.
func TestAdversary_T16_missing_apiversion(t *testing.T) {
	_, err := loadAdversarial(t, "missing_apiversion.yaml")
	if err == nil {
		t.Fatal("expected loader rejection of missing apiVersion, got nil")
	}
	if !strings.Contains(err.Error(), "apiVersion") {
		t.Errorf("error should mention apiVersion: %v", err)
	}
	var le *LoadError
	if !errors.As(err, &le) {
		t.Fatalf("expected *LoadError, got %T", err)
	}
	if le.Code != ErrCodeAPIVersion {
		t.Errorf("Code=%q, want %q", le.Code, ErrCodeAPIVersion)
	}
}

// TestAdversary_T16_wrong_apiversion asserts the loader rejects YAML whose
// apiVersion is not exactly "dashgen.io/v1". The CUE schema constrains the
// field to that exact literal.
func TestAdversary_T16_wrong_apiversion(t *testing.T) {
	_, err := loadAdversarial(t, "wrong_apiversion.yaml")
	if err == nil {
		t.Fatal("expected loader rejection of wrong apiVersion, got nil")
	}
	if !strings.Contains(err.Error(), "apiVersion") {
		t.Errorf("error should mention apiVersion: %v", err)
	}
}

// =============================================================================
// T18 — Embedded literal secret (out-of-scope for loader; opt-in scanner)
// =============================================================================

// TestAdversary_T18_embedded_secret verifies that the loader accepts a
// recipe with a hardcoded credential-shaped string. Detecting "secret" is
// out of scope for the loader; this test pins the documented user-error
// contract: the recipe loads cleanly and the literal appears in the
// rendered query for downstream lint/scanner tools to flag.
func TestAdversary_T18_embedded_secret(t *testing.T) {
	loaded, err := loadAdversarial(t, "embedded_secret.yaml")
	if err != nil {
		t.Fatalf("loader should accept fixture (T18 is user-error, opt-in scanner WARN): %v", err)
	}
	yr, err := NewYAMLRecipe(loaded)
	if err != nil {
		t.Fatalf("NewYAMLRecipe: %v", err)
	}
	hit := makeView("http_requests_total", inventory.MetricTypeCounter, nil, []string{"job"})
	snap := ClassifiedInventorySnapshot{Metrics: []ClassifiedMetricView{hit}}
	panels := yr.BuildPanels(snap, profiles.ProfileService)
	if len(panels) == 0 {
		t.Fatal("expected at least one rendered panel")
	}
	if !strings.Contains(panels[0].Queries[0].Expr, "sk-test-") {
		t.Errorf("rendered expr should contain the literal credential placeholder for downstream lint to detect: %q", panels[0].Queries[0].Expr)
	}
}

// =============================================================================
// T20 — Implicit unicode normalization / homoglyph
// =============================================================================

// TestAdversary_T20_unicode_homograph asserts the schema rejects a
// metric-name field containing a non-ASCII character (Cyrillic 'е' here).
// #MetricNameASCII enforces ASCII-only on the name-shape fields.
func TestAdversary_T20_unicode_homograph(t *testing.T) {
	_, err := loadAdversarial(t, "unicode_homograph.yaml")
	if err == nil {
		t.Fatal("expected schema rejection of non-ASCII name_equals, got nil")
	}
	var le *LoadError
	if !errors.As(err, &le) {
		t.Fatalf("expected *LoadError, got %T: %v", err, err)
	}
}

// =============================================================================
// Helper utilities
// =============================================================================

// padIdx returns prefix + a zero-padded two-digit index. Avoids importing
// fmt for a trivial helper used only in T2's directory-fan-out fixture.
func padIdx(prefix string, i int) string {
	if i < 10 {
		return prefix + "0" + string(rune('0'+i))
	}
	return prefix + string(rune('0'+(i/10))) + string(rune('0'+(i%10)))
}
