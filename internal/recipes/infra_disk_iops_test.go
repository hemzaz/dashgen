package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestInfraDiskIOPS_Match(t *testing.T) {
	r := NewInfraDiskIOPS()

	positives := []string{
		"node_disk_reads_completed_total",
		"node_disk_writes_completed_total",
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
			"node_disk_read_bytes_total",
			"bytes counter belongs to infra_disk (throughput), not iops",
		},
		{
			"node_filesystem_size_bytes",
			"filesystem gauge is an entirely different metric family",
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
		Descriptor: inventory.MetricDescriptor{Name: "node_disk_reads_completed_total"},
		Type:       inventory.MetricTypeGauge,
	}
	if r.Match(gauge) {
		t.Error("expected no match when type is gauge, not counter")
	}
}

func TestInfraDiskIOPS_BuildPanels(t *testing.T) {
	r := NewInfraDiskIOPS()

	t.Run("both metrics produce two panels in title order", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{
						Name:   "node_disk_reads_completed_total",
						Labels: []string{"instance", "device"},
					},
					Type: inventory.MetricTypeCounter,
				},
				{
					Descriptor: inventory.MetricDescriptor{
						Name:   "node_disk_writes_completed_total",
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
		// Alphabetical title order: "Disk read IOPS" < "Disk write IOPS"
		if panels[0].Title != "Disk read IOPS" {
			t.Errorf("expected first panel title %q, got %q", "Disk read IOPS", panels[0].Title)
		}
		if panels[1].Title != "Disk write IOPS" {
			t.Errorf("expected second panel title %q, got %q", "Disk write IOPS", panels[1].Title)
		}
		for _, p := range panels {
			if p.Unit != "iops" {
				t.Errorf("panel %q: expected unit iops, got %q", p.Title, p.Unit)
			}
			if len(p.Queries) != 1 {
				t.Errorf("panel %q: expected 1 query candidate, got %d", p.Title, len(p.Queries))
			}
			q := p.Queries[0]
			if !strings.Contains(q.Expr, "rate(") {
				t.Errorf("panel %q: query missing rate(): %s", p.Title, q.Expr)
			}
			if !strings.Contains(q.Expr, "sum by (instance, device)") {
				t.Errorf("panel %q: query missing sum by (instance, device): %s", p.Title, q.Expr)
			}
		}
	})

	t.Run("only reads metric produces one panel", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{
						Name:   "node_disk_reads_completed_total",
						Labels: []string{"instance", "device"},
					},
					Type: inventory.MetricTypeCounter,
				},
			},
		}
		panels := r.BuildPanels(inv, profiles.ProfileInfra)
		if len(panels) != 1 {
			t.Fatalf("expected 1 panel, got %d", len(panels))
		}
		if panels[0].Title != "Disk read IOPS" {
			t.Errorf("expected panel title %q, got %q", "Disk read IOPS", panels[0].Title)
		}
	})

	t.Run("neither metric produces zero panels", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{Name: "node_disk_read_bytes_total"},
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
					Descriptor: inventory.MetricDescriptor{Name: "node_disk_reads_completed_total"},
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
