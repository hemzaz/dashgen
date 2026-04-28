package recipe

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dashgen/internal/recipes"
)

// TestInit_CleanRun verifies that a fresh tempdir receives all three
// scaffolded files and that stdout contains the directory path and
// the expected one-liner.
func TestInit_CleanRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	var buf bytes.Buffer
	cmd := newInitCmd()
	cmd.SetOut(&buf)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--config-dir", dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// All three files must exist and be non-empty.
	for _, f := range scaffoldedFiles {
		p := filepath.Join(dir, f)
		info, err := os.Stat(p)
		if err != nil {
			t.Errorf("expected file %s: %v", f, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("file %s is empty", f)
		}
	}

	// Stdout must contain the resolved directory path and the success message.
	out := buf.String()
	if !strings.Contains(out, dir) {
		t.Errorf("stdout %q does not contain directory path %q", out, dir)
	}
	if !strings.Contains(out, "Recipes directory ready") {
		t.Errorf("stdout %q does not contain expected success message", out)
	}
}

// TestInit_RefusesOverwrite verifies that running init a second time (without
// --force) returns ErrDirExists when any scaffolded file already exists.
// This maps to exit code 2 in main.exitCodeFor.
func TestInit_RefusesOverwrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Pre-create the first scaffolded file to trigger the guard.
	existing := filepath.Join(dir, "README.md")
	if err := os.WriteFile(existing, []byte("pre-existing"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cmd := newInitCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--config-dir", dir})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected ErrDirExists, got nil")
	}
	if !errors.Is(err, ErrDirExists) {
		t.Errorf("expected errors.Is(err, ErrDirExists)=true, got err=%v", err)
	}
}

// TestInit_ForceOverwrite verifies that --force overwrites existing files and
// exits 0.
func TestInit_ForceOverwrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Pre-create one scaffolded file with stale content.
	existing := filepath.Join(dir, "README.md")
	if err := os.WriteFile(existing, []byte("stale content"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cmd := newInitCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--config-dir", dir, "--force"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute with --force: %v", err)
	}

	// README.md must have been overwritten with canonical content.
	got, err := os.ReadFile(existing)
	if err != nil {
		t.Fatalf("ReadFile README.md: %v", err)
	}
	if strings.Contains(string(got), "stale content") {
		t.Error("README.md still contains stale content after --force overwrite")
	}
	if !strings.Contains(string(got), "DashGen User Recipes") {
		t.Error("README.md does not contain expected header after --force overwrite")
	}

	// All three scaffolded files must exist.
	for _, f := range scaffoldedFiles {
		if _, statErr := os.Stat(filepath.Join(dir, f)); statErr != nil {
			t.Errorf("file %s missing after --force: %v", f, statErr)
		}
	}
}

// TestInit_ConfigDirOverride verifies that --config-dir <path> writes files to
// that path rather than the XDG default.
func TestInit_ConfigDirOverride(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	cmd := newInitCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--config-dir", dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	for _, f := range scaffoldedFiles {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("expected %s in override dir %s: %v", f, dir, err)
		}
	}
}

// TestInit_XDGFallback verifies that when XDG_CONFIG_HOME is unset,
// resolveRecipesDir falls back to $HOME/.config/dashgen/recipes/.
func TestInit_XDGFallback(t *testing.T) {
	// Not parallel: modifies the process environment.
	t.Setenv("XDG_CONFIG_HOME", "")

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot determine home directory: %v", err)
	}
	want := filepath.Join(home, ".config", "dashgen", "recipes")

	got, err := resolveRecipesDir("")
	if err != nil {
		t.Fatalf("resolveRecipesDir: %v", err)
	}
	if got != want {
		t.Errorf("resolveRecipesDir() = %q, want %q", got, want)
	}
}

// TestInit_XDGConfigHome verifies that when XDG_CONFIG_HOME is set,
// resolveRecipesDir uses it as the base directory.
func TestInit_XDGConfigHome(t *testing.T) {
	// Not parallel: modifies the process environment.
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	want := filepath.Join(tmp, "dashgen", "recipes")
	got, err := resolveRecipesDir("")
	if err != nil {
		t.Fatalf("resolveRecipesDir: %v", err)
	}
	if got != want {
		t.Errorf("resolveRecipesDir() = %q, want %q", got, want)
	}
}

// TestInit_ExampleYAMLIsValid loads the embedded example.yaml content through
// the recipe loader to confirm it passes CUE schema validation.
// This is the sanity-check mandated by T2A.1's CONSTRAINTS section.
func TestInit_ExampleYAMLIsValid(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "example.yaml")
	if err := os.WriteFile(p, []byte(recipeExampleYAML), 0o644); err != nil {
		t.Fatalf("write example.yaml: %v", err)
	}
	_, err := recipes.LoadFile(context.Background(), recipes.LoaderConfig{}, p, recipes.SourceUser)
	if err != nil {
		t.Errorf("example.yaml failed schema validation: %v", err)
	}
}
