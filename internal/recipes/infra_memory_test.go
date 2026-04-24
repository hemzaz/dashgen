package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestInfraMemory_Match(t *testing.T) {
	r := NewInfraMemory()
	for _, name := range []string{"node_memory_MemAvailable_bytes", "node_memory_MemTotal_bytes"} {
		pos := ClassifiedMetricView{
			Descriptor: inventory.MetricDescriptor{Name: name},
			Type:       inventory.MetricTypeGauge,
		}
		if !r.Match(pos) {
			t.Errorf("expected match on %s", name)
		}
	}
	neg := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "node_memory_Buffers_bytes"},
		Type:       inventory.MetricTypeGauge,
	}
	if r.Match(neg) {
		t.Errorf("expected no match on unknown memory metric")
	}
}

func TestInfraMemory_BuildPanels(t *testing.T) {
	r := NewInfraMemory()
	inv := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{
			{
				Descriptor: inventory.MetricDescriptor{Name: "node_memory_MemAvailable_bytes", Labels: []string{"instance"}},
				Type:       inventory.MetricTypeGauge,
			},
			{
				Descriptor: inventory.MetricDescriptor{Name: "node_memory_MemTotal_bytes", Labels: []string{"instance"}},
				Type:       inventory.MetricTypeGauge,
			},
		},
	}
	panels := r.BuildPanels(inv, profiles.ProfileInfra)
	if len(panels) != 1 {
		t.Fatalf("expected 1 panel, got %d", len(panels))
	}
	expr := panels[0].Queries[0].Expr
	if !strings.Contains(expr, "node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes") {
		t.Errorf("expected ratio expression, got %q", expr)
	}
}

func TestInfraMemory_SkipsWhenOneSideMissing(t *testing.T) {
	r := NewInfraMemory()
	inv := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{{
			Descriptor: inventory.MetricDescriptor{Name: "node_memory_MemAvailable_bytes"},
			Type:       inventory.MetricTypeGauge,
		}},
	}
	if got := r.BuildPanels(inv, profiles.ProfileInfra); len(got) != 0 {
		t.Errorf("expected no panels when MemTotal is missing, got %d", len(got))
	}
}
