package coverage

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	covpkg "dashgen/internal/coverage"
)

func writeJSON(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s/%s: %v", dir, name, err)
	}
}

// TestRun_NilConfig defensive guard.
func TestRun_NilConfig(t *testing.T) {
	t.Parallel()
	if err := Run(nil); err == nil {
		t.Fatal("Run(nil) returned nil; want error")
	}
}

// TestRun_FixtureDirRequired: empty FixtureDir triggers ErrInput.
func TestRun_FixtureDirRequired(t *testing.T) {
	t.Parallel()
	err := Run(&Config{})
	if err == nil {
		t.Fatal("expected error; got nil")
	}
	if !errors.Is(err, ErrInput) {
		t.Errorf("error chain missing ErrInput: %v", err)
	}
}

// TestRun_NoDashboardEverythingUncovered: with FixtureDir only, every
// metric is uncovered and family grouping surfaces them.
func TestRun_NoDashboardEverythingUncovered(t *testing.T) {
	t.Parallel()
	fix := t.TempDir()
	writeJSON(t, fix, "metadata.json", `{
		"http_requests_total": [],
		"node_load1": [],
		"node_cpu_seconds_total": []
	}`)
	out := filepath.Join(t.TempDir(), "coverage.json")
	if err := Run(&Config{FixtureDir: fix, Out: out}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	body, _ := os.ReadFile(out)
	var r covpkg.Report
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if r.Summary.MetricsTotal != 3 || r.Summary.MetricsCovered != 0 || r.Summary.MetricsUncovered != 3 {
		t.Errorf("unexpected summary: %+v", r.Summary)
	}
	if len(r.UnknownFamilies) == 0 || r.UnknownFamilies[0].Name != "node" {
		t.Errorf("expected node family first; got %+v", r.UnknownFamilies)
	}
}

// TestRun_WithDashboardCoversReferencedMetrics: a dashboard.json
// referencing some inventory metrics moves them to Covered and out
// of UnknownFamilies.
func TestRun_WithDashboardCoversReferencedMetrics(t *testing.T) {
	t.Parallel()
	fix := t.TempDir()
	writeJSON(t, fix, "metadata.json", `{
		"http_requests_total": [],
		"node_load1": [],
		"node_cpu_seconds_total": []
	}`)
	dash := t.TempDir()
	writeJSON(t, dash, "dashboard.json", `{
		"panels": [
			{"id": 1, "type": "row", "title": "traffic"},
			{"id": 2, "type": "timeseries", "title": "rps", "targets": [{"expr": "sum by (job) (rate(http_requests_total[5m]))"}]}
		]
	}`)
	out := filepath.Join(t.TempDir(), "coverage.json")
	if err := Run(&Config{FixtureDir: fix, In: dash, Out: out}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	body, _ := os.ReadFile(out)
	var r covpkg.Report
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if r.Summary.MetricsCovered != 1 {
		t.Errorf("MetricsCovered = %d; want 1", r.Summary.MetricsCovered)
	}
	if len(r.Covered) != 1 || r.Covered[0] != "http_requests_total" {
		t.Errorf("Covered = %v", r.Covered)
	}
	if r.SourceDashboard == "" {
		t.Errorf("SourceDashboard must be populated when In is set")
	}
}

// TestRun_DeterministicReport: same input twice → byte-identical
// output. Step 3.1 acceptance row.
func TestRun_DeterministicReport(t *testing.T) {
	t.Parallel()
	fix := t.TempDir()
	writeJSON(t, fix, "metadata.json", `{
		"a_one": [], "b_one": [], "c_one": [], "a_two": [], "b_two": []
	}`)
	first := filepath.Join(t.TempDir(), "cov.json")
	second := filepath.Join(t.TempDir(), "cov.json")
	if err := Run(&Config{FixtureDir: fix, Out: first}); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if err := Run(&Config{FixtureDir: fix, Out: second}); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	a, _ := os.ReadFile(first)
	b, _ := os.ReadFile(second)
	if string(a) != string(b) {
		t.Errorf("two runs differ:\nfirst:\n%s\nsecond:\n%s", a, b)
	}
}

// TestRun_MissingMetadataMapsToErrInput: pointing at a directory
// without metadata.json fails with ErrInput.
func TestRun_MissingMetadataMapsToErrInput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	err := Run(&Config{FixtureDir: dir})
	if err == nil {
		t.Fatal("expected error; got nil")
	}
	if !errors.Is(err, ErrInput) {
		t.Errorf("error chain missing ErrInput: %v", err)
	}
}

// TestRun_AgainstRealFixturesProducesCoverage: drives the orchestrator
// against an actual committed fixture + golden bundle so we exercise
// the real on-disk schemas. The committed bundle covers most of the
// fixture inventory; we just assert the summary is populated and at
// least one metric ends up in Covered.
func TestRun_AgainstRealFixturesProducesCoverage(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "cov.json")
	cfg := &Config{
		FixtureDir: "../../../testdata/fixtures/service-basic",
		In:         "../../../testdata/goldens/service-basic",
		Out:        out,
	}
	if err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	body, _ := os.ReadFile(out)
	var r covpkg.Report
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if r.Summary.MetricsTotal == 0 {
		t.Errorf("MetricsTotal = 0; expected the service-basic fixture to declare metrics")
	}
	if r.Summary.MetricsCovered == 0 {
		t.Errorf("MetricsCovered = 0; expected the committed golden to reference at least one metric")
	}
}
