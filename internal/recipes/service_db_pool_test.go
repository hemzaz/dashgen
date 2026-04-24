package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestServiceDBPool_Match(t *testing.T) {
	r := NewServiceDBPool()

	tests := []struct {
		name   string
		metric ClassifiedMetricView
		want   bool
	}{
		{
			name: "go_sql in-use gauge matches",
			metric: ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "go_sql_stats_connections_in_use"},
				Type:       inventory.MetricTypeGauge,
			},
			want: true,
		},
		{
			name: "go_sql in-use unknown type matches",
			metric: ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "go_sql_stats_connections_in_use"},
				Type:       inventory.MetricTypeUnknown,
			},
			want: true,
		},
		{
			name: "pgxpool acquired gauge matches",
			metric: ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "pgxpool_acquired_connections"},
				Type:       inventory.MetricTypeGauge,
			},
			want: true,
		},
		{
			name: "pgxpool acquired unknown type matches",
			metric: ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "pgxpool_acquired_connections"},
				Type:       inventory.MetricTypeUnknown,
			},
			want: true,
		},
		{
			name: "go_sql in-use counter type does not match",
			metric: ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "go_sql_stats_connections_in_use"},
				Type:       inventory.MetricTypeCounter,
			},
			want: false,
		},
		{
			name: "unrelated gauge does not match",
			metric: ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "go_sql_stats_connections_open"},
				Type:       inventory.MetricTypeGauge,
			},
			want: false,
		},
		{
			name: "go_sql max alone does not match",
			metric: ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "go_sql_stats_connections_max"},
				Type:       inventory.MetricTypeGauge,
			},
			want: false,
		},
		{
			name: "pgxpool max alone does not match",
			metric: ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "pgxpool_max_connections"},
				Type:       inventory.MetricTypeGauge,
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

func TestServiceDBPool_BuildPanels(t *testing.T) {
	r := NewServiceDBPool()

	t.Run("go_sql in-use + max yields 1 panel with ratio expression", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{Name: "go_sql_stats_connections_in_use", Labels: []string{"instance", "job"}},
					Type:       inventory.MetricTypeGauge,
				},
				{
					Descriptor: inventory.MetricDescriptor{Name: "go_sql_stats_connections_max", Labels: []string{"instance", "job"}},
					Type:       inventory.MetricTypeGauge,
				},
			},
		}
		panels := r.BuildPanels(inv, profiles.ProfileService)
		if len(panels) != 1 {
			t.Fatalf("expected 1 panel, got %d", len(panels))
		}
		panel := panels[0]
		if panel.Title != "DB pool utilization: go_sql" {
			t.Errorf("unexpected title %q", panel.Title)
		}
		if panel.Unit != "percentunit" {
			t.Errorf("unexpected unit %q", panel.Unit)
		}
		if panel.Confidence != 0.80 {
			t.Errorf("unexpected confidence %v", panel.Confidence)
		}
		if len(panel.Queries) != 1 {
			t.Fatalf("expected 1 query, got %d", len(panel.Queries))
		}
		expr := panel.Queries[0].Expr
		if !strings.Contains(expr, "go_sql_stats_connections_in_use") || !strings.Contains(expr, "go_sql_stats_connections_max") {
			t.Errorf("expression %q does not reference both metric names", expr)
		}
	})

	t.Run("pgxpool acquired + max yields 1 panel", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{Name: "pgxpool_acquired_connections", Labels: []string{"instance", "job"}},
					Type:       inventory.MetricTypeGauge,
				},
				{
					Descriptor: inventory.MetricDescriptor{Name: "pgxpool_max_connections", Labels: []string{"instance", "job"}},
					Type:       inventory.MetricTypeGauge,
				},
			},
		}
		panels := r.BuildPanels(inv, profiles.ProfileService)
		if len(panels) != 1 {
			t.Fatalf("expected 1 panel, got %d", len(panels))
		}
		panel := panels[0]
		if panel.Title != "DB pool utilization: pgxpool" {
			t.Errorf("unexpected title %q", panel.Title)
		}
		expr := panel.Queries[0].Expr
		if !strings.Contains(expr, "pgxpool_acquired_connections") || !strings.Contains(expr, "pgxpool_max_connections") {
			t.Errorf("expression %q does not reference both metric names", expr)
		}
	})

	t.Run("only in-use present yields 0 panels", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{Name: "go_sql_stats_connections_in_use"},
					Type:       inventory.MetricTypeGauge,
				},
			},
		}
		panels := r.BuildPanels(inv, profiles.ProfileService)
		if len(panels) != 0 {
			t.Errorf("expected 0 panels when max absent, got %d", len(panels))
		}
	})

	t.Run("both pairs present yields 2 panels in deterministic order", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{Name: "go_sql_stats_connections_in_use"},
					Type:       inventory.MetricTypeGauge,
				},
				{
					Descriptor: inventory.MetricDescriptor{Name: "go_sql_stats_connections_max"},
					Type:       inventory.MetricTypeGauge,
				},
				{
					Descriptor: inventory.MetricDescriptor{Name: "pgxpool_acquired_connections"},
					Type:       inventory.MetricTypeGauge,
				},
				{
					Descriptor: inventory.MetricDescriptor{Name: "pgxpool_max_connections"},
					Type:       inventory.MetricTypeGauge,
				},
			},
		}
		panels := r.BuildPanels(inv, profiles.ProfileService)
		if len(panels) != 2 {
			t.Fatalf("expected 2 panels, got %d", len(panels))
		}
		// Deterministic order: sorted by title alphabetically.
		// "DB pool utilization: go_sql" < "DB pool utilization: pgxpool"
		if panels[0].Title != "DB pool utilization: go_sql" {
			t.Errorf("expected first panel title %q, got %q", "DB pool utilization: go_sql", panels[0].Title)
		}
		if panels[1].Title != "DB pool utilization: pgxpool" {
			t.Errorf("expected second panel title %q, got %q", "DB pool utilization: pgxpool", panels[1].Title)
		}
	})

	t.Run("wrong profile yields 0 panels", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{Name: "go_sql_stats_connections_in_use"},
					Type:       inventory.MetricTypeGauge,
				},
				{
					Descriptor: inventory.MetricDescriptor{Name: "go_sql_stats_connections_max"},
					Type:       inventory.MetricTypeGauge,
				},
			},
		}
		panels := r.BuildPanels(inv, profiles.ProfileInfra)
		if len(panels) != 0 {
			t.Errorf("expected 0 panels for non-service profile, got %d", len(panels))
		}
	})
}
