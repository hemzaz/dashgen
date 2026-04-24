package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestK8sRestarts_Match(t *testing.T) {
	r := NewK8sRestarts()
	pos := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "kube_pod_container_status_restarts_total"},
		Type:       inventory.MetricTypeCounter,
	}
	if !r.Match(pos) {
		t.Errorf("expected match on kube_pod_container_status_restarts_total")
	}
	neg := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "kube_pod_container_status_waiting"},
		Type:       inventory.MetricTypeGauge,
	}
	if r.Match(neg) {
		t.Errorf("expected no match on gauge variant")
	}
}

func TestK8sRestarts_BuildPanels(t *testing.T) {
	r := NewK8sRestarts()
	inv := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{{
			Descriptor: inventory.MetricDescriptor{Name: "kube_pod_container_status_restarts_total", Labels: []string{"container", "namespace", "pod"}},
			Type:       inventory.MetricTypeCounter,
		}},
	}
	panels := r.BuildPanels(inv, profiles.ProfileK8s)
	if len(panels) != 1 {
		t.Fatalf("expected 1 panel, got %d", len(panels))
	}
	expr := panels[0].Queries[0].Expr
	if !strings.Contains(expr, "[15m]") {
		t.Errorf("expected 15m window, got %q", expr)
	}
	if !strings.Contains(expr, "sum by (namespace, pod, container)") {
		t.Errorf("expected namespace+pod+container grouping, got %q", expr)
	}
}
