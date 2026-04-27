package recipes

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

// loadFixture is a test helper that loads a single named fixture from
// testdata/valid and returns the LoadedRecipe.
func loadFixture(t *testing.T, name string) LoadedRecipe {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("testdata/valid", name))
	if err != nil {
		t.Fatalf("abs(%s): %v", name, err)
	}
	got, err := LoadFile(context.Background(), LoaderConfig{}, path, SourceUser)
	if err != nil {
		t.Fatalf("LoadFile(%s): %v", name, err)
	}
	return got
}

// makeView is a test helper that builds a ClassifiedMetricView with the
// given name, type, traits, and labels.
func makeView(name string, mt inventory.MetricType, traits []string, labels []string) ClassifiedMetricView {
	return ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{
			Name:   name,
			Type:   mt,
			Labels: labels,
		},
		Type:   mt,
		Traits: traits,
	}
}

// =============================================================================
// TestYAMLRecipe_Match
// =============================================================================

func TestYAMLRecipe_Match(t *testing.T) {
	loaded := loadFixture(t, "service_http_rate.yaml")
	yr, err := NewYAMLRecipe(loaded)
	if err != nil {
		t.Fatalf("NewYAMLRecipe: %v", err)
	}

	if yr.Name() != "service_http_rate" {
		t.Errorf("Name=%q, want service_http_rate", yr.Name())
	}
	if yr.Section() != "traffic" {
		t.Errorf("Section=%q, want traffic", yr.Section())
	}

	hit := makeView(
		"http_requests_total",
		inventory.MetricTypeCounter,
		[]string{"service_http"},
		[]string{"job", "instance", "method", "route", "status_code"},
	)
	if !yr.Match(hit) {
		t.Errorf("Match should be true for counter+service_http")
	}

	gauge := makeView(
		"http_requests_total",
		inventory.MetricTypeGauge,
		[]string{"service_http"},
		[]string{"job"},
	)
	if yr.Match(gauge) {
		t.Errorf("Match should be false for gauge type")
	}

	noTrait := makeView(
		"http_requests_total",
		inventory.MetricTypeCounter,
		nil,
		[]string{"job"},
	)
	if yr.Match(noTrait) {
		t.Errorf("Match should be false without service_http trait")
	}
}

// =============================================================================
// TestYAMLRecipe_BuildPanels
// =============================================================================

func TestYAMLRecipe_BuildPanels(t *testing.T) {
	loaded := loadFixture(t, "service_http_rate.yaml")
	yr, err := NewYAMLRecipe(loaded)
	if err != nil {
		t.Fatalf("NewYAMLRecipe: %v", err)
	}

	hit := makeView(
		"http_requests_total",
		inventory.MetricTypeCounter,
		[]string{"service_http"},
		[]string{"job", "instance", "method", "route", "status_code"},
	)
	snapshot := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{hit},
	}

	panels := yr.BuildPanels(snapshot, profiles.ProfileService)
	if len(panels) == 0 {
		t.Fatal("expected at least one panel")
	}
	p := panels[0]
	if p.UID != "" {
		t.Errorf("UID=%q, want empty (synth fills it)", p.UID)
	}
	if p.Unit != "ops/sec" {
		t.Errorf("Unit=%q, want ops/sec", p.Unit)
	}
	if len(p.Queries) != 1 {
		t.Fatalf("Queries=%d, want 1", len(p.Queries))
	}
	expr := p.Queries[0].Expr
	if !strings.Contains(expr, "rate(http_requests_total") {
		t.Errorf("query expr should contain rate(http_requests_total: %q", expr)
	}
	if !strings.Contains(expr, "[5m]") {
		t.Errorf("query expr should use default 5m window: %q", expr)
	}

	// Wrong profile → no panels (T17).
	if got := yr.BuildPanels(snapshot, profiles.ProfileInfra); len(got) != 0 {
		t.Errorf("BuildPanels(infra) = %d panels, want 0 (T17)", len(got))
	}
}

// =============================================================================
// TestYAMLRecipe_PairResolved
// =============================================================================

func TestYAMLRecipe_PairResolved(t *testing.T) {
	loaded := loadFixture(t, "infra_filesystem_usage.yaml")
	yr, err := NewYAMLRecipe(loaded)
	if err != nil {
		t.Fatalf("NewYAMLRecipe: %v", err)
	}

	size := makeView(
		"node_filesystem_size_bytes",
		inventory.MetricTypeGauge,
		nil,
		[]string{"instance", "mountpoint", "fstype"},
	)
	avail := makeView(
		"node_filesystem_avail_bytes",
		inventory.MetricTypeGauge,
		nil,
		[]string{"instance", "mountpoint", "fstype"},
	)
	snapshot := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{size, avail},
	}

	panels := yr.BuildPanels(snapshot, profiles.ProfileInfra)
	if len(panels) == 0 {
		t.Fatal("expected at least one panel when both pair sides present")
	}
	expr := panels[0].Queries[0].Expr
	if !strings.Contains(expr, "node_filesystem_size_bytes") {
		t.Errorf("query should reference matched metric: %q", expr)
	}
	if !strings.Contains(expr, "node_filesystem_avail_bytes") {
		t.Errorf("query should reference paired metric: %q", expr)
	}
}

