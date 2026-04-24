package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestInfraNTPOffset_Match(t *testing.T) {
	r := NewInfraNTPOffset()
	cases := []struct {
		name    string
		metric  string
		typ     inventory.MetricType
		wantHit bool
	}{
		{
			name:    "exact_name_gauge_matches",
			metric:  "node_timex_offset_seconds",
			typ:     inventory.MetricTypeGauge,
			wantHit: true,
		},
		{
			name:    "node_ntp_stratum_rejected",
			metric:  "node_ntp_stratum",
			typ:     inventory.MetricTypeGauge,
			wantHit: false,
		},
		{
			name:    "gauge_with_offset_substring_rejected",
			metric:  "some_offset_bytes",
			typ:     inventory.MetricTypeGauge,
			wantHit: false,
		},
		{
			name:    "counter_with_exact_name_rejected",
			metric:  "node_timex_offset_seconds",
			typ:     inventory.MetricTypeCounter,
			wantHit: false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			m := ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: tc.metric},
				Type:       tc.typ,
			}
			if got := r.Match(m); got != tc.wantHit {
				t.Fatalf("Match(name=%q, type=%s) = %v, want %v", tc.metric, tc.typ, got, tc.wantHit)
			}
		})
	}
}

func TestInfraNTPOffset_BuildPanels(t *testing.T) {
	r := NewInfraNTPOffset()
	inv := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{{
			Descriptor: inventory.MetricDescriptor{
				Name:   "node_timex_offset_seconds",
				Labels: []string{"instance"},
			},
			Type: inventory.MetricTypeGauge,
		}},
	}

	t.Run("matching_metric_produces_one_panel", func(t *testing.T) {
		panels := r.BuildPanels(inv, profiles.ProfileInfra)
		if len(panels) != 1 {
			t.Fatalf("expected 1 panel, got %d", len(panels))
		}
		if len(panels[0].Queries) != 1 {
			t.Fatalf("expected 1 query, got %d", len(panels[0].Queries))
		}
		expr := panels[0].Queries[0].Expr
		if !strings.Contains(expr, "max by (instance) (node_timex_offset_seconds)") {
			t.Errorf("expected max by (instance) (node_timex_offset_seconds) in expression, got %q", expr)
		}
		if panels[0].Queries[0].Unit != "s" {
			t.Errorf("expected unit %q, got %q", "s", panels[0].Queries[0].Unit)
		}
		if panels[0].Title != "NTP offset" {
			t.Errorf("expected title %q, got %q", "NTP offset", panels[0].Title)
		}
	})

	t.Run("wrong_profile_produces_no_panels", func(t *testing.T) {
		panels := r.BuildPanels(inv, profiles.ProfileService)
		if len(panels) != 0 {
			t.Fatalf("expected 0 panels for non-infra profile, got %d", len(panels))
		}
	})
}
