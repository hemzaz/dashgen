package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestServiceGRPCErrors_Match(t *testing.T) {
	r := NewServiceGRPCErrors()
	cases := []struct {
		name    string
		labels  []string
		traits  []string
		wantHit bool
	}{
		{
			name:    "counter_with_grpc_trait_and_grpc_code_matches",
			labels:  []string{"grpc_code", "grpc_method"},
			traits:  []string{"service_grpc"},
			wantHit: true,
		},
		{
			// Even if the trait fires (because of grpc_method/service), no
			// grpc_code label means we can't build the !="OK" filter.
			// SPECS Rule 5: prefer omission.
			name:    "counter_with_grpc_trait_but_no_grpc_code_rejected",
			labels:  []string{"grpc_method", "grpc_service"},
			traits:  []string{"service_grpc"},
			wantHit: false,
		},
		{
			name:    "http_code_label_is_not_grpc_code",
			labels:  []string{"code"}, // promhttp label, not grpc
			traits:  []string{"service_http"},
			wantHit: false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			m := ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "grpc_server_handled_total", Labels: tc.labels},
				Type:       inventory.MetricTypeCounter,
				Traits:     tc.traits,
			}
			if got := r.Match(m); got != tc.wantHit {
				t.Fatalf("Match(labels=%v, traits=%v) = %v, want %v", tc.labels, tc.traits, got, tc.wantHit)
			}
		})
	}
}

func TestServiceGRPCErrors_BuildPanels(t *testing.T) {
	r := NewServiceGRPCErrors()
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
	if !strings.Contains(expr, `grpc_code!="OK"`) {
		t.Errorf("expected grpc_code!=\"OK\" filter, got %q", expr)
	}
	if !strings.Contains(expr, "sum by (grpc_method, grpc_service, instance, job)") {
		t.Errorf("expected grpc-native grouping, got %q", expr)
	}
}
