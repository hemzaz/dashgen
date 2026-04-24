package recipes

import (
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestInfraFileDescriptors_Match(t *testing.T) {
	r := NewInfraFileDescriptors()

	tests := []struct {
		name   string
		metric ClassifiedMetricView
		want   bool
	}{
		{
			name: "process_open_fds matches",
			metric: ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "process_open_fds"},
				Type:       inventory.MetricTypeGauge,
			},
			want: true,
		},
		{
			name: "process_max_fds alone does not match",
			metric: ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "process_max_fds"},
				Type:       inventory.MetricTypeGauge,
			},
			want: false,
		},
		{
			name: "unrelated gauge does not match",
			metric: ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "node_filefd_allocated"},
				Type:       inventory.MetricTypeGauge,
			},
			want: false,
		},
		{
			name: "counter type with process_open_fds does not match",
			metric: ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "process_open_fds"},
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

func TestInfraFileDescriptors_BuildPanels(t *testing.T) {
	r := NewInfraFileDescriptors()

	t.Run("both metrics present yields 1 panel", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{Name: "process_open_fds", Labels: []string{"instance", "job"}},
					Type:       inventory.MetricTypeGauge,
				},
				{
					Descriptor: inventory.MetricDescriptor{Name: "process_max_fds", Labels: []string{"instance", "job"}},
					Type:       inventory.MetricTypeGauge,
				},
			},
		}
		panels := r.BuildPanels(inv, profiles.ProfileInfra)
		if len(panels) != 1 {
			t.Fatalf("expected 1 panel, got %d", len(panels))
		}
		panel := panels[0]
		if panel.Title != "Open fds vs max" {
			t.Errorf("unexpected title %q", panel.Title)
		}
		if panel.Unit != "percentunit" {
			t.Errorf("unexpected unit %q", panel.Unit)
		}
		if len(panel.Queries) != 1 {
			t.Fatalf("expected 1 query, got %d", len(panel.Queries))
		}
		expr := panel.Queries[0].Expr
		if expr != "process_open_fds / process_max_fds" {
			t.Errorf("unexpected expr %q", expr)
		}
		if panel.Confidence != 0.90 {
			t.Errorf("unexpected confidence %v", panel.Confidence)
		}
	})

	t.Run("only process_open_fds yields 0 panels", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{Name: "process_open_fds", Labels: []string{"instance", "job"}},
					Type:       inventory.MetricTypeGauge,
				},
			},
		}
		panels := r.BuildPanels(inv, profiles.ProfileInfra)
		if len(panels) != 0 {
			t.Errorf("expected 0 panels when process_max_fds absent, got %d", len(panels))
		}
	})

	t.Run("wrong profile yields 0 panels", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{Name: "process_open_fds"},
					Type:       inventory.MetricTypeGauge,
				},
				{
					Descriptor: inventory.MetricDescriptor{Name: "process_max_fds"},
					Type:       inventory.MetricTypeGauge,
				},
			},
		}
		panels := r.BuildPanels(inv, profiles.ProfileService)
		if len(panels) != 0 {
			t.Errorf("expected 0 panels for non-infra profile, got %d", len(panels))
		}
	})
}
