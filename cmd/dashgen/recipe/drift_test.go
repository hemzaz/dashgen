// drift_test.go — documentation drift guard for cmd/dashgen/recipe/.
//
// TestDocsDrift_AllFlagsDocumented cross-checks every --flag registered on
// every recipe subcommand against docs/RECIPES-CLI.md. If a flag is added to
// any subcommand without a matching entry in the spec, this test fails.
//
// Manual verification protocol (DoD):
//
//  1. Add a dummy flag (e.g. `cmd.Flags().BoolVar(&x, "undocumented-test", false, "")`)
//     to any newXxxCmd() function in this package.
//  2. Run `go test ./cmd/dashgen/recipe/...` — TestDocsDrift_AllFlagsDocumented
//     must fail with a message naming the subcommand and the flag.
//  3. Remove the dummy flag. The test must return to GREEN.
package recipe

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestDocsDrift_AllFlagsDocumented verifies that every --flag exposed by
// every subcommand in the recipe command tree is mentioned in
// docs/RECIPES-CLI.md.
func TestDocsDrift_AllFlagsDocumented(t *testing.T) {
	t.Parallel()

	docsPath := filepath.Join("..", "..", "..", "docs", "RECIPES-CLI.md")
	docsBytes, err := os.ReadFile(docsPath)
	if err != nil {
		t.Skipf("RECIPES-CLI.md not found at %s: %v", docsPath, err)
	}
	docs := string(docsBytes)

	// cobra injects --help automatically; skip it.
	cobraBuiltins := map[string]bool{
		"help": true,
	}

	// Extract --flagname tokens from pflag's FlagUsages() output.
	// FlagUsages lines look like:
	//   "      --flag-name type   description text"
	flagRe := regexp.MustCompile(`--([a-zA-Z][a-zA-Z0-9-]*)`)

	parent := NewCmd()
	for _, sub := range parent.Commands() {
		subName := sub.Name()
		// LocalFlags() yields only flags declared directly on sub, not
		// persistent flags inherited from a parent command.
		usages := sub.LocalFlags().FlagUsages()
		for _, m := range flagRe.FindAllStringSubmatch(usages, -1) {
			flagName := m[1]
			if cobraBuiltins[flagName] {
				continue
			}
			if !strings.Contains(docs, "--"+flagName) {
				t.Errorf("subcommand %q: flag --%s is not documented in docs/RECIPES-CLI.md",
					subName, flagName)
			}
		}
	}
}

// TestHarness_ExecCmdCapturesOutput verifies that execCmd (harness) captures
// stdout independently of os.Stdout.
func TestHarness_ExecCmdCapturesOutput(t *testing.T) {
	t.Parallel()
	stdout, _, err := execCmd(t, newListCmd(), []string{"--no-user-recipes", "--output", "json"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout, `"name"`) {
		t.Errorf("expected JSON 'name' field in captured stdout, got: %.120s", stdout)
	}
}

// TestHarness_ExecCmdPropagatesError verifies that execCmd returns the error
// from Execute() without calling t.Fatal.
func TestHarness_ExecCmdPropagatesError(t *testing.T) {
	t.Parallel()
	_, _, err := execCmd(t, newListCmd(), []string{"--source", "invalid"})
	if err == nil {
		t.Error("expected error for --source invalid, got nil")
	}
}
