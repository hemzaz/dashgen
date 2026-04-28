package recipe

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// =============================================================================
// Helpers
// =============================================================================

// runShowCmd executes the show subcommand against a freshly-built cobra
// command (no shared state between tests). It returns stdout. Test
// failures fatalf with the captured stderr+stdout for triage.
func runShowCmd(t *testing.T, args []string) string {
	t.Helper()
	out, _ := mustExecCmd(t, newShowCmd(), args)
	return out
}

// runShowCmdErr executes show and returns the (stdout, error) pair.
// Used by negative tests asserting error categories.
func runShowCmdErr(t *testing.T, args []string) (string, error) {
	t.Helper()
	out, _, err := execCmd(t, newShowCmd(), args)
	return out, err
}

// writeUserRecipe writes a minimal YAML recipe to dir/<name>.yaml so
// tests can supply user-side recipes without touching the embedded
// builtin corpus.
func writeUserRecipe(t *testing.T, dir, name, profile string) {
	t.Helper()
	body := `apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: ` + name + `
  section: traffic
  profile: ` + profile + `
  confidence: 0.55
  tier: v0.3
  description: "user override for ` + name + `"
match:
  type: counter
  name_equals: ` + name + `_total
panels:
  - title_template: 'rate: {{ .Metric }}'
    kind: timeseries
    unit: ops/sec
    query_template: 'sum by ({{ groupBy . }}) (rate({{ .Metric }}[{{ .Window }}]))'
    legend_template: '{{ legendFor . }}'
`
	p := filepath.Join(dir, name+".yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write user recipe %s: %v", p, err)
	}
}

// =============================================================================
// Validation
// =============================================================================

func TestShow_RejectsBadSource(t *testing.T) {
	t.Parallel()
	_, err := runShowCmdErr(t, []string{"service_grpc_rate", "--source", "bogus"})
	if err == nil {
		t.Fatal("expected error for --source bogus, got nil")
	}
	if !strings.Contains(err.Error(), "--source") {
		t.Errorf("error should mention --source, got: %v", err)
	}
}

func TestShow_RejectsBadProfile(t *testing.T) {
	t.Parallel()
	_, err := runShowCmdErr(t, []string{"service_grpc_rate", "--profile", "bogus"})
	if err == nil {
		t.Fatal("expected error for --profile bogus, got nil")
	}
	if !strings.Contains(err.Error(), "--profile") {
		t.Errorf("error should mention --profile, got: %v", err)
	}
}

func TestShow_RejectsBadOutput(t *testing.T) {
	t.Parallel()
	_, err := runShowCmdErr(t, []string{"service_grpc_rate", "--output", "xml"})
	if err == nil {
		t.Fatal("expected error for --output xml, got nil")
	}
	if !strings.Contains(err.Error(), "--output") {
		t.Errorf("error should mention --output, got: %v", err)
	}
}

func TestShow_RejectsMissingName(t *testing.T) {
	t.Parallel()
	_, err := runShowCmdErr(t, []string{})
	if err == nil {
		t.Fatal("expected error when <name> argument is missing, got nil")
	}
}

// =============================================================================
// Not-found / Go-recipe handling
// =============================================================================

func TestShow_NotFoundReturnsSentinel(t *testing.T) {
	t.Parallel()
	_, err := runShowCmdErr(t, []string{"zzz_no_such_recipe", "--no-user-recipes"})
	if err == nil {
		t.Fatal("expected ErrShowNotFound, got nil")
	}
	if !errors.Is(err, ErrShowNotFound) {
		t.Errorf("expected errors.Is(err, ErrShowNotFound), got: %v", err)
	}
}

// service_http_rate is currently a Go-implemented recipe (T1B.1 has not
// migrated it yet). show only supports YAML recipes so a Go-only match
// must report not-supported via the ErrShowNotFound sentinel.
//
// If/when worker-1 migrates service_http_rate to YAML, this test will
// flip to "found" — at that point delete it; the YAML-format coverage
// already exercises that path via service_grpc_rate.
func TestShow_GoOnlyRecipeReturnsSentinel(t *testing.T) {
	t.Parallel()
	_, err := runShowCmdErr(t, []string{"service_http_rate", "--no-user-recipes"})
	if err == nil {
		// service_http_rate may have been migrated; nothing to assert.
		t.Skip("service_http_rate migrated to YAML; Go-only path no longer reachable here")
	}
	if !errors.Is(err, ErrShowNotFound) {
		t.Errorf("expected ErrShowNotFound for Go-only recipe, got: %v", err)
	}
	if !strings.Contains(err.Error(), "implemented in Go") {
		t.Errorf("error should mention Go implementation, got: %v", err)
	}
}

