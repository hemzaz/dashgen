package generate

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"dashgen/internal/config"
	"dashgen/internal/ir"
)

// fixtureDir is the path to the canonical first-slice fixture, resolved from
// the package test working directory.
const fixtureDir = "../../../testdata/fixtures/service-basic"
const goldenDir = "../../../testdata/goldens/service-basic"

const infraFixtureDir = "../../../testdata/fixtures/infra-basic"
const infraGoldenDir = "../../../testdata/goldens/infra-basic"
const k8sFixtureDir = "../../../testdata/fixtures/k8s-basic"
const k8sGoldenDir = "../../../testdata/goldens/k8s-basic"

// TestGolden_ServiceBasic verifies byte-for-byte equality between the
// pipeline's output and the stored goldens. Run with UPDATE_GOLDENS=1 to
// refresh the goldens after an intentional change.
func TestGolden_ServiceBasic(t *testing.T) {
	out := t.TempDir()
	cfg := &config.RunConfig{
		FixtureDir: fixtureDir,
		Profile:    "service",
		OutDir:     out,
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	files := []string{"dashboard.json", "rationale.md", "warnings.json"}
	update := os.Getenv("UPDATE_GOLDENS") == "1"
	for _, name := range files {
		got := readFile(t, filepath.Join(out, name))
		goldenPath := filepath.Join(goldenDir, name)
		if update {
			if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
				t.Fatalf("write golden %s: %v", name, err)
			}
			continue
		}
		want := readFile(t, goldenPath)
		if !bytes.Equal(got, want) {
			t.Errorf("%s mismatch\n-- want --\n%s\n-- got --\n%s", name, want, got)
		}
	}
}

// TestDeterminism_ServiceBasic runs the pipeline twice and asserts identical
// byte output. This catches accidental map iteration or time-of-day leaks.
func TestDeterminism_ServiceBasic(t *testing.T) {
	first := runOnce(t)
	second := runOnce(t)
	for _, name := range []string{"dashboard.json", "rationale.md", "warnings.json"} {
		if !bytes.Equal(first[name], second[name]) {
			t.Errorf("determinism: %s differs across runs", name)
		}
	}
}

// TestFirstStrictWarning covers the strict-mode detection that Run() invokes
// before rendering. End-to-end strict-mode behavior over a real query is
// covered by app/validate's `strict_promotes_warning_to_error` case; here we
// verify the panel-walk logic in isolation so it does not depend on which
// fixtures happen to produce warnings.
func TestFirstStrictWarning(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		dash *ir.Dashboard
		want string
	}{
		{name: "nil_dashboard", dash: nil, want: ""},
		{name: "no_panels", dash: &ir.Dashboard{}, want: ""},
		{
			name: "clean_dashboard",
			dash: &ir.Dashboard{Rows: []ir.Row{{
				Title: "traffic",
				Panels: []ir.Panel{{
					Title:   "rps",
					Queries: []ir.QueryCandidate{{Verdict: ir.VerdictAccept}},
				}},
			}}},
			want: "",
		},
		{
			name: "panel_level_warning_wins",
			dash: &ir.Dashboard{Rows: []ir.Row{{
				Title: "traffic",
				Panels: []ir.Panel{{
					Title:    "rps",
					Warnings: []string{"unscoped_aggregation"},
					Queries:  []ir.QueryCandidate{{Verdict: ir.VerdictAcceptWithWarning}},
				}},
			}}},
			want: `panel "rps": unscoped_aggregation`,
		},
		{
			name: "query_level_warning_when_panel_clean",
			dash: &ir.Dashboard{Rows: []ir.Row{{
				Title: "traffic",
				Panels: []ir.Panel{{
					Title: "rps",
					Queries: []ir.QueryCandidate{{
						Verdict:      ir.VerdictAcceptWithWarning,
						WarningCodes: []string{"empty_result"},
					}},
				}},
			}}},
			want: `query on panel "rps": empty_result`,
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := firstStrictWarning(c.dash); got != c.want {
				t.Fatalf("firstStrictWarning=%q want %q", got, c.want)
			}
		})
	}
}

