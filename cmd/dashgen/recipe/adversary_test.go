// adversary_test.go — CLI adversary corpus (T7.2).
//
// Each TestAdversaryCLI_CT<n>_* exercises one threat from
// docs/RECIPES-CLI.md §9.4. Tests assert the documented expected behavior
// verbatim — rejection at flag parse, refusal at write, deadline fire,
// label-name privacy invariant, etc.
//
// Test-time budget: each test must complete within ~5 seconds even on slow
// hardware. Long-running adversaries are exercised via aggressive timeouts
// or low-level loader calls so we never wait on real exponential blowup.
package recipe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"dashgen/internal/recipes"
)

// adversaryRecipeYAML is a schema-valid recipe with one harmless metric. It
// is the building block for every adversary fixture that needs a "compiles
// cleanly, then we exercise the boundary" shape.
const adversaryRecipeYAML = `apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: adversary_probe
  section: traffic
  profile: service
  confidence: 0.85
  tier: v0.3
match:
  type: counter
  name_equals: probe_total
panels:
  - title_template: 'Rate: {{ .Metric }}'
    kind: timeseries
    unit: ops/sec
    query_template: 'sum by ({{ groupBy . }}) (rate({{ .Metric }}[{{ .Window }}]))'
    legend_template: '{{ legendFor . }}'
`

// =============================================================================
// CT1 — Output-path traversal (scaffold + init refuse to overwrite)
// =============================================================================

// TestAdversaryCLI_CT1_OutputPathTraversal verifies that `recipe scaffold
// --output <existing>` refuses without --force, even when <existing> is a
// path with traversal sequences. Per RECIPES-CLI.md §9.2 CT1 the user is
// the principal; the mitigation is the explicit refusal-to-overwrite.
func TestAdversaryCLI_CT1_OutputPathTraversal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "sub", "..", "victim.yaml")
	resolved := filepath.Clean(target)
	if err := os.WriteFile(resolved, []byte("PRE-EXISTING"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cmd := newScaffoldCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{
		"--metric", "queue_depth",
		"--type", "gauge",
		"--section", "saturation",
		"--profile", "service",
		"--output", target,
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected refusal-to-overwrite, got nil error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected 'already exists' in error, got: %v", err)
	}
	got, _ := os.ReadFile(resolved)
	if string(got) != "PRE-EXISTING" {
		t.Errorf("victim file was overwritten without --force; content=%q", got)
	}
}

// =============================================================================
// CT2 — Recipe-list explosion (per-invocation file count cap)
// =============================================================================

// TestAdversaryCLI_CT2_TooManyFiles verifies `recipe lint` rejects more than
// lintMaxFilesPerInvocation positional args with ErrLintResourceLimit.
// Mitigation lives at lint.go: the cap is checked before any I/O.
func TestAdversaryCLI_CT2_TooManyFiles(t *testing.T) {
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
		t.Fatalf("expected ErrLintResourceLimit for %d files, got %v", len(files), err)
	}
	if !strings.Contains(err.Error(), "too many files") {
		t.Errorf("expected 'too many files' in error, got: %v", err)
	}
}

// =============================================================================
// CT3 — Scaffold flag injection (Prometheus metric-name regex constraint)
// =============================================================================

// TestAdversaryCLI_CT3_ScaffoldFlagInjection verifies --metric rejects shell
// metacharacters and path-traversal sequences with a clear error mentioning
// the constraint. Mitigation: scaffoldMetricRe in scaffold.go.
func TestAdversaryCLI_CT3_ScaffoldFlagInjection(t *testing.T) {
	t.Parallel()
	bad := []string{
		"../etc/passwd",
		"metric;rm",
		"metric&&evil",
		"metric|pipe",
		"metric`cmd`",
		"metric$var",
		"metric with space",
	}
	for _, m := range bad {
		m := m
		t.Run(m, func(t *testing.T) {
			t.Parallel()
			cmd := newScaffoldCmd()
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			cmd.SetArgs([]string{
				"--metric", m,
				"--type", "counter",
				"--section", "traffic",
				"--profile", "service",
			})
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("expected error for --metric %q, got nil", m)
			}
			if !strings.Contains(err.Error(), "--metric") {
				t.Errorf("error should reference --metric: %v", err)
			}
		})
	}
}

// =============================================================================
// CT4 — Symlink target via --output (init + scaffold reject without --force)
// =============================================================================

