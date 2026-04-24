package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestK8sPodHealth_Match(t *testing.T) {
	r := NewK8sPodHealth()
	pos := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "kube_pod_status_phase"},
		Type:       inventory.MetricTypeGauge,
	}
	if !r.Match(pos) {
		t.Errorf("expected match on kube_pod_status_phase")
	}
	neg := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "kube_pod_info"},
		Type:       inventory.MetricTypeGauge,
	}
	if r.Match(neg) {
		t.Errorf("expected no match on kube_pod_info")
	}
}

func TestK8sPodHealth_BuildPanels(t *testing.T) {
	r := NewK8sPodHealth()
	inv := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{{
			Descriptor: inventory.MetricDescriptor{Name: "kube_pod_status_phase", Labels: []string{"namespace", "phase", "pod"}},
			Type:       inventory.MetricTypeGauge,
		}},
	}
	panels := r.BuildPanels(inv, profiles.ProfileK8s)
	if len(panels) != 1 {
		t.Fatalf("expected 1 panel, got %d", len(panels))
	}
	expr := panels[0].Queries[0].Expr
	if !strings.Contains(expr, "sum by (namespace, phase)") {
		t.Errorf("expected sum by (namespace, phase), got %q", expr)
	}
}
