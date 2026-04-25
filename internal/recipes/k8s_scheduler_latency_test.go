package recipes

import (
	"strings"
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"

	"github.com/prometheus/prometheus/promql/parser"
)

func TestK8sSchedulerLatency_Match(t *testing.T) {
	r := NewK8sSchedulerLatency()
	cases := []struct {
		name    string
		metric  string
		typ     inventory.MetricType
		wantHit bool
	}{
		{
			// Prometheus may expose the _bucket series name directly.
			name:    "bucket_suffix_histogram_matches",
			metric:  "scheduler_scheduling_attempt_duration_seconds_bucket",
			typ:     inventory.MetricTypeHistogram,
			wantHit: true,
		},
		{
			// Prometheus metadata API returns bare base name.
			name:    "base_name_histogram_matches",
			metric:  "scheduler_scheduling_attempt_duration_seconds",
			typ:     inventory.MetricTypeHistogram,
			wantHit: true,
		},
		{
			// kube-apiserver latency histogram — must NOT match.
			name:    "apiserver_request_duration_rejected",
			metric:  "apiserver_request_duration_seconds_bucket",
			typ:     inventory.MetricTypeHistogram,
			wantHit: false,
		},
		{
			// etcd internal histogram — must NOT match.
			name:    "etcd_disk_backend_commit_duration_rejected",
			metric:  "etcd_disk_backend_commit_duration_seconds_bucket",
			typ:     inventory.MetricTypeHistogram,
			wantHit: false,
		},
		{
			// Correct name but wrong type — must not match.
			name:    "counter_same_name_rejected",
			metric:  "scheduler_scheduling_attempt_duration_seconds",
			typ:     inventory.MetricTypeCounter,
			wantHit: false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			m := ClassifiedMetricView{
				Descriptor: inventory.MetricDescriptor{
					Name:   tc.metric,
					Labels: []string{"job", "result"},
				},
				Type:   tc.typ,
				Traits: []string{"latency_histogram"},
			}
			if got := r.Match(m); got != tc.wantHit {
				t.Fatalf("Match(name=%q, type=%s) = %v, want %v", tc.metric, tc.typ, got, tc.wantHit)
			}
		})
	}
}

func TestK8sSchedulerLatency_BuildPanels(t *testing.T) {
	r := NewK8sSchedulerLatency()
	inv := ClassifiedInventorySnapshot{
		Metrics: []ClassifiedMetricView{{
			Descriptor: inventory.MetricDescriptor{
				Name:   "scheduler_scheduling_attempt_duration_seconds_bucket",
				Labels: []string{"instance", "job", "le", "result"},
			},
			Type:   inventory.MetricTypeHistogram,
			Traits: []string{"latency_histogram"},
		}},
	}

	t.Run("k8s_profile_produces_panel", func(t *testing.T) {
		panels := r.BuildPanels(inv, profiles.ProfileK8s)
		if len(panels) != 1 {
			t.Fatalf("expected 1 panel, got %d", len(panels))
		}
		panel := panels[0]

		// Must have exactly 3 quantile queries.
		if len(panel.Queries) != 3 {
			t.Fatalf("expected 3 quantile queries, got %d", len(panel.Queries))
		}

		// Percentiles in low-to-high order.
		wantQuantiles := []string{"0.50", "0.95", "0.99"}
		for i, wq := range wantQuantiles {
			q := panel.Queries[i]
			if !strings.Contains(q.Expr, "histogram_quantile("+wq) {
				t.Errorf("query[%d] expected quantile %s, got %q", i, wq, q.Expr)
			}
		}

		// Every query must reference the _bucket series.
		for i, q := range panel.Queries {
			if !strings.Contains(q.Expr, "scheduler_scheduling_attempt_duration_seconds_bucket") {
				t.Errorf("query[%d] must reference _bucket series, got %q", i, q.Expr)
			}
		}

		// le and result must appear in every group.
		for i, q := range panel.Queries {
			if !strings.Contains(q.Expr, "le") {
				t.Errorf("query[%d] must group by le, got %q", i, q.Expr)
			}
			if !strings.Contains(q.Expr, "result") {
				t.Errorf("query[%d] must group by result, got %q", i, q.Expr)
			}
		}

		// Legend format must carry pN prefix.
		legendPrefixes := []string{"p50", "p95", "p99"}
		for i, prefix := range legendPrefixes {
			if !strings.HasPrefix(panel.Queries[i].LegendFormat, prefix) {
				t.Errorf("query[%d] legend %q should start with %q", i, panel.Queries[i].LegendFormat, prefix)
			}
		}

		// Every Expr must parse as valid PromQL.
		prs := parser.NewParser(parser.Options{})
		for i, q := range panel.Queries {
			if _, err := prs.ParseExpr(q.Expr); err != nil {
				t.Errorf("query[%d] expr %q does not parse: %v", i, q.Expr, err)
			}
		}

		// Unit and confidence.
		if panel.Unit != "s" {
			t.Errorf("panel unit = %q, want %q", panel.Unit, "s")
		}
		if panel.Confidence != 0.85 {
			t.Errorf("panel confidence = %v, want 0.85", panel.Confidence)
		}
	})

	t.Run("wrong_profile_produces_no_panels", func(t *testing.T) {
		for _, prof := range []profiles.Profile{profiles.ProfileService, profiles.ProfileInfra} {
			panels := r.BuildPanels(inv, prof)
			if len(panels) != 0 {
				t.Errorf("profile %q: expected 0 panels, got %d", prof, len(panels))
			}
		}
	})
}
