package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestInfraCPU_Match(t *testing.T) {
	r := NewInfraCPU()
	pos := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "node_cpu_seconds_total"},
		Type:       inventory.MetricTypeCounter,
	}
	if !r.Match(pos) {
		t.Errorf("expected match on node_cpu_seconds_total")
	}
	neg := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "process_cpu_seconds_total"},
		Type:       inventory.MetricTypeCounter,
	}
	if r.Match(neg) {
		t.Errorf("expected no match on non-infra CPU metric")
	}
}

func TestInfraCPU_BuildPanels(t *testing.T) {
	r := NewInfraCPU()
	inv := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{{
			Descriptor: inventory.MetricDescriptor{
				Name:   "node_cpu_seconds_total",
				Labels: []string{"cpu", "instance", "job", "mode"},
			},
			Type: inventory.MetricTypeCounter,
		}},
	}
	panels := r.BuildPanels(inv, profiles.ProfileInfra)
	if len(panels) != 1 {
		t.Fatalf("expected 1 panel, got %d", len(panels))
	}
	expr := panels[0].Queries[0].Expr
	if !strings.Contains(expr, `mode="idle"`) {
		t.Errorf("expected idle-mode filter, got %q", expr)
	}
	if !strings.Contains(expr, "[5m]") {
		t.Errorf("expected 5m window, got %q", expr)
	}
	if panels[0].Unit != "percentunit" {
		t.Errorf("expected percentunit, got %q", panels[0].Unit)
	}
}
