package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestInfraNetwork_Match(t *testing.T) {
	r := NewInfraNetwork()
	for _, name := range []string{"node_network_receive_bytes_total", "node_network_transmit_bytes_total"} {
		pos := ClassifiedMetricView{
			Descriptor: inventory.MetricDescriptor{Name: name},
			Type:       inventory.MetricTypeCounter,
		}
		if !r.Match(pos) {
			t.Errorf("expected match on %s", name)
		}
	}
	neg := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "node_network_receive_packets_total"},
		Type:       inventory.MetricTypeCounter,
	}
	if r.Match(neg) {
		t.Errorf("expected no match on packets counter (only bytes)")
	}
}

func TestInfraNetwork_BuildPanels(t *testing.T) {
	r := NewInfraNetwork()
	inv := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{
			{
				Descriptor: inventory.MetricDescriptor{Name: "node_network_receive_bytes_total", Labels: []string{"device", "instance"}},
				Type:       inventory.MetricTypeCounter,
			},
			{
				Descriptor: inventory.MetricDescriptor{Name: "node_network_transmit_bytes_total", Labels: []string{"device", "instance"}},
				Type:       inventory.MetricTypeCounter,
			},
		},
	}
	panels := r.BuildPanels(inv, profiles.ProfileInfra)
	if len(panels) != 2 {
		t.Fatalf("expected 2 panels (RX+TX), got %d", len(panels))
	}
	for _, p := range panels {
		expr := p.Queries[0].Expr
		if !strings.Contains(expr, `device!~"lo|veth.*"`) {
			t.Errorf("expected lo/veth exclusion, got %q", expr)
		}
		if p.Unit != "Bps" {
			t.Errorf("expected Bps unit, got %q", p.Unit)
		}
	}
}
