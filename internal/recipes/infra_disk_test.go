package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestInfraDisk_Match(t *testing.T) {
	r := NewInfraDisk()
	pos := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "node_filesystem_avail_bytes"},
		Type:       inventory.MetricTypeGauge,
	}
	if !r.Match(pos) {
		t.Errorf("expected match on node_filesystem_avail_bytes")
	}
	neg := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "node_filesystem_files"},
		Type:       inventory.MetricTypeGauge,
	}
	if r.Match(neg) {
		t.Errorf("expected no match on unrelated filesystem metric")
	}
}

func TestInfraDisk_BuildPanels(t *testing.T) {
	r := NewInfraDisk()
	inv := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{
			{
				Descriptor: inventory.MetricDescriptor{Name: "node_filesystem_avail_bytes", Labels: []string{"device", "fstype", "instance", "mountpoint"}},
				Type:       inventory.MetricTypeGauge,
			},
			{
				Descriptor: inventory.MetricDescriptor{Name: "node_filesystem_size_bytes", Labels: []string{"device", "fstype", "instance", "mountpoint"}},
				Type:       inventory.MetricTypeGauge,
			},
		},
	}
	panels := r.BuildPanels(inv, profiles.ProfileInfra)
	if len(panels) != 1 {
		t.Fatalf("expected 1 panel, got %d", len(panels))
	}
	expr := panels[0].Queries[0].Expr
	if !strings.Contains(expr, "node_filesystem_avail_bytes / node_filesystem_size_bytes") {
		t.Errorf("expected ratio expression, got %q", expr)
	}
}
