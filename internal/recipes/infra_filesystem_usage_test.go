package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestInfraFilesystemUsage_Match(t *testing.T) {
	r := NewInfraFilesystemUsage()

	tests := []struct {
		name   string
		metric ClassifiedMetricView
		want   bool
	}{
		{
			name: "matches node_filesystem_size_bytes gauge",
			metric: ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "node_filesystem_size_bytes"},
				Type:       inventory.MetricTypeGauge,
			},
			want: true,
		},
		{
			name: "matches node_filesystem_size_bytes unknown type",
			metric: ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "node_filesystem_size_bytes"},
				Type:       inventory.MetricTypeUnknown,
			},
			want: true,
		},
		{
			name: "does not match node_filesystem_avail_bytes (avail, not size)",
			metric: ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "node_filesystem_avail_bytes"},
				Type:       inventory.MetricTypeGauge,
			},
			want: false,
		},
		{
			name: "does not match node_filesystem_files (look-alike: inode count)",
			metric: ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "node_filesystem_files"},
				Type:       inventory.MetricTypeGauge,
			},
			want: false,
		},
		{
			name: "does not match kubelet_volume_stats_available_bytes (k8s look-alike)",
			metric: ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "kubelet_volume_stats_available_bytes"},
				Type:       inventory.MetricTypeGauge,
			},
			want: false,
		},
		{
			name: "does not match counter type with size_bytes name",
			metric: ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "node_filesystem_size_bytes"},
				Type:       inventory.MetricTypeCounter,
			},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := r.Match(tc.metric)
			if got != tc.want {
				t.Errorf("Match() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestInfraFilesystemUsage_BuildPanels(t *testing.T) {
	r := NewInfraFilesystemUsage()

	bothMetrics := []ClassifiedMetricView{
		{
			Descriptor: inventory.MetricDescriptor{
				Name:   "node_filesystem_size_bytes",
				Labels: []string{"device", "fstype", "instance", "mountpoint"},
			},
			Type: inventory.MetricTypeGauge,
		},
		{
			Descriptor: inventory.MetricDescriptor{
				Name:   "node_filesystem_avail_bytes",
				Labels: []string{"device", "fstype", "instance", "mountpoint"},
			},
			Type: inventory.MetricTypeGauge,
		},
	}

	t.Run("both metrics present yields 1 panel with ratio expression", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{Metrics: bothMetrics}
		panels := r.BuildPanels(inv, profiles.ProfileInfra)
		if len(panels) != 1 {
			t.Fatalf("expected 1 panel, got %d", len(panels))
		}
		p := panels[0]
		if p.Title != "Filesystem used ratio" {
			t.Errorf("unexpected panel title %q", p.Title)
		}
		if p.Unit != "percentunit" {
			t.Errorf("unexpected unit %q", p.Unit)
		}
		if len(p.Queries) != 1 {
			t.Fatalf("expected 1 query candidate, got %d", len(p.Queries))
		}
		expr := p.Queries[0].Expr
		if !strings.Contains(expr, "node_filesystem_size_bytes") {
			t.Errorf("expression missing node_filesystem_size_bytes: %q", expr)
		}
		if !strings.Contains(expr, "node_filesystem_avail_bytes") {
			t.Errorf("expression missing node_filesystem_avail_bytes: %q", expr)
		}
	})

	t.Run("missing avail metric yields 0 panels", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{
						Name:   "node_filesystem_size_bytes",
						Labels: []string{"device", "fstype", "instance", "mountpoint"},
					},
					Type: inventory.MetricTypeGauge,
				},
			},
		}
		panels := r.BuildPanels(inv, profiles.ProfileInfra)
		if len(panels) != 0 {
			t.Errorf("expected 0 panels when avail metric missing, got %d", len(panels))
		}
	})

	t.Run("wrong profile yields 0 panels", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{Metrics: bothMetrics}
		panels := r.BuildPanels(inv, profiles.ProfileService)
		if len(panels) != 0 {
			t.Errorf("expected 0 panels for non-infra profile, got %d", len(panels))
		}
	})
}