// =============================================================================
// YAML output
// =============================================================================

func TestShow_YAMLDefaultRendersBuiltin(t *testing.T) {
	t.Parallel()
	// service_grpc_rate is a YAML built-in (T1B.1).
	out := runShowCmd(t, []string{"service_grpc_rate", "--no-user-recipes"})

	// Must round-trip as YAML.
	var obj map[string]any
	if err := yaml.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("yaml unmarshal failed: %v\nOutput:\n%s", err, out)
	}

	if obj["apiVersion"] != "dashgen.io/v1" {
		t.Errorf("apiVersion = %v, want dashgen.io/v1", obj["apiVersion"])
	}
	meta, ok := obj["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata is not a map: %T", obj["metadata"])
	}
	if meta["name"] != "service_grpc_rate" {
		t.Errorf("metadata.name = %v, want service_grpc_rate", meta["name"])
	}
	if meta["profile"] != "service" {
		t.Errorf("metadata.profile = %v, want service", meta["profile"])
	}
}

// =============================================================================
// JSON output
// =============================================================================

func TestShow_JSONOutputValid(t *testing.T) {
	t.Parallel()
	out := runShowCmd(t, []string{"service_grpc_rate", "--no-user-recipes", "--output", "json"})

	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("json unmarshal failed: %v\nOutput:\n%s", err, out)
	}
	if obj["apiVersion"] != "dashgen.io/v1" {
		t.Errorf("apiVersion = %v, want dashgen.io/v1", obj["apiVersion"])
	}
	meta, ok := obj["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata is not a map: %T", obj["metadata"])
	}
	if meta["name"] != "service_grpc_rate" {
		t.Errorf("metadata.name = %v, want service_grpc_rate", meta["name"])
	}
	panels, ok := obj["panels"].([]any)
	if !ok {
		t.Fatalf("panels is not an array: %T", obj["panels"])
	}
	if len(panels) < 1 {
		t.Errorf("panels length = %d, want >= 1", len(panels))
	}
}

// =============================================================================
// Tree output
// =============================================================================

func TestShow_TreeOutputContainsExpectedNodes(t *testing.T) {
	t.Parallel()
	out := runShowCmd(t, []string{"service_grpc_rate", "--no-user-recipes", "--output", "tree"})

	// Header line is the recipe name.
	lines := strings.Split(out, "\n")
	if len(lines) < 5 {
		t.Fatalf("tree output too short (%d lines):\n%s", len(lines), out)
	}
	if lines[0] != "service_grpc_rate" {
		t.Errorf("tree[0] = %q, want service_grpc_rate", lines[0])
	}

	// Tree should mention the canonical structural keys.
	for _, want := range []string{"apiVersion", "metadata", "match", "panels"} {
		if !strings.Contains(out, want) {
			t.Errorf("tree output missing %q:\n%s", want, out)
		}
	}
	// And use box-drawing connectors.
	if !strings.Contains(out, "├──") && !strings.Contains(out, "└──") {
		t.Errorf("tree output missing box-drawing connectors:\n%s", out)
	}
}

// =============================================================================
// Source filter
// =============================================================================

func TestShow_SourceBuiltinShowsBuiltinDespiteUserOverride(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Override the existing built-in name with a low-confidence user copy.
	writeUserRecipe(t, dir, "service_grpc_rate", "service")

	out := runShowCmd(t, []string{
		"service_grpc_rate",
		"--recipes-dir", dir,
		"--source", "builtin",
		"--output", "json",
	})
	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("json unmarshal: %v\nOutput:\n%s", err, out)
	}
	meta, ok := obj["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata is not a map: %T", obj["metadata"])
	}
	// Built-in confidence is 0.85; user override is 0.55. Verify built-in won.
	conf, ok := meta["confidence"].(float64)
	if !ok {
		t.Fatalf("confidence is not a float64: %T", meta["confidence"])
	}
	if conf <= 0.6 {
		t.Errorf("--source builtin returned override (confidence=%v); built-in expected", conf)
	}
}