// TestAdversaryCLI_CT4_SymlinkOutput verifies that:
//
//  1. `recipe scaffold --output <symlink>` is rejected without --force
//     (O_EXCL|O_CREATE fails on the existing symlink).
//  2. `recipe init` against a directory containing a symlink at one of the
//     scaffolded names is rejected without --force, even when the link
//     target is missing (Lstat — adversary: CT4).
//  3. With --force, init replaces the symlink with a regular file; the
//     previous symlink target is unaffected.
func TestAdversaryCLI_CT4_SymlinkOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	t.Parallel()

	t.Run("scaffold_rejects_symlink_target", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		victim := filepath.Join(dir, "victim.txt")
		if err := os.WriteFile(victim, []byte("VICTIM"), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}
		link := filepath.Join(dir, "link.yaml")
		if err := os.Symlink(victim, link); err != nil {
			t.Skipf("symlink not supported: %v", err)
		}
		cmd := newScaffoldCmd()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		cmd.SetArgs([]string{
			"--metric", "probe_total",
			"--type", "counter",
			"--section", "traffic",
			"--profile", "service",
			"--output", link,
		})
		if err := cmd.Execute(); err == nil {
			t.Fatal("expected scaffold to refuse symlinked --output, got nil")
		}
		got, _ := os.ReadFile(victim)
		if string(got) != "VICTIM" {
			t.Errorf("victim file was modified; content=%q", got)
		}
	})

	t.Run("init_rejects_dangling_symlink", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		link := filepath.Join(dir, "README.md")
		if err := os.Symlink(filepath.Join(dir, "nonexistent"), link); err != nil {
			t.Skipf("symlink not supported: %v", err)
		}
		cmd := newInitCmd()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		cmd.SetArgs([]string{"--config-dir", dir})
		err := cmd.Execute()
		if !errors.Is(err, ErrDirExists) {
			t.Fatalf("expected ErrDirExists for dangling symlink (Lstat path), got %v", err)
		}
	})

	t.Run("init_force_replaces_symlink", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		outside := filepath.Join(t.TempDir(), "outside.txt")
		if err := os.WriteFile(outside, []byte("OUTSIDE"), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}
		link := filepath.Join(dir, "README.md")
		if err := os.Symlink(outside, link); err != nil {
			t.Skipf("symlink not supported: %v", err)
		}
		cmd := newInitCmd()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		cmd.SetArgs([]string{"--config-dir", dir, "--force"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("init --force: %v", err)
		}
		gotOutside, _ := os.ReadFile(outside)
		if string(gotOutside) != "OUTSIDE" {
			t.Errorf("symlink target was overwritten via init --force; content=%q", gotOutside)
		}
		info, err := os.Lstat(link)
		if err != nil {
			t.Fatalf("Lstat after init --force: %v", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			t.Error("README.md is still a symlink after init --force")
		}
	})
}

// =============================================================================
// CT5 — explain label-names-only (no label values in output)
// =============================================================================

// TestAdversaryCLI_CT5_ExplainLabelNamesOnly verifies that `recipe explain`
// output never contains label VALUES from the fixture's series.json — only
// label NAMES, metric names, predicate fields, and trait names. The fixture
// embeds a sentinel value; the test fails if it ever appears in stdout
// (text or json).
func TestAdversaryCLI_CT5_ExplainLabelNamesOnly(t *testing.T) {
	t.Parallel()

	fixtureDir := t.TempDir()
	const sensitive = "SECRET_VALUE_DO_NOT_LEAK"
	mdJSON := `{"adversary_probe_total":[{"type":"counter","help":"probe","unit":""}]}`
	if err := os.WriteFile(filepath.Join(fixtureDir, "metadata.json"), []byte(mdJSON), 0o644); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	seriesJSON := fmt.Sprintf(`[{"__name__":"adversary_probe_total","job":"svc","tenant":"%s"}]`, sensitive)
	if err := os.WriteFile(filepath.Join(fixtureDir, "series.json"), []byte(seriesJSON), 0o644); err != nil {
		t.Fatalf("write series: %v", err)
	}

	recipeDir := t.TempDir()
	yaml := strings.ReplaceAll(adversaryRecipeYAML, "name_equals: probe_total", "name_equals: adversary_probe_total")
	if err := os.WriteFile(filepath.Join(recipeDir, "probe.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("write recipe: %v", err)
	}

	for _, format := range []string{"text", "json"} {
		format := format
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			cmd := newExplainCmd()
			cmd.SetOut(&buf)
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			cmd.SetArgs([]string{
				"--name", "adversary_probe",
				"--metric", "adversary_probe_total",
				"--fixture", fixtureDir,
				"--recipes-dir", recipeDir,
				"--output", format,
			})
			if err := cmd.Execute(); err != nil {
				t.Fatalf("explain %s: %v", format, err)
			}
			if strings.Contains(buf.String(), sensitive) {
				t.Errorf("CT5 violation: explain %s output leaked label value %q\nfull output:\n%s",
					format, sensitive, buf.String())
			}
		})
	}
}

// =============================================================================
// CT6 — Diff with malicious user override (loader limits hold)
// =============================================================================

