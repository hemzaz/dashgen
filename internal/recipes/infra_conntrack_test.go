package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestInfraConntrack_Match(t *testing.T) {
	r := NewInfraConntrack()

	// Positive: exact primary signal.
	pos := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "node_nf_conntrack_entries"},
		Type:       inventory.MetricTypeGauge,
	}
	if !r.Match(pos) {
		t.Error("expected match on node_nf_conntrack_entries")
	}

	// Negative: limit metric alone is not the primary signal.
	negLimit := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "node_nf_conntrack_entries_limit"},
		Type:       inventory.MetricTypeGauge,
	}
	if r.Match(negLimit) {
		t.Error("expected no match on node_nf_conntrack_entries_limit alone")
	}

	// Negative: unrelated gauge.
	negUnrelated := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "node_network_receive_bytes_total"},
		Type:       inventory.MetricTypeGauge,
	}
	if r.Match(negUnrelated) {
		t.Error("expected no match on unrelated gauge")
	}
}

func TestInfraConntrack_BuildPanels(t *testing.T) {
	r := NewInfraConntrack()

	t.Run("both present emits one ratio panel", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{Name: "node_nf_conntrack_entries", Labels: []string{"instance"}},
					Type:       inventory.MetricTypeGauge,
				},
				{
					Descriptor: inventory.MetricDescriptor{Name: "node_nf_conntrack_entries_limit", Labels: []string{"instance"}},
					Type:       inventory.MetricTypeGauge,
				},
			},
		}
		panels := r.BuildPanels(inv, profiles.ProfileInfra)
		if len(panels) != 1 {
			t.Fatalf("expected 1 panel, got %d", len(panels))
		}
		expr := panels[0].Queries[0].Expr
		if !strings.Contains(expr, "node_nf_conntrack_entries / node_nf_conntrack_entries_limit") {
			t.Errorf("expected ratio expression, got %q", expr)
		}
	})

	t.Run("missing limit yields zero panels", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{Name: "node_nf_conntrack_entries", Labels: []string{"instance"}},
					Type:       inventory.MetricTypeGauge,
				},
			},
		}
		panels := r.BuildPanels(inv, profiles.ProfileInfra)
		if len(panels) != 0 {
			t.Errorf("expected 0 panels when limit is missing, got %d", len(panels))
		}
	})
}
