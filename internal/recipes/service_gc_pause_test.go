package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestServiceGCPause_Match(t *testing.T) {
	r := NewServiceGCPause()
	cases := []struct {
		name    string
		metName string
		typ     inventory.MetricType
		wantHit bool
	}{
		{
			name:    "summary_with_exact_name_matches",
			metName: "go_gc_duration_seconds",
			typ:     inventory.MetricTypeSummary,
			wantHit: true,
		},
		{
			name:    "histogram_with_exact_name_matches",
			metName: "go_gc_duration_seconds",
			typ:     inventory.MetricTypeHistogram,
			wantHit: true,
		},
		{
			name:    "summary_with_different_name_rejected",
			metName: "some_other_duration_seconds",
			typ:     inventory.MetricTypeSummary,
			wantHit: false,
		},
		{
			name:    "histogram_with_different_name_rejected",
			metName: "some_other_duration_seconds",
			typ:     inventory.MetricTypeHistogram,
			wantHit: false,
		},
		{
			name:    "counter_with_exact_name_rejected",
			metName: "go_gc_duration_seconds",
			typ:     inventory.MetricTypeCounter,
			wantHit: false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			m := ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: tc.metName},
				Type:       tc.typ,
			}
			if got := r.Match(m); got != tc.wantHit {
				t.Fatalf("Match(name=%q, type=%s) = %v, want %v", tc.metName, tc.typ, got, tc.wantHit)
			}
		})
	}
}

func TestServiceGCPause_BuildPanels_Summary(t *testing.T) {
	r := NewServiceGCPause()
	inv := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{{
			Descriptor: inventory.MetricDescriptor{
				Name:   "go_gc_duration_seconds",
				Labels: []string{"instance", "job"},
			},
			Type: inventory.MetricTypeSummary,
		}},
	}
	panels := r.BuildPanels(inv, profiles.ProfileService)
	if len(panels) != 1 {
		t.Fatalf("expected 1 panel, got %d", len(panels))
	}
	if len(panels[0].Queries) != 1 {
		t.Fatalf("expected 1 query for summary path, got %d", len(panels[0].Queries))
	}
	expr := panels[0].Queries[0].Expr
	if !strings.Contains(expr, `quantile="0.99"`) {
		t.Errorf("summary query must select quantile=0.99, got %q", expr)
	}
	if !strings.Contains(expr, "avg by") {
		t.Errorf("summary query must use avg by, got %q", expr)
	}
	if !strings.Contains(expr, "instance") {
		t.Errorf("summary query must group by instance, got %q", expr)
	}
	if !strings.Contains(expr, "job") {
		t.Errorf("summary query must group by job, got %q", expr)
	}
}

func TestServiceGCPause_BuildPanels_Histogram(t *testing.T) {
	r := NewServiceGCPause()
	inv := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{{
			Descriptor: inventory.MetricDescriptor{
				Name:   "go_gc_duration_seconds",
				Labels: []string{"instance", "job", "le"},
			},
			Type: inventory.MetricTypeHistogram,
		}},
	}
	panels := r.BuildPanels(inv, profiles.ProfileService)
	if len(panels) != 1 {
		t.Fatalf("expected 1 panel, got %d", len(panels))
	}
	if len(panels[0].Queries) != 3 {
		t.Fatalf("expected 3 queries (p50/p95/p99) for histogram path, got %d", len(panels[0].Queries))
	}
	wants := []string{"0.50", "0.95", "0.99"}
	for i, want := range wants {
		expr := panels[0].Queries[i].Expr
		if !strings.Contains(expr, "histogram_quantile("+want) {
			t.Errorf("query[%d] expected quantile %s, got %q", i, want, expr)
		}
		if !strings.Contains(expr, "go_gc_duration_seconds_bucket") {
			t.Errorf("query[%d] must reference _bucket series, got %q", i, expr)
		}
		if !strings.Contains(expr, "instance") {
			t.Errorf("query[%d] must group by instance, got %q", i, expr)
		}
		if !strings.Contains(expr, "job") {
			t.Errorf("query[%d] must group by job, got %q", i, expr)
		}
		if !strings.Contains(expr, "le") {
			t.Errorf("query[%d] must group by le, got %q", i, expr)
		}
	}
}
