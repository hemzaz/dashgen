package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestServiceGoroutines_Match(t *testing.T) {
	r := NewServiceGoroutines()
	cases := []struct {
		name    string
		metric  string
		typ     inventory.MetricType
		wantHit bool
	}{
		{
			name:    "exact_name_go_goroutines_matches",
			metric:  "go_goroutines",
			typ:     inventory.MetricTypeGauge,
			wantHit: true,
		},
		{
			name:    "different_go_gauge_go_threads_rejected",
			metric:  "go_threads",
			typ:     inventory.MetricTypeGauge,
			wantHit: false,
		},
		{
			name:    "go_goroutines_as_counter_rejected",
			metric:  "go_goroutines",
			typ:     inventory.MetricTypeCounter,
			wantHit: false,
		},
		{
			name:    "unrelated_gauge_rejected",
			metric:  "process_resident_memory_bytes",
			typ:     inventory.MetricTypeGauge,
			wantHit: false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			m := ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: tc.metric},
				Type:       tc.typ,
			}
			if got := r.Match(m); got != tc.wantHit {
				t.Fatalf("Match(name=%q, type=%s) = %v, want %v", tc.metric, tc.typ, got, tc.wantHit)
			}
		})
	}
}

func TestServiceGoroutines_BuildPanels(t *testing.T) {
	r := NewServiceGoroutines()
	inv := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{{
			Descriptor: inventory.MetricDescriptor{
				Name:   "go_goroutines",
				Labels: []string{"instance", "job"},
			},
			Type: inventory.MetricTypeGauge,
		}},
	}
	panels := r.BuildPanels(inv, profiles.ProfileService)
	if len(panels) != 1 {
		t.Fatalf("expected 1 panel, got %d", len(panels))
	}
	if len(panels[0].Queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(panels[0].Queries))
	}
	expr := panels[0].Queries[0].Expr
	if !strings.Contains(expr, "max by (instance, job) (go_goroutines)") {
		t.Errorf("expected max by (instance, job) (go_goroutines) in expression, got %q", expr)
	}
	if panels[0].Queries[0].Unit != "" {
		t.Errorf("expected empty unit, got %q", panels[0].Queries[0].Unit)
	}
	if !strings.Contains(panels[0].Title, "go_goroutines") {
		t.Errorf("expected title to contain go_goroutines, got %q", panels[0].Title)
	}
}
