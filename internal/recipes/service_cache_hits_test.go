package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestServiceCacheHits_Match(t *testing.T) {
	r := NewServiceCacheHits()

	tests := []struct {
		name      string
		metric    string
		wantMatch bool
	}{
		{
			name:      "hits counter matches",
			metric:    "http_cache_hits_total",
			wantMatch: true,
		},
		{
			name:      "misses counter alone does not match",
			metric:    "http_cache_misses_total",
			wantMatch: false,
		},
		{
			name:      "unrelated counter does not match",
			metric:    "something_else_total",
			wantMatch: false,
		},
		{
			name:      "redis hits counter matches",
			metric:    "redis_cache_hits_total",
			wantMatch: true,
		},
		{
			name:      "metric containing cache but wrong suffix does not match",
			metric:    "cache_evictions_total",
			wantMatch: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: tc.metric},
				Type:       inventory.MetricTypeCounter,
			}
			got := r.Match(m)
			if got != tc.wantMatch {
				t.Errorf("Match(%q) = %v, want %v", tc.metric, got, tc.wantMatch)
			}
		})
	}
}

func TestServiceCacheHits_BuildPanels(t *testing.T) {
	r := NewServiceCacheHits()

	t.Run("both http pair present yields 1 panel with 3 queries", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{
						Name:   "http_cache_hits_total",
						Labels: []string{"instance", "job"},
					},
					Type: inventory.MetricTypeCounter,
				},
				{
					Descriptor: inventory.MetricDescriptor{
						Name:   "http_cache_misses_total",
						Labels: []string{"instance", "job"},
					},
					Type: inventory.MetricTypeCounter,
				},
			},
		}
		panels := r.BuildPanels(inv, profiles.ProfileService)
		if len(panels) != 1 {
			t.Fatalf("expected 1 panel, got %d", len(panels))
		}
		panel := panels[0]
		if len(panel.Queries) != 3 {
			t.Fatalf("expected 3 query candidates, got %d", len(panel.Queries))
		}

		// Query 0: hit rate — must reference hits metric.
		hitRate := panel.Queries[0].Expr
		if !strings.Contains(hitRate, "http_cache_hits_total") {
			t.Errorf("query[0] (hit rate) missing hits metric, got %q", hitRate)
		}

		// Query 1: miss rate — must reference misses metric.
		missRate := panel.Queries[1].Expr
		if !strings.Contains(missRate, "http_cache_misses_total") {
			t.Errorf("query[1] (miss rate) missing misses metric, got %q", missRate)
		}

		// Query 2: hit ratio — must reference both metrics and use '+'.
		ratio := panel.Queries[2].Expr
		if !strings.Contains(ratio, "http_cache_hits_total") {
			t.Errorf("query[2] (ratio) missing hits metric, got %q", ratio)
		}
		if !strings.Contains(ratio, "http_cache_misses_total") {
			t.Errorf("query[2] (ratio) missing misses metric, got %q", ratio)
		}
		if !strings.Contains(ratio, "+") {
			t.Errorf("query[2] (ratio) missing '+' operator, got %q", ratio)
		}
	})

	t.Run("only hits metric present yields 0 panels", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{
						Name:   "http_cache_hits_total",
						Labels: []string{"instance", "job"},
					},
					Type: inventory.MetricTypeCounter,
				},
			},
		}
		panels := r.BuildPanels(inv, profiles.ProfileService)
		if len(panels) != 0 {
			t.Errorf("expected 0 panels when misses metric absent, got %d", len(panels))
		}
	})

	t.Run("two distinct families yields 2 panels sorted by title", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{
						Name:   "redis_cache_hits_total",
						Labels: []string{"instance", "job"},
					},
					Type: inventory.MetricTypeCounter,
				},
				{
					Descriptor: inventory.MetricDescriptor{
						Name:   "http_cache_hits_total",
						Labels: []string{"instance", "job"},
					},
					Type: inventory.MetricTypeCounter,
				},
				{
					Descriptor: inventory.MetricDescriptor{
						Name:   "redis_cache_misses_total",
						Labels: []string{"instance", "job"},
					},
					Type: inventory.MetricTypeCounter,
				},
				{
					Descriptor: inventory.MetricDescriptor{
						Name:   "http_cache_misses_total",
						Labels: []string{"instance", "job"},
					},
					Type: inventory.MetricTypeCounter,
				},
			},
		}
		panels := r.BuildPanels(inv, profiles.ProfileService)
		if len(panels) != 2 {
			t.Fatalf("expected 2 panels for two families, got %d", len(panels))
		}
		// Panels sorted by prefix: "http" < "redis"
		if panels[0].Title != "Cache hit/miss: http" {
			t.Errorf("expected first panel title 'Cache hit/miss: http', got %q", panels[0].Title)
		}
		if panels[1].Title != "Cache hit/miss: redis" {
			t.Errorf("expected second panel title 'Cache hit/miss: redis', got %q", panels[1].Title)
		}
	})

	t.Run("wrong profile yields 0 panels", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{Name: "http_cache_hits_total"},
					Type:       inventory.MetricTypeCounter,
				},
				{
					Descriptor: inventory.MetricDescriptor{Name: "http_cache_misses_total"},
					Type:       inventory.MetricTypeCounter,
				},
			},
		}
		for _, prof := range []profiles.Profile{profiles.ProfileInfra, profiles.ProfileK8s} {
			if got := r.BuildPanels(inv, prof); len(got) != 0 {
				t.Errorf("expected 0 panels for profile %v, got %d", prof, len(got))
			}
		}
	})
}
