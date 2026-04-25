package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"

	"github.com/prometheus/prometheus/promql/parser"
)

func TestK8sCoreDNS_Match(t *testing.T) {
	r := NewK8sCoreDNS()
	cases := []struct {
		name    string
		metric  string
		typ     inventory.MetricType
		wantHit bool
	}{
		// Positive: histogram base name.
		{
			name:    "latency_histogram_base_name",
			metric:  "coredns_dns_request_duration_seconds",
			typ:     inventory.MetricTypeHistogram,
			wantHit: true,
		},
		// Positive: histogram with _bucket suffix (Prometheus may expose this).
		{
			name:    "latency_histogram_bucket_suffix",
			metric:  "coredns_dns_request_duration_seconds_bucket",
			typ:     inventory.MetricTypeHistogram,
			wantHit: true,
		},
		// Positive: counter request rate signal.
		{
			name:    "request_rate_counter",
			metric:  "coredns_dns_requests_total",
			typ:     inventory.MetricTypeCounter,
			wantHit: true,
		},
		// Positive: unknown type for request rate (some scrapers classify as unknown).
		{
			name:    "request_rate_unknown_type",
			metric:  "coredns_dns_requests_total",
			typ:     inventory.MetricTypeUnknown,
			wantHit: true,
		},
		// Negative: legacy kube-dns counter must not match.
		{
			name:    "kube_dns_legacy_counter_rejected",
			metric:  "kube_dns_responses_total",
			typ:     inventory.MetricTypeCounter,
			wantHit: false,
		},
		// Negative: coredns health-check duration — similar prefix but different signal.
		{
			name:    "coredns_health_duration_rejected",
			metric:  "coredns_health_request_duration_seconds_bucket",
			typ:     inventory.MetricTypeHistogram,
			wantHit: false,
		},
		// Negative: apiserver latency histogram — different component entirely.
		{
			name:    "apiserver_duration_bucket_rejected",
			metric:  "apiserver_request_duration_seconds_bucket",
			typ:     inventory.MetricTypeHistogram,
			wantHit: false,
		},
		// Negative: correct latency name but wrong type (counter, not histogram).
		{
			name:    "latency_name_wrong_type_rejected",
			metric:  "coredns_dns_request_duration_seconds",
			typ:     inventory.MetricTypeCounter,
			wantHit: false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			m := ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{Name: tc.metric},
				Type:       tc.typ,
			}
			if got := r.Match(m); got != tc.wantHit {
				t.Fatalf("Match(name=%q, type=%s) = %v, want %v", tc.metric, tc.typ, got, tc.wantHit)
			}
		})
	}
}

func TestK8sCoreDNS_BuildPanels(t *testing.T) {
	r := NewK8sCoreDNS()

	histMetric := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{
			Name:   "coredns_dns_request_duration_seconds",
			Labels: []string{"instance", "job", "le", "server", "type", "zone"},
		},
		Type: inventory.MetricTypeHistogram,
	}
	counterMetric := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{
			Name:   "coredns_dns_requests_total",
			Labels: []string{"instance", "job", "server", "zone"},
		},
		Type: inventory.MetricTypeCounter,
	}

	t.Run("both_signals_produce_two_panels", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{histMetric, counterMetric},
		}
		panels := r.BuildPanels(inv, profiles.ProfileK8s)
		if len(panels) != 2 {
			t.Fatalf("expected 2 panels, got %d", len(panels))
		}

		// Panels are sorted alphabetically: "CoreDNS request latency (p95)" < "CoreDNS request rate".
		latIdx, rateIdx := -1, -1
		for i, p := range panels {
			if strings.Contains(p.Title, "latency") {
				latIdx = i
			}
			if strings.Contains(p.Title, "rate") {
				rateIdx = i
			}
		}
		if latIdx < 0 {
			t.Fatal("expected a latency panel, found none")
		}
		if rateIdx < 0 {
			t.Fatal("expected a rate panel, found none")
		}

		// Latency panel: p95 only, references _bucket, groups by le.
		if len(panels[latIdx].Queries) != 1 {
			t.Fatalf("latency panel: expected 1 query (p95 only), got %d", len(panels[latIdx].Queries))
		}
		latExpr := panels[latIdx].Queries[0].Expr
		if !strings.Contains(latExpr, "histogram_quantile(0.95") {
			t.Errorf("latency expr must use histogram_quantile(0.95, got %q", latExpr)
		}
		if !strings.Contains(latExpr, "coredns_dns_request_duration_seconds_bucket") {
			t.Errorf("latency expr must reference _bucket series, got %q", latExpr)
		}
		if !strings.Contains(latExpr, "le") {
			t.Errorf("latency expr must group by le, got %q", latExpr)
		}

		// Rate panel: references the counter metric.
		if len(panels[rateIdx].Queries) != 1 {
			t.Fatalf("rate panel: expected 1 query, got %d", len(panels[rateIdx].Queries))
		}
		rateExpr := panels[rateIdx].Queries[0].Expr
		if !strings.Contains(rateExpr, "rate(coredns_dns_requests_total") {
			t.Errorf("rate expr must reference coredns_dns_requests_total, got %q", rateExpr)
		}

		// Every expression must be valid PromQL.
		prs := parser.NewParser(parser.Options{})
		for _, panel := range panels {
			for _, q := range panel.Queries {
				if _, err := prs.ParseExpr(q.Expr); err != nil {
					t.Errorf("panel %q expr %q does not parse: %v", panel.Title, q.Expr, err)
				}
			}
		}
	})

	t.Run("histogram_only_produces_one_latency_panel", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{histMetric},
		}
		panels := r.BuildPanels(inv, profiles.ProfileK8s)
		if len(panels) != 1 {
			t.Fatalf("expected 1 panel, got %d", len(panels))
		}
		if !strings.Contains(panels[0].Title, "latency") {
			t.Errorf("expected latency panel, got %q", panels[0].Title)
		}
	})

	t.Run("counter_only_produces_one_rate_panel", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{counterMetric},
		}
		panels := r.BuildPanels(inv, profiles.ProfileK8s)
		if len(panels) != 1 {
			t.Fatalf("expected 1 panel, got %d", len(panels))
		}
		if !strings.Contains(panels[0].Title, "rate") {
			t.Errorf("expected rate panel, got %q", panels[0].Title)
		}
	})

	t.Run("wrong_profile_produces_no_panels", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{histMetric, counterMetric},
		}
		for _, prof := range []profiles.Profile{profiles.ProfileService, profiles.ProfileInfra} {
			panels := r.BuildPanels(inv, prof)
			if len(panels) != 0 {
				t.Errorf("profile %q: expected 0 panels, got %d", prof, len(panels))
			}
		}
	})
}