// TestBackendSelection_MutualExclusion catches the common wiring mistake of
// passing both --prom-url and --fixture-dir.
func TestBackendSelection_MutualExclusion(t *testing.T) {
	cfg := &config.RunConfig{
		FixtureDir: fixtureDir,
		PromURL:    "http://example:9090",
		Profile:    "service",
		OutDir:     t.TempDir(),
	}
	if err := Run(context.Background(), cfg); err == nil {
		t.Fatal("expected mutual-exclusion error, got nil")
	}
}

// TestBackendSelection_Required asserts the run refuses when neither backend
// flag is set.
func TestBackendSelection_Required(t *testing.T) {
	cfg := &config.RunConfig{
		Profile: "service",
		OutDir:  t.TempDir(),
	}
	if err := Run(context.Background(), cfg); err == nil {
		t.Fatal("expected missing-backend error, got nil")
	}
}

func runOnce(t *testing.T) map[string][]byte {
	t.Helper()
	out := t.TempDir()
	cfg := &config.RunConfig{
		FixtureDir: fixtureDir,
		Profile:    "service",
		OutDir:     out,
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	result := map[string][]byte{}
	for _, name := range []string{"dashboard.json", "rationale.md", "warnings.json"} {
		result[name] = readFile(t, filepath.Join(out, name))
	}
	return result
}

// runOnceWith is the profile-parameterized sibling of runOnce; used by the
// infra and k8s golden + determinism tests.
func runOnceWith(t *testing.T, fixture, profile string) map[string][]byte {
	t.Helper()
	out := t.TempDir()
	cfg := &config.RunConfig{
		FixtureDir: fixture,
		Profile:    profile,
		OutDir:     out,
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	result := map[string][]byte{}
	for _, name := range []string{"dashboard.json", "rationale.md", "warnings.json"} {
		result[name] = readFile(t, filepath.Join(out, name))
	}
	return result
}

// assertGolden compares Run's output against the goldens in goldenDirPath.
// UPDATE_GOLDENS=1 rewrites the goldens instead of comparing.
func assertGolden(t *testing.T, fixture, profile, goldenDirPath string) {
	t.Helper()
	out := t.TempDir()
	cfg := &config.RunConfig{
		FixtureDir: fixture,
		Profile:    profile,
		OutDir:     out,
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	files := []string{"dashboard.json", "rationale.md", "warnings.json"}
	update := os.Getenv("UPDATE_GOLDENS") == "1"
	for _, name := range files {
		got := readFile(t, filepath.Join(out, name))
		goldenPath := filepath.Join(goldenDirPath, name)
		if update {
			if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
				t.Fatalf("write golden %s: %v", name, err)
			}
			continue
		}
		want := readFile(t, goldenPath)
		if !bytes.Equal(got, want) {
			t.Errorf("%s mismatch\n-- want --\n%s\n-- got --\n%s", name, want, got)
		}
	}
}

// TestGolden_InfraBasic mirrors TestGolden_ServiceBasic for the infra profile.
func TestGolden_InfraBasic(t *testing.T) {
	assertGolden(t, infraFixtureDir, "infra", infraGoldenDir)
}

// TestDeterminism_InfraBasic mirrors TestDeterminism_ServiceBasic.
func TestDeterminism_InfraBasic(t *testing.T) {
	first := runOnceWith(t, infraFixtureDir, "infra")
	second := runOnceWith(t, infraFixtureDir, "infra")
	for _, name := range []string{"dashboard.json", "rationale.md", "warnings.json"} {
		if !bytes.Equal(first[name], second[name]) {
			t.Errorf("determinism: %s differs across runs", name)
		}
	}
}

// TestGolden_K8sBasic mirrors TestGolden_ServiceBasic for the k8s profile.
func TestGolden_K8sBasic(t *testing.T) {
	assertGolden(t, k8sFixtureDir, "k8s", k8sGoldenDir)
}

// realisticFixtureDir is a live-shape fixture encoding the patterns that
// caused false positives on real Prometheus backends: promhttp-convention
// HTTP metrics (code/handler labels), a bare-histogram base-name latency,
// plus ambiguous look-alikes — a queue counter whose `status` label is not
// HTTP and an internal-op duration histogram without HTTP-shape labels.
const realisticFixtureDir = "../../../testdata/fixtures/service-realistic"
const realisticGoldenDir = "../../../testdata/goldens/service-realistic"

// TestGolden_ServiceRealistic mirrors the other golden tests but is the
// regression anchor for live-shape behaviors: it must fail if recipe
// discrimination regresses.
func TestGolden_ServiceRealistic(t *testing.T) {
	assertGolden(t, realisticFixtureDir, "service", realisticGoldenDir)
}

// TestDeterminism_ServiceRealistic asserts byte-stability across runs for
// the realistic fixture.
func TestDeterminism_ServiceRealistic(t *testing.T) {
	first := runOnceWith(t, realisticFixtureDir, "service")
	second := runOnceWith(t, realisticFixtureDir, "service")
	for _, name := range []string{"dashboard.json", "rationale.md", "warnings.json"} {
		if !bytes.Equal(first[name], second[name]) {
			t.Errorf("determinism: %s differs across runs", name)
		}
	}
}

// TestDiscrimination_ServiceRealistic is an explicit behavior assertion
// (not a byte-golden) that checks the specific false-positive cases don't
// regress even if the golden is ever blindly refreshed.
func TestDiscrimination_ServiceRealistic(t *testing.T) {
	out := runOnceWith(t, realisticFixtureDir, "service")
	dash := out["dashboard.json"]
	shouldAppear := []string{
		"api_http_requests_total",                  // rate + errors (v0.1)
		"api_http_request_duration_seconds_bucket", // HTTP latency (v0.1, bucket suffix synthesized)
		"process_cpu_seconds_total",                // cpu saturation (v0.1)
		"process_resident_memory_bytes",            // memory saturation (v0.1)
		"grpc_server_handled_total",                // gRPC rate + errors (v0.2)
		"grpc_server_handling_seconds_bucket",      // gRPC latency (v0.2, bucket suffix synthesized)
		"go_goroutines",                            // Go runtime goroutine count (v0.2)
		"go_gc_duration_seconds",                   // Go GC pause via summary quantile (v0.2)
	}
	for _, want := range shouldAppear {
		if !bytes.Contains(dash, []byte(want)) {
			t.Errorf("expected dashboard to reference %q", want)
		}
	}
	mustNotAppear := []string{
		// A queue counter whose `status` label is NOT an HTTP status
		// (alertmanager-style false positive). Recipe tightening: bare
		// `status` is excluded from httpStatusLabels.
		"queue_items_received_total",
		// An internal-op duration histogram without HTTP-shape labels
		// (alertmanager_notification_latency-style false positive).
		// Recipe tightening: service_http_latency requires both
		// latency_histogram AND service_http traits.
		"queue_processing_duration_seconds",
		// A grpc_*-named counter without grpc_method/grpc_service labels
		// (client-side retries style false positive). Recipe tightening:
		// service_grpc_rate requires the service_grpc trait, which only
		// fires when at least one grpc_* label is present on the metric.
		"grpc_client_retries_total",
	}
	for _, nope := range mustNotAppear {
		if bytes.Contains(dash, []byte(nope)) {
			t.Errorf("dashboard must NOT reference %q (regression in recipe discrimination)", nope)
		}
	}
}

// TestDeterminism_K8sBasic mirrors TestDeterminism_ServiceBasic.
func TestDeterminism_K8sBasic(t *testing.T) {
	first := runOnceWith(t, k8sFixtureDir, "k8s")
	second := runOnceWith(t, k8sFixtureDir, "k8s")
	for _, name := range []string{"dashboard.json", "rationale.md", "warnings.json"} {
		if !bytes.Equal(first[name], second[name]) {
			t.Errorf("determinism: %s differs across runs", name)
		}
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}
