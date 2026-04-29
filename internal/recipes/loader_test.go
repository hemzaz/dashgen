package recipes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// validRecipe is a minimal but schema-conformant YAML body used by tests
// that need a "good" baseline to mutate into adversarial variants.
const validRecipe = `apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: test_recipe
  section: errors
  profile: service
  confidence: 0.5
  tier: v0.1
match:
  type: counter
panels:
  - title_template: "x"
    unit: ops/sec
    query_template: "rate(foo[5m])"
    legend_template: "x"
`

// =============================================================================
// TestLoad_HappyPath — load all 7 testdata/valid fixtures.
// =============================================================================

func TestLoad_HappyPath(t *testing.T) {
	cfg := LoaderConfig{UserDirs: []string{"testdata/valid"}}
	got, err := Load(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	type want struct {
		section string
		profile string
	}
	expected := map[string]want{
		"service_http_rate":        {"traffic", "service"},
		"service_http_latency":     {"latency", "service"},
		"service_request_size":     {"saturation", "service"},
		"service_db_query_latency": {"latency", "service"},
		"service_gc_pause":         {"latency", "service"},
		"infra_filesystem_usage":   {"disk", "infra"},
		"k8s_node_conditions":      {"resources", "k8s"},
	}
	if len(got) != len(expected) {
		t.Fatalf("loaded %d recipes, want %d", len(got), len(expected))
	}

	for _, r := range got {
		if r.Source != SourceUser {
			t.Errorf("%s: Source=%q, want %q", r.Spec.Metadata.Name, r.Source, SourceUser)
		}
		if r.Spec.APIVersion != "dashgen.io/v1" {
			t.Errorf("%s: APIVersion=%q, want dashgen.io/v1", r.Spec.Metadata.Name, r.Spec.APIVersion)
		}
		if r.Spec.Kind != "Recipe" {
			t.Errorf("%s: Kind=%q, want Recipe", r.Spec.Metadata.Name, r.Spec.Kind)
		}
		w, ok := expected[r.Spec.Metadata.Name]
		if !ok {
			t.Errorf("unexpected recipe name: %q", r.Spec.Metadata.Name)
			continue
		}
		if r.Spec.Metadata.Section != w.section {
			t.Errorf("%s: Section=%q, want %q", r.Spec.Metadata.Name, r.Spec.Metadata.Section, w.section)
		}
		if r.Spec.Metadata.Profile != w.profile {
			t.Errorf("%s: Profile=%q, want %q", r.Spec.Metadata.Name, r.Spec.Metadata.Profile, w.profile)
		}
		if len(r.Spec.Panels) == 0 {
			t.Errorf("%s: no panels decoded", r.Spec.Metadata.Name)
		}
	}
}

// TestLoad_DiscoveryDeterministic asserts repeated Discover calls return
// identical lexicographic ordering. Determinism is part of the contract.
func TestLoad_DiscoveryDeterministic(t *testing.T) {
	dir := t.TempDir()
	// Write files in non-alphabetic order to flush out unstable iterators.
	for _, name := range []string{"zeta.yaml", "alpha.yaml", "mid.yaml"} {
		writeFile(t, filepath.Join(dir, name), validRecipe)
	}
	cfg := LoaderConfig{UserDirs: []string{dir}}
	first, err := Discover(cfg)
	if err != nil {
		t.Fatalf("first Discover: %v", err)
	}
	for i := 0; i < 5; i++ {
		got, err := Discover(cfg)
		if err != nil {
			t.Fatalf("repeat Discover: %v", err)
		}
		if !equalStrings(first, got) {
			t.Fatalf("non-deterministic Discover: %v vs %v", first, got)
		}
	}
}

// =============================================================================
// TestLoad_MissingApiVersion — T16 enforcement.
// =============================================================================

func TestLoad_MissingApiVersion(t *testing.T) {
	body := `kind: Recipe
metadata:
  name: foo
  section: errors
  profile: service
  confidence: 0.5
  tier: v0.1
match:
  type: counter
panels:
  - title_template: "x"
    unit: ops/sec
    query_template: "rate(foo[5m])"
    legend_template: "x"
`
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "broken.yaml"), body)

	_, err := Load(context.Background(), LoaderConfig{UserDirs: []string{dir}})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "apiVersion") {
		t.Errorf("error should mention apiVersion: %v", err)
	}
	var le *LoadError
	if !errors.As(err, &le) {
		t.Fatalf("expected *LoadError, got %T", err)
	}
}

// =============================================================================
// TestLoad_FileSizeCap — T1.
// =============================================================================

