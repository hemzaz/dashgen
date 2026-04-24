package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestServiceDBQueryLatency_Match(t *testing.T) {
	r := NewServiceDBQueryLatency()
	cases := []struct {
		name       string
		metricName string
		traits     []string
		typ        inventory.MetricType
		wantHit    bool
	}{
		{
			// Canonical DB query histogram: name contains "query", has
			// latency_histogram trait, no HTTP/gRPC traits.
			name:       "db_query_duration_seconds_matches",
			metricName: "db_query_duration_seconds",
			traits:     []string{"latency_histogram"},
			typ:        inventory.MetricTypeHistogram,
			wantHit:    true,
		},
		{
			// Already-bucketed name with "query" prefix — still matches.
			name:       "mysql_query_duration_seconds_bucket_matches",
			metricName: "mysql_query_duration_seconds_bucket",
			traits:     []string{"latency_histogram"},
			typ:        inventory.MetricTypeHistogram,
			wantHit:    true,
		},
		{
			// HTTP latency histogram whose name happens to contain "query":
			// service_http trait present → must be rejected (belongs to
			// service_http_latency).
			name:       "http_trait_rejects_even_with_query_name",
			metricName: "db_query_duration_seconds",
			traits:     []string{"latency_histogram", "service_http"},
			typ:        inventory.MetricTypeHistogram,
			wantHit:    false,
		},
		{
			// gRPC latency histogram with "db" in name: service_grpc trait
			// present → must be rejected (belongs to service_grpc_latency).
			name:       "grpc_trait_rejects_even_with_db_name",
			metricName: "grpc_db_query_duration_seconds",
			traits:     []string{"latency_histogram", "service_grpc"},
			typ:        inventory.MetricTypeHistogram,
			wantHit:    false,
		},
		{
			// Counter with "query" in name: wrong metric type → rejected.
			name:       "counter_with_query_name_rejected",
			metricName: "db_query_total",
			traits:     []string{"latency_histogram"},
			typ:        inventory.MetricTypeCounter,
			wantHit:    false,
		},
		{
			// Histogram without the latency_histogram trait → rejected.
			name:       "histogram_without_latency_trait_rejected",
			metricName: "db_query_duration_seconds",
			traits:     []string{},
			typ:        inventory.MetricTypeHistogram,
			wantHit:    false,
		},
		{
			// Histogram with latency_histogram trait but name lacks query/db/sql
			// — could be notification latency or internal processing time.
			// No positive domain evidence → rejected.
			name:       "histogram_with_latency_trait_but_no_domain_name_rejected",
			metricName: "notification_latency_seconds",
			traits:     []string{"latency_histogram"},
			typ:        inventory.MetricTypeHistogram,
			wantHit:    false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			m := ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: tc.metricName},
				Type:       tc.typ,
				Traits:     tc.traits,
			}
			if got := r.Match(m); got != tc.wantHit {
				t.Fatalf("Match(name=%q, traits=%v, type=%s) = %v, want %v",
					tc.metricName, tc.traits, tc.typ, got, tc.wantHit)
			}
		})
	}
}

func TestServiceDBQueryLatency_BuildPanels(t *testing.T) {
	r := NewServiceDBQueryLatency()
	inv := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{{
			Descriptor: inventory.MetricDescriptor{
				Name:   "db_query_duration_seconds",
				Labels: []string{"instance", "job", "le"},
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
	for i, q := range panels[0].Queries {
		if !strings.Contains(q.Expr, "histogram_quantile") {
			t.Errorf("query[%d] must use histogram_quantile, got %q", i, q.Expr)
		}
		if !strings.Contains(q.Expr, "db_query_duration_seconds_bucket") {
			t.Errorf("query[%d] must reference _bucket series, got %q", i, q.Expr)
		}
		if !strings.Contains(q.Expr, "le") {
			t.Errorf("query[%d] must group by le, got %q", i, q.Expr)
		}
	}
	// Percentiles in expected order.
	wants := []string{"0.50", "0.95", "0.99"}
	for i, want := range wants {
		if !strings.Contains(panels[0].Queries[i].Expr, "histogram_quantile("+want) {
			t.Errorf("query[%d] expected quantile %s, got %q", i, want, panels[0].Queries[i].Expr)
		}
	}
	// Profile gate: non-service profile must return nothing.
	if got := r.BuildPanels(inv, profiles.ProfileInfra); len(got) != 0 {
		t.Errorf("expected no panels for ProfileInfra, got %d", len(got))
	}
}