func TestShow_SourceUserShowsUserOverride(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeUserRecipe(t, dir, "service_grpc_rate", "service")

	out := runShowCmd(t, []string{
		"service_grpc_rate",
		"--recipes-dir", dir,
		"--source", "user",
		"--output", "json",
	})
	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("json unmarshal: %v\nOutput:\n%s", err, out)
	}
	meta, ok := obj["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata is not a map: %T", obj["metadata"])
	}
	// User override has confidence 0.55.
	conf, ok := meta["confidence"].(float64)
	if !ok {
		t.Fatalf("confidence is not a float64: %T", meta["confidence"])
	}
	if conf > 0.6 {
		t.Errorf("--source user returned built-in (confidence=%v); override expected", conf)
	}
}

func TestShow_SourceUserNoUserDirsReturnsNotFound(t *testing.T) {
	t.Parallel()
	// Built-in recipe, but --source user with no user dirs leaves it
	// invisible — must report not found.
	_, err := runShowCmdErr(t, []string{
		"service_grpc_rate",
		"--no-user-recipes",
		"--source", "user",
	})
	if err == nil {
		t.Fatal("expected ErrShowNotFound, got nil")
	}
	if !errors.Is(err, ErrShowNotFound) {
		t.Errorf("expected ErrShowNotFound, got: %v", err)
	}
}

// =============================================================================
// Cross-profile ambiguity
// =============================================================================

func TestShow_AmbiguousAcrossProfilesReturnsSentinel(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Same name registered under two different profiles. ProfileRegistries
	// keeps a separate Registry per profile, so both entries succeed.
	writeUserRecipe(t, dir, "ambig_recipe", "service")
	// Second entry with profile=infra; needs a unique filename so the
	// loader does not see two files with the same recipe.name.
	body := `apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: ambig_recipe
  section: cpu
  profile: infra
  confidence: 0.66
  tier: v0.3
match:
  type: gauge
  name_equals: ambig_recipe_value
panels:
  - title_template: 'gauge: {{ .Metric }}'
    kind: timeseries
    unit: short
    query_template: 'max by ({{ groupBy . }}) ({{ .Metric }})'
    legend_template: '{{ legendFor . }}'
`
	if err := os.WriteFile(filepath.Join(dir, "ambig_recipe.infra.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write ambig infra recipe: %v", err)
	}

	_, err := runShowCmdErr(t, []string{
		"ambig_recipe",
		"--recipes-dir", dir,
	})
	if err == nil {
		t.Fatal("expected ErrShowAmbiguous, got nil")
	}
	if !errors.Is(err, ErrShowAmbiguous) {
		t.Errorf("expected ErrShowAmbiguous, got: %v", err)
	}
	// Error message should list both profiles for diagnosability.
	if !strings.Contains(err.Error(), "service") || !strings.Contains(err.Error(), "infra") {
		t.Errorf("ambiguity error should list both profiles, got: %v", err)
	}
}

func TestShow_ProfileFilterDisambiguates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeUserRecipe(t, dir, "ambig_recipe2", "service")
	body := `apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: ambig_recipe2
  section: cpu
  profile: infra
  confidence: 0.42
  tier: v0.3
match:
  type: gauge
  name_equals: ambig_recipe2_value
panels:
  - title_template: 'gauge: {{ .Metric }}'
    kind: timeseries
    unit: short
    query_template: 'max by ({{ groupBy . }}) ({{ .Metric }})'
    legend_template: '{{ legendFor . }}'
`
	if err := os.WriteFile(filepath.Join(dir, "ambig_recipe2.infra.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write ambig recipe: %v", err)
	}

	out := runShowCmd(t, []string{
		"ambig_recipe2",
		"--recipes-dir", dir,
		"--profile", "infra",
		"--output", "json",
	})
	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("json unmarshal: %v\nOutput:\n%s", err, out)
	}
	meta, ok := obj["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata is not a map: %T", obj["metadata"])
	}
	if meta["profile"] != "infra" {
		t.Errorf("--profile infra returned profile=%v", meta["profile"])
	}
}

// =============================================================================
// Determinism
// =============================================================================

func TestShow_DeterministicOutput(t *testing.T) {
	t.Parallel()
	for _, output := range []string{"yaml", "json", "tree"} {
		out1 := runShowCmd(t, []string{"service_grpc_rate", "--no-user-recipes", "--output", output})
		out2 := runShowCmd(t, []string{"service_grpc_rate", "--no-user-recipes", "--output", output})
		if out1 != out2 {
			t.Errorf("--output %s is not deterministic across runs", output)
		}
	}
}
