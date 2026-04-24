package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestServiceClientHTTP_Match(t *testing.T) {
	r := NewServiceClientHTTP()
	cases := []struct {
		name    string
		metric  string
		labels  []string
		wantHit bool
	}{
		{
			name:    "http_client_requests_total_with_code_and_host",
			metric:  "http_client_requests_total",
			labels:  []string{"code", "host"},
			wantHit: true,
		},
		{
			name:    "some_service_client_calls_total_with_status_code_and_upstream",
			metric:  "some_service_client_calls_total",
			labels:  []string{"status_code", "upstream"},
			wantHit: true,
		},
		{
			// No "client" in name — belongs to service_http_rate or
			// service_http_errors, not this recipe.
			name:    "http_requests_total_without_client_in_name",
			metric:  "http_requests_total",
			labels:  []string{"code", "handler"},
			wantHit: false,
		},
		{
			// Has "client" but no HTTP-status label — not HTTP-shaped.
			name:    "cache_client_hits_total_no_status_label",
			metric:  "cache_client_hits_total",
			labels:  []string{"cache_name", "result"},
			wantHit: false,
		},
		{
			// Has "_client_" in name but no status_code or code label.
			name:    "grpc_client_calls_total_no_http_status",
			metric:  "grpc_client_calls_total",
			labels:  []string{"grpc_method", "grpc_service"},
			wantHit: false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			m := ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: tc.metric, Labels: tc.labels},
				Type:       inventory.MetricTypeCounter,
			}
			if got := r.Match(m); got != tc.wantHit {
				t.Fatalf("Match(%q, labels=%v) = %v, want %v", tc.metric, tc.labels, got, tc.wantHit)
			}
		})
	}
}

func TestServiceClientHTTP_BuildPanels(t *testing.T) {
	r := NewServiceClientHTTP()
	inv := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{{
			Descriptor: inventory.MetricDescriptor{
				Name:   "http_client_requests_total",
				Labels: []string{"job", "host", "status_code"},
			},
			Type: inventory.MetricTypeCounter,
		}},
	}
	panels := r.BuildPanels(inv, profiles.ProfileService)
	if len(panels) != 1 {
		t.Fatalf("expected 1 panel, got %d", len(panels))
	}
	expr := panels[0].Queries[0].Expr
	if !strings.Contains(expr, "rate(http_client_requests_total[5m])") {
		t.Errorf("expected rate() with 5m window, got %q", expr)
	}
	if !strings.Contains(expr, "sum by (") {
		t.Errorf("expected sum by aggregation, got %q", expr)
	}
	if panels[0].Unit != "reqps" {
		t.Errorf("expected unit reqps, got %q", panels[0].Unit)
	}
	if !strings.HasPrefix(panels[0].Title, "Outbound HTTP call rate:") {
		t.Errorf("unexpected title %q", panels[0].Title)
	}
}

func TestServiceClientHTTP_NoPanelsOffProfile(t *testing.T) {
	r := NewServiceClientHTTP()
	inv := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{{
			Descriptor: inventory.MetricDescriptor{
				Name:   "http_client_requests_total",
				Labels: []string{"status_code", "host"},
			},
			Type: inventory.MetricTypeCounter,
		}},
	}
	if got := r.BuildPanels(inv, profiles.ProfileInfra); len(got) != 0 {
		t.Errorf("expected zero panels on non-service profile, got %d", len(got))
	}
}
