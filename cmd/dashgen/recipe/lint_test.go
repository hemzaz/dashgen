package recipe

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// validRecipeYAML is a minimal schema-valid recipe for use in tests.
const validRecipeYAML = `apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: test_http_rate
  section: traffic
  profile: service
  confidence: 0.85
  tier: v0.3
match:
  type: counter
  any_trait: [service_http]
panels:
  - title_template: 'HTTP rate: {{ .Metric }}'
    kind: timeseries
    unit: ops/sec
    query_template: 'sum by ({{ groupBy . }}) (rate({{ .Metric }}[{{ .Window }}]))'
    legend_template: '{{ legendFor . }}'
`

// nonCanonicalUnitYAML is a valid recipe that uses a non-canonical unit.
const nonCanonicalUnitYAML = `apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: test_rpc_rate
  section: traffic
  profile: service
  confidence: 0.85
  tier: v0.3
match:
  type: counter
  any_trait: [service_grpc]
panels:
  - title_template: 'RPC rate: {{ .Metric }}'
    kind: timeseries
    unit: reqps
    query_template: 'sum by ({{ groupBy . }}) (rate({{ .Metric }}[{{ .Window }}]))'
    legend_template: '{{ legendFor . }}'
`

// missingAPIVersionYAML lacks the required apiVersion field.
const missingAPIVersionYAML = `kind: Recipe
metadata:
  name: broken_recipe
  section: traffic
  profile: service
  confidence: 0.85
  tier: v0.3
match:
  type: counter
panels:
  - title_template: 'Rate: {{ .Metric }}'
    unit: ops/sec
    query_template: 'rate({{ .Metric }}[5m])'
    legend_template: '{{ legendFor . }}'
`

// badSectionYAML has an invalid section enum value.
const badSectionYAML = `apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: bad_section_recipe
  section: satturation
  profile: service
  confidence: 0.85
  tier: v0.3
match:
  type: counter
panels:
  - title_template: 'Rate: {{ .Metric }}'
    unit: ops/sec
    query_template: 'rate({{ .Metric }}[5m])'
    legend_template: '{{ legendFor . }}'
`

// writeRecipe writes content to a temp file and returns its path.
func writeRecipe(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("writeRecipe %s: %v", name, err)
	}
	return p
}

// TestLint_ValidFileExitsZero verifies that a valid recipe file produces
// "OK" output and returns no error.
func TestLint_ValidFileExitsZero(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeRecipe(t, dir, "valid.yaml", validRecipeYAML)

	var buf bytes.Buffer
	cmd := newLintCmd()
	cmd.SetOut(&buf)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{p})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected exit 0, got error: %v", err)
	}
	if !strings.Contains(buf.String(), "OK") {
		t.Errorf("stdout %q does not contain 'OK'", buf.String())
	}
}

// TestLint_InvalidFileExitsOne verifies that an invalid recipe file returns
// ErrLintFailure (exit code 1) and prints a positional error.
func TestLint_InvalidFileExitsOne(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeRecipe(t, dir, "broken.yaml", missingAPIVersionYAML)

	var buf bytes.Buffer
	cmd := newLintCmd()
	cmd.SetOut(&buf)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{p})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrLintFailure) {
		t.Errorf("expected errors.Is(err, ErrLintFailure), got %v", err)
	}
	// Output must contain the file name and an error description.
	out := buf.String()
	if !strings.Contains(out, "broken.yaml") {
		t.Errorf("output %q does not contain filename", out)
	}
}

// TestLint_BadSectionExitsOne verifies that a bad enum value triggers a
// schema error with exit code 1.
func TestLint_BadSectionExitsOne(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeRecipe(t, dir, "badsection.yaml", badSectionYAML)

	cmd := newLintCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{p})

	err := cmd.Execute()
	if !errors.Is(err, ErrLintFailure) {
		t.Errorf("expected ErrLintFailure for bad section, got %v", err)
	}
}

// TestLint_MissingFileExitsTwo verifies that a non-existent file path returns
// ErrLintInputError (exit code 2).
func TestLint_MissingFileExitsTwo(t *testing.T) {
	t.Parallel()

	cmd := newLintCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"/nonexistent/path/recipe.yaml"})

	err := cmd.Execute()
	if !errors.Is(err, ErrLintInputError) {
		t.Errorf("expected ErrLintInputError for missing file, got %v", err)
	}
}

// TestLint_QuietSuppressesOK verifies that --quiet suppresses "OK" output for
// valid files and produces empty stdout.
func TestLint_QuietSuppressesOK(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeRecipe(t, dir, "valid.yaml", validRecipeYAML)

	var buf bytes.Buffer
	cmd := newLintCmd()
	cmd.SetOut(&buf)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--quiet", p})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("--quiet output should be empty for valid file, got %q", buf.String())
	}
}