// =============================================================================
// TestYAMLRecipe_PairMissing_Omit
// =============================================================================

func TestYAMLRecipe_PairMissing_Omit(t *testing.T) {
	loaded := loadFixture(t, "infra_filesystem_usage.yaml")
	yr, err := NewYAMLRecipe(loaded)
	if err != nil {
		t.Fatalf("NewYAMLRecipe: %v", err)
	}

	// Only the matched side present; pair side absent.
	size := makeView(
		"node_filesystem_size_bytes",
		inventory.MetricTypeGauge,
		nil,
		[]string{"instance", "mountpoint", "fstype"},
	)
	snapshot := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{size},
	}

	panels := yr.BuildPanels(snapshot, profiles.ProfileInfra)
	if len(panels) != 0 {
		t.Errorf("expected 0 panels under on_missing=omit, got %d", len(panels))
	}
}

// =============================================================================
// TestYAMLRecipe_PanelKindMapping
// =============================================================================

func TestYAMLRecipe_PanelKindMapping(t *testing.T) {
	cases := map[string]string{
		"":           "timeseries",
		"timeseries": "timeseries",
		"stat":       "stat",
		"gauge":      "stat", // mapped to closest IR kind
		"barchart":   "graph",
		"graph":      "graph",
	}
	for in, want := range cases {
		got := string(panelKindFor(in))
		if got != want {
			t.Errorf("panelKindFor(%q) = %q, want %q", in, got, want)
		}
	}
}

// =============================================================================
// TestYAMLRecipe_ScopeFilterThreaded
// =============================================================================

// TestYAMLRecipe_ScopeFilterThreaded verifies that the scope-filter value
// supplied via ClassifiedInventorySnapshot.ScopeFilter flows through to
// RenderContext.ScopeFilter and reaches the rendered query string. T1B.1
// prerequisite: synth.snapshotOf populates this field; YAMLRecipe.BuildPanels
// must read it.
func TestYAMLRecipe_ScopeFilterThreaded(t *testing.T) {
	loaded := loadFixture(t, "service_http_rate.yaml")
	yr, err := NewYAMLRecipe(loaded)
	if err != nil {
		t.Fatalf("NewYAMLRecipe: %v", err)
	}

	hit := makeView(
		"http_requests_total",
		inventory.MetricTypeCounter,
		[]string{"service_http"},
		[]string{"job", "instance", "method", "route", "status_code"},
	)

	// Case 1 — empty ScopeFilter (today's Go-recipe baseline).
	emptySnap := ClassifiedInventorySnapshot{
		Metrics:     []ClassifiedMetricView{hit},
		ScopeFilter: "",
	}
	emptyPanels := yr.BuildPanels(emptySnap, profiles.ProfileService)
	if len(emptyPanels) == 0 {
		t.Fatal("expected at least one panel from empty-ScopeFilter snapshot")
	}
	emptyExpr := emptyPanels[0].Queries[0].Expr
	if strings.Contains(emptyExpr, `job="$job"`) {
		t.Errorf("empty ScopeFilter must NOT inject job=\"$job\": %q", emptyExpr)
	}

	// Case 2 — populated ScopeFilter (forward-compatible synth path).
	scoped := `job="$job"`
	popSnap := ClassifiedInventorySnapshot{
		Metrics:     []ClassifiedMetricView{hit},
		ScopeFilter: scoped,
	}
	popPanels := yr.BuildPanels(popSnap, profiles.ProfileService)
	if len(popPanels) == 0 {
		t.Fatal("expected at least one panel from populated-ScopeFilter snapshot")
	}
	popExpr := popPanels[0].Queries[0].Expr
	if !strings.Contains(popExpr, scoped) {
		t.Errorf("populated ScopeFilter %q not threaded into rendered expr: %q", scoped, popExpr)
	}
}

// =============================================================================
// TestYAMLRecipe_FormatQuantile
// =============================================================================

func TestYAMLRecipe_FormatQuantile(t *testing.T) {
	cases := []struct {
		q    float64
		s    string
		s100 string
	}{
		{0.5, "0.5", "50"},
		{0.95, "0.95", "95"},
		{0.99, "0.99", "99"},
	}
	for _, tc := range cases {
		if got := formatQuantile(tc.q); got != tc.s {
			t.Errorf("formatQuantile(%v) = %q, want %q", tc.q, got, tc.s)
		}
		if got := formatQuantile100(tc.q); got != tc.s100 {
			t.Errorf("formatQuantile100(%v) = %q, want %q", tc.q, got, tc.s100)
		}
	}
}
