package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestK8sContainerResources_Match(t *testing.T) {
	r := NewK8sContainerResources()
	posCPU := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "container_cpu_usage_seconds_total"},
		Type:       inventory.MetricTypeCounter,
	}
	if !r.Match(posCPU) {
		t.Errorf("expected match on container_cpu_usage_seconds_total")
	}
	posMem := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "container_memory_working_set_bytes"},
		Type:       inventory.MetricTypeGauge,
	}
	if !r.Match(posMem) {
		t.Errorf("expected match on container_memory_working_set_bytes")
	}
	neg := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "container_network_receive_bytes_total"},
		Type:       inventory.MetricTypeCounter,
	}
	if r.Match(neg) {
		t.Errorf("expected no match on unrelated container metric")
	}
}

func TestK8sContainerResources_BuildPanels(t *testing.T) {
	r := NewK8sContainerResources()
	inv := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{
			{
				Descriptor: inventory.MetricDescriptor{Name: "container_cpu_usage_seconds_total", Labels: []string{"container", "namespace", "pod"}},
				Type:       inventory.MetricTypeCounter,
			},
			{
				Descriptor: inventory.MetricDescriptor{Name: "container_memory_working_set_bytes", Labels: []string{"container", "namespace", "pod"}},
				Type:       inventory.MetricTypeGauge,
			},
		},
	}
	panels := r.BuildPanels(inv, profiles.ProfileK8s)
	if len(panels) != 2 {
		t.Fatalf("expected 2 panels (CPU+memory), got %d", len(panels))
	}
	for _, p := range panels {
		expr := p.Queries[0].Expr
		if !strings.Contains(expr, `container!=""`) || !strings.Contains(expr, `pod!=""`) {
			t.Errorf("expected empty-label filter, got %q", expr)
		}
	}
}
