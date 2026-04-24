package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestServiceCPU_Match(t *testing.T) {
	r := NewServiceCPU()
	pos := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "process_cpu_seconds_total"},
		Type:       inventory.MetricTypeCounter,
	}
	if !r.Match(pos) {
		t.Errorf("expected match on process_cpu_seconds_total")
	}
	posContainer := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "container_cpu_usage_seconds_total"},
		Type:       inventory.MetricTypeCounter,
	}
	if !r.Match(posContainer) {
		t.Errorf("expected match on container_cpu_usage_seconds_total")
	}
	neg := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "other_cpu_seconds_total"},
		Type:       inventory.MetricTypeCounter,
	}
	if r.Match(neg) {
		t.Errorf("expected no match on unknown CPU metric name")
	}
}

func TestServiceCPU_BuildPanels(t *testing.T) {
	r := NewServiceCPU()
	inv := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{{
			Descriptor: inventory.MetricDescriptor{
				Name:   "process_cpu_seconds_total",
				Labels: []string{"instance", "job"},
			},
			Type: inventory.MetricTypeCounter,
		}},
	}
	panels := r.BuildPanels(inv, profiles.ProfileService)
	if len(panels) != 1 {
		t.Fatalf("expected 1 panel, got %d", len(panels))
	}
	expr := panels[0].Queries[0].Expr
	if !strings.Contains(expr, "rate(process_cpu_seconds_total[5m])") {
		t.Errorf("expected rate expression, got %q", expr)
	}
}