func TestLoad_FileSizeCap(t *testing.T) {
	dir := t.TempDir()
	// Write a body longer than the cap we'll set.
	big := strings.Repeat("# pad pad pad pad\n", 200) // ~3.6 KB
	writeFile(t, filepath.Join(dir, "big.yaml"), big)

	cfg := LoaderConfig{
		UserDirs:    []string{dir},
		MaxFileSize: 256, // tight cap
	}
	_, err := Discover(cfg)
	if err == nil {
		t.Fatal("expected size cap error")
	}
	var le *LoadError
	if !errors.As(err, &le) {
		t.Fatalf("expected *LoadError, got %T", err)
	}
	if le.Code != ErrCodeFileSize {
		t.Errorf("Code=%q, want %q", le.Code, ErrCodeFileSize)
	}
	if !strings.Contains(le.Message, "size") {
		t.Errorf("Message should mention size: %q", le.Message)
	}
}

// TestLoad_FileSizeCap_Defaults sanity-checks the default 64KB cap rejects a 65KB file.
func TestLoad_FileSizeCap_Defaults(t *testing.T) {
	dir := t.TempDir()
	body := strings.Repeat("a", int(DefaultMaxFileSize)+1024) // 65KB+
	writeFile(t, filepath.Join(dir, "huge.yaml"), body)

	_, err := Discover(LoaderConfig{UserDirs: []string{dir}})
	if err == nil {
		t.Fatal("expected size cap error at default")
	}
	if !strings.Contains(err.Error(), "size") {
		t.Errorf("error should mention size: %v", err)
	}
}

// =============================================================================
// TestLoad_FileCountCap — T2.
// =============================================================================

func TestLoad_FileCountCap(t *testing.T) {
	dir := t.TempDir()
	// Write more files than the cap we'll set. Default 1024 is too slow
	// for tests; the cap is configurable so we can exercise the path
	// efficiently.
	for i := 0; i < 6; i++ {
		writeFile(t, filepath.Join(dir, fmt.Sprintf("r%02d.yaml", i)), validRecipe)
	}
	cfg := LoaderConfig{
		UserDirs:       []string{dir},
		MaxFilesPerDir: 3,
	}
	_, err := Discover(cfg)
	if err == nil {
		t.Fatal("expected count cap error")
	}
	var le *LoadError
	if !errors.As(err, &le) {
		t.Fatalf("expected *LoadError, got %T", err)
	}
	if le.Code != ErrCodeFileCount {
		t.Errorf("Code=%q, want %q", le.Code, ErrCodeFileCount)
	}
	if !strings.Contains(le.Message, "count") {
		t.Errorf("Message should mention count: %q", le.Message)
	}
}

// =============================================================================
// TestLoad_CueDeadline — T3 smoke test.
// =============================================================================
//
// Tests the deadline goroutine wrapper directly (decoupled from CUE).
// Phase 7 hardening adds end-to-end stress; here we only prove the
// timeout context is wired and fires on slow work.

func TestLoad_CueDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	_, err := withDeadline(ctx, "synthetic.yaml", func() cueResult {
		// Sleep WELL beyond the deadline so the select reliably picks
		// ctx.Done() first.
		time.Sleep(200 * time.Millisecond)
		return cueResult{}
	})
	if err == nil {
		t.Fatal("expected deadline error")
	}
	var le *LoadError
	if !errors.As(err, &le) {
		t.Fatalf("expected *LoadError, got %T", err)
	}
	if le.Code != ErrCodeDeadline {
		t.Errorf("Code=%q, want %q", le.Code, ErrCodeDeadline)
	}
	if !strings.Contains(le.Message, "deadline") {
		t.Errorf("Message should mention deadline: %q", le.Message)
	}
}

// TestLoad_CueDeadline_FastPath confirms the goroutine wins when CUE is fast.
func TestLoad_CueDeadline_FastPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := withDeadline(ctx, "synthetic.yaml", func() cueResult {
		// Return immediately; success path returns the zero cue.Value.
		return cueResult{}
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// =============================================================================
// TestLoad_SymlinkEscape — T11.
// =============================================================================

func TestLoad_SymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test is unix-only")
	}
	inside := t.TempDir()
	outside := t.TempDir()

	// Place a file outside the recipe dir.
	target := filepath.Join(outside, "evil.yaml")
	writeFile(t, target, validRecipe)

	// Symlink inside the recipe dir → outside file.
	link := filepath.Join(inside, "link.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	_, err := Discover(LoaderConfig{UserDirs: []string{inside}})
	if err == nil {
		t.Fatal("expected symlink escape error")
	}
	var le *LoadError
	if !errors.As(err, &le) {
		t.Fatalf("expected *LoadError, got %T", err)
	}
	if le.Code != ErrCodeSymlinkEscape {
		t.Errorf("Code=%q, want %q", le.Code, ErrCodeSymlinkEscape)
	}
	if !strings.Contains(le.Message, "symlink target outside dir") {
		t.Errorf("Message should mention symlink target outside dir: %q", le.Message)
	}
}

// TestLoad_SymlinkInsideAccepted confirms symlinks within the dir
// resolve cleanly. Defense in depth: the symlink-escape check must
// not over-reject.
func TestLoad_SymlinkInsideAccepted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test is unix-only")
	}
	dir := t.TempDir()
	real := filepath.Join(dir, "real.yaml")
	writeFile(t, real, validRecipe)

	link := filepath.Join(dir, "link.yaml")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	got, err := Discover(LoaderConfig{UserDirs: []string{dir}})
	if err != nil {
		t.Fatalf("Discover returned error: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("expected at least one discovered file")
	}
}