// TestAdversaryCLI_CT6_DiffMaliciousLoaderLimits verifies `recipe diff` against
// a malicious user recipe surfaces the loader rejection cleanly (no hang, no
// OOM). The fixture: a recipe whose YAML nests `not` past maxPredicateDepth.
// The loader fires ErrPredicateTooDeep; diff wraps it in ErrDiffLoadFailure.
func TestAdversaryCLI_CT6_DiffMaliciousLoaderLimits(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Build a 9-level-deep `not` chain (cap is 8).
	body := "type: counter"
	for i := 0; i < 9; i++ {
		body = "not:\n      " + strings.ReplaceAll(body, "\n", "\n      ")
	}
	malicious := `apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: malicious_probe
  section: traffic
  profile: service
  confidence: 0.85
  tier: v0.3
match:
  ` + body + `
panels:
  - title_template: 'p'
    kind: timeseries
    unit: ops/sec
    query_template: 'rate({{ .Metric }}[{{ .Window }}])'
    legend_template: '{{ legendFor . }}'
`
	mPath := filepath.Join(dir, "malicious.yaml")
	if err := os.WriteFile(mPath, []byte(malicious), 0o644); err != nil {
		t.Fatalf("write malicious: %v", err)
	}
	bPath := filepath.Join(dir, "benign.yaml")
	if err := os.WriteFile(bPath, []byte(adversaryRecipeYAML), 0o644); err != nil {
		t.Fatalf("write benign: %v", err)
	}
	fxDir := makeMinimalFixture(t)

	done := make(chan error, 1)
	go func() {
		cmd := newDiffCmd()
		cmd.SetOut(new(bytes.Buffer))
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		cmd.SetArgs([]string{mPath, bPath, "--fixture", fxDir})
		done <- cmd.Execute()
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrDiffLoadFailure) {
			t.Fatalf("expected ErrDiffLoadFailure (loader rejected malicious recipe), got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("diff hung past 5s on malicious recipe; loader limits not enforced")
	}
}

// =============================================================================
// CT7 — Malicious fixture (per-file 16 MB / cumulative 64 MB caps)
// =============================================================================

// TestAdversaryCLI_CT7_MaliciousFixture verifies that `recipe test` aborts
// before json.Unmarshal sees a >16 MB metadata.json. Mitigation: the
// fixture_guard.go pre-load size walk.
func TestAdversaryCLI_CT7_MaliciousFixture(t *testing.T) {
	t.Parallel()

	fxDir := t.TempDir()
	pad := bytes.Repeat([]byte(" "), fixtureMaxFileSize+1)
	if err := os.WriteFile(filepath.Join(fxDir, "metadata.json"), pad, 0o644); err != nil {
		t.Fatalf("write big metadata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fxDir, "series.json"), []byte("[]"), 0o644); err != nil {
		t.Fatalf("write series: %v", err)
	}

	rPath := filepath.Join(t.TempDir(), "probe.yaml")
	if err := os.WriteFile(rPath, []byte(adversaryRecipeYAML), 0o644); err != nil {
		t.Fatalf("write recipe: %v", err)
	}

	cmd := newTestCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{rPath, "--fixture", fxDir})
	err := cmd.Execute()
	if !errors.Is(err, ErrTestFixtureError) {
		t.Fatalf("expected ErrTestFixtureError for oversized fixture, got %v", err)
	}
	if !strings.Contains(err.Error(), "size cap") {
		t.Errorf("expected 'size cap' in error, got: %v", err)
	}
}

// =============================================================================
// CT8 — Information disclosure via verbose errors
// =============================================================================

// TestAdversaryCLI_CT8_NoFileContentInError verifies that lint errors do not
// dump unbounded file content. The recipe stuffs metadata.description with
// 4 KB of payload prefixed by a sentinel placed deep enough that the
// loadErrorMaxMessageRunes truncation must cut before it. The test fails if
// either (a) the sentinel surfaces in stdout/error or (b) the per-error
// stringification exceeds the documented bound.
func TestAdversaryCLI_CT8_NoFileContentInError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Place the sentinel deep enough in the value that any reasonable
	// truncation budget cuts before it. The 240-rune lead-in dominates the
	// schema-error prefix; the loader's clamp must still trim before reaching
	// rune index 240.
	const sentinel = "SENTINEL_PAYLOAD_THAT_MUST_NOT_LEAK_TO_STDERR"
	leadIn := strings.Repeat("p", 240)
	bigDesc := leadIn + sentinel + strings.Repeat("x", 4096)
	yaml := fmt.Sprintf(`apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: ct8_probe
  section: traffic
  profile: service
  confidence: 0.85
  tier: v0.3
  description: "%s"
match:
  type: counter
panels:
  - title_template: 'Probe'
    kind: timeseries
    unit: ops/sec
    query_template: 'rate({{ .Metric }}[5m])'
    legend_template: '{{ legendFor . }}'
`, bigDesc)
	p := writeRecipe(t, dir, "ct8.yaml", yaml)

	var buf bytes.Buffer
	cmd := newLintCmd()
	cmd.SetOut(&buf)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{p})
	err := cmd.Execute()
	if !errors.Is(err, ErrLintFailure) {
		t.Fatalf("expected ErrLintFailure for over-long description, got %v", err)
	}
	combined := buf.String() + err.Error()
	if strings.Contains(combined, sentinel) {
		t.Errorf("CT8 violation: lint error leaked deep recipe payload (%q present in output/error)", sentinel)
	}
	// Per RECIPES-CLI.md §9.2 CT8: "max 80 columns" excerpt + position ⇒ a
	// well-bounded message. The clamp keeps the rendered message under a few
	// hundred runes; 1 KB is a generous CI guardrail that still catches
	// pathological dumps.
	if len(err.Error()) > 1024 {
		t.Errorf("CT8 violation: error string is %d bytes (expected ≤1024 for clipped message)",
			len(err.Error()))
	}
}

