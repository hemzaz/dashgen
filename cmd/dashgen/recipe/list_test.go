package recipe

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// runListCmd executes the list subcommand with the given args and returns stdout.
func runListCmd(t *testing.T, args []string) string {
	t.Helper()
	var buf bytes.Buffer
	cmd := newListCmd()
	cmd.SetOut(&buf)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute(%v): %v", args, err)
	}
	return buf.String()
}

// runListCmdErr executes list and returns the error (expects failure).
func runListCmdErr(t *testing.T, args []string) error {
	t.Helper()
	var buf bytes.Buffer
	cmd := newListCmd()
	cmd.SetOut(&buf)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs(args)
	return cmd.Execute()
}

// ─── Validation ──────────────────────────────────────────────────────────────

func TestList_RejectsBadSource(t *testing.T) {
	t.Parallel()
	err := runListCmdErr(t, []string{"--source", "bogus"})
	if err == nil {
		t.Fatal("expected error for --source bogus, got nil")
	}
	if !strings.Contains(err.Error(), "--source") {
		t.Errorf("error should mention --source, got: %v", err)
	}
}

func TestList_RejectsBadProfile(t *testing.T) {
	t.Parallel()
	err := runListCmdErr(t, []string{"--profile", "nope"})
	if err == nil {
		t.Fatal("expected error for --profile nope, got nil")
	}
	if !strings.Contains(err.Error(), "--profile") {
		t.Errorf("error should mention --profile, got: %v", err)
	}
}

func TestList_RejectsBadOutput(t *testing.T) {
	t.Parallel()
	err := runListCmdErr(t, []string{"--output", "xml"})
	if err == nil {
		t.Fatal("expected error for --output xml, got nil")
	}
	if !strings.Contains(err.Error(), "--output") {
		t.Errorf("error should mention --output, got: %v", err)
	}
}

func TestList_RejectsBadGlob(t *testing.T) {
	t.Parallel()
	err := runListCmdErr(t, []string{"--match", "service_[*"})
	if err == nil {
		t.Fatal("expected error for malformed glob, got nil")
	}
	if !strings.Contains(err.Error(), "--match") {
		t.Errorf("error should mention --match, got: %v", err)
	}
}

// ─── Text output ─────────────────────────────────────────────────────────────

func TestList_TextHasHeader(t *testing.T) {
	t.Parallel()
	out := runListCmd(t, []string{"--no-user-recipes"})
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected header + at least one recipe; got %d lines:\n%s", len(lines), out)
	}
	header := lines[0]
	for _, col := range []string{"NAME", "PROFILE", "SECTION", "CONFIDENCE", "SOURCE", "PATH"} {
		if !strings.Contains(header, col) {
			t.Errorf("header missing column %q:\n%s", col, header)
		}
	}
}

func TestList_TextSortedByProfileThenName(t *testing.T) {
	t.Parallel()
	out := runListCmd(t, []string{"--no-user-recipes"})
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var names, profiles []string
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		names = append(names, fields[0])
		profiles = append(profiles, fields[1])
	}
	for i := 1; i < len(names); i++ {
		prev := profiles[i-1] + "/" + names[i-1]
		curr := profiles[i] + "/" + names[i]
		if curr < prev {
			t.Errorf("sort broken at row %d: %q > %q", i, prev, curr)
		}
	}
}

// ─── Counts ───────────────────────────────────────────────────────────────────

func TestList_DefaultShowsAllRecipes(t *testing.T) {
	t.Parallel()
	out := runListCmd(t, []string{"--no-user-recipes"})
	lines := strings.Split(strings.TrimSpace(out), "\n")
	dataLines := len(lines) - 1 // subtract header
	// 39 Go recipes + 5 YAML builtins (T1B.1) = 44 minimum.
	if dataLines < 44 {
		t.Errorf("expected ≥44 recipes, got %d", dataLines)
	}
}

func TestList_ProfileFilterReducesCount(t *testing.T) {
	t.Parallel()
	allOut := runListCmd(t, []string{"--no-user-recipes"})
	svcOut := runListCmd(t, []string{"--no-user-recipes", "--profile", "service"})

	allLines := strings.Split(strings.TrimSpace(allOut), "\n")
	svcLines := strings.Split(strings.TrimSpace(svcOut), "\n")

	if len(svcLines) >= len(allLines) {
		t.Errorf("--profile service should produce fewer lines than unfiltered: %d >= %d",
			len(svcLines), len(allLines))
	}
}

func TestList_SourceUserNoUserDirs(t *testing.T) {
	t.Parallel()
	// With --no-user-recipes, --source user must yield zero data rows.
	out := runListCmd(t, []string{"--no-user-recipes", "--source", "user"})
	lines := strings.Split(strings.TrimSpace(out), "\n")
	dataLines := len(lines) - 1
	if dataLines != 0 {
		t.Errorf("--source user with --no-user-recipes: want 0 data rows, got %d\n%s",
			dataLines, out)
	}
}

func TestList_SourceBuiltinExcludesUser(t *testing.T) {
	t.Parallel()
	out := runListCmd(t, []string{"--no-user-recipes", "--source", "builtin"})
	for i, line := range strings.Split(out, "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) >= 5 && fields[4] == "user" {
			t.Errorf("--source builtin: row %d shows source=user: %s", i+1, line)
		}
	}
}

// ─── Glob matching ────────────────────────────────────────────────────────────