// TestLint_NonCanonicalUnitWarning verifies that a non-canonical unit emits
// a warning (not an error) in default mode — exit 0.
func TestLint_NonCanonicalUnitWarning(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeRecipe(t, dir, "noncanon.yaml", nonCanonicalUnitYAML)

	var buf bytes.Buffer
	cmd := newLintCmd()
	cmd.SetOut(&buf)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{p})

	// Should exit 0 — non-canonical unit is a warning, not an error.
	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected exit 0 for non-canonical unit warning, got: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "warning") {
		t.Errorf("expected 'warning' in output, got %q", out)
	}
	if !strings.Contains(out, "not in the canonical set") {
		t.Errorf("expected canonical-set message in output, got %q", out)
	}
}

// TestLint_StrictModePromotesWarningToError verifies that --strict promotes a
// non-canonical unit warning to an error (exit code 1).
func TestLint_StrictModePromotesWarningToError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeRecipe(t, dir, "noncanon.yaml", nonCanonicalUnitYAML)

	cmd := newLintCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--strict", p})

	err := cmd.Execute()
	if !errors.Is(err, ErrLintFailure) {
		t.Errorf("expected ErrLintFailure with --strict for non-canonical unit, got %v", err)
	}
}

// TestLint_JSONOutputShape verifies that --output json produces a valid JSON
// array matching the Appendix B schema (file, valid, elapsed_ms, errors, warnings).
func TestLint_JSONOutputShape(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeRecipe(t, dir, "valid.yaml", validRecipeYAML)

	var buf bytes.Buffer
	cmd := newLintCmd()
	cmd.SetOut(&buf)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--output", "json", p})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var results []lintFileResult
	if err := json.Unmarshal(buf.Bytes(), &results); err != nil {
		t.Fatalf("invalid JSON output: %v\nraw: %s", err, buf.String())
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if !r.Valid {
		t.Errorf("expected valid=true for valid recipe")
	}
	if r.File == "" {
		t.Error("file field must not be empty")
	}
	if r.ElapsedMs < 0 {
		t.Errorf("elapsed_ms must be non-negative, got %d", r.ElapsedMs)
	}
	if r.Errors == nil {
		t.Error("errors must be an array (not null)")
	}
	if r.Warnings == nil {
		t.Error("warnings must be an array (not null)")
	}
}

// TestLint_JSONOutputInvalidFile verifies that --output json for an invalid
// file populates the errors array with code and message fields.
func TestLint_JSONOutputInvalidFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeRecipe(t, dir, "broken.yaml", missingAPIVersionYAML)

	var buf bytes.Buffer
	cmd := newLintCmd()
	cmd.SetOut(&buf)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--output", "json", p})

	err := cmd.Execute()
	if !errors.Is(err, ErrLintFailure) {
		t.Errorf("expected ErrLintFailure, got %v", err)
	}

	var results []lintFileResult
	if jsonErr := json.Unmarshal(buf.Bytes(), &results); jsonErr != nil {
		t.Fatalf("invalid JSON output: %v\nraw: %s", jsonErr, buf.String())
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Valid {
		t.Error("expected valid=false for broken recipe")
	}
	if len(results[0].Errors) == 0 {
		t.Error("expected at least one error in errors array")
	}
	for i, e := range results[0].Errors {
		if e.Code == "" {
			t.Errorf("error[%d].code is empty", i)
		}
		if e.Message == "" {
			t.Errorf("error[%d].message is empty", i)
		}
	}
}

// TestLint_MultiFileAggregation verifies that when linting a mix of valid and
// invalid files, all results appear in JSON output and the exit code reflects
// the worst outcome.
func TestLint_MultiFileAggregation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ok := writeRecipe(t, dir, "ok.yaml", validRecipeYAML)
	bad := writeRecipe(t, dir, "broken.yaml", missingAPIVersionYAML)

	var buf bytes.Buffer
	cmd := newLintCmd()
	cmd.SetOut(&buf)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--output", "json", ok, bad})

	err := cmd.Execute()
	if !errors.Is(err, ErrLintFailure) {
		t.Errorf("expected ErrLintFailure for mixed batch, got %v", err)
	}

	var results []lintFileResult
	if jsonErr := json.Unmarshal(buf.Bytes(), &results); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", jsonErr, buf.String())
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	validCount := 0
	for _, r := range results {
		if r.Valid {
			validCount++
		}
	}
	if validCount != 1 {
		t.Errorf("expected exactly 1 valid result, got %d", validCount)
	}
}

// TestLint_CT2_TooManyFilesExitsFive verifies that passing more than
// lintMaxFilesPerInvocation files returns ErrLintResourceLimit (exit code 5).
// The cap check runs before any I/O so dummy paths suffice.
func TestLint_CT2_TooManyFilesExitsFive(t *testing.T) {
	t.Parallel()

	files := make([]string, lintMaxFilesPerInvocation+1)
	for i := range files {
		files[i] = "/dev/null"
	}

	cmd := newLintCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true

	err := runLint(cmd, lintFlags{outputFmt: "text"}, files)
	if !errors.Is(err, ErrLintResourceLimit) {
		t.Errorf("expected ErrLintResourceLimit for %d files, got %v", len(files), err)
	}
}

