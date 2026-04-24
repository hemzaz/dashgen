package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestServiceHTTPErrors_Match(t *testing.T) {
	r := NewServiceHTTPErrors()
	cases := []struct {
		name    string
		labels  []string
		wantHit bool
	}{
		{name: "status_code_matches", labels: []string{"status_code"}, wantHit: true},
		{name: "code_matches_promhttp_convention", labels: []string{"code", "handler"}, wantHit: true},
		{
			// Bare "status" is ambiguous: in alertmanager's
			// alerts_received_total it holds values like "firing" —
			// filtering to 5.. would always return empty and emit a
			// dead panel. The recipe must NOT accept it.
			name:   "bare_status_is_rejected",
			labels: []string{"status"}, wantHit: false,
		},
		{name: "method_only_without_status_label_is_rejected", labels: []string{"method"}, wantHit: false},
		{name: "empty_labels_rejected", labels: nil, wantHit: false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			m := ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "http_requests_total", Labels: tc.labels},
				Type:       inventory.MetricTypeCounter,
			}
			if got := r.Match(m); got != tc.wantHit {
				t.Fatalf("Match(labels=%v) = %v, want %v", tc.labels, got, tc.wantHit)
			}
		})
	}
}

func TestServiceHTTPErrors_QueryUsesDiscoveredStatusLabel(t *testing.T) {
	r := NewServiceHTTPErrors()
	inv := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{{
			Descriptor: inventory.MetricDescriptor{
				Name:   "prometheus_http_requests_total",
				Labels: []string{"code", "handler", "instance", "job"},
			},
			Type: inventory.MetricTypeCounter,
		}},
	}
	panels := r.BuildPanels(inv, profiles.ProfileService)
	if len(panels) != 1 {
		t.Fatalf("expected 1 panel, got %d", len(panels))
	}
	expr := panels[0].Queries[0].Expr
	if !strings.Contains(expr, `code=~"5.."`) {
		t.Errorf("expected code-based 5xx filter, got %q", expr)
	}
	// Must not emit a status_code filter when the metric only has `code`.
	if strings.Contains(expr, `status_code=~"5.."`) {
		t.Errorf("query should not reference status_code when metric has only `code`, got %q", expr)
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
