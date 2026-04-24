package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestServiceGRPCRate_Match(t *testing.T) {
	r := NewServiceGRPCRate()
	cases := []struct {
		name    string
		traits  []string
		typ     inventory.MetricType
		wantHit bool
	}{
		{name: "counter_with_grpc_trait_matches", traits: []string{"service_grpc"}, typ: inventory.MetricTypeCounter, wantHit: true},
		{name: "counter_without_grpc_trait_rejected", traits: nil, typ: inventory.MetricTypeCounter, wantHit: false},
		{name: "http_trait_alone_does_not_match_grpc_recipe", traits: []string{"service_http"}, typ: inventory.MetricTypeCounter, wantHit: false},
		{name: "gauge_with_grpc_trait_rejected_not_a_counter", traits: []string{"service_grpc"}, typ: inventory.MetricTypeGauge, wantHit: false},
		{name: "histogram_with_grpc_trait_rejected", traits: []string{"service_grpc"}, typ: inventory.MetricTypeHistogram, wantHit: false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			m := ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "grpc_server_handled_total"},
				Type:       tc.typ,
				Traits:     tc.traits,
			}
			if got := r.Match(m); got != tc.wantHit {
				t.Fatalf("Match(traits=%v, type=%s) = %v, want %v", tc.traits, tc.typ, got, tc.wantHit)
			}
		})
	}
}

func TestServiceGRPCRate_BuildPanels(t *testing.T) {
	r := NewServiceGRPCRate()
	inv := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{{
			Descriptor: inventory.MetricDescriptor{
				Name:   "grpc_server_handled_total",
				Labels: []string{"grpc_code", "grpc_method", "grpc_service", "instance", "job"},
			},
			Type:   inventory.MetricTypeCounter,
			Traits: []string{"service_grpc"},
		}},
	}
	panels := r.BuildPanels(inv, profiles.ProfileService)
	if len(panels) != 1 {
		t.Fatalf("expected 1 panel, got %d", len(panels))
	}
	expr := panels[0].Queries[0].Expr
	if !strings.Contains(expr, "sum by (grpc_method, grpc_service, instance, job)") {
		t.Errorf("expected grouping by grpc_method/grpc_service/instance/job, got %q", expr)
	}
	if !strings.Contains(expr, "rate(grpc_server_handled_total[5m])") {
		t.Errorf("expected rate over 5m on grpc_server_handled_total, got %q", expr)
	}
	if panels[0].Unit != "reqps" {
		t.Errorf("expected reqps unit, got %q", panels[0].Unit)
	}
}