// TestLint_CT9_PerFileDeadlineConfig verifies lintPerFileDeadline == 5s
// per the CT9 requirement in RECIPES-CLI.md §9.2.
func TestLint_CT9_PerFileDeadlineConfig(t *testing.T) {
	const want = 5 * time.Second
	if lintPerFileDeadline != want {
		t.Errorf("lintPerFileDeadline = %v, want %v (CT9 requires 5s)", lintPerFileDeadline, want)
	}
}

// TestLint_SecretScanDetectsPattern verifies that --secret-scan emits a
// "potential_secret" warning when a template contains a matching pattern.
// In default mode it is a warning (exit 0).
func TestLint_SecretScanDetectsPattern(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	const secretRecipeYAML = `apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: test_secret_scan
  section: traffic
  profile: service
  confidence: 0.85
  tier: v0.3
match:
  type: counter
panels:
  - title_template: 'Rate'
    unit: ops/sec
    query_template: 'rate(my_metric[5m]) # api_key=supersecret123'
    legend_template: '{{ legendFor . }}'
`
	p := writeRecipe(t, dir, "secrety.yaml", secretRecipeYAML)

	var buf bytes.Buffer
	cmd := newLintCmd()
	cmd.SetOut(&buf)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--secret-scan", p})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected exit 0 (warning only), got: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "potential secret") {
		t.Errorf("expected 'potential secret' warning in output, got %q", out)
	}
}

// TestLint_SecretScanStrictExitsOne verifies that --secret-scan --strict
// promotes a potential_secret finding to an error (exit code 1).
func TestLint_SecretScanStrictExitsOne(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	const secretRecipeYAML = `apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: test_secret_strict
  section: traffic
  profile: service
  confidence: 0.85
  tier: v0.3
match:
  type: counter
panels:
  - title_template: 'Rate'
    unit: ops/sec
    query_template: 'rate(my_metric[5m]) # api_key=supersecret123'
    legend_template: '{{ legendFor . }}'
`
	p := writeRecipe(t, dir, "secrety.yaml", secretRecipeYAML)

	cmd := newLintCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--secret-scan", "--strict", p})

	err := cmd.Execute()
	if !errors.Is(err, ErrLintFailure) {
		t.Errorf("expected ErrLintFailure for secret + --strict, got %v", err)
	}
}

// TestLint_ValidBuiltinFixtures runs lint against the testdata/valid fixtures
// in internal/recipes/testdata/valid/ to confirm they all pass schema validation.
func TestLint_ValidBuiltinFixtures(t *testing.T) {
	t.Parallel()

	fixtureDir := "../../../../internal/recipes/testdata/valid"
	entries, err := os.ReadDir(fixtureDir)
	if err != nil {
		t.Skipf("fixture dir not accessible: %v", err)
	}

	var yamlFiles []string
	for _, e := range entries {
		if !e.IsDir() && (strings.HasSuffix(e.Name(), ".yaml") || strings.HasSuffix(e.Name(), ".yml")) {
			yamlFiles = append(yamlFiles, filepath.Join(fixtureDir, e.Name()))
		}
	}
	if len(yamlFiles) == 0 {
		t.Skip("no YAML fixtures found")
	}

	for _, f := range yamlFiles {
		f := f
		t.Run(filepath.Base(f), func(t *testing.T) {
			t.Parallel()
			cmd := newLintCmd()
			cmd.SetOut(new(bytes.Buffer))
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			cmd.SetArgs([]string{f})

			if err := cmd.Execute(); err != nil {
				t.Errorf("valid fixture %s failed lint: %v", f, err)
			}
		})
	}
}

// BenchmarkLint_SingleFile measures cold lint performance for a single file.
// Target: ≤200ms cold (RECIPES-CLI.md §3.3 G3, §10.3). T7.3 wired the budget
// assertion so a regression that pushes lint past 200ms/op fails the bench.
func BenchmarkLint_SingleFile(b *testing.B) {
	const budgetMS = 200.0

	dir := b.TempDir()
	p := filepath.Join(dir, "bench.yaml")
	if err := os.WriteFile(p, []byte(validRecipeYAML), 0o644); err != nil {
		b.Fatalf("write: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cmd := newLintCmd()
		cmd.SetOut(new(bytes.Buffer))
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		cmd.SetArgs([]string{p})
		if err := cmd.Execute(); err != nil {
			b.Fatalf("lint error: %v", err)
		}
	}
	b.StopTimer()

	msPerOp := float64(b.Elapsed()) / float64(b.N) / 1e6
	if msPerOp > budgetMS {
		b.Errorf("budget exceeded: lint %.1fms/op > %.0fms (T7.3)", msPerOp, budgetMS)
	}
}
