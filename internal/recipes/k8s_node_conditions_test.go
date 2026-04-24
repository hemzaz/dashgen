package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestK8sNodeConditions_Match(t *testing.T) {
	r := NewK8sNodeConditions()

	tests := []struct {
		name      string
		metric    ClassifiedMetricView
		wantMatch bool
	}{
		{
			name: "positive: kube_node_status_condition gauge",
			metric: ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "kube_node_status_condition"},
				Type:       inventory.MetricTypeGauge,
			},
			wantMatch: true,
		},
		{
			name: "negative: kube_pod_status_phase is a different metric",
			metric: ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "kube_pod_status_phase"},
				Type:       inventory.MetricTypeGauge,
			},
			wantMatch: false,
		},
		{
			name: "negative: unrelated gauge",
			metric: ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "node_cpu_seconds_total"},
				Type:       inventory.MetricTypeGauge,
			},
			wantMatch: false,
		},
		{
			name: "negative: counter with matching name prefix",
			metric: ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "kube_node_status_condition"},
				Type:       inventory.MetricTypeCounter,
			},
			wantMatch: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := r.Match(tc.metric)
			if got != tc.wantMatch {
				t.Errorf("Match() = %v, want %v", got, tc.wantMatch)
			}
		})
	}
}

func TestK8sNodeConditions_BuildPanels(t *testing.T) {
	r := NewK8sNodeConditions()
	inv := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{{
			Descriptor: inventory.MetricDescriptor{
				Name:   "kube_node_status_condition",
				Labels: []string{"node", "condition", "status"},
			},
			Type: inventory.MetricTypeGauge,
		}},
	}

	panels := r.BuildPanels(inv, profiles.ProfileK8s)
	if len(panels) != 1 {
		t.Fatalf("expected 1 panel, got %d", len(panels))
	}

	panel := panels[0]
	if len(panel.Queries) != 4 {
		t.Fatalf("expected 4 query candidates, got %d", len(panel.Queries))
	}

	// Verify all four condition values are present.
	wantConditions := []string{"NotReady", "MemoryPressure", "DiskPressure", "PIDPressure"}
	for i, cond := range wantConditions {
		expr := panel.Queries[i].Expr
		if !strings.Contains(expr, `condition="`+cond+`"`) {
			t.Errorf("query[%d]: expected condition=%q, got %q", i, cond, expr)
		}
	}

	// Verify deterministic order: NotReady must be first, PIDPressure last.
	firstExpr := panel.Queries[0].Expr
	if !strings.Contains(firstExpr, `condition="NotReady"`) {
		t.Errorf("query[0] must be NotReady, got %q", firstExpr)
	}
	lastExpr := panel.Queries[3].Expr
	if !strings.Contains(lastExpr, `condition="PIDPressure"`) {
		t.Errorf("query[3] must be PIDPressure, got %q", lastExpr)
	}

	// Verify max aggregation and status="true" filter are present in every query.
	for i, q := range panel.Queries {
		if !strings.Contains(q.Expr, "max by (node, condition)") {
			t.Errorf("query[%d]: expected max by (node, condition), got %q", i, q.Expr)
		}
		if !strings.Contains(q.Expr, `status="true"`) {
			t.Errorf("query[%d]: expected status=\"true\" filter, got %q", i, q.Expr)
		}
		if q.LegendFormat != "{{node}} {{condition}}" {
			t.Errorf("query[%d]: legend = %q, want {{node}} {{condition}}", i, q.LegendFormat)
		}
	}

	// Verify profile gate: non-k8s profile returns no panels.
	if got := r.BuildPanels(inv, profiles.ProfileService); got != nil {
		t.Errorf("expected nil panels for non-k8s profile, got %d", len(got))
	}
}
