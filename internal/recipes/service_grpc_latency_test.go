package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestServiceGRPCLatency_Match(t *testing.T) {
	r := NewServiceGRPCLatency()
	cases := []struct {
		name    string
		traits  []string
		typ     inventory.MetricType
		wantHit bool
	}{
		{
			name:    "histogram_with_both_traits_matches",
			traits:  []string{"latency_histogram", "service_grpc"},
			typ:     inventory.MetricTypeHistogram,
			wantHit: true,
		},
		{
			// Latency trait without gRPC signal — could be any duration
			// histogram (DB query, internal op). Must not match.
			name:    "latency_trait_without_grpc_trait_rejected",
			traits:  []string{"latency_histogram"},
			typ:     inventory.MetricTypeHistogram,
			wantHit: false,
		},
		{
			// gRPC trait without latency trait — could be a gRPC counter
			// that happens to be typed histogram elsewhere. Must not match.
			name:    "grpc_trait_without_latency_trait_rejected",
			traits:  []string{"service_grpc"},
			typ:     inventory.MetricTypeHistogram,
			wantHit: false,
		},
		{
			// Both traits present but not a histogram. Type-guard sanity.
			name:    "counter_with_both_traits_rejected_not_a_histogram",
			traits:  []string{"latency_histogram", "service_grpc"},
			typ:     inventory.MetricTypeCounter,
			wantHit: false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			m := ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: "grpc_server_handling_seconds_bucket"},
				Type:       tc.typ,
				Traits:     tc.traits,
			}
			if got := r.Match(m); got != tc.wantHit {
				t.Fatalf("Match(traits=%v, type=%s) = %v, want %v", tc.traits, tc.typ, got, tc.wantHit)
			}
		})
	}
}

func TestServiceGRPCLatency_BuildPanels(t *testing.T) {
	r := NewServiceGRPCLatency()
	inv := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{{
			Descriptor: inventory.MetricDescriptor{
				Name:   "grpc_server_handling_seconds",
				Labels: []string{"grpc_method", "grpc_service", "instance", "job", "le"},
			},
			Type:   inventory.MetricTypeHistogram,
			Traits: []string{"latency_histogram", "service_grpc"},
		}},
	}
	panels := r.BuildPanels(inv, profiles.ProfileService)
	if len(panels) != 1 {
		t.Fatalf("expected 1 panel, got %d", len(panels))
	}
	if len(panels[0].Queries) != 3 {
		t.Fatalf("expected 3 quantile queries, got %d", len(panels[0].Queries))
	}
	// Must append _bucket to the bare metadata name when synthesizing.
	for i, q := range panels[0].Queries {
		if !strings.Contains(q.Expr, "grpc_server_handling_seconds_bucket") {
			t.Errorf("query[%d] must reference _bucket series, got %q", i, q.Expr)
		}
		if !strings.Contains(q.Expr, "le") {
			t.Errorf("query[%d] must group by le, got %q", i, q.Expr)
		}
		if !strings.Contains(q.Expr, "grpc_method") {
			t.Errorf("query[%d] must group by grpc_method, got %q", i, q.Expr)
		}
	}
	// Percentiles in the expected order.
	wants := []string{"0.50", "0.95", "0.99"}
	for i, want := range wants {
		if !strings.Contains(panels[0].Queries[i].Expr, "histogram_quantile("+want) {
			t.Errorf("query[%d] expected quantile %s, got %q", i, want, panels[0].Queries[i].Expr)
		}
	}
}
