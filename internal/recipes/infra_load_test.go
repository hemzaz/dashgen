package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestInfraLoad_Match(t *testing.T) {
	r := NewInfraLoad()

	tests := []struct {
		name    string
		metric  ClassifiedMetricView
		wantHit bool
	}{
		{
			name: "node_load1 gauge matches",
			metric: ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "node_load1"},
				Type:       inventory.MetricTypeGauge,
			},
			wantHit: true,
		},
		{
			name: "node_load5 gauge matches",
			metric: ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "node_load5"},
				Type:       inventory.MetricTypeGauge,
			},
			wantHit: true,
		},
		{
			name: "node_load15 gauge matches",
			metric: ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "node_load15"},
				Type:       inventory.MetricTypeGauge,
			},
			wantHit: true,
		},
		{
			name: "bare node_load does not match",
			metric: ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "node_load"},
				Type:       inventory.MetricTypeGauge,
			},
			wantHit: false,
		},
		{
			name: "node_memory_MemAvailable_bytes does not match",
			metric: ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "node_memory_MemAvailable_bytes"},
				Type:       inventory.MetricTypeGauge,
			},
			wantHit: false,
		},
		{
			name: "counter with load name does not match",
			metric: ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "node_load1"},
				Type:       inventory.MetricTypeCounter,
			},
			wantHit: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := r.Match(tc.metric)
			if got != tc.wantHit {
				t.Errorf("Match(%q, type=%q) = %v, want %v",
					tc.metric.Descriptor.Name, tc.metric.Type, got, tc.wantHit)
			}
		})
	}
}

func TestInfraLoad_BuildPanels(t *testing.T) {
	r := NewInfraLoad()
	inv := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{
			{
				Descriptor: inventory.MetricDescriptor{
					Name:   "node_load1",
					Labels: []string{"instance"},
				},
				Type: inventory.MetricTypeGauge,
			},
			{
				Descriptor: inventory.MetricDescriptor{
					Name:   "node_load5",
					Labels: []string{"instance"},
				},
				Type: inventory.MetricTypeGauge,
			},
		},
	}

	panels := r.BuildPanels(inv, profiles.ProfileInfra)
	if len(panels) != 2 {
		t.Fatalf("expected 2 panels, got %d", len(panels))
	}

	// Collect titles and exprs for assertion.
	titles := make([]string, len(panels))
	exprs := make([]string, len(panels))
	for i, p := range panels {
		titles[i] = p.Title
		if len(p.Queries) == 0 {
			t.Fatalf("panel %d has no queries", i)
		}
		exprs[i] = p.Queries[0].Expr
	}

	// Panel for node_load1 must mention "1m" and reference the metric.
	found1m := false
	found5m := false
	for i := range panels {
		if strings.Contains(titles[i], "1m") {
			found1m = true
			if !strings.Contains(exprs[i], "avg by (instance)") {
				t.Errorf("panel %q: expected 'avg by (instance)' in expr, got %q", titles[i], exprs[i])
			}
			if !strings.Contains(exprs[i], "node_load1") {
				t.Errorf("panel %q: expected 'node_load1' in expr, got %q", titles[i], exprs[i])
			}
		}
		if strings.Contains(titles[i], "5m") {
			found5m = true
			if !strings.Contains(exprs[i], "avg by (instance)") {
				t.Errorf("panel %q: expected 'avg by (instance)' in expr, got %q", titles[i], exprs[i])
			}
			if !strings.Contains(exprs[i], "node_load5") {
				t.Errorf("panel %q: expected 'node_load5' in expr, got %q", titles[i], exprs[i])
			}
		}
	}
	if !found1m {
		t.Errorf("no panel title containing '1m' found; titles: %v", titles)
	}
	if !found5m {
		t.Errorf("no panel title containing '5m' found; titles: %v", titles)
	}
}

func TestInfraLoad_BuildPanels_SkipsOtherProfiles(t *testing.T) {
	r := NewInfraLoad()
	inv := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{{
			Descriptor: inventory.MetricDescriptor{Name: "node_load1"},
			Type:       inventory.MetricTypeGauge,
		}},
	}
	for _, p := range []profiles.Profile{profiles.ProfileService, profiles.ProfileK8s} {
		got := r.BuildPanels(inv, p)
		if len(got) != 0 {
			t.Errorf("profile %q: expected no panels, got %d", p, len(got))
		}
	}
}