// =============================================================================
// TestErrorMapping — DSL §9.3 corpus.
// =============================================================================

func TestErrorMapping(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantSubs []string // all must appear in the error message
	}{
		{
			name: "WrongApiVersion",
			body: `apiVersion: dashgen.io/v0
kind: Recipe
metadata:
  name: foo
  section: errors
  profile: service
  confidence: 0.5
  tier: v0.1
match:
  type: counter
panels:
  - title_template: x
    unit: ops/sec
    query_template: x
    legend_template: x
`,
			wantSubs: []string{"apiVersion"},
		},
		{
			name: "BadProfile",
			body: `apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: foo
  section: errors
  profile: kubernetes
  confidence: 0.5
  tier: v0.1
match:
  type: counter
panels:
  - title_template: x
    unit: ops/sec
    query_template: x
    legend_template: x
`,
			wantSubs: []string{"profile"},
		},
		{
			name: "ConfidenceOutOfRange",
			body: `apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: foo
  section: errors
  profile: service
  confidence: 1.5
  tier: v0.1
match:
  type: counter
panels:
  - title_template: x
    unit: ops/sec
    query_template: x
    legend_template: x
`,
			wantSubs: []string{"confidence"},
		},
		{
			name: "BadTier",
			body: `apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: foo
  section: errors
  profile: service
  confidence: 0.5
  tier: v9.9
match:
  type: counter
panels:
  - title_template: x
    unit: ops/sec
    query_template: x
    legend_template: x
`,
			wantSubs: []string{"tier"},
		},
		{
			name: "BadSection",
			body: `apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: foo
  section: not_a_section
  profile: service
  confidence: 0.5
  tier: v0.1
match:
  type: counter
panels:
  - title_template: x
    unit: ops/sec
    query_template: x
    legend_template: x
`,
			wantSubs: []string{"section"},
		},
		{
			name: "MalformedYAML",
			// Unterminated flow sequence — yaml.v3 rejects with a hard parse error.
			body:     "apiVersion: dashgen.io/v1\nkind: Recipe\nfoo: [unterminated\n",
			wantSubs: []string{"YAML parse"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "case.yaml"), tc.body)

			_, err := Load(context.Background(), LoaderConfig{UserDirs: []string{dir}})
			if err == nil {
				t.Fatalf("%s: expected error, got nil", tc.name)
			}
			msg := err.Error()
			for _, sub := range tc.wantSubs {
				if !strings.Contains(msg, sub) {
					t.Errorf("%s: error %q does not contain %q", tc.name, msg, sub)
				}
			}
			var le *LoadError
			if !errors.As(err, &le) {
				t.Errorf("%s: expected *LoadError, got %T", tc.name, err)
			}
		})
	}
}

// =============================================================================
// TestPanelQueryFormMutex — T5.0.E mutual-exclusion enforcement.
// =============================================================================
//
// The CUE schema's #PanelTemplate disjunction (schema.cue) requires every panel
// to use exactly one query-emission form: either the single-query trio
// (query_template + legend_template) OR the multi-query queries: list — never
// both, and never neither. Multi-query is also incompatible with quantiles.
// These three rejection tests pin those invariants at the loader level so a
// regression in the disjunction surfaces immediately.

