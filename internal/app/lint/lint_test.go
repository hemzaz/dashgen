package lint

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeBundle creates a minimal `dashboard.json` for the test in a
// fresh tempdir and returns the directory.
func writeBundle(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "dashboard.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return dir
}

// TestRun_CleanBundleProducesEmptyReport: a dashboard with one valid
// panel produces an empty issues list and no error.
func TestRun_CleanBundleProducesEmptyReport(t *testing.T) {
	t.Parallel()
	dir := writeBundle(t, `{
		"panels": [
			{"id": 1, "type": "row", "title": "traffic"},
			{"id": 2, "type": "timeseries", "title": "rps", "targets": [{"expr": "sum by (job) (rate(http_requests_total[5m]))"}]}
		]
	}`)
	out := filepath.Join(t.TempDir(), "lint.json")
	if err := Run(&Config{In: dir, Out: out}); err != nil {
		t.Fatalf("Run on clean bundle returned error: %v", err)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var r Report
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if len(r.Issues) != 0 {
		t.Errorf("expected empty issues; got %d: %+v", len(r.Issues), r.Issues)
	}
	if r.Source == "" {
		t.Errorf("Source must be populated")
	}
}

// TestRun_BannedLabelExitsLintFailure: a panel using user_id triggers a
// refusal; Run returns ErrLintFailure but still writes the full report.
func TestRun_BannedLabelExitsLintFailure(t *testing.T) {
	t.Parallel()
	dir := writeBundle(t, `{
		"panels": [
			{"id": 10, "type": "timeseries", "title": "bad", "targets": [{"expr": "rate(http_requests_total{user_id=\"abc\"}[5m])"}]}
		]
	}`)
	out := filepath.Join(t.TempDir(), "lint.json")
	err := Run(&Config{In: dir, Out: out})
	if err == nil {
		t.Fatal("Run returned nil; want ErrLintFailure")
	}
	if !errors.Is(err, ErrLintFailure) {
		t.Errorf("error chain missing ErrLintFailure: %v", err)
	}
	// Report must still be on disk so CI can parse it.
	body, ferr := os.ReadFile(out)
	if ferr != nil {
		t.Fatalf("report not written despite lint failure: %v", ferr)
	}
	var r Report
	if jerr := json.Unmarshal(body, &r); jerr != nil {
		t.Fatalf("decode report: %v", jerr)
	}
	if len(r.Issues) != 1 || r.Issues[0].Code != "banned-label" {
		t.Errorf("unexpected issues: %+v", r.Issues)
	}
}

// TestRun_MissingDashboardJSON_ErrInput: pointing at an empty directory
// fails with ErrInput, not ErrLintFailure.
func TestRun_MissingDashboardJSON_ErrInput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	err := Run(&Config{In: dir})
	if err == nil {
		t.Fatal("Run on empty dir returned nil; want ErrInput")
	}
	if !errors.Is(err, ErrInput) {
		t.Errorf("error chain missing ErrInput: %v", err)
	}
}

// TestRun_MalformedDashboardJSON_ErrInput: a non-JSON file fails parsing
// with ErrInput.
func TestRun_MalformedDashboardJSON_ErrInput(t *testing.T) {
	t.Parallel()
	dir := writeBundle(t, "not json at all")
	err := Run(&Config{In: dir})
	if err == nil {
		t.Fatal("Run on malformed JSON returned nil; want ErrInput")
	}
	if !errors.Is(err, ErrInput) {
		t.Errorf("error chain missing ErrInput: %v", err)
	}
}

// TestRun_NilConfig: defensive guard.
func TestRun_NilConfig(t *testing.T) {
	t.Parallel()
	if err := Run(nil); err == nil {
		t.Fatal("Run(nil) returned nil; want error")
	}
}

// TestRun_EmptyInIsRejected: `--in` is mandatory.
func TestRun_EmptyInIsRejected(t *testing.T) {
	t.Parallel()
	err := Run(&Config{})
	if err == nil {
		t.Fatal("Run with empty In returned nil; want ErrInput")
	}
	if !errors.Is(err, ErrInput) {
		t.Errorf("error chain missing ErrInput: %v", err)
	}
}

// TestRun_DeterministicReport: running twice over identical input
// produces byte-identical reports (covers the "lint output is
// deterministic" acceptance row from Step 3.0).
func TestRun_DeterministicReport(t *testing.T) {
	t.Parallel()
	dir := writeBundle(t, `{
		"panels": [
			{"id": 200, "type": "timeseries", "title": "a", "targets": [{"expr": "rate(http_requests_total{user_id=\"x\"}[5m])"}]},
			{"id": 100, "type": "timeseries", "title": "b"},
			{"id": 150, "type": "timeseries", "title": "c", "targets": [{"expr": "rate(foo{trace_id=\"y\"}[5m])"}]}
		]
	}`)
	first := filepath.Join(t.TempDir(), "lint.json")
	second := filepath.Join(t.TempDir(), "lint.json")
	// Both runs are expected to fail with ErrLintFailure (banned labels);
	// we only care about the report bytes here.
	_ = Run(&Config{In: dir, Out: first})
	_ = Run(&Config{In: dir, Out: second})
	a, err := os.ReadFile(first)
	if err != nil {
		t.Fatalf("read first: %v", err)
	}
	b, err := os.ReadFile(second)
	if err != nil {
		t.Fatalf("read second: %v", err)
	}
	if string(a) != string(b) {
		t.Errorf("reports differ across runs:\nfirst:\n%s\nsecond:\n%s", a, b)
	}
}
