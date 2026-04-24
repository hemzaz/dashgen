package recipes

import (
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestInfraNICErrors_Match(t *testing.T) {
	r := NewInfraNICErrors()

	positives := []string{
		"node_network_receive_errs_total",
		"node_network_transmit_errs_total",
		"node_network_receive_drop_total",
		"node_network_transmit_drop_total",
	}
	for _, name := range positives {
		m := ClassifiedMetricView{
			Descriptor: inventory.MetricDescriptor{Name: name},
			Type:       inventory.MetricTypeCounter,
		}
		if !r.Match(m) {
			t.Errorf("expected match on %s", name)
		}
	}

	negatives := []struct {
		name   string
		reason string
	}{
		{
			"node_network_receive_bytes_total",
			"bytes counter belongs to infra_network (throughput), not nic_errors",
		},
		{
			"node_network_transmit_packets_total",
			"packets counter is not an error/drop metric",
		},
		{
			"some_random_counter_total",
			"unrelated counter must not match",
		},
	}
	for _, tc := range negatives {
		m := ClassifiedMetricView{
			Descriptor: inventory.MetricDescriptor{Name: tc.name},
			Type:       inventory.MetricTypeCounter,
		}
		if r.Match(m) {
			t.Errorf("unexpected match on %s: %s", tc.name, tc.reason)
		}
	}

	// Gauge variant of a matching name must also not match.
	gauge := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "node_network_receive_errs_total"},
		Type:       inventory.MetricTypeGauge,
	}
	if r.Match(gauge) {
		t.Error("expected no match when type is gauge, not counter")
	}
}

func TestInfraNICErrors_BuildPanels(t *testing.T) {
	r := NewInfraNICErrors()

	t.Run("two metrics produce two panels in title order", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{
						Name:   "node_network_receive_errs_total",
						Labels: []string{"instance", "device"},
					},
					Type: inventory.MetricTypeCounter,
				},
				{
					Descriptor: inventory.MetricDescriptor{
						Name:   "node_network_transmit_drop_total",
						Labels: []string{"instance", "device"},
					},
					Type: inventory.MetricTypeCounter,
				},
			},
		}
		panels := r.BuildPanels(inv, profiles.ProfileInfra)
		if len(panels) != 2 {
			t.Fatalf("expected 2 panels, got %d", len(panels))
		}
		// Expect alphabetical title order: "NIC RX errors" < "NIC TX drops"
		if panels[0].Title != "NIC RX errors" {
			t.Errorf("expected first panel title %q, got %q", "NIC RX errors", panels[0].Title)
		}
		if panels[1].Title != "NIC TX drops" {
			t.Errorf("expected second panel title %q, got %q", "NIC TX drops", panels[1].Title)
		}
		for _, p := range panels {
			if p.Unit != "cps" {
				t.Errorf("panel %q: expected unit cps, got %q", p.Title, p.Unit)
			}
			if len(p.Queries) != 1 {
				t.Errorf("panel %q: expected 1 query candidate, got %d", p.Title, len(p.Queries))
			}
		}
	})

	t.Run("no matching metrics produce zero panels", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{Name: "node_network_receive_bytes_total"},
					Type:       inventory.MetricTypeCounter,
				},
			},
		}
		panels := r.BuildPanels(inv, profiles.ProfileInfra)
		if len(panels) != 0 {
			t.Fatalf("expected 0 panels, got %d", len(panels))
		}
	})

	t.Run("non-infra profile returns nil", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{Name: "node_network_receive_errs_total"},
					Type:       inventory.MetricTypeCounter,
				},
			},
		}
		panels := r.BuildPanels(inv, profiles.ProfileService)
		if panels != nil {
			t.Errorf("expected nil for non-infra profile, got %v", panels)
		}
	})
}
