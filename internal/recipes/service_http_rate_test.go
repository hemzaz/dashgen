package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestServiceHTTPRate_Match(t *testing.T) {
	r := NewServiceHTTPRate()
	// Positive: counter with service_http trait.
	pos := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "http_requests_total", Labels: []string{"method"}},
		Type:       inventory.MetricTypeCounter,
		Traits:     []string{"service_http"},
	}
	if !r.Match(pos) {
		t.Errorf("expected match on counter with service_http trait")
	}
	// Negative: counter without the trait.
	neg := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "kafka_messages_total"},
		Type:       inventory.MetricTypeCounter,
	}
	if r.Match(neg) {
		t.Errorf("expected no match on plain counter")
	}
}

func TestServiceHTTPRate_BuildPanels(t *testing.T) {
	r := NewServiceHTTPRate()
	inv := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{{
			Descriptor: inventory.MetricDescriptor{
				Name:   "http_requests_total",
				Labels: []string{"job", "method", "route"},
			},
			Type:   inventory.MetricTypeCounter,
			Traits: []string{"service_http"},
		}},
	}
	panels := r.BuildPanels(inv, profiles.ProfileService)
	if len(panels) != 1 {
		t.Fatalf("expected 1 panel, got %d", len(panels))
	}
	expr := panels[0].Queries[0].Expr
	if !strings.Contains(expr, "rate(http_requests_total[5m])") {
		t.Errorf("expected rate() with 5m window, got %q", expr)
	}
	if !strings.Contains(expr, "sum by (job, route)") {
		t.Errorf("expected sum by (job, route), got %q", expr)
	}
}

func TestServiceHTTPRate_NoPanelsOffProfile(t *testing.T) {
	r := NewServiceHTTPRate()
	inv := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{{
			Descriptor: inventory.MetricDescriptor{Name: "http_requests_total"},
			Type:       inventory.MetricTypeCounter,
			Traits:     []string{"service_http"},
		}},
	}
	if got := r.BuildPanels(inv, profiles.ProfileInfra); len(got) != 0 {
		t.Errorf("expected zero panels on non-service profile, got %d", len(got))
	}
}
