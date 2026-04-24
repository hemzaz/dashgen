package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestServiceHTTPErrors_Match(t *testing.T) {
	r := NewServiceHTTPErrors()
	pos := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "http_requests_total", Labels: []string{"status_code"}},
		Type:       inventory.MetricTypeCounter,
	}
	if !r.Match(pos) {
		t.Errorf("expected match with status_code label")
	}
	neg := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "http_requests_total", Labels: []string{"method"}},
		Type:       inventory.MetricTypeCounter,
	}
	if r.Match(neg) {
		t.Errorf("expected no match without status_code label")
	}
}

func TestServiceHTTPErrors_BuildPanels(t *testing.T) {
	r := NewServiceHTTPErrors()
	inv := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{{
			Descriptor: inventory.MetricDescriptor{
				Name:   "http_requests_total",
				Labels: []string{"job", "status_code"},
			},
			Type: inventory.MetricTypeCounter,
		}},
	}
	panels := r.BuildPanels(inv, profiles.ProfileService)
	if len(panels) != 1 {
		t.Fatalf("expected 1 panel, got %d", len(panels))
	}
	expr := panels[0].Queries[0].Expr
	if !strings.Contains(expr, `status_code=~"5.."`) {
		t.Errorf("expected 5xx filter, got %q", expr)
	}
	if !strings.Contains(expr, "[5m]") {
		t.Errorf("expected 5m rate window, got %q", expr)
	}
	if !strings.Contains(expr, "rate(http_requests_total{") {
		t.Errorf("expected rate() over http_requests_total with matcher, got %q", expr)
	}
}