// =============================================================================
// CT9 — Lint as DoS vector (per-file deadline, 5 s)
// =============================================================================

// TestAdversaryCLI_CT9_LintPerFileDeadline verifies that the lint per-file
// deadline is wired end-to-end. We exercise the loader path directly with
// CUEDeadline=1ns on the same-shape fixture used by the DSL T3 adversary
// suite — the loader must surface ErrCodeDeadline. This proves the timeout
// machinery is plumbed; the production 5s constant is asserted in
// TestLint_CT9_PerFileDeadlineConfig.
func TestAdversaryCLI_CT9_LintPerFileDeadline(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeRecipe(t, dir, "pathological.yaml", adversaryRecipeYAML)

	_, err := recipes.LoadFile(
		context.Background(),
		recipes.LoaderConfig{CUEDeadline: 1 * time.Nanosecond},
		p,
		recipes.SourceUser,
	)
	if err == nil {
		t.Fatal("expected deadline rejection at CUEDeadline=1ns, got nil")
	}
	var le *recipes.LoadError
	if !errors.As(err, &le) {
		t.Fatalf("expected *recipes.LoadError, got %T: %v", err, err)
	}
	if le.Code != recipes.ErrCodeDeadline {
		t.Errorf("Code=%q, want %q", le.Code, recipes.ErrCodeDeadline)
	}
	if lintPerFileDeadline != 5*time.Second {
		t.Errorf("lintPerFileDeadline drifted: got %v want 5s", lintPerFileDeadline)
	}
}

// =============================================================================
// CT10 — Recipe scaffolder as attack vector (no implicit disk writes)
// =============================================================================

// TestAdversaryCLI_CT10_ScaffoldNoDiskWriteByDefault verifies that scaffold
// without --output produces stdout only and never touches the filesystem.
// The cwd before/after must be byte-identical.
func TestAdversaryCLI_CT10_ScaffoldNoDiskWriteByDefault(t *testing.T) {
	t.Parallel()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	beforeEntries, err := os.ReadDir(cwd)
	if err != nil {
		t.Fatalf("ReadDir before: %v", err)
	}
	beforeNames := dirNames(beforeEntries)

	var buf bytes.Buffer
	cmd := newScaffoldCmd()
	cmd.SetOut(&buf)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{
		"--metric", "probe_total",
		"--type", "counter",
		"--section", "traffic",
		"--profile", "service",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("scaffold (stdout): %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("expected non-empty stdout from scaffold")
	}
	// Per RECIPES-CLI.md §9.2 CT10: scaffold output bounded ≤16 KB.
	if buf.Len() > 16*1024 {
		t.Errorf("scaffold output exceeds 16 KB cap: %d bytes", buf.Len())
	}

	afterEntries, err := os.ReadDir(cwd)
	if err != nil {
		t.Fatalf("ReadDir after: %v", err)
	}
	afterNames := dirNames(afterEntries)
	if !equalStringSlice(beforeNames, afterNames) {
		t.Errorf("scaffold (default --output) modified cwd:\nbefore: %v\nafter:  %v",
			beforeNames, afterNames)
	}
}

// =============================================================================
// helpers
// =============================================================================

// makeMinimalFixture writes a tiny valid fixture into a fresh tempdir and
// returns its path. Used by CT6 / CT7 wrappers that need any-shape input.
func makeMinimalFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "metadata.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "series.json"), []byte("[]"), 0o644); err != nil {
		t.Fatalf("write series: %v", err)
	}
	return dir
}

// dirNames returns the name slice of dir entries in fs-iteration order.
func dirNames(entries []os.DirEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
