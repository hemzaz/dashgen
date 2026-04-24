package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestK8sOOMKills_Match(t *testing.T) {
	r := NewK8sOOMKills()

	tests := []struct {
		name    string
		metric  ClassifiedMetricView
		wantHit bool
	}{
		{
			name: "positive: kube_pod_container_status_terminated_reason gauge",
			metric: ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "kube_pod_container_status_terminated_reason"},
				Type:       inventory.MetricTypeGauge,
			},
			wantHit: true,
		},
		{
			name: "negative: kube_pod_container_status_ready",
			metric: ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "kube_pod_container_status_ready"},
				Type:       inventory.MetricTypeGauge,
			},
			wantHit: false,
		},
		{
			name: "negative: unrelated metric",
			metric: ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "kube_pod_info"},
				Type:       inventory.MetricTypeGauge,
			},
			wantHit: false,
		},
		{
			name: "negative: restarts counter (look-alike)",
			metric: ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "kube_pod_container_status_restarts_total"},
				Type:       inventory.MetricTypeCounter,
			},
			wantHit: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := r.Match(tc.metric)
			if got != tc.wantHit {
				t.Errorf("Match() = %v, want %v", got, tc.wantHit)
			}
		})
	}
}

func TestK8sOOMKills_BuildPanels(t *testing.T) {
	r := NewK8sOOMKills()
	inv := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{{
			Descriptor: inventory.MetricDescriptor{
				Name:   "kube_pod_container_status_terminated_reason",
				Labels: []string{"namespace", "pod", "container", "reason"},
			},
			Type: inventory.MetricTypeGauge,
		}},
	}

	panels := r.BuildPanels(inv, profiles.ProfileK8s)
	if len(panels) != 1 {
		t.Fatalf("expected 1 panel, got %d", len(panels))
	}

	p := panels[0]
	if len(p.Queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(p.Queries))
	}

	expr := p.Queries[0].Expr
	if !strings.Contains(expr, `reason="OOMKilled"`) {
		t.Errorf("query missing reason=OOMKilled filter, got: %q", expr)
	}
	if !strings.Contains(expr, "sum by (namespace, pod)") {
		t.Errorf("query missing sum by (namespace, pod) grouping, got: %q", expr)
	}
}
