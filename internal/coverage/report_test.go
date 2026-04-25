package coverage

import (
	"reflect"
	"testing"
)

func TestFamilyOf(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"http_requests_total", "http"},
		{"node_cpu_seconds_total", "node"},
		{"up", "up"},
		{"", ""},
		{"_leading_underscore", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := FamilyOf(tc.in); got != tc.want {
				t.Errorf("FamilyOf(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestCompute_NoDashboardEverythingUncovered: with no dashboardRefs,
// every inventory metric is uncovered, families surface ordered by
// descending count then name.
func TestCompute_NoDashboardEverythingUncovered(t *testing.T) {
	t.Parallel()
	inv := []string{
		"node_cpu_seconds_total",
		"node_memory_bytes",
		"node_load1",
		"http_requests_total",
		"go_goroutines",
	}
	summary, covered, uncovered, families := Compute(inv, nil)

	if got, want := summary.MetricsTotal, 5; got != want {
		t.Errorf("MetricsTotal = %d; want %d", got, want)
	}
	if got, want := summary.MetricsCovered, 0; got != want {
		t.Errorf("MetricsCovered = %d; want %d", got, want)
	}
	if got, want := summary.MetricsUncovered, 5; got != want {
		t.Errorf("MetricsUncovered = %d; want %d", got, want)
	}
	if len(covered) != 0 {
		t.Errorf("expected no covered; got %v", covered)
	}
	wantUncovered := []string{
		"go_goroutines", "http_requests_total",
		"node_cpu_seconds_total", "node_load1", "node_memory_bytes",
	}
	if !reflect.DeepEqual(uncovered, wantUncovered) {
		t.Errorf("Uncovered = %v; want %v", uncovered, wantUncovered)
	}
	wantFamilies := []Family{
		{Name: "node", Count: 3, Metrics: []string{"node_cpu_seconds_total", "node_load1", "node_memory_bytes"}},
		{Name: "go", Count: 1, Metrics: []string{"go_goroutines"}},
		{Name: "http", Count: 1, Metrics: []string{"http_requests_total"}},
	}
	if !reflect.DeepEqual(families, wantFamilies) {
		t.Errorf("UnknownFamilies = %+v;\n want %+v", families, wantFamilies)
	}
}

// TestCompute_PartiallyCovered: the dashboard references some
// inventory metrics; covered/uncovered partitions accordingly.
func TestCompute_PartiallyCovered(t *testing.T) {
	t.Parallel()
	inv := []string{"a_one", "a_two", "b_one", "c"}
	refs := []string{"a_one", "c"}
	summary, covered, uncovered, families := Compute(inv, refs)

	if summary.MetricsTotal != 4 || summary.MetricsCovered != 2 || summary.MetricsUncovered != 2 {
		t.Errorf("unexpected summary: %+v", summary)
	}
	if !reflect.DeepEqual(covered, []string{"a_one", "c"}) {
		t.Errorf("Covered = %v", covered)
	}
	if !reflect.DeepEqual(uncovered, []string{"a_two", "b_one"}) {
		t.Errorf("Uncovered = %v", uncovered)
	}
	wantFamilies := []Family{
		{Name: "a", Count: 1, Metrics: []string{"a_two"}},
		{Name: "b", Count: 1, Metrics: []string{"b_one"}},
	}
	if !reflect.DeepEqual(families, wantFamilies) {
		t.Errorf("UnknownFamilies = %+v;\n want %+v", families, wantFamilies)
	}
}

// TestCompute_DedupesInventory: a duplicate metric in the inventory
// is counted once.
func TestCompute_DedupesInventory(t *testing.T) {
	t.Parallel()
	inv := []string{"foo_total", "foo_total", "bar"}
	summary, _, _, _ := Compute(inv, nil)
	if summary.MetricsTotal != 2 {
		t.Errorf("MetricsTotal = %d; want 2 (dup deduped)", summary.MetricsTotal)
	}
}

// TestCompute_RefsOutsideInventoryIgnored: a dashboard that
// references metrics not in the inventory does not inflate Covered.
func TestCompute_RefsOutsideInventoryIgnored(t *testing.T) {
	t.Parallel()
	inv := []string{"foo_total"}
	refs := []string{"foo_total", "ghost_metric"}
	summary, covered, _, _ := Compute(inv, refs)
	if summary.MetricsCovered != 1 {
		t.Errorf("MetricsCovered = %d; want 1", summary.MetricsCovered)
	}
	if !reflect.DeepEqual(covered, []string{"foo_total"}) {
		t.Errorf("Covered = %v; want [foo_total]", covered)
	}
}

// TestCompute_DeterministicOrdering: running twice over the same
// input produces identical output. Family-by-count tie-break is
// stable.
func TestCompute_DeterministicOrdering(t *testing.T) {
	t.Parallel()
	inv := []string{"a_one", "b_one", "c_one", "a_two", "b_two", "c_two"}
	s1, c1, u1, f1 := Compute(inv, nil)
	s2, c2, u2, f2 := Compute(inv, nil)
	if !reflect.DeepEqual(s1, s2) || !reflect.DeepEqual(c1, c2) || !reflect.DeepEqual(u1, u2) || !reflect.DeepEqual(f1, f2) {
		t.Errorf("two runs over identical input differ:\nrun 1: %+v %v %v %+v\nrun 2: %+v %v %v %+v",
			s1, c1, u1, f1, s2, c2, u2, f2)
	}
}

// TestExtractReferencedMetrics covers the inventory-intersection
// extraction the orchestrator uses to convert dashboard PromQL into
// the reference set.
func TestExtractReferencedMetrics(t *testing.T) {
	t.Parallel()
	inv := []string{"http_requests_total", "node_load1", "go_goroutines"}
	cases := []struct {
		name  string
		exprs []string
		want  []string
	}{
		{
			name:  "single_simple_match",
			exprs: []string{`sum by (job) (rate(http_requests_total[5m]))`},
			want:  []string{"http_requests_total"},
		},
		{
			name: "multiple_targets_dedup",
			exprs: []string{
				`rate(http_requests_total[5m])`,
				`rate(http_requests_total[1m])`,
				`max(go_goroutines)`,
			},
			want: []string{"go_goroutines", "http_requests_total"},
		},
		{
			name:  "no_match_returns_nil_or_empty",
			exprs: []string{`foo + bar`},
			want:  nil,
		},
		{
			name:  "histogram_quantile_matches",
			exprs: []string{`histogram_quantile(0.95, sum by (le) (rate(node_load1[5m])))`},
			want:  []string{"node_load1"},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ExtractReferencedMetrics(inv, tc.exprs)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ExtractReferencedMetrics(...) = %v; want %v", got, tc.want)
			}
		})
	}
}

// TestExtractReferencedMetrics_EmptyInputs returns nil for both
// edge cases (empty inventory, empty exprs) so the orchestrator
// can pass nil through without a special branch.
func TestExtractReferencedMetrics_EmptyInputs(t *testing.T) {
	t.Parallel()
	if got := ExtractReferencedMetrics(nil, []string{"foo"}); got != nil {
		t.Errorf("nil inventory: got %v; want nil", got)
	}
	if got := ExtractReferencedMetrics([]string{"foo"}, nil); got != nil {
		t.Errorf("nil exprs: got %v; want nil", got)
	}
}
