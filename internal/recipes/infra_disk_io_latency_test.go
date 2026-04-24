package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestInfraDiskIOLatency_Match(t *testing.T) {
	r := NewInfraDiskIOLatency()

	positives := []string{
		"node_disk_io_time_seconds_total",
		"node_disk_io_time_weighted_seconds_total",
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
			"node_disk_reads_completed_total",
			"IOPS counter belongs to infra_disk_iops, not io-time latency",
		},
		{
			"node_disk_written_bytes_total",
			"byte-throughput counter is a different metric family",
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
		Descriptor: inventory.MetricDescriptor{Name: "node_disk_io_time_seconds_total"},
		Type:       inventory.MetricTypeGauge,
	}
	if r.Match(gauge) {
		t.Error("expected no match when type is gauge, not counter")
	}
}

func TestInfraDiskIOLatency_BuildPanels(t *testing.T) {
	r := NewInfraDiskIOLatency()

	t.Run("both metrics produce two panels in title order", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{
						Name:   "node_disk_io_time_seconds_total",
						Labels: []string{"instance", "device"},
					},
					Type: inventory.MetricTypeCounter,
				},
				{
					Descriptor: inventory.MetricDescriptor{
						Name:   "node_disk_io_time_weighted_seconds_total",
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
		// Alphabetical title order: "Disk IO busy fraction" < "Disk IO weighted time"
		if panels[0].Title != "Disk IO busy fraction" {
			t.Errorf("expected first panel title %q, got %q", "Disk IO busy fraction", panels[0].Title)
		}
		if panels[1].Title != "Disk IO weighted time" {
			t.Errorf("expected second panel title %q, got %q", "Disk IO weighted time", panels[1].Title)
		}
		// Verify units
		if panels[0].Unit != "percentunit" {
			t.Errorf("panel %q: expected unit percentunit, got %q", panels[0].Title, panels[0].Unit)
		}
		if panels[1].Unit != "s" {
			t.Errorf("panel %q: expected unit s, got %q", panels[1].Title, panels[1].Unit)
		}
		for _, p := range panels {
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

	t.Run("only io_time metric produces one panel", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{
						Name:   "node_disk_io_time_seconds_total",
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
		if panels[0].Title != "Disk IO busy fraction" {
			t.Errorf("expected panel title %q, got %q", "Disk IO busy fraction", panels[0].Title)
		}
	})

	t.Run("neither metric produces zero panels", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{Name: "node_disk_reads_completed_total"},
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
					Descriptor: inventory.MetricDescriptor{Name: "node_disk_io_time_seconds_total"},
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
