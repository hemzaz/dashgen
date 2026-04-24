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
		Traits:     []string{"latency_histogram", "service_http"},
	}
	if !r.Match(pos) {
		t.Errorf("expected match on histogram with both latency_histogram and service_http traits")
	}
	missingLatency := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "generic_bucket"},
		Type:       inventory.MetricTypeHistogram,
		Traits:     []string{"service_http"},
	}
	if r.Match(missingLatency) {
		t.Errorf("expected no match without latency_histogram trait")
	}
	// Latency histogram without the HTTP-request signal — e.g., an
	// alertmanager notification latency histogram: has `le`, name contains
	// "latency", but no HTTP-shape labels. The recipe must refuse it so we
	// don't emit a dead HTTP panel from an internal operation histogram.
	missingHTTP := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "notification_latency_seconds_bucket"},
		Type:       inventory.MetricTypeHistogram,
		Traits:     []string{"latency_histogram"},
	}
	if r.Match(missingHTTP) {
		t.Errorf("expected no match without service_http trait")
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
			Traits: []string{"latency_histogram", "service_http"},
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