func TestPanelQueryFormMutex(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantSubs []string
	}{
		{
			name: "BothFormsPresent",
			// Single-query trio AND queries: list — schema must reject via the
			// #PanelTemplate disjunction's empty-disjunction error.
			body: `apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: ambiguous_recipe
  section: traffic
  profile: service
  confidence: 0.5
  tier: v0.3
match:
  type: counter
panels:
  - title_template: "x"
    unit: cps
    query_template: "rate(foo[5m])"
    legend_template: "x"
    queries:
      - query_template: "rate(foo[5m])"
        legend_template: "x"
        unit: cps
`,
			wantSubs: []string{"panels.0", "empty disjunction"},
		},
		{
			name: "NeitherFormPresent",
			// No query_template AND no queries: — schema must reject because
			// the single-query arm requires query_template + legend_template.
			body: `apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: empty_recipe
  section: traffic
  profile: service
  confidence: 0.5
  tier: v0.3
match:
  type: counter
panels:
  - title_template: "x"
    unit: cps
`,
			wantSubs: []string{"missing required field", "query_template"},
		},
		{
			name: "QuantilesWithMultiQuery",
			// queries: list combined with quantiles: — schema's multi-query
			// disjunction arm forbids quantiles, so unification fails.
			body: `apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: forbidden_combo
  section: latency
  profile: service
  confidence: 0.5
  tier: v0.3
match:
  type: histogram
panels:
  - title_template: "x"
    unit: s
    quantiles: [0.5, 0.95]
    queries:
      - query_template: "rate(foo[5m])"
        legend_template: "x"
        unit: s
`,
			wantSubs: []string{"panels.0", "empty disjunction"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "case.yaml"), tc.body)

			_, err := Load(context.Background(), LoaderConfig{UserDirs: []string{dir}})
			if err == nil {
				t.Fatalf("%s: expected schema rejection, got nil", tc.name)
			}
			msg := err.Error()
			for _, sub := range tc.wantSubs {
				if !strings.Contains(msg, sub) {
					t.Errorf("%s: error %q does not contain %q", tc.name, msg, sub)
				}
			}
			var le *LoadError
			if !errors.As(err, &le) {
				t.Errorf("%s: expected *LoadError, got %T", tc.name, err)
			}
		})
	}
}

// =============================================================================
// SchemaBytes — defensive copy.
// =============================================================================

func TestSchemaBytes_DefensiveCopy(t *testing.T) {
	a := SchemaBytes()
	b := SchemaBytes()
	if len(a) == 0 {
		t.Fatal("SchemaBytes returned empty slice")
	}
	if &a[0] == &b[0] {
		t.Error("SchemaBytes returned shared backing array")
	}
	a[0] = 0
	c := SchemaBytes()
	if c[0] != b[0] {
		t.Error("mutating SchemaBytes affected subsequent calls")
	}
}

// =============================================================================
// LoadFile single-file path.
// =============================================================================

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ok.yaml")
	writeFile(t, p, validRecipe)

	got, err := LoadFile(context.Background(), LoaderConfig{}, p, SourceUser)
	if err != nil {
		t.Fatalf("LoadFile returned error: %v", err)
	}
	if got.Spec.Metadata.Name != "test_recipe" {
		t.Errorf("Name=%q, want test_recipe", got.Spec.Metadata.Name)
	}
	if got.Source != SourceUser {
		t.Errorf("Source=%q, want %q", got.Source, SourceUser)
	}
}

// =============================================================================
// SplitDiscovered round-trip.
// =============================================================================

func TestSplitDiscovered(t *testing.T) {
	cases := []struct {
		in   string
		src  string
		path string
		ok   bool
	}{
		{"builtin:data/svc/foo.yaml", "builtin", "data/svc/foo.yaml", true},
		{"user:/tmp/abc.yaml", "user", "/tmp/abc.yaml", true},
		{"no_colon_here", "", "no_colon_here", false},
	}
	for _, tc := range cases {
		src, path, ok := SplitDiscovered(tc.in)
		if src != tc.src || path != tc.path || ok != tc.ok {
			t.Errorf("SplitDiscovered(%q) = (%q,%q,%v), want (%q,%q,%v)",
				tc.in, src, path, ok, tc.src, tc.path, tc.ok)
		}
	}
}

// =============================================================================
// LoadError.Error formatting.
// =============================================================================

func TestLoadError_Error(t *testing.T) {
	cases := []struct {
		name string
		e    LoadError
		want string
	}{
		{"FullPos", LoadError{File: "x.yaml", Line: 12, Col: 4, Message: "oops"}, "x.yaml:12:4: oops"},
		{"LineOnly", LoadError{File: "x.yaml", Line: 3, Message: "huh"}, "x.yaml:3: huh"},
		{"NoPos", LoadError{File: "x.yaml", Message: "bad"}, "x.yaml: bad"},
		{"NoFile", LoadError{Message: "global"}, "recipes: global"},
	}
	for _, tc := range cases {
		got := tc.e.Error()
		if got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

// =============================================================================
// Helpers
// =============================================================================

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func equalStrings(a, b []string) bool {
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
