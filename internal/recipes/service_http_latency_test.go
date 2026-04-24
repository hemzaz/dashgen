package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestServiceHTTPLatency_Match(t *testing.T) {
	r := NewServiceHTTPLatency()
	pos := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "http_request_duration_seconds_bucket"},
		Type:       inventory.MetricTypeHistogram,
		Traits:     []string{"latency_histogram"},
	}
	if !r.Match(pos) {
		t.Errorf("expected match on histogram with latency_histogram trait")
	}
	neg := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "generic_bucket"},
		Type:       inventory.MetricTypeHistogram,
	}
	if r.Match(neg) {
		t.Errorf("expected no match without latency_histogram trait")
	}
}

func TestServiceHTTPLatency_BuildPanels(t *testing.T) {
	r := NewServiceHTTPLatency()
	inv := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{{
			Descriptor: inventory.MetricDescriptor{
				Name:   "http_request_duration_seconds_bucket",
				Labels: []string{"job", "le", "route"},
			},
			Type:   inventory.MetricTypeHistogram,
			Traits: []string{"latency_histogram"},
		}},
	}
	panels := r.BuildPanels(inv, profiles.ProfileService)
	if len(panels) != 1 {
		t.Fatalf("expected 1 panel, got %d", len(panels))
	}
	if len(panels[0].Queries) != 3 {
		t.Fatalf("expected 3 quantile queries (p50/p95/p99), got %d", len(panels[0].Queries))
	}
	// Verify each quantile appears.
	wants := []string{"0.50", "0.95", "0.99"}
	for i, want := range wants {
		expr := panels[0].Queries[i].Expr
		if !strings.Contains(expr, "histogram_quantile("+want) {
			t.Errorf("query %d expected quantile %s, got %q", i, want, expr)
		}
		if !strings.Contains(expr, "le") {
			t.Errorf("query %d must group by le, got %q", i, expr)
		}
	}
}