func TestList_MatchGlobFiltersNames(t *testing.T) {
	t.Parallel()
	out := runListCmd(t, []string{"--no-user-recipes", "--match", "service_http_*"})
	lines := strings.Split(strings.TrimSpace(out), "\n")
	dataLines := lines[1:]
	if len(dataLines) < 3 {
		t.Errorf("expected ≥3 service_http_* recipes, got %d lines:\n%s", len(dataLines), out)
	}
	for _, line := range dataLines {
		if line == "" {
			continue
		}
		name := strings.Fields(line)[0]
		if !strings.HasPrefix(name, "service_http_") {
			t.Errorf("glob 'service_http_*' matched unexpected recipe %q", name)
		}
	}
}

func TestList_MatchGlobNoMatch(t *testing.T) {
	t.Parallel()
	out := runListCmd(t, []string{"--no-user-recipes", "--match", "zzz_no_such_recipe"})
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Errorf("expected only header line for unmatched glob, got %d lines:\n%s",
			len(lines), out)
	}
}

// ─── JSON output ─────────────────────────────────────────────────────────────

func TestList_JSONValid(t *testing.T) {
	t.Parallel()
	out := runListCmd(t, []string{"--no-user-recipes", "--output", "json"})
	var infos []recipeInfo
	if err := json.Unmarshal([]byte(out), &infos); err != nil {
		t.Fatalf("JSON unmarshal failed: %v\nOutput:\n%s", err, out)
	}
	if len(infos) < 44 {
		t.Errorf("expected ≥44 recipes in JSON, got %d", len(infos))
	}
}

func TestList_JSONEmptyForUserSourceNoUserDirs(t *testing.T) {
	t.Parallel()
	out := runListCmd(t, []string{"--no-user-recipes", "--source", "user", "--output", "json"})
	var infos []recipeInfo
	if err := json.Unmarshal([]byte(out), &infos); err != nil {
		t.Fatalf("JSON unmarshal failed: %v\nOutput:\n%s", err, out)
	}
	if len(infos) != 0 {
		t.Errorf("expected empty JSON array, got %d recipes", len(infos))
	}
}

func TestList_JSONFieldsPresent(t *testing.T) {
	t.Parallel()
	out := runListCmd(t, []string{
		"--no-user-recipes",
		"--profile", "service",
		"--match", "service_http_rate",
		"--output", "json",
	})
	var infos []recipeInfo
	if err := json.Unmarshal([]byte(out), &infos); err != nil {
		t.Fatalf("JSON unmarshal: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected exactly 1 result for service_http_rate, got %d", len(infos))
	}
	r := infos[0]
	if r.Name != "service_http_rate" {
		t.Errorf("Name = %q, want service_http_rate", r.Name)
	}
	if r.Profile != "service" {
		t.Errorf("Profile = %q, want service", r.Profile)
	}
	if r.Source != "builtin" {
		t.Errorf("Source = %q, want builtin", r.Source)
	}
	if r.Section == "" {
		t.Error("Section must be non-empty")
	}
}

// ─── Provenance ──────────────────────────────────────────────────────────────

func TestList_YAMLBuiltinHasPath(t *testing.T) {
	t.Parallel()
	// service_grpc_rate was migrated to YAML in T1B.1 — must have a Path.
	out := runListCmd(t, []string{
		"--no-user-recipes",
		"--match", "service_grpc_rate",
		"--output", "json",
	})
	var infos []recipeInfo
	if err := json.Unmarshal([]byte(out), &infos); err != nil {
		t.Fatalf("JSON unmarshal: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected exactly 1 result for service_grpc_rate, got %d", len(infos))
	}
	r := infos[0]
	if r.Path == "" {
		t.Error("YAML builtin recipe service_grpc_rate must have a non-empty Path")
	}
	if r.Source != "builtin" {
		t.Errorf("Source = %q, want builtin", r.Source)
	}
}

// TestList_GoRecipeHasEmptyPath was removed in T6A.2: the last remaining Go
// recipes (service_db_pool, infra_network, k8s_container_resources) were
// migrated to YAML splits. No Go recipes remain in the registry, so the
// "Go recipe has empty Path" assertion no longer has a target. Reintroduce
// only if a future feature reintroduces a Go-implementing Recipe.

func TestList_UserRecipeFromDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	yaml := `apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: test_user_recipe
  section: traffic
  profile: service
  confidence: 0.75
  tier: v0.3
match:
  type: counter
  name_equals: test_user_metric_total
panels:
  - title_template: 'Rate: {{ .Metric }}'
    kind: timeseries
    unit: ops/sec
    query_template: 'sum by ({{ groupBy . }}) (rate({{ .Metric }}[{{ .Window }}]))'
    legend_template: '{{ legendFor . }}'
`
	p := dir + "/test_user_recipe.yaml"
	if err := os.WriteFile(p, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write user recipe: %v", err)
	}

	out := runListCmd(t, []string{
		"--recipes-dir", dir,
		"--source", "user",
		"--output", "json",
	})
	var infos []recipeInfo
	if err := json.Unmarshal([]byte(out), &infos); err != nil {
		t.Fatalf("JSON unmarshal: %v\nOutput:\n%s", err, out)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 user recipe, got %d\nOutput:\n%s", len(infos), out)
	}
	r := infos[0]
	if r.Name != "test_user_recipe" {
		t.Errorf("Name = %q, want test_user_recipe", r.Name)
	}
	if r.Source != "user" {
		t.Errorf("Source = %q, want user", r.Source)
	}
	if r.Confidence != 0.75 {
		t.Errorf("Confidence = %.2f, want 0.75", r.Confidence)
	}
	if r.Path == "" {
		t.Error("user recipe must have a non-empty Path")
	}
}

// ─── Determinism ─────────────────────────────────────────────────────────────

func TestList_DeterministicOutput(t *testing.T) {
	t.Parallel()
	first := runListCmd(t, []string{"--no-user-recipes"})
	second := runListCmd(t, []string{"--no-user-recipes"})
	if first != second {
		t.Error("list output is not deterministic across two runs")
	}
}
